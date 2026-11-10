package mongodb

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"host-panel-api/internal/docker"
	"host-panel-api/internal/services"
	"host-panel-api/internal/services/servicestest"
)

var update = flag.Bool("update", false, "rewrite golden files")

const testPassword = "Abcdefghijklmnopqrstuvwxyz012345"

type fakeExec struct {
	reqs    []docker.ExecRequest
	answers []docker.ExecResult
}

func (f *fakeExec) Exec(_ context.Context, req docker.ExecRequest) (docker.ExecResult, error) {
	f.reqs = append(f.reqs, req)
	if len(f.answers) == 0 {
		return docker.ExecResult{Stdout: []byte(resultPrefix + "{}\n")}, nil
	}
	a := f.answers[0]
	f.answers = f.answers[1:]
	return a, nil
}

func target(x *fakeExec) services.Target {
	return services.Target{Version: services.Version{Version: "8.0"}, Executor: x}
}

func TestQuotingRefusesHostileInput(t *testing.T) {
	for _, s := range []string{"", "a'b", `a"b`, "a\\b", "a b", "a;b", "A", "é", "x'); db.dropDatabase(); ('", strings.Repeat("a", 33)} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("js(%q) must panic", s)
				}
			}()
			js(s)
		}()
	}
	if js("shop_1") != `"shop_1"` || js(testPassword) != `"`+testPassword+`"` {
		t.Fatal("js")
	}
}

func TestRequestsCarryNoSecretInArgv(t *testing.T) {
	x := &fakeExec{}
	p := provisioner{}
	if err := p.Provision(context.Background(), target(x), "shop", testPassword); err != nil {
		t.Fatal(err)
	}
	if err := p.SetPassword(context.Background(), target(x), "shop", testPassword); err != nil {
		t.Fatal(err)
	}
	for _, r := range x.reqs {
		cmd := strings.Join(r.Cmd, " ")
		if strings.Contains(cmd, testPassword) || !strings.HasPrefix(cmd, "timeout -s KILL "+killAfterSeconds+" sh -c ") {
			t.Fatalf("cmd = %s", cmd)
		}
		if strings.Count(string(r.Stdin), "\n") != 1 {
			t.Fatal("the script must be a single line (the shell evaluates stdin line by line)")
		}
	}
	create := string(x.reqs[0].Stdin)
	if !strings.Contains(create, `roles: [{role: 'readWrite', db: "shop"}, {role: 'dbAdmin', db: "shop"}]`) || !strings.Contains(create, managedMark) ||
		strings.Contains(create, "dbOwner") || strings.Contains(create, "userAdmin") {
		t.Fatalf("tenants must not administer users: %s", create)
	}
	// Rotation recreates the user: dropUser closes the authenticated connections.
	if rot := string(x.reqs[1].Stdin); !strings.Contains(rot, "dropUser") || !strings.Contains(rot, "createUser") || strings.Contains(rot, "updateUser") {
		t.Fatalf("rotation = %s", rot)
	}
	if err := p.Provision(context.Background(), target(x), "shop", "short"); err == nil {
		t.Fatal("foreign password accepted")
	}
}

func TestResultParsingAndErrors(t *testing.T) {
	ctx := context.Background()
	noisy := docker.ExecResult{Stdout: []byte("Current Mongosh Log ID: x\n> " + resultPrefix + `{"resources":[{"name":"blog","managed":true},{"name":"Legacy","managed":true},{"name":"admin.ci","managed":false}]}` + "\n")}
	list, err := (provisioner{}).List(ctx, target(&fakeExec{answers: []docker.ExecResult{noisy}}))
	if err != nil || len(list) != 3 || !list[0].Managed || list[1].Managed || list[2].Managed {
		t.Fatalf("list=%+v err=%v (names the panel would not create are never managed)", list, err)
	}
	cases := map[int]error{codeDuplicateUser: services.ErrResourceExists, codeUserNotFound: services.ErrResourceNotFound}
	for code, want := range cases {
		res := docker.ExecResult{ExitCode: 3, Stdout: []byte(resultPrefix + `{"error":` + strconv.Itoa(code) + "}\n"), Stderr: []byte(testPassword)}
		_, err := run(ctx, &fakeExec{answers: []docker.ExecResult{res}}, "op", ok(`{}`))
		if !errors.Is(err, want) || strings.Contains(err.Error(), testPassword) {
			t.Fatalf("%d: %v", code, err)
		}
	}
	for _, res := range []docker.ExecResult{{ExitCode: 1}, {ExitCode: 0, Stdout: []byte(resultPrefix + "{broken")}, {Truncated: true}} {
		if _, err := run(ctx, &fakeExec{answers: []docker.ExecResult{res}}, "op", ok(`{}`)); err == nil {
			t.Fatalf("%+v must fail", res)
		}
	}
	if _, err := run(ctx, &fakeExec{answers: []docker.ExecResult{{ExitCode: 137}}}, "op", ok(`{}`)); !errors.Is(err, services.ErrOutcomeUnknown) {
		t.Fatalf("killed: %v", err)
	}
}

func TestValidateName(t *testing.T) {
	v := services.Version{Version: "8.0"}
	for _, n := range []string{"admin", "config", "local", "system_x"} {
		if (provisioner{}).ValidateName(v, n) == nil {
			t.Errorf("%s must be reserved", n)
		}
	}
	if (provisioner{}).ValidateName(v, "shop") != nil {
		t.Fatal("shop refused")
	}
}

func TestRenderGolden(t *testing.T) {
	e := New()
	v := e.Catalog()[0]
	out, err := services.Render(e, services.InstanceRef{Engine: e.ID(), Version: v.Version}, v, nil, []string{"services"}, services.RenderConfig{DataDir: "/data/services", Networks: servicestest.Networks()})
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "compose-mongodb-8-0.golden.yaml")
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
		t.Fatalf("golden differs (run with -update):\n%s", out)
	}
	if strings.Contains(string(out), "ROOT_PASSWORD:") || strings.Contains(string(out), "ports:") {
		t.Fatal("no secret, no published port in compose.yaml")
	}
	if _, err := services.NewRegistry([]services.Engine{e}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestListIsSafe(t *testing.T) {
	// "constructor" is a valid name: the script must not use a prototype object.
	if !strings.Contains(listBody, "Object.create(null)") || strings.Contains(listBody, " in out") {
		t.Fatal("listBody must not use an object with a prototype")
	}
	ctx := context.Background()
	for _, res := range []docker.ExecResult{
		{Stdout: []byte(resultPrefix + "{}\n")}, // no list: never an empty list by default
		{Stdout: []byte(resultPrefix + `{"resources":[]}` + "\n" + resultPrefix + `{"resources":[]}` + "\n")}, // two results
	} {
		if _, err := (provisioner{}).List(ctx, target(&fakeExec{answers: []docker.ExecResult{res}})); err == nil {
			t.Fatalf("%q must fail", res.Stdout)
		}
	}
	if list, err := (provisioner{}).List(ctx, target(&fakeExec{answers: []docker.ExecResult{{Stdout: []byte(resultPrefix + `{"resources":[]}`)}}})); err != nil || len(list) != 0 {
		t.Fatalf("empty list: %v %v", list, err)
	}
}

func TestDeprovisionDropsEveryUserOfTheDatabase(t *testing.T) {
	x := &fakeExec{}
	if err := (provisioner{}).Deprovision(context.Background(), target(x), "shop"); err != nil {
		t.Fatal(err)
	}
	body := string(x.reqs[0].Stdin)
	if !strings.Contains(body, "dropAllUsers()") || !strings.Contains(body, "dropAllRoles") || !strings.Contains(body, "dropDatabase") {
		t.Fatalf("drop = %s", body)
	}
}
