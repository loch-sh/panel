//go:build integration

package postgres

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
	"host-panel-api/internal/services"
	"host-panel-api/internal/services/servicestest"
)

// TestPublishedPortRequiresTLS publishes PostgreSQL on every address with
// TLS required: TLS connections through the published port work, clear-text
// ones are refused, from the host and from the shared network alike.
func TestPublishedPortRequiresTLS(t *testing.T) {
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
	network := "loch-services-it-tls"
	exec.Command("docker", "network", "create", "--internal", "--label", "sh.loch.managed=true", network).Run()
	t.Cleanup(func() { exec.Command("docker", "network", "rm", network).Run() })

	version := "18"
	reg, _ := services.NewRegistry([]services.Engine{New()}, nil, map[string][]string{"postgres": {version}}, nil)
	client, err := docker.NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	logs := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	m := services.NewManager(services.Config{
		StacksDir: stacks, Render: services.RenderConfig{DataDir: data, Networks: servicestest.Registry(t, client, network)},
		Exposure: servicestest.AllInterfaces(), PublicHost: "host.docker.internal",
	}, reg, composekit.ExecRunner{Files: []string{composekit.DefaultComposeFile, services.OverrideFile}}, client, logger, logger, nil)
	project := "svc-postgres-18"
	t.Cleanup(func() {
		exec.Command("docker", "compose", "--project-name", project, "--project-directory", filepath.Join(stacks, project),
			"-f", filepath.Join(stacks, project, "compose.yaml"), "-f", filepath.Join(stacks, project, services.OverrideFile), "down").Run()
	})

	if _, err := m.Create(ctx, services.CreateRequest{Engine: "postgres", Version: version, Name: "shop"}, "it"); err != nil {
		t.Fatalf("create: %v\n%s", err, logs)
	}
	servicestest.Expose(t, m, "postgres-18", 15418, false)
	creds, err := m.RotatePassword(ctx, "postgres-18", "shop", "it")
	if err != nil {
		t.Fatalf("password: %v\n%s", err, logs)
	}
	res := &services.CreatedResource{Credentials: creds}
	var password, external string
	for _, c := range res.Credentials {
		switch c.Key {
		case "password":
			password = c.Value
		case "external":
			external = c.Value
		}
	}
	if external != "host.docker.internal:15418" {
		t.Fatalf("external = %q", external)
	}
	psql := func(netArgs []string, host, port, sslmode string) error {
		args := append([]string{"run", "--rm"}, netArgs...)
		args = append(args, "-e", "PGPASSWORD="+password, "postgres:"+version, "psql",
			"host="+host+" port="+port+" dbname=shop user=shop sslmode="+sslmode, "-Atc", "SELECT 1")
		out, err := exec.Command("docker", args...).CombinedOutput()
		if err != nil {
			t.Logf("psql %s %s: %s", host, sslmode, strings.TrimSpace(string(out)))
		}
		return err
	}
	hostNet := []string{"--add-host", "host.docker.internal:host-gateway"}
	if err := psql(hostNet, "host.docker.internal", "15418", "require"); err != nil {
		t.Fatal("TLS connection through the published port refused")
	}
	if err := psql(hostNet, "host.docker.internal", "15418", "disable"); err == nil {
		t.Fatal("clear-text connection accepted on the published port")
	}
	if err := psql([]string{"--network", network}, project, "5432", "disable"); err == nil {
		t.Fatal("clear-text connection accepted on the shared network (TLS is required for every client)")
	}
	if err := psql([]string{"--network", network}, project, "5432", "prefer"); err != nil {
		t.Fatal("sites using the default sslmode=prefer must keep working")
	}
	list, err := m.Instances(ctx)
	if err != nil || len(list) != 1 || !list[0].Ready || list[0].Exposure == nil || !list[0].Exposure.TLS {
		t.Fatalf("instances = %+v %v\n%s", list, err, logs)
	}
}
