//go:build integration

package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// TestPublishedTLS: published on a public address with SERVICES_TLS=auto,
// TLS is required from every client, or only offered with
// SERVICES_TLS_OPTIONAL.
func TestPublishedTLS(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	t.Run("required", func(t *testing.T) { publishedTLS(t, false, 25418) })
	t.Run("optional", func(t *testing.T) { publishedTLS(t, true, 25419) })
}

func publishedTLS(t *testing.T, optional bool, port int) {
	ctx := context.Background()
	version := "18"
	root, _ := os.MkdirTemp(os.Getenv("HOME"), ".loch-services-it-")
	root, _ = filepath.EvalSymlinks(root)
	t.Cleanup(func() { os.RemoveAll(root) })
	stacks, data := filepath.Join(root, "compose"), filepath.Join(root, "services")
	os.Mkdir(stacks, 0o755)
	os.Mkdir(data, 0o755)
	suffix := make([]byte, 4)
	rand.Read(suffix)
	network := "loch-services-it-" + hex.EncodeToString(suffix)
	exec.Command("docker", "network", "create", "--internal", "--label", "sh.loch.managed=true", network).Run()
	t.Cleanup(func() { exec.Command("docker", "network", "rm", network).Run() })

	reg, err := services.NewRegistry([]services.Engine{New()}, nil, map[string][]string{"postgres": {version}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	key, project := "postgres-"+version, "svc-postgres-"+version
	client, err := docker.NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	logs := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	m := services.NewManager(services.Config{
		StacksDir: stacks, Render: services.RenderConfig{DataDir: data, Networks: servicestest.Registry(t, client, network)},
		Exposure: servicestest.AllInterfaces(), TLS: services.TLSAuto,
	}, reg, composekit.ExecRunner{Files: []string{composekit.DefaultComposeFile, services.OverrideFile}}, client, logger, logger, nil)
	dir := filepath.Join(stacks, project)
	t.Cleanup(func() {
		exec.Command("docker", "compose", "--project-name", project, "--project-directory", dir,
			"-f", filepath.Join(dir, "compose.yaml"), "-f", filepath.Join(dir, services.OverrideFile), "down").Run()
	})
	res, err := m.Create(ctx, services.CreateRequest{Engine: "postgres", Version: version, Name: "shop"}, "it")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, logs)
	}
	servicestest.Expose(t, m, key, port, optional)
	var pw string
	for _, c := range res.Credentials {
		if c.Key == "password" {
			pw = c.Value
		}
	}
	connect := func(sslmode string) error {
		uri := "postgresql://shop:" + pw + "@" + project + ":5432/shop?sslmode=" + sslmode
		out, err := exec.Command("docker", "run", "--rm", "--network", network, "mirror.gcr.io/library/postgres:"+version, "psql", uri, "-Atc", "SELECT 1").CombinedOutput()
		if err != nil {
			t.Logf("sslmode=%s: %s", sslmode, strings.TrimSpace(string(out)))
		}
		return err
	}
	if err := connect("require"); err != nil {
		t.Fatal("a TLS client must connect")
	}
	if err := connect("disable"); (err == nil) != optional {
		t.Fatalf("plain client: optional=%v, error=%v", optional, err)
	}
	inst, _ := m.Instances(ctx)
	if len(inst) != 1 || inst[0].Exposure == nil || !inst[0].Exposure.TLS || inst[0].Exposure.TLSOptional != optional {
		t.Fatalf("exposure shown: %+v", inst)
	}
}
