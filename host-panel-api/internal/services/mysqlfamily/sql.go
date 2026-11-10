package mysqlfamily

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"host-panel-api/internal/docker"
	"host-panel-api/internal/services"
)

// dialect holds the differences between flavors and versions.
type dialect struct {
	client    string // client binary in the image
	legacy    bool   // MySQL 5.6: no DROP USER IF EXISTS, no CREATE USER ... WITH, old PASSWORD syntax
	plainAuth bool   // MySQL 8.4+: caching_sha2_password, password sent in clear (stdin)
	// nativePlugin: the mysql_native_password plugin can be loaded (MySQL
	// 8.4 to 8.x; removed in 9.0): legacy authentication is offered.
	nativePlugin bool
	collation    string
	timeout      string // session statement timeout, "" when unsupported
	sqlMode      string // session sql_mode pinned by the preamble
}

// noAutoCreate: GRANT must never create an account (it would have no
// password); the mode no longer exists (nor the behavior) on MySQL 8.
const (
	sqlModeStrict      = "STRICT_ALL_TABLES,NO_ENGINE_SUBSTITUTION"
	sqlModeNoAutoUsers = sqlModeStrict + ",NO_AUTO_CREATE_USER"
)

func dialectFor(f flavor, version string) dialect {
	if f == flavorMariaDB {
		return dialect{client: "mariadb", collation: "utf8mb4_unicode_ci", timeout: "SET SESSION max_statement_time = 20;\n", sqlMode: sqlModeNoAutoUsers}
	}
	if services.CompareVersions(version, "5.7") < 0 {
		return dialect{client: "mysql", legacy: true, collation: "utf8mb4_unicode_ci", sqlMode: sqlModeNoAutoUsers}
	}
	native := services.CompareVersions(version, "8.4") >= 0 && services.Major(version) == 8
	return dialect{client: "mysql", plainAuth: true, nativePlugin: native, collation: "utf8mb4_0900_ai_ci", timeout: "SET SESSION max_execution_time = 20000;\n", sqlMode: sqlModeStrict}
}

// identRe is checked again right before quoting (defense in depth: the
// Manager validates names first). A matching name holds no character that is
// special in a backtick identifier or a string literal.
var identRe = regexp.MustCompile(`\A[a-z][a-z0-9_]{0,31}\z`)

// literalRe is the only content accepted in a string literal: generated
// passwords ([A-Za-z0-9]) and native hashes ("*" + hex). No quote, no
// backslash: the quoting is right whatever sql_mode (NO_BACKSLASH_ESCAPES).
var literalRe = regexp.MustCompile(`\A[A-Za-z0-9*]{1,64}\z`)

func quoteIdent(name string) string {
	if !identRe.MatchString(name) {
		panic(fmt.Sprintf("mysqlfamily: refusing to quote identifier %q", name))
	}
	return "`" + name + "`"
}

// quoteGrantDB quotes a database name in a GRANT: "_" and "%" are wildcards
// there, so `my_ql` would also grant `mysql` (the system database). "_" is
// escaped; "%" cannot appear in a valid name.
func quoteGrantDB(name string) string {
	q := quoteIdent(name)
	return strings.ReplaceAll(q, "_", `\_`)
}

func quoteLiteral(s string) string {
	if !literalRe.MatchString(s) {
		panic("mysqlfamily: refusing to quote a literal with unexpected characters")
	}
	return "'" + s + "'"
}

func quoteNameLiteral(name string) string {
	if !identRe.MatchString(name) {
		panic(fmt.Sprintf("mysqlfamily: refusing to quote name %q", name))
	}
	return "'" + name + "'"
}

// account is the user of a resource: same name, any host (the sites connect
// from the shared network).
func account(name string) string { return quoteNameLiteral(name) + "@'%'" }

// nativeHash is the mysql_native_password hash of a password.
func nativeHash(password string) string {
	first := sha1.Sum([]byte(password))
	second := sha1.Sum(first[:])
	return "*" + strings.ToUpper(hex.EncodeToString(second[:]))
}

const (
	execTimeout      = 30 * time.Second
	killAfterSeconds = "25"
	// metaDB records the resources created by the panel (the equivalent of
	// the PostgreSQL role comment): a database or user is managed only when
	// listed there. Tenants have no privilege on it.
	metaDB = "loch_meta"
)

// adminCmd connects as root through the local socket. The root password is
// read from the secret file inside the container by a constant shell string
// (no interpolation of any input): it never appears in argv nor reaches the
// API. The SQL goes through stdin.
func (d dialect) adminCmd() []string {
	return []string{
		"timeout", "-s", "KILL", killAfterSeconds,
		"sh", "-c", `MYSQL_PWD="$(cat ` + services.AdminSecretMount + `)" exec ` + d.client + ` --protocol=socket -uroot -N -B --connect-timeout=10`,
	}
}

// preamble pins the session settings the quoting relies on (no
// NO_BACKSLASH_ESCAPES / ANSI_QUOTES surprises) and bounds the statements.
func (d dialect) preamble() string {
	return "SET SESSION sql_mode = '" + d.sqlMode + "';\nSET SESSION lock_wait_timeout = 10;\n" + d.timeout
}

// errorRe extracts the error number printed by the client
// ("ERROR 1396 (HY000) at line 3: ...").
var errorRe = regexp.MustCompile(`ERROR (\d{4,5}) \(`)

// run executes SQL as root. Its errors never include the output.
func (d dialect) run(ctx context.Context, x services.Executor, op, sql string) ([]byte, error) {
	res, err := x.Exec(ctx, docker.ExecRequest{
		Cmd:     d.adminCmd(),
		Stdin:   []byte(d.preamble() + sql),
		Timeout: execTimeout,
	})
	if errors.Is(err, docker.ErrExecTimeout) || errors.Is(err, docker.ErrExecStillRunning) {
		return nil, fmt.Errorf("%s %s: %w", d.client, op, services.ErrOutcomeUnknown)
	}
	if err != nil {
		return nil, &services.EngineError{Op: op, Detail: "exec: " + err.Error()}
	}
	if res.ExitCode == 0 {
		if res.Truncated {
			return nil, &services.EngineError{Op: op, Detail: "output truncated"}
		}
		return res.Stdout, nil
	}
	if res.ExitCode == 137 {
		return nil, fmt.Errorf("%s %s: %w", d.client, op, services.ErrOutcomeUnknown)
	}
	code := ""
	if m := errorRe.FindSubmatch(res.Stderr); m != nil {
		code = string(m[1])
	}
	switch code {
	case "1007": // database exists
		return nil, fmt.Errorf("%s %s: %w", d.client, op, services.ErrResourceExists)
	case "1049": // unknown database
		return nil, fmt.Errorf("%s %s: %w", d.client, op, services.ErrResourceNotFound)
	case "":
		return nil, &services.EngineError{Op: op, Detail: fmt.Sprintf("exit code %d", res.ExitCode)}
	}
	return nil, &services.EngineError{Op: op, Detail: "error " + code}
}

// initSQL creates the panel registry (idempotent).
const initSQL = "CREATE DATABASE IF NOT EXISTS `" + metaDB + "` CHARACTER SET ascii COLLATE ascii_bin;\n" +
	"CREATE TABLE IF NOT EXISTS `" + metaDB + "`.`resources` (`name` VARCHAR(64) NOT NULL PRIMARY KEY) ENGINE=InnoDB;\n"

// systemUsers are never listed.
const systemUsers = "'root','mysql','mariadb.sys','healthcheck','mysql.sys','mysql.session','mysql.infoschema',''"

// listSQL returns one "<hex name>:<managed>" row per user-level object:
// every non-system database, and every non-system user without a database
// of its name (left by an interrupted operation). Retirement deletes the data
// when this list is empty, so nothing may be missed. Names are hex-encoded:
// objects created outside the panel may have any name. Comparisons are
// binary: the catalogs use different collations.
const listSQL = "SELECT CONCAT(HEX(s.schema_name), ':', IF(r.name IS NOT NULL AND EXISTS (SELECT 1 FROM mysql.user u WHERE BINARY u.User = BINARY s.schema_name AND u.Host = '%'), '1', '0'))\n" +
	"FROM information_schema.schemata s LEFT JOIN `" + metaDB + "`.`resources` r ON BINARY r.name = BINARY s.schema_name\n" +
	"WHERE BINARY s.schema_name NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys', '" + metaDB + "')\n" +
	"UNION ALL\n" +
	"SELECT CONCAT(HEX(u.User), ':', IF(MAX(r.name) IS NOT NULL, '1', '0'))\n" +
	"FROM mysql.user u LEFT JOIN `" + metaDB + "`.`resources` r ON BINARY r.name = BINARY u.User\n" +
	"WHERE BINARY u.User NOT IN (" + systemUsers + ")\n" +
	"  AND NOT EXISTS (SELECT 1 FROM information_schema.schemata s WHERE BINARY s.schema_name = BINARY u.User)\n" +
	"GROUP BY u.User\n" +
	"ORDER BY 1;\n"

func parseList(out []byte) ([]services.Resource, error) {
	var list []services.Resource
	for _, line := range bytes.Split(bytes.TrimSpace(out), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		h, flag, ok := strings.Cut(string(line), ":")
		name, err := hex.DecodeString(h)
		if !ok || err != nil || (flag != "0" && flag != "1") {
			return nil, &services.EngineError{Op: "list", Detail: "unexpected output"}
		}
		list = append(list, services.Resource{Name: string(name), Managed: flag == "1" && services.IsValidName(string(name))})
	}
	return list, nil
}

// identified returns the authentication clause for a password.
func (d dialect) identified(password string) string {
	switch {
	case d.plainAuth:
		return "IDENTIFIED BY " + quoteLiteral(password)
	case d.legacy:
		return "IDENTIFIED BY PASSWORD " + quoteLiteral(nativeHash(password))
	default: // MariaDB: the clear password never reaches the server
		return "IDENTIFIED VIA mysql_native_password USING " + quoteLiteral(nativeHash(password))
	}
}

// identifiedNative is the MySQL 8.4 clause for the legacy mechanism: the
// panel computes the hash, the clear password never reaches the server.
func identifiedNative(password string) string {
	return "IDENTIFIED WITH mysql_native_password AS " + quoteLiteral(nativeHash(password))
}

// createUserSQL creates a user; native selects mysql_native_password on
// MySQL 8.4 (the other versions already use it).
func (d dialect) createUserSQL(name, password string, native bool) string {
	if d.legacy {
		return fmt.Sprintf("CREATE USER %s %s;\n", account(name), d.identified(password))
	}
	clause := d.identified(password)
	if native && d.plainAuth {
		clause = identifiedNative(password)
	}
	return fmt.Sprintf("CREATE USER %s %s WITH MAX_USER_CONNECTIONS %d;\n", account(name), clause, connectionLimit)
}

// nativeUserSQL tells whether a user authenticates with
// mysql_native_password ("1" or "0").
func nativeUserSQL(name string) string {
	return fmt.Sprintf("SELECT COUNT(*) FROM mysql.user WHERE User = %s AND Host = '%%' AND plugin = 'mysql_native_password';\n", quoteNameLiteral(name))
}

// createDatabaseSQL creates the database, grants it to its user and records
// it. The first attempt fails on an existing database (never adopted, hence
// never dropped by the compensation); the replay after an unknown outcome is
// idempotent.
func (d dialect) createDatabaseSQL(name string, replay bool) string {
	limit := ""
	if d.legacy {
		limit = fmt.Sprintf(" WITH MAX_USER_CONNECTIONS %d", connectionLimit)
	}
	ifNotExists := ""
	if replay {
		ifNotExists = "IF NOT EXISTS "
	}
	return fmt.Sprintf("CREATE DATABASE %s%s CHARACTER SET utf8mb4 COLLATE %s;\n", ifNotExists, quoteIdent(name), d.collation) +
		fmt.Sprintf("GRANT ALL PRIVILEGES ON %s.* TO %s%s;\n", quoteGrantDB(name), account(name), limit) +
		fmt.Sprintf("INSERT IGNORE INTO `%s`.`resources` (`name`) VALUES (%s);\n", metaDB, quoteNameLiteral(name))
}

func (d dialect) dropUserSQL(name string, exists bool) string {
	if !d.legacy {
		return fmt.Sprintf("DROP USER IF EXISTS %s;\n", account(name))
	}
	if !exists { // MySQL 5.6 has no DROP USER IF EXISTS
		return ""
	}
	return fmt.Sprintf("DROP USER %s;\n", account(name))
}

// dropDatabaseSQL drops the database and its record (the user is dropped
// first, see Deprovision).
func dropDatabaseSQL(name string) string {
	return fmt.Sprintf("DROP DATABASE IF EXISTS %s;\n", quoteIdent(name)) +
		fmt.Sprintf("DELETE FROM `%s`.`resources` WHERE `name` = %s;\n", metaDB, quoteNameLiteral(name))
}

func (d dialect) setPasswordSQL(name, password string, native bool) string {
	if d.legacy {
		return fmt.Sprintf("SET PASSWORD FOR %s = %s;\n", account(name), quoteLiteral(nativeHash(password)))
	}
	clause := d.identified(password)
	if native && d.plainAuth {
		clause = identifiedNative(password)
	}
	return fmt.Sprintf("ALTER USER %s %s;\n", account(name), clause)
}

func userExistsSQL(name string) string {
	return fmt.Sprintf("SELECT COUNT(*) FROM mysql.user WHERE User = %s AND Host = '%%';\n", quoteNameLiteral(name))
}

func sessionsSQL(name string) string {
	return fmt.Sprintf("SELECT id FROM information_schema.processlist WHERE user = %s;\n", quoteNameLiteral(name))
}

// killSQL builds the KILL statements from the session ids (digits only).
func killSQL(out []byte) (string, error) {
	var b strings.Builder
	for _, line := range bytes.Fields(out) {
		id, err := strconv.ParseUint(string(line), 10, 64)
		if err != nil {
			return "", &services.EngineError{Op: "sessions", Detail: "unexpected output"}
		}
		// A session may end meanwhile: KILL of an unknown id is an error,
		// so each statement runs on its own (see killSessions).
		fmt.Fprintf(&b, "KILL %d;\n", id)
	}
	return b.String(), nil
}
