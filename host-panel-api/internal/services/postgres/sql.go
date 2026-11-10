package postgres

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"host-panel-api/internal/docker"
	"host-panel-api/internal/services"
)

// identRe is checked again right before quoting (defense in depth: the
// Manager validates names first). A name matching it holds no character
// that is special inside a quoted identifier.
var identRe = regexp.MustCompile(`\A[a-z][a-z0-9_]{0,31}\z`)

// literalRe is the only content accepted in a string literal: SCRAM
// verifiers (base64, "$", ":", "-") — no quote, no backslash.
var literalRe = regexp.MustCompile(`\A[A-Za-z0-9+/=$:-]{1,256}\z`)

// quoteIdent quotes a validated identifier. It panics on anything else: a
// missed validation must show up in tests, never reach the database.
func quoteIdent(name string) string {
	if !identRe.MatchString(name) {
		panic(fmt.Sprintf("postgres: refusing to quote identifier %q", name))
	}
	return `"` + name + `"`
}

// quoteNameLiteral quotes a validated identifier as a string literal (for
// catalog lookups). It panics like quoteIdent.
func quoteNameLiteral(name string) string {
	if !identRe.MatchString(name) {
		panic(fmt.Sprintf("postgres: refusing to quote name %q", name))
	}
	return "'" + name + "'"
}

// quoteLiteral quotes a verifier. It panics on any other character.
func quoteLiteral(s string) string {
	if !literalRe.MatchString(s) {
		panic("postgres: refusing to quote a literal with unexpected characters")
	}
	return "'" + s + "'"
}

const (
	// execTimeout bounds the exec; killAfter (timeout -s KILL in the
	// container) and the SQL timeouts end the command before it, so a
	// timed-out exec does not keep running behind the panel's back.
	execTimeout      = 30 * time.Second
	killAfterSeconds = "25"
	// preamble: errors only print their SQLSTATE (psql never echoes the
	// statement, which may hold a verifier), and statements are bounded.
	preamble = "\\set VERBOSITY sqlstate\nSET statement_timeout = '20s';\nSET lock_timeout = '10s';\n"
)

// psqlCmd connects as the superuser through the local socket (trust
// authentication in the official image). The SQL goes through stdin, never
// argv. -X: ignore ~/.psqlrc; -q: no command tags; -A -t: raw rows.
var psqlCmd = []string{
	"timeout", "-s", "KILL", killAfterSeconds,
	"psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "-A", "-t", "-U", "postgres", "-d", "postgres", "-f", "-",
}

// sqlstateRe extracts the SQLSTATE of an error printed with VERBOSITY sqlstate.
var sqlstateRe = regexp.MustCompile(`(?m)(?:ERROR|FATAL):\s+([0-9A-Z]{5})\s*$`)

// run executes SQL as the superuser. Its errors never include the output.
func run(ctx context.Context, x services.Executor, op, sql string) ([]byte, error) {
	res, err := x.Exec(ctx, docker.ExecRequest{
		Cmd:     psqlCmd,
		User:    "postgres",
		Stdin:   []byte(preamble + sql),
		Timeout: execTimeout,
	})
	if errors.Is(err, docker.ErrExecTimeout) || errors.Is(err, docker.ErrExecStillRunning) {
		return nil, fmt.Errorf("postgres %s: %w", op, services.ErrOutcomeUnknown)
	}
	if err != nil {
		return nil, &services.EngineError{Op: op, Detail: "exec: " + err.Error()}
	}
	if res.ExitCode == 0 {
		if res.Truncated {
			// A partial catalog could make an instance look empty.
			return nil, &services.EngineError{Op: op, Detail: "output truncated"}
		}
		return res.Stdout, nil
	}
	if res.ExitCode == 137 { // killed by timeout -s KILL
		return nil, fmt.Errorf("postgres %s: %w", op, services.ErrOutcomeUnknown)
	}
	state := ""
	if m := sqlstateRe.FindSubmatch(res.Stderr); m != nil {
		state = string(m[1])
	}
	switch state {
	case "42710", "42P04": // duplicate_object (role), duplicate_database
		return nil, fmt.Errorf("postgres %s: %w", op, services.ErrResourceExists)
	case "3D000", "42704": // invalid_catalog_name, undefined_object
		return nil, fmt.Errorf("postgres %s: %w", op, services.ErrResourceNotFound)
	case "":
		return nil, &services.EngineError{Op: op, Detail: fmt.Sprintf("exit code %d", res.ExitCode)}
	}
	return nil, &services.EngineError{Op: op, Detail: "sqlstate " + state}
}

// managedComment marks the roles created by the panel.
const managedComment = "loch-managed"

// listSQL returns one "<hex name>:<managed>" row per user-level object:
// every database but the maintenance ones (templates included: an owner may
// turn its database into a template), and every role without a database of
// its name (left by an interrupted creation or deletion). Retirement deletes
// the data when this list is empty, so nothing may be missed. Names are
// hex-encoded: an object created outside the panel may have any name. An
// object is managed when its role carries the panel comment.
const listSQL = `SELECT encode(convert_to(d.datname::text, 'UTF8'), 'hex') || ':' ||
  CASE WHEN r.rolname = d.datname AND NOT r.rolsuper
    AND shobj_description(r.oid, 'pg_authid') = '` + managedComment + `' THEN '1' ELSE '0' END
FROM pg_database d JOIN pg_roles r ON r.oid = d.datdba
WHERE d.datname NOT IN ('postgres', 'template0', 'template1')
UNION ALL
SELECT encode(convert_to(r.rolname::text, 'UTF8'), 'hex') || ':' ||
  CASE WHEN NOT r.rolsuper AND shobj_description(r.oid, 'pg_authid') = '` + managedComment + `' THEN '1' ELSE '0' END
FROM pg_roles r
WHERE r.rolname <> 'postgres' AND r.rolname NOT LIKE 'pg\_%'
  AND NOT EXISTS (SELECT 1 FROM pg_database d WHERE d.datname = r.rolname)
ORDER BY 1;
`

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

// connectionLimit bounds the connections of each user, and maxConnections
// those of the instance: one site must not exhaust the slots of the others.
const (
	connectionLimit = 20
	maxConnections  = "200"
)

// createRoleSQL creates the role and marks it as created by the panel.
func createRoleSQL(name, verifier string) string {
	q := quoteIdent(name)
	return fmt.Sprintf("CREATE ROLE %s LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT %d PASSWORD %s;\nCOMMENT ON ROLE %s IS '%s';\n",
		q, connectionLimit, quoteLiteral(verifier), q, managedComment)
}

// createDatabaseSQL creates the database closed to connections, restricts
// it, then opens it: an interruption never leaves a database open to every
// user. CREATE DATABASE cannot run in a transaction: psql runs each statement
// on its own (autocommit) and stops at the first error.
func createDatabaseSQL(name string) string {
	q := quoteIdent(name)
	return fmt.Sprintf("CREATE DATABASE %s OWNER %s TEMPLATE template0 ENCODING 'UTF8' ALLOW_CONNECTIONS false;\n", q, q) +
		openDatabaseSQL(name)
}

// openDatabaseSQL restricts then opens a database (idempotent).
func openDatabaseSQL(name string) string {
	q := quoteIdent(name)
	return fmt.Sprintf("REVOKE ALL ON DATABASE %s FROM PUBLIC;\nALTER DATABASE %s ALLOW_CONNECTIONS true;\n", q, q)
}

func dropRoleSQL(name string) string {
	return fmt.Sprintf("DROP ROLE IF EXISTS %s;\n", quoteIdent(name))
}

// dropSQL deletes a database and its role, whatever state they are in:
// the role can no longer log in and its sessions are closed first; a
// database turned into a template is turned back; DROP OWNED (run in the
// maintenance database) revokes the privileges granted to the role on shared
// objects (e.g. CONNECT on another database), which would block DROP ROLE.
// Every step is idempotent, so a failed deletion can be retried.
func dropSQL(name string) string {
	q, l := quoteIdent(name), quoteNameLiteral(name)
	return fmt.Sprintf(`SELECT 'ALTER ROLE %[1]s NOLOGIN' FROM pg_roles WHERE rolname = %[2]s
\gexec
SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = %[2]s AND pid <> pg_backend_pid();
SELECT 'ALTER DATABASE %[1]s IS_TEMPLATE false' FROM pg_database WHERE datname = %[2]s AND datistemplate
\gexec
DROP DATABASE IF EXISTS %[1]s WITH (FORCE);
SELECT 'DROP OWNED BY %[1]s' FROM pg_roles WHERE rolname = %[2]s
\gexec
DROP ROLE IF EXISTS %[1]s;
`, q, l)
}

// setPasswordSQL changes the password and closes the open sessions, which
// would otherwise keep working with the old one.
func setPasswordSQL(name, verifier string) string {
	return fmt.Sprintf("ALTER ROLE %s PASSWORD %s;\nSELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = %s AND pid <> pg_backend_pid();\n",
		quoteIdent(name), quoteLiteral(verifier), quoteNameLiteral(name))
}

// initSQL hardens an instance (idempotent): only owners may connect to their
// database (each one is created with REVOKE ALL FROM PUBLIC), and nobody but
// the superuser may connect to the maintenance databases.
const initSQL = "REVOKE CONNECT ON DATABASE postgres FROM PUBLIC;\nREVOKE CONNECT ON DATABASE template1 FROM PUBLIC;\n"

// md5RoleSQL tells whether a role has an md5 password ("t" or "f").
func md5RoleSQL(name string) string {
	return fmt.Sprintf("SELECT COALESCE(bool_or(rolpassword LIKE 'md5%%'), false) FROM pg_authid WHERE rolname = %s;\n", quoteNameLiteral(name))
}
