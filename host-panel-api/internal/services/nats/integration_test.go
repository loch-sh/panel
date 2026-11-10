//go:build integration

package nats

import (
	"context"
	"encoding/json"
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

const clientImage = "mirror.gcr.io/natsio/nats-box:latest"

func TestEndToEnd(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	for _, v := range []string{"2.15", "2.12", "2.10"} {
		t.Run(v, func(t *testing.T) { endToEnd(t, v, false) })
	}
	t.Run("2.12-tls", func(t *testing.T) { endToEnd(t, "2.12", true) })
}

func credential(creds []services.Credential, key string) string {
	for _, c := range creds {
		if c.Key == key {
			return c.Value
		}
	}
	return ""
}

func endToEnd(t *testing.T, version string, tls bool) {
	ctx := context.Background()
	root, _ := os.MkdirTemp(os.Getenv("HOME"), ".loch-services-it-")
	root, _ = filepath.EvalSymlinks(root)
	t.Cleanup(func() { os.RemoveAll(root) })
	stacks, data := filepath.Join(root, "compose"), filepath.Join(root, "services")
	os.Mkdir(stacks, 0o755)
	os.Mkdir(data, 0o755)
	network := "loch-services-it-nats"
	exec.Command("docker", "network", "create", "--internal", "--label", "sh.loch.managed=true", network).Run()
	t.Cleanup(func() { exec.Command("docker", "network", "rm", network).Run() })

	reg, err := services.NewRegistry([]services.Engine{New()}, nil, map[string][]string{"nats": {version}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := docker.NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	logs := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	cfg := services.Config{StacksDir: stacks, Render: services.RenderConfig{DataDir: data, Networks: servicestest.Registry(t, client, network)}}
	if tls {
		cfg.Exposure = servicestest.AllInterfaces()
	}
	m := services.NewManager(cfg, reg, composekit.ExecRunner{Files: []string{composekit.DefaultComposeFile, services.OverrideFile}}, client, logger, logger, nil)
	project := services.InstanceRef{Engine: "nats", Name: "bus"}.Project()
	dir := filepath.Join(stacks, project)
	t.Cleanup(func() {
		exec.Command("docker", "compose", "--project-name", project, "--project-directory", dir,
			"-f", filepath.Join(dir, "compose.yaml"), "-f", filepath.Join(dir, services.OverrideFile), "down").Run()
	})

	res, err := m.Create(ctx, services.CreateRequest{Engine: "nats", Version: version, Name: "bus"}, "it")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, logs.String())
	}
	servicestest.CheckPinning(t, m, stacks, data, "nats-bus", services.InstanceRef{Engine: "nats", Name: "bus"}.Project())
	if tls {
		servicestest.Expose(t, m, "nats-bus", 24222, false)
	}
	host, pw := credential(res.Credentials, "host"), credential(res.Credentials, "password")
	run := func(user, pass string, args ...string) (string, error) {
		server := "nats://" + host + ":4222"
		full := []string{"run", "--rm", "--network", network}
		if tls {
			server = "tls://" + host + ":4222"
			full = append(full, "-v", filepath.Join(dir, services.CertsDir)+":/certs:ro")
		}
		full = append(full, clientImage, "nats", "--server", server, "--timeout", "5s")
		if tls {
			full = append(full, "--tlsca", "/certs/"+services.CertFile)
		}
		if user != "" {
			full = append(full, "--user", user, "--password", pass)
		}
		out, err := exec.Command("docker", append(full, args...)...).CombinedOutput()
		return string(out), err
	}

	if out, err := run("bus", pw, "pub", "hello", "world"); err != nil {
		t.Fatalf("publish: %v %s\n%s", err, out, logs.String())
	}
	if out, err := run("bus", pw, "stream", "add", "events", "--subjects", "events.>", "--storage", "file", "--defaults"); err != nil {
		t.Fatalf("JetStream: %v %s", err, out)
	}
	if out, err := run("", "", "pub", "hello", "world"); err == nil || !strings.Contains(strings.ToLower(out), "authorization") {
		t.Fatalf("anonymous access must be refused: %v %s", err, out)
	}
	if out, err := run("bus", "wrong", "pub", "hello", "world"); err == nil {
		t.Fatalf("wrong password accepted: %s", out)
	}
	if tls {
		// Plain clients are refused once TLS is required.
		out, err := exec.Command("docker", "run", "--rm", "--network", network, clientImage,
			"nats", "--server", "nats://"+host+":4222", "--timeout", "5s", "--user", "bus", "--password", pw, "pub", "a", "b").CombinedOutput()
		if err == nil {
			t.Fatalf("plain client accepted: %s", out)
		}
	}

	var info []struct {
		Config     struct{ User string }
		HostConfig struct {
			ReadonlyRootfs bool
			CapDrop        []string
		}
	}
	out, _ := exec.Command("docker", "inspect", host).Output()
	if json.Unmarshal(out, &info) != nil || len(info) != 1 || info[0].Config.User != "1000:1000" || !info[0].HostConfig.ReadonlyRootfs || len(info[0].HostConfig.CapDrop) != 1 {
		t.Fatalf("container hardening: %s", out)
	}

	if _, err := m.UpdateOptions(ctx, "nats-bus", map[string]string{"memory": "512", "storage": "5120"}, "it"); err != nil {
		t.Fatalf("update: %v\n%s", err, logs.String())
	}
	if out, err := run("bus", pw, "stream", "info", "events"); err != nil {
		t.Fatalf("stream lost by the update: %v %s", err, out)
	}

	creds, err := m.RotatePassword(ctx, "nats-bus", "bus", "it")
	if err != nil {
		t.Fatalf("rotate: %v\n%s", err, logs.String())
	}
	pw2 := credential(creds, "password")
	if _, err := run("bus", pw, "pub", "hello", "world"); err == nil {
		t.Fatal("old password still accepted")
	}
	if out, err := run("bus", pw2, "stream", "info", "events"); err != nil {
		t.Fatalf("stream lost or new password refused: %v %s", err, out)
	}

	if _, err := m.Delete(ctx, "nats-bus", "bus", "it"); err != nil {
		t.Fatalf("delete: %v\n%s", err, logs.String())
	}
	if err := exec.Command("docker", "inspect", host).Run(); err == nil {
		t.Fatal("container still present")
	}
	if strings.Contains(logs.String(), pw) || strings.Contains(logs.String(), pw2) {
		t.Fatal("password in the logs")
	}
}
