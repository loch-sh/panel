//go:build integration

package mysqlfamily

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
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

// TestPublishedPortRequiresTLS: published on every address with TLS
// required (require_secure_transport), clear-text connections are refused,
// the admin commands (local socket) keep working.
func TestPublishedPortRequiresTLS(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	for _, c := range []struct {
		e              *Engine
		version        string
		port           int
		tlsArgs, plain []string
	}{
		{NewMariaDB(), "11.8", 13318, []string{"--ssl", "--disable-ssl-verify-server-cert"}, []string{"--skip-ssl"}},
		{NewMySQL(), "8.4", 13384, []string{"--ssl-mode=REQUIRED"}, []string{"--ssl-mode=DISABLED"}},
	} {
		t.Run(c.e.ID(), func(t *testing.T) { publishedTLS(t, c.e, c.version, c.port, c.tlsArgs, c.plain) })
	}
}

func publishedTLS(t *testing.T, e *Engine, version string, port int, tlsArgs, plainArgs []string) {
	ctx := context.Background()
	root, _ := os.MkdirTemp(os.Getenv("HOME"), ".loch-services-it-")
	root, _ = filepath.EvalSymlinks(root)
	t.Cleanup(func() { os.RemoveAll(root) })
	stacks, data := filepath.Join(root, "compose"), filepath.Join(root, "services")
	os.Mkdir(stacks, 0o755)
	os.Mkdir(data, 0o755)
	network := "loch-services-it-tls-" + e.ID()
	exec.Command("docker", "network", "create", "--internal", "--label", "sh.loch.managed=true", network).Run()
	t.Cleanup(func() { exec.Command("docker", "network", "rm", network).Run() })

	reg, _ := services.NewRegistry([]services.Engine{e}, nil, map[string][]string{e.ID(): {version}}, nil)
	client, err := docker.NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	logs := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	ref := services.InstanceRef{Engine: e.ID(), Version: version}
	m := services.NewManager(services.Config{
		StacksDir: stacks, Render: services.RenderConfig{DataDir: data, Networks: servicestest.Registry(t, client, network)},
		Exposure: servicestest.AllInterfaces(),
	}, reg, composekit.ExecRunner{Files: []string{composekit.DefaultComposeFile, services.OverrideFile}}, client, logger, logger, nil)
	project := ref.Project()
	t.Cleanup(func() {
		exec.Command("docker", "compose", "--project-name", project, "--project-directory", filepath.Join(stacks, project),
			"-f", filepath.Join(stacks, project, "compose.yaml"), "-f", filepath.Join(stacks, project, services.OverrideFile), "down").Run()
	})
	if _, err := m.Create(ctx, services.CreateRequest{Engine: e.ID(), Version: version, Name: "shop"}, "it"); err != nil {
		t.Fatalf("create: %v\n%s", err, logs)
	}
	servicestest.Expose(t, m, ref.Key(), port, false)
	creds, err := m.RotatePassword(ctx, ref.Key(), "shop", "it")
	if err != nil {
		t.Fatalf("password: %v\n%s", err, logs)
	}
	res := &services.CreatedResource{Credentials: creds}
	var password string
	for _, c := range res.Credentials {
		if c.Key == "password" {
			password = c.Value
		}
	}
	d := dialectFor(e.flavor, version)
	sql := func(netArgs []string, host string, port string, extra []string) error {
		args := append([]string{"run", "--rm"}, netArgs...)
		args = append(args, "-e", "MYSQL_PWD="+password, e.VersionOf(version, version).Image)
		args = append(args, d.client, "-h", host, "-P", port, "-ushop", "shop")
		args = append(args, extra...)
		out, err := exec.Command("docker", append(args, "-e", "SELECT 1")...).CombinedOutput()
		if err != nil {
			t.Logf("%s %v: %s", host, extra, strings.TrimSpace(string(out)))
		}
		return err
	}
	hostNet := []string{"--add-host", "host.docker.internal:host-gateway"}
	published := strings.TrimPrefix(netip.AddrPortFrom(netip.MustParseAddr("0.0.0.0"), uint16(port)).String(), "0.0.0.0:")
	if err := sql(hostNet, "host.docker.internal", published, tlsArgs); err != nil {
		t.Fatal("TLS connection through the published port refused")
	}
	if err := sql(hostNet, "host.docker.internal", published, plainArgs); err == nil {
		t.Fatal("clear-text connection accepted on the published port")
	}
	if err := sql([]string{"--network", network}, project, "3306", plainArgs); err == nil {
		t.Fatal("clear-text connection accepted on the shared network")
	}
	// Admin commands go through the local socket: rotation still works.
	if _, err := m.RotatePassword(ctx, ref.Key(), "shop", "it"); err != nil {
		t.Fatalf("rotate: %v\n%s", err, logs)
	}
	list, err := m.Instances(ctx)
	if err != nil || len(list) != 1 || !list[0].Ready || list[0].Exposure == nil || !list[0].Exposure.TLS {
		t.Fatalf("instances = %+v %v\n%s", list, err, logs)
	}
}
