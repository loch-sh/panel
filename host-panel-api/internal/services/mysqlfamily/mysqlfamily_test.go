package mysqlfamily

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"host-panel-api/internal/docker"
	"host-panel-api/internal/services"
	"host-panel-api/internal/services/servicestest"
)

var update = flag.Bool("update", false, "rewrite golden files")

const testPassword = "Abcdefghijklmnopqrstuvwxyz012345"

var hostileNames = []string{
	"", "A", "a`b", "a'b", `a\b`, "a;b", "a b", "a-b", "a.b", "a%b", "abc\n", "é", "1abc", strings.Repeat("a", 33),
	"x`; DROP DATABASE mysql; --",
}

func mustPanic(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s must panic", name)
		}
	}()
	f()
}

func TestQuotingRefusesHostileInput(t *testing.T) {
	for _, n := range hostileNames {
		mustPanic(t, "quoteIdent("+n+")", func() { quoteIdent(n) })
		mustPanic(t, "quoteGrantDB("+n+")", func() { quoteGrantDB(n) })
		mustPanic(t, "quoteNameLiteral("+n+")", func() { quoteNameLiteral(n) })
	}
	for _, s := range []string{"", "a'b", `a\b`, "a b", "é", strings.Repeat("a", 65)} {
		mustPanic(t, "quoteLiteral("+s+")", func() { quoteLiteral(s) })
	}
}

// "_" is a wildcard in GRANT ... ON `db`.*: unescaped, `my_ql` would grant
// the system database `mysql` (confirmed against real servers).
func TestGrantEscapesWildcards(t *testing.T) {
	if got := quoteGrantDB("my_ql"); got != "`my\\_ql`" {
		t.Fatalf("quoteGrantDB = %s", got)
	}
	if got := quoteGrantDB("shop"); got != "`shop`" {
		t.Fatalf("quoteGrantDB = %s", got)
	}
}

func TestNativeHash(t *testing.T) {
	// Well-known value: PASSWORD('password') on MySQL 5.x.
	if got := nativeHash("password"); got != "*2470C0C06DEE42FD1618BB99005ADCA2EC9D1E19" {
		t.Fatalf("nativeHash = %s", got)
	}
}

func TestSQLGolden(t *testing.T) {
	mariadb := dialectFor(flavorMariaDB, "11.4")
	mysql84 := dialectFor(flavorMySQL, "8.4")
	mysql56 := dialectFor(flavorMySQL, "5.6")
	h := nativeHash(testPassword)
	cases := map[string][2]string{
		"mariadb user": {mariadb.createUserSQL("my_ql", testPassword, false),
			"CREATE USER 'my_ql'@'%' IDENTIFIED VIA mysql_native_password USING '" + h + "' WITH MAX_USER_CONNECTIONS 20;\n"},
		"mysql84 user": {mysql84.createUserSQL("my_ql", testPassword, false),
			"CREATE USER 'my_ql'@'%' IDENTIFIED BY '" + testPassword + "' WITH MAX_USER_CONNECTIONS 20;\n"},
		"mysql56 user": {mysql56.createUserSQL("my_ql", testPassword, false),
			"CREATE USER 'my_ql'@'%' IDENTIFIED BY PASSWORD '" + h + "';\n"},
		"mariadb db": {mariadb.createDatabaseSQL("my_ql", false),
			"CREATE DATABASE `my_ql` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;\n" +
				"GRANT ALL PRIVILEGES ON `my\\_ql`.* TO 'my_ql'@'%';\n" +
				"INSERT IGNORE INTO `loch_meta`.`resources` (`name`) VALUES ('my_ql');\n"},
		"mysql56 db replay": {mysql56.createDatabaseSQL("my_ql", true),
			"CREATE DATABASE IF NOT EXISTS `my_ql` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;\n" +
				"GRANT ALL PRIVILEGES ON `my\\_ql`.* TO 'my_ql'@'%' WITH MAX_USER_CONNECTIONS 20;\n" +
				"INSERT IGNORE INTO `loch_meta`.`resources` (`name`) VALUES ('my_ql');\n"},
		"mysql84 drop user": {mysql84.dropUserSQL("shop", true), "DROP USER IF EXISTS 'shop'@'%';\n"},
		"mysql56 drop user": {mysql56.dropUserSQL("shop", true), "DROP USER 'shop'@'%';\n"},
		"mysql56 no user":   {mysql56.dropUserSQL("shop", false), ""},
		"drop database": {dropDatabaseSQL("shop"),
			"DROP DATABASE IF EXISTS `shop`;\nDELETE FROM `loch_meta`.`resources` WHERE `name` = 'shop';\n"},
		"mariadb password": {mariadb.setPasswordSQL("shop", testPassword, false),
			"ALTER USER 'shop'@'%' IDENTIFIED VIA mysql_native_password USING '" + h + "';\n"},
		"mysql56 password": {mysql56.setPasswordSQL("shop", testPassword, false),
			"SET PASSWORD FOR 'shop'@'%' = '" + h + "';\n"},
		// Legacy mechanism on MySQL 8.4: hash computed by the panel.
		"mysql84 native user": {mysql84.createUserSQL("shop", testPassword, true),
			"CREATE USER 'shop'@'%' IDENTIFIED WITH mysql_native_password AS '" + h + "' WITH MAX_USER_CONNECTIONS 20;\n"},
		"mysql84 native password": {mysql84.setPasswordSQL("shop", testPassword, true),
			"ALTER USER 'shop'@'%' IDENTIFIED WITH mysql_native_password AS '" + h + "';\n"},
		// Ignored where native already is the mechanism.
		"mariadb native user": {mariadb.createUserSQL("shop", testPassword, true), mariadb.createUserSQL("shop", testPassword, false)},
	}
	for name, c := range cases {
		if c[0] != c[1] {
			t.Errorf("%s:\n got %q\nwant %q", name, c[0], c[1])
		}
	}
	// MariaDB and 5.6 never send the clear password.
	for _, d := range []dialect{mariadb, mysql56} {
		if strings.Contains(d.createUserSQL("shop", testPassword, false)+d.setPasswordSQL("shop", testPassword, false), testPassword) {
			t.Fatalf("%s: clear password sent", d.client)
		}
	}
}

func TestAdminCommandHoldsNoSecret(t *testing.T) {
	for _, d := range []dialect{dialectFor(flavorMariaDB, "11.8"), dialectFor(flavorMySQL, "8.4"), dialectFor(flavorMySQL, "5.6")} {
		cmd := strings.Join(d.adminCmd(), " ")
		if !strings.HasPrefix(cmd, "timeout -s KILL "+killAfterSeconds+" sh -c ") ||
			!strings.Contains(cmd, `MYSQL_PWD="$(cat /run/secrets/admin_password)" exec `+d.client+" --protocol=socket -uroot") {
			t.Fatalf("command = %s", cmd)
		}
		if !strings.Contains(d.preamble(), "sql_mode = 'STRICT_ALL_TABLES,NO_ENGINE_SUBSTITUTION") {
			t.Fatal("the session sql_mode must be pinned (quoting relies on it)")
		}
		// GRANT must never create a passwordless account (the mode does
		// not exist on MySQL 8).
		if wantNoAuto := d.client == "mariadb" || d.legacy; strings.Contains(d.preamble(), "NO_AUTO_CREATE_USER") != wantNoAuto {
			t.Fatalf("%s: NO_AUTO_CREATE_USER expected=%v", d.client, wantNoAuto)
		}
	}
	kill, _ := time.ParseDuration(killAfterSeconds + "s")
	if kill >= execTimeout {
		t.Fatal("the command must be killed before the exec deadline")
	}
}

type fakeExec struct {
	reqs    []docker.ExecRequest
	answers []fakeAnswer
}

type fakeAnswer struct {
	res docker.ExecResult
	err error
}

func (f *fakeExec) Exec(_ context.Context, req docker.ExecRequest) (docker.ExecResult, error) {
	f.reqs = append(f.reqs, req)
	if len(f.answers) == 0 {
		return docker.ExecResult{}, nil
	}
	a := f.answers[0]
	f.answers = f.answers[1:]
	return a.res, a.err
}

func fail(code string) fakeAnswer {
	return fakeAnswer{res: docker.ExecResult{ExitCode: 1, Stderr: []byte("ERROR " + code + " (HY000) at line 4: near 'IDENTIFIED BY '" + testPassword + "''")}}
}

func target(x *fakeExec, version string) services.Target {
	return services.Target{Version: services.Version{Version: version}, Executor: x}
}

func TestErrorMapping(t *testing.T) {
	d := dialectFor(flavorMariaDB, "11.4")
	for code, want := range map[string]error{"1007": services.ErrResourceExists, "1049": services.ErrResourceNotFound} {
		_, err := d.run(context.Background(), &fakeExec{answers: []fakeAnswer{fail(code)}}, "op", "SELECT 1;")
		if !errors.Is(err, want) || strings.Contains(err.Error(), testPassword) {
			t.Fatalf("%s: %v", code, err)
		}
	}
	for _, a := range []fakeAnswer{{err: docker.ErrExecTimeout}, {res: docker.ExecResult{ExitCode: 137}}} {
		if _, err := d.run(context.Background(), &fakeExec{answers: []fakeAnswer{a}}, "op", "SELECT 1;"); !errors.Is(err, services.ErrOutcomeUnknown) {
			t.Fatalf("err = %v", err)
		}
	}
	_, err := d.run(context.Background(), &fakeExec{answers: []fakeAnswer{fail("1045")}}, "op", "SELECT 1;")
	var ee *services.EngineError
	if !errors.As(err, &ee) || ee.Detail != "error 1045" || strings.Contains(err.Error(), testPassword) {
		t.Fatalf("err = %v", err)
	}
	if _, err := d.run(context.Background(), &fakeExec{answers: []fakeAnswer{{res: docker.ExecResult{Truncated: true}}}}, "op", "SELECT 1;"); err == nil {
		t.Fatal("truncated output must fail")
	}
}

func TestProvision(t *testing.T) {
	ctx := context.Background()
	p := provisioner{e: NewMariaDB()}
	// Success: user, then database + grant + record.
	x := &fakeExec{}
	if err := p.Provision(ctx, target(x, "11.4"), "shop", testPassword); err != nil {
		t.Fatal(err)
	}
	if len(x.reqs) != 2 || !strings.Contains(string(x.reqs[0].Stdin), "CREATE USER") || !strings.Contains(string(x.reqs[1].Stdin), "GRANT ALL PRIVILEGES ON `shop`.*") {
		t.Fatalf("%d requests", len(x.reqs))
	}
	for _, r := range x.reqs {
		if strings.Contains(strings.Join(r.Cmd, " "), testPassword) || strings.Contains(string(r.Stdin), testPassword) || r.User != "" {
			t.Fatal("secret in the command, or clear password sent to MariaDB")
		}
	}
	// The user exists already: nothing is dropped.
	x = &fakeExec{answers: []fakeAnswer{fail("1396")}}
	if err := p.Provision(ctx, target(x, "11.4"), "shop", testPassword); !errors.Is(err, services.ErrResourceExists) || len(x.reqs) != 1 {
		t.Fatalf("err=%v reqs=%d", err, len(x.reqs))
	}
	// The database step fails after the user was created: both are dropped.
	x = &fakeExec{answers: []fakeAnswer{{}, fail("1044")}}
	if err := p.Provision(ctx, target(x, "11.4"), "shop", testPassword); err == nil || len(x.reqs) != 3 ||
		!strings.Contains(string(x.reqs[2].Stdin), "DROP USER IF EXISTS 'shop'@'%'") ||
		!strings.Contains(string(x.reqs[2].Stdin), "DROP DATABASE IF EXISTS `shop`") {
		t.Fatalf("err=%v reqs=%d", err, len(x.reqs))
	}
	// The database existed before (1007): it is never dropped, only the new user.
	x = &fakeExec{answers: []fakeAnswer{{}, fail("1007")}}
	if err := p.Provision(ctx, target(x, "11.4"), "shop", testPassword); err == nil || len(x.reqs) != 3 ||
		strings.Contains(string(x.reqs[2].Stdin), "DROP DATABASE") {
		t.Fatalf("a pre-existing database must never be dropped: err=%v reqs=%d", err, len(x.reqs))
	}
	// Unknown outcome of the database step: replayed (idempotent).
	x = &fakeExec{answers: []fakeAnswer{{}, {err: docker.ErrExecTimeout}, {}}}
	if err := p.Provision(ctx, target(x, "11.4"), "shop", testPassword); err != nil || len(x.reqs) != 3 ||
		!strings.Contains(string(x.reqs[2].Stdin), "CREATE DATABASE IF NOT EXISTS") {
		t.Fatalf("err=%v reqs=%d", err, len(x.reqs))
	}
	// Foreign passwords are refused.
	if err := p.Provision(ctx, target(&fakeExec{}, "11.4"), "shop", "short'"); err == nil {
		t.Fatal("foreign password accepted")
	}
}

func TestDeprovisionAndRotateCloseSessions(t *testing.T) {
	ctx := context.Background()
	p := provisioner{e: NewMySQL()}
	// 5.6: user existence checked first (no DROP USER IF EXISTS); the user
	// goes first and its sessions are closed before DROP DATABASE (an open
	// transaction would block it).
	x := &fakeExec{answers: []fakeAnswer{
		{res: docker.ExecResult{Stdout: []byte("1\n")}}, {}, {res: docker.ExecResult{Stdout: []byte("12\n34\n")}}, {}, fail("1094"), {},
	}}
	if err := p.Deprovision(ctx, target(x, "5.6"), "shop"); err != nil {
		t.Fatal(err)
	}
	if len(x.reqs) != 6 || !strings.Contains(string(x.reqs[1].Stdin), "DROP USER 'shop'@'%';") ||
		!strings.HasSuffix(string(x.reqs[3].Stdin), "KILL 12;\n") || !strings.HasSuffix(string(x.reqs[4].Stdin), "KILL 34;\n") ||
		!strings.Contains(string(x.reqs[5].Stdin), "DROP DATABASE IF EXISTS `shop`") {
		t.Fatalf("requests: %d", len(x.reqs))
	}
	// The new password is kept even when closing the sessions fails.
	x = &fakeExec{answers: []fakeAnswer{{}, {res: docker.ExecResult{Stdout: []byte("7; DROP\n")}}}}
	if err := p.SetPassword(ctx, target(x, "8.4"), "shop", testPassword); err != nil {
		t.Fatalf("a failed KILL must not lose the new password: %v", err)
	}
}

func TestValidateNameMySQL56(t *testing.T) {
	p := provisioner{e: NewMySQL()}
	v56, v84 := NewMySQL().VersionOf("5.6", "5.6"), NewMySQL().VersionOf("8.4", "8.4")
	long := strings.Repeat("a", 17)
	if p.ValidateName(v56, long) == nil || v56.NameLimit() != 16 {
		t.Fatal("MySQL 5.6 user names are limited to 16 characters")
	}
	if p.ValidateName(v84, long) != nil || p.ValidateName(v56, strings.Repeat("a", 16)) != nil {
		t.Fatal("valid names refused")
	}
	for _, n := range []string{"mysql", "sys", "information_schema", "performance_schema", "mariadb_x", "mysqlx", "sys_x"} {
		if p.ValidateName(v84, n) == nil {
			t.Errorf("%s must be reserved", n)
		}
	}
}

func TestParseList(t *testing.T) {
	list, err := parseList([]byte("73686F70:1\n4C45474143:0\n"))
	if err != nil || len(list) != 2 || list[0] != (services.Resource{Name: "shop", Managed: true}) || list[1].Managed {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if strings.Contains(listSQL, "NOT LIKE") {
		t.Fatal("no pattern may hide users from the catalog")
	}
	if _, err := parseList([]byte("zz:1")); err == nil {
		t.Fatal("garbage accepted")
	}
	for _, want := range []string{"'" + metaDB + "')", "NOT EXISTS (SELECT 1 FROM information_schema.schemata", "BINARY s.schema_name NOT IN", "BINARY u.User NOT IN"} {
		if !strings.Contains(listSQL, want) {
			t.Fatalf("listSQL must contain %q", want)
		}
	}
}

func TestRenderGolden(t *testing.T) {
	cfg := services.RenderConfig{DataDir: "/data/services", Networks: servicestest.Networks()}
	for _, c := range []struct {
		e       *Engine
		version string
	}{{NewMariaDB(), "11.4"}, {NewMySQL(), "8.4"}, {NewMySQL(), "5.6"}} {
		// 5.6 is not in the built-in catalog (offered only when pinned).
		v := c.e.VersionOf(c.version, c.version)
		out, err := services.Render(c.e, services.InstanceRef{Engine: c.e.ID(), Version: v.Version}, v, nil, []string{"services"}, cfg)
		if err != nil {
			t.Fatal(err)
		}
		golden := filepath.Join("testdata", "compose-"+c.e.ID()+"-"+services.Slug(c.version)+".golden.yaml")
		if *update {
			if err := os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(golden, out, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(golden)
		if err != nil || string(out) != string(want) {
			t.Fatalf("%s differs (run with -update):\n%s", golden, out)
		}
		for _, forbidden := range []string{"ports:", "privileged", "ROOT_PASSWORD:", "network_mode"} {
			if strings.Contains(string(out), forbidden) {
				t.Fatalf("%s must not contain %q", golden, forbidden)
			}
		}
		if !strings.Contains(string(out), "ROOT_HOST: localhost") {
			t.Fatalf("%s: root must be limited to localhost", golden)
		}
	}
}

func TestRegistryAcceptsBothEngines(t *testing.T) {
	if _, err := services.NewRegistry([]services.Engine{NewMariaDB(), NewMySQL()}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}
