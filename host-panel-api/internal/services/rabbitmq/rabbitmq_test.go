package rabbitmq

import (
	"context"
	"strings"
	"testing"

	"host-panel-api/internal/docker"
	"host-panel-api/internal/services"
)

const testPassword = "Abcdefghijklmnopqrstuvwxyz012345"

type fakeExec struct {
	reqs    []docker.ExecRequest
	answers map[string]string // rabbitmqctl subcommand -> stdout
	failOn  string            // subcommand exiting with 70
}

func (x *fakeExec) Exec(_ context.Context, req docker.ExecRequest) (docker.ExecResult, error) {
	x.reqs = append(x.reqs, req)
	for _, a := range req.Cmd {
		if x.failOn != "" && a == x.failOn {
			return docker.ExecResult{ExitCode: 70}, nil
		}
	}
	for i, a := range req.Cmd {
		if out, ok := x.answers[a]; ok && i > 4 {
			return docker.ExecResult{Stdout: []byte(out)}, nil
		}
	}
	return docker.ExecResult{}, nil
}

func target(x *fakeExec) services.Target { return services.Target{Executor: x} }

func TestRegistryAcceptsEngine(t *testing.T) {
	if _, err := services.NewRegistry([]services.Engine{New()}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionKeepsThePasswordOffTheCommandLine(t *testing.T) {
	x := &fakeExec{}
	if err := (provisioner{}).Provision(context.Background(), target(x), "shop", testPassword); err != nil {
		t.Fatal(err)
	}
	sawStdin, sawPerms, sawPolicy := false, false, false
	for _, r := range x.reqs {
		line := strings.Join(r.Cmd, " ")
		if strings.Contains(line, testPassword) {
			t.Fatalf("password on the command line: %s", line)
		}
		if r.User != "rabbitmq" || r.Cmd[0] != "timeout" {
			t.Fatalf("bounded commands as the rabbitmq user: %+v", r.Cmd)
		}
		if string(r.Stdin) == testPassword {
			sawStdin = true
			if strings.Contains(line, " -q ") {
				t.Fatal("-q with a password on stdin blocks rabbitmqctl")
			}
		}
		sawPerms = sawPerms || strings.Contains(line, "set_permissions -p shop shop")
		sawPolicy = sawPolicy || strings.Contains(line, "set_operator_policy -p shop")
	}
	if !sawStdin || !sawPerms || !sawPolicy {
		t.Fatalf("steps: %+v", x.reqs)
	}
	if err := (provisioner{}).Provision(context.Background(), target(x), "shop", "chosen"); err == nil {
		t.Fatal("only generated passwords")
	}
}

func TestList(t *testing.T) {
	x := &fakeExec{answers: map[string]string{
		"list_vhosts": `[{"name":"shop","description":"loch-managed"},{"name":"other","description":""},{"name":"/","description":"Default"}]`,
		"list_users":  `[{"user":"shop","tags":["loch-managed"]},{"user":"other","tags":[]},{"user":"lonely","tags":[]}]`,
	}}
	list, err := (provisioner{}).List(context.Background(), target(x))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range list {
		got[r.Name] = r.Managed
	}
	if len(got) != 4 || !got["shop"] || got["other"] || got["/"] || got["lonely"] {
		t.Fatalf("list = %+v", list)
	}
	x.answers["list_vhosts"] = "not json"
	if _, err := (provisioner{}).List(context.Background(), target(x)); err == nil {
		t.Fatal("unexpected output must fail (never an empty list)")
	}
}

func TestNames(t *testing.T) {
	for _, n := range []string{"guest", "amqbad"} {
		if (provisioner{}).ValidateName(services.Version{}, n) == nil {
			t.Fatalf("%s must be reserved", n)
		}
	}
}

func TestProvisionCleansUpAHalfCreatedResource(t *testing.T) {
	x := &fakeExec{failOn: "set_user_tags"}
	if err := (provisioner{}).Provision(context.Background(), target(x), "shop", testPassword); err == nil {
		t.Fatal("the failure must be reported")
	}
	var cleanup []string
	for _, r := range x.reqs {
		line := strings.Join(r.Cmd, " ")
		if strings.Contains(line, "delete_user shop") || strings.Contains(line, "delete_vhost shop") {
			cleanup = append(cleanup, line)
		}
	}
	if len(cleanup) != 2 {
		t.Fatalf("a half-created resource must be removed: %v", cleanup)
	}
}
