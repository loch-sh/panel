//go:build integration

package elasticmq

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"host-panel-api/internal/composekit"
	"host-panel-api/internal/docker"
	"host-panel-api/internal/networks"
	"host-panel-api/internal/services"
	"host-panel-api/internal/services/servicestest"
)

const clientImage = "mirror.gcr.io/amazon/aws-cli:2.31.0"

func TestEndToEnd(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	ctx := context.Background()
	root, _ := os.MkdirTemp(os.Getenv("HOME"), ".loch-services-it-")
	root, _ = filepath.EvalSymlinks(root)
	t.Cleanup(func() { os.RemoveAll(root) })
	stacks, data := filepath.Join(root, "compose"), filepath.Join(root, "services")
	os.Mkdir(stacks, 0o755)
	os.Mkdir(data, 0o755)
	network := "loch-services-it-sqs"
	exec.Command("docker", "network", "create", "--internal", "--label", "sh.loch.managed=true", network).Run()
	t.Cleanup(func() { exec.Command("docker", "network", "rm", network).Run() })
	reg, err := services.NewRegistry([]services.Engine{New()}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := docker.NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	logs := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	nets := servicestest.Registry(t, client, network)
	// No authentication: the instance lives on a private network, never on
	// the shared one.
	private, err := nets.Create(ctx, networks.CreateRequest{Name: "itsqs"}, "it")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { exec.Command("docker", "network", "rm", private.DockerName).Run() })
	m := services.NewManager(services.Config{StacksDir: stacks, Render: services.RenderConfig{DataDir: data, Networks: nets}},
		reg, composekit.ExecRunner{Files: []string{composekit.DefaultComposeFile, services.OverrideFile}}, client, logger, logger, nil)
	project := services.InstanceRef{Engine: "elasticmq", Name: "jobs"}.Project()
	dir := filepath.Join(stacks, project)
	t.Cleanup(func() {
		exec.Command("docker", "compose", "--project-name", project, "--project-directory", dir,
			"-f", filepath.Join(dir, "compose.yaml"), "-f", filepath.Join(dir, services.OverrideFile), "down").Run()
	})
	if _, err := m.Create(ctx, services.CreateRequest{Engine: "elasticmq", Version: "1.7", Name: "jobs"}, "it"); services.CodeOf(err) != services.CodeInvalidNetwork {
		t.Fatalf("an engine without authentication never joins the shared network: %v", err)
	}
	res, err := m.Create(ctx, services.CreateRequest{Engine: "elasticmq", Version: "1.7", Name: "jobs", Networks: []string{"itsqs"}}, "it")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, logs.String())
	}
	servicestest.CheckPinning(t, m, stacks, data, "elasticmq-jobs", services.InstanceRef{Engine: "elasticmq", Name: "jobs"}.Project())
	var endpoint string
	for _, c := range res.Credentials {
		if c.Secret {
			t.Fatalf("no secret to show: %+v", c)
		}
		if c.Key == "endpoint" {
			endpoint = c.Value
		}
	}
	sqsOn := func(net string, args ...string) (string, error) {
		full := append([]string{"run", "--rm", "--network", net, "-e", "AWS_ACCESS_KEY_ID=x", "-e", "AWS_SECRET_ACCESS_KEY=x", "-e", "AWS_DEFAULT_REGION=" + Region,
			clientImage, "--endpoint-url", endpoint, "sqs"}, args...)
		out, err := exec.Command("docker", full...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	sqs := func(args ...string) (string, error) { return sqsOn(private.DockerName, args...) }
	// A site on the shared network does not reach it (no DNS, no route).
	if out, err := sqsOn(network, "--cli-connect-timeout", "3", "list-queues"); err == nil {
		t.Fatalf("reachable from the shared network: %s", out)
	}
	if out, err := sqs("create-queue", "--queue-name", "emails"); err != nil || !strings.Contains(out, project+":9324/000000000000/emails") {
		t.Fatalf("create queue: %v %s\n%s", err, out, logs.String())
	}
	queue := endpoint + "/000000000000/emails"
	if out, err := sqs("send-message", "--queue-url", queue, "--message-body", "hello"); err != nil {
		t.Fatalf("send: %v %s", err, out)
	}
	// Resize: recreated, the queue and its message are kept (persisted).
	if _, err := m.UpdateOptions(ctx, "elasticmq-jobs", map[string]string{"memory": "384"}, "it"); err != nil {
		t.Fatalf("update: %v\n%s", err, logs.String())
	}
	if out, err := sqs("receive-message", "--queue-url", queue, "--wait-time-seconds", "5", "--query", "Messages[0].Body", "--output", "text"); err != nil || out != "hello" {
		t.Fatalf("message lost by the update: %v %q", err, out)
	}
	if out, _ := exec.Command("docker", "inspect", "-f", "{{.Config.User}} {{.HostConfig.ReadonlyRootfs}}", project).Output(); strings.TrimSpace(string(out)) != "10002:10002 true" {
		t.Fatalf("hardening: %s", out)
	}
	if _, err := m.RotatePassword(ctx, "elasticmq-jobs", "jobs", "it"); services.CodeOf(err) != services.CodeNoCredentials {
		t.Fatalf("no password to rotate: %v", err)
	}
	if _, err := m.Delete(ctx, "elasticmq-jobs", "jobs", "it"); err != nil {
		t.Fatalf("delete: %v\n%s", err, logs.String())
	}
	if err := exec.Command("docker", "inspect", project).Run(); err == nil {
		t.Fatal("container still present")
	}
}
