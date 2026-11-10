package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"host-panel-api/internal/docker"
	"host-panel-api/internal/services"
	"host-panel-api/internal/services/servicestest"
)

var update = flag.Bool("update", false, "rewrite golden files")

const testPassword = "Abcdefghijklmnopqrstuvwxyz012345" // shape of a generated password

// --- quoting ---

var hostileNames = []string{
	"", "A", "aB", "a\"b", "a`b", "a'b", "a;b", "a b", "a-b", "a.b", "a\\b", "a\x00b",
	"abc\n", "\nabc", "é", "ａbc", "1abc", "_abc", strings.Repeat("a", 33), `x"; DROP DATABASE postgres; --`,
}

func TestQuoteIdentRefusesHostileNames(t *testing.T) {
	for _, n := range hostileNames {
		t.Run(n, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("quoteIdent(%q) must panic", n)
				}
			}()
			quoteIdent(n)
		})
	}
	if got := quoteIdent("shop_1"); got != `"shop_1"` {
		t.Fatalf("quoteIdent = %s", got)
	}
}

func TestQuoteLiteralRefusesSpecialCharacters(t *testing.T) {
	for _, s := range []string{"", "a'b", `a\b`, "a\nb", "a b", "a;b", "é", strings.Repeat("a", 257)} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("quoteLiteral(%q) must panic", s)
				}
			}()
			quoteLiteral(s)
		}()
	}
	v, _ := scramVerifier(testPassword)
	if got := quoteLiteral(v); got != "'"+v+"'" {
		t.Fatalf("quoteLiteral = %s", got)
	}
}

func FuzzQuoteIdent(f *testing.F) {
	for _, n := range append(hostileNames, "shop", "a_1") {
		f.Add(n)
	}
	f.Fuzz(func(t *testing.T, s string) {
		defer func() { recover() }()
		q := quoteIdent(s)
		inner := q[1 : len(q)-1]
		if strings.ContainsAny(inner, "\"\\'`\x00\n; ") {
			t.Fatalf("quoted identifier %q holds a special character", q)
		}
	})
}

// --- SCRAM ---

// TestSCRAMKeysRFC7677 checks the derivation against the example exchange of
// RFC 7677 (user "user", password "pencil"): the client proof and the server
// signature computed from our keys must match the RFC.
func TestSCRAMKeysRFC7677(t *testing.T) {
	salt, _ := base64.StdEncoding.DecodeString("W22ZaJ0SNY7soEsUEjb6gQ==")
	stored, server, err := scramKeys("pencil", salt, 4096)
	if err != nil {
		t.Fatal(err)
	}
	authMessage := "n=user,r=rOprNGfwEbeRWgbNEkqO," +
		"r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096," +
		"c=biws,r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0"
	if got := base64.StdEncoding.EncodeToString(hmacSHA256(server, authMessage)); got != "6rriTRBi23WpRR/wtup+mMhUZUn/dB5nLTJRsjl95G4=" {
		t.Fatalf("server signature = %s", got)
	}
	// ClientProof = ClientKey XOR HMAC(StoredKey, AuthMessage), with
	// StoredKey = H(ClientKey): recover ClientKey from the RFC proof and
	// check that it hashes to our stored key.
	proof, _ := base64.StdEncoding.DecodeString("dHzbZapWIk4jUhN+Ute9ytag9zjfMHgsqmmiz7AndVQ=")
	sig := hmacSHA256(stored, authMessage)
	clientKey := make([]byte, len(proof))
	for i := range proof {
		clientKey[i] = proof[i] ^ sig[i]
	}
	sumArr := sha256.Sum256(clientKey)
	sum := sumArr[:]
	if !hmac.Equal(sum, stored) {
		t.Fatal("stored key does not match the RFC client proof")
	}
}

func TestSCRAMVerifierFormat(t *testing.T) {
	v1, err := scramVerifier(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	v2, _ := scramVerifier(testPassword)
	if v1 == v2 {
		t.Fatal("verifiers must use a random salt")
	}
	if !strings.HasPrefix(v1, "SCRAM-SHA-256$4096:") || strings.Contains(v1, testPassword) || !literalRe.MatchString(v1) {
		t.Fatalf("verifier = %s", v1)
	}
}

// --- SQL ---

func TestSQLGolden(t *testing.T) {
	const verifier = "SCRAM-SHA-256$4096:c2FsdA==$c3RvcmVk:c2VydmVy"
	cases := map[string]string{
		"create role": createRoleSQL("shop", verifier),
		"create db":   createDatabaseSQL("shop"),
		"drop role":   dropRoleSQL("shop"),
		"drop":        dropSQL("shop"),
		"password":    setPasswordSQL("shop", verifier),
	}
	want := map[string]string{
		"create role": `CREATE ROLE "shop" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 20 PASSWORD 'SCRAM-SHA-256$4096:c2FsdA==$c3RvcmVk:c2VydmVy';
COMMENT ON ROLE "shop" IS 'loch-managed';
`,
		"create db": `CREATE DATABASE "shop" OWNER "shop" TEMPLATE template0 ENCODING 'UTF8' ALLOW_CONNECTIONS false;
REVOKE ALL ON DATABASE "shop" FROM PUBLIC;
ALTER DATABASE "shop" ALLOW_CONNECTIONS true;
`,
		"drop role": "DROP ROLE IF EXISTS \"shop\";\n",
		"drop": `SELECT 'ALTER ROLE "shop" NOLOGIN' FROM pg_roles WHERE rolname = 'shop'
\gexec
SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = 'shop' AND pid <> pg_backend_pid();
SELECT 'ALTER DATABASE "shop" IS_TEMPLATE false' FROM pg_database WHERE datname = 'shop' AND datistemplate
\gexec
DROP DATABASE IF EXISTS "shop" WITH (FORCE);
SELECT 'DROP OWNED BY "shop"' FROM pg_roles WHERE rolname = 'shop'
\gexec
DROP ROLE IF EXISTS "shop";
`,
		"password": `ALTER ROLE "shop" PASSWORD 'SCRAM-SHA-256$4096:c2FsdA==$c3RvcmVk:c2VydmVy';
SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = 'shop' AND pid <> pg_backend_pid();
`,
	}
	for k, got := range cases {
		if got != want[k] {
			t.Errorf("%s:\n got %q\nwant %q", k, got, want[k])
		}
	}
}

func TestParseList(t *testing.T) {
	out := []byte("73686f70:1\n" + // shop, managed
		"6c6567616379:0\n" + // legacy, other owner
		"4d6978656443617365:1\n" + // MixedCase: owned by same-name role but not a panel name
		"0a3b:0\n") // "\n;" created outside the panel
	list, err := parseList(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []services.Resource{{Name: "shop", Managed: true}, {Name: "legacy"}, {Name: "MixedCase"}, {Name: "\n;"}}
	if len(list) != len(want) {
		t.Fatalf("list = %+v", list)
	}
	for i := range want {
		if list[i] != want[i] {
			t.Fatalf("list[%d] = %+v, want %+v", i, list[i], want[i])
		}
	}
	if l, err := parseList([]byte("\n")); err != nil || len(l) != 0 {
		t.Fatalf("empty output: %v %v", l, err)
	}
	for _, bad := range []string{"zz:1", "73686f70", "73686f70:2"} {
		if _, err := parseList([]byte(bad)); err == nil {
			t.Fatalf("output %q must be refused", bad)
		}
	}
}

// --- execution ---

// fakeExec records requests and answers with scripted results.
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

func failWith(state string, stderrExtra string) fakeAnswer {
	return fakeAnswer{res: docker.ExecResult{ExitCode: 3, Stderr: []byte("psql:<stdin>:4: ERROR:  " + state + "\n" + stderrExtra)}}
}

func TestRequestsNeverCarryThePassword(t *testing.T) {
	x := &fakeExec{}
	p := provisioner{}
	if err := p.Provision(context.Background(), services.Target{Executor: x}, "shop", testPassword); err != nil {
		t.Fatal(err)
	}
	if err := p.SetPassword(context.Background(), services.Target{Executor: x}, "shop", testPassword); err != nil {
		t.Fatal(err)
	}
	if len(x.reqs) != 4 {
		t.Fatalf("%d requests, want 4 (role, database, auth method, password)", len(x.reqs))
	}
	for _, r := range x.reqs {
		if strings.Contains(strings.Join(r.Cmd, " "), "SCRAM") || strings.Contains(strings.Join(r.Cmd, " "), testPassword) {
			t.Fatalf("secret in argv: %v", r.Cmd)
		}
		if strings.Contains(string(r.Stdin), testPassword) {
			t.Fatal("the clear password must never be sent, only its verifier")
		}
		if !strings.HasPrefix(string(r.Stdin), preamble) || r.User != "postgres" || r.Timeout != execTimeout {
			t.Fatalf("request = %+v", r)
		}
		if !strings.HasPrefix(strings.Join(r.Cmd, " "), "timeout -s KILL "+killAfterSeconds+" psql ") {
			t.Fatalf("command not bounded: %v", r.Cmd)
		}
	}
}

func TestProvisionRefusesForeignPasswords(t *testing.T) {
	for _, pw := range []string{"short", "a'b" + strings.Repeat("x", 29), strings.Repeat("é", 16)} {
		if err := (provisioner{}).Provision(context.Background(), services.Target{Executor: &fakeExec{}}, "shop", pw); err == nil {
			t.Fatalf("password %q must be refused", pw)
		}
	}
}

func TestErrorMapping(t *testing.T) {
	ctx := context.Background()
	leak := "LINE 1: CREATE ROLE ... PASSWORD '" + testPassword + "'"
	cases := []struct {
		name   string
		answer fakeAnswer
		want   error
	}{
		{"duplicate role", failWith("42710", leak), services.ErrResourceExists},
		{"duplicate db", failWith("42P04", leak), services.ErrResourceExists},
		{"missing", failWith("3D000", ""), services.ErrResourceNotFound},
		{"killed by timeout", fakeAnswer{res: docker.ExecResult{ExitCode: 137}}, services.ErrOutcomeUnknown},
		{"exec timeout", fakeAnswer{err: docker.ErrExecTimeout}, services.ErrOutcomeUnknown},
		{"still running", fakeAnswer{err: docker.ErrExecStillRunning}, services.ErrOutcomeUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(ctx, &fakeExec{answers: []fakeAnswer{tc.answer}}, "op", "SELECT 1;")
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if strings.Contains(err.Error(), testPassword) {
				t.Fatal("error leaks the engine output")
			}
		})
	}
	_, err := run(ctx, &fakeExec{answers: []fakeAnswer{failWith("42501", leak)}}, "op", "SELECT 1;")
	var ee *services.EngineError
	if !errors.As(err, &ee) || ee.Detail != "sqlstate 42501" || strings.Contains(err.Error(), testPassword) {
		t.Fatalf("err = %v", err)
	}
}

func TestProvisionCompensation(t *testing.T) {
	ctx := context.Background()
	// The database fails after the role was created: the role is dropped.
	x := &fakeExec{answers: []fakeAnswer{{}, failWith("53100", "")}}
	if err := (provisioner{}).Provision(ctx, services.Target{Executor: x}, "shop", testPassword); err == nil {
		t.Fatal("expected an error")
	}
	if len(x.reqs) != 3 || !strings.Contains(string(x.reqs[2].Stdin), `DROP ROLE IF EXISTS "shop";`) {
		t.Fatalf("role not dropped after a failed database creation: %d requests", len(x.reqs))
	}
	// The role already exists: nothing is dropped (it is not ours).
	x = &fakeExec{answers: []fakeAnswer{failWith("42710", "")}}
	if err := (provisioner{}).Provision(ctx, services.Target{Executor: x}, "shop", testPassword); !errors.Is(err, services.ErrResourceExists) {
		t.Fatalf("err = %v", err)
	}
	if len(x.reqs) != 1 {
		t.Fatalf("an existing role must never be dropped: %d requests", len(x.reqs))
	}
	// Unknown outcome of the database creation, database absent: no
	// compensation (the role stays listed), outcome reported unknown.
	x = &fakeExec{answers: []fakeAnswer{{}, {err: docker.ErrExecTimeout}, failWith("3D000", "")}}
	if err := (provisioner{}).Provision(ctx, services.Target{Executor: x}, "shop", testPassword); !errors.Is(err, services.ErrOutcomeUnknown) {
		t.Fatalf("err = %v", err)
	}
	if len(x.reqs) != 3 || !strings.Contains(string(x.reqs[2].Stdin), "ALLOW_CONNECTIONS true") || strings.Contains(string(x.reqs[2].Stdin), "DROP") {
		t.Fatalf("expected a single attempt to finish the database: %d requests", len(x.reqs))
	}
	// Unknown outcome, database present: it is finished and the call succeeds.
	x = &fakeExec{answers: []fakeAnswer{{}, {err: docker.ErrExecTimeout}, {}}}
	if err := (provisioner{}).Provision(ctx, services.Target{Executor: x}, "shop", testPassword); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestConnectionInfo(t *testing.T) {
	creds := New().ConnectionInfo(services.ConnParams{Host: "svc-postgres-16", Port: 5432, Name: "shop", Password: testPassword})
	got := map[string]services.Credential{}
	for _, c := range creds {
		got[c.Key] = c
	}
	if got["uri"].Value != "postgresql://shop:"+testPassword+"@svc-postgres-16:5432/shop" || !got["uri"].Secret || !got["password"].Secret || got["host"].Secret {
		t.Fatalf("creds = %+v", creds)
	}
}

// --- compose ---

func TestRenderGolden(t *testing.T) {
	e := New()
	cfg := services.RenderConfig{DataDir: "/data/services", Networks: servicestest.Networks()}
	for _, v := range []string{"16", "18"} {
		t.Run(v, func(t *testing.T) {
			var version services.Version
			for _, c := range e.Catalog() {
				if c.Version == v {
					version = c
				}
			}
			out, err := services.Render(e, services.InstanceRef{Engine: "postgres", Version: v}, version, nil, []string{"services"}, cfg)
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", "compose-postgres-"+v+".golden.yaml")
			if *update {
				os.MkdirAll("testdata", 0o755)
				os.WriteFile(golden, out, 0o644)
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update)", err)
			}
			if string(out) != string(want) {
				t.Fatalf("compose differs from %s:\n%s", golden, out)
			}
			for _, forbidden := range []string{"ports:", "privileged", "network_mode", "POSTGRES_PASSWORD:", "pid:"} {
				if strings.Contains(string(out), forbidden) {
					t.Fatalf("compose must not contain %q", forbidden)
				}
			}
		})
	}
}

// The command is killed inside the container before the exec deadline, and
// the SQL timeouts end statements before the kill.
func TestTimeoutsAreNested(t *testing.T) {
	kill, err := time.ParseDuration(killAfterSeconds + "s")
	if err != nil {
		t.Fatal(err)
	}
	statement := 20 * time.Second
	if !strings.Contains(preamble, "statement_timeout = '20s'") || !(statement < kill && kill < execTimeout) {
		t.Fatalf("statement %s < kill %s < exec %s must hold", statement, kill, execTimeout)
	}
}

func TestTruncatedOutputIsAnError(t *testing.T) {
	x := &fakeExec{answers: []fakeAnswer{{res: docker.ExecResult{Stdout: []byte("73686f70:1\n"), Truncated: true}}}}
	if _, err := (provisioner{}).List(context.Background(), services.Target{Executor: x}); err == nil {
		t.Fatal("a truncated catalog could make an instance look empty")
	}
}

func TestListSQLCoversEveryUserObject(t *testing.T) {
	for _, want := range []string{
		"d.datname NOT IN ('postgres', 'template0', 'template1')",              // templates made by owners are listed
		"NOT EXISTS (SELECT 1 FROM pg_database d WHERE d.datname = r.rolname)", // orphan roles
		"shobj_description(r.oid, 'pg_authid') = 'loch-managed'",
	} {
		if !strings.Contains(listSQL, want) {
			t.Fatalf("listSQL must contain %q", want)
		}
	}
	if strings.Contains(listSQL, "datistemplate") {
		t.Fatal("templates must not be hidden")
	}
}

func TestQuoteNameLiteral(t *testing.T) {
	if quoteNameLiteral("shop_1") != "'shop_1'" {
		t.Fatal("quoteNameLiteral")
	}
	for _, n := range hostileNames {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("quoteNameLiteral(%q) must panic", n)
				}
			}()
			quoteNameLiteral(n)
		}()
	}
}

func TestValidateNameReservesSystemNames(t *testing.T) {
	v := services.Version{Version: "17"}
	for _, n := range []string{"postgres", "template0", "template1", "public", "information_schema", "pg_shop", "replicator"} {
		if (provisioner{}).ValidateName(v, n) == nil {
			t.Errorf("%s must be reserved", n)
		}
	}
	if (provisioner{}).ValidateName(v, "mysql_events") != nil {
		t.Fatal("names reserved by other engines are free here")
	}
}

func TestLegacyAuth(t *testing.T) {
	// md5 of testPassword + "shop", as PostgreSQL computes it.
	v, err := md5Verifier(testPassword, "shop")
	if err != nil || !regexp.MustCompile(`\Amd5[0-9a-f]{32}\z`).MatchString(v) || strings.Contains(v, testPassword) {
		t.Fatalf("%q %v", v, err)
	}
	if other, _ := md5Verifier(testPassword, "blog"); other == v {
		t.Fatal("the md5 verifier is salted with the role name")
	}
	if _, err := md5Verifier("chosen", "shop"); err == nil {
		t.Fatal("only generated passwords")
	}
	x := &fakeExec{}
	if err := (provisioner{}).ProvisionLegacy(context.Background(), services.Target{Executor: x}, "shop", testPassword); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(x.reqs[0].Stdin), "PASSWORD '"+v+"'") || strings.Contains(string(x.reqs[0].Stdin), testPassword) {
		t.Fatalf("role created with the md5 verifier only: %s", x.reqs[0].Stdin)
	}
	// The hba method accepts both mechanisms (SCRAM for SCRAM passwords).
	if !strings.Contains(hbaContent, "host all all all md5") || !strings.Contains(hbaTLSContent, "hostssl all all all md5") {
		t.Fatal("hba")
	}
}
