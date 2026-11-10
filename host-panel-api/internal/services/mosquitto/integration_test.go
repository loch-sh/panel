//go:build integration

package mosquitto

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

const clientImage = "mirror.gcr.io/library/eclipse-mosquitto:2.0.22"

func credential(creds []services.Credential, key string) string {
	for _, c := range creds {
		if c.Key == key {
			return c.Value
		}
	}
	return ""
}

func TestEndToEnd(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	t.Run("plain", func(t *testing.T) { endToEnd(t, false) })
	t.Run("tls", func(t *testing.T) { endToEnd(t, true) })
}

func endToEnd(t *testing.T, tls bool) {
	ctx := context.Background()
	root, _ := os.MkdirTemp(os.Getenv("HOME"), ".loch-services-it-")
	root, _ = filepath.EvalSymlinks(root)
	t.Cleanup(func() { os.RemoveAll(root) })
	stacks, data := filepath.Join(root, "compose"), filepath.Join(root, "services")
	os.Mkdir(stacks, 0o755)
	os.Mkdir(data, 0o755)
	network := "loch-services-it-mqtt"
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
	cfg := services.Config{StacksDir: stacks, Render: services.RenderConfig{DataDir: data, Networks: servicestest.Registry(t, client, network)}}
	if tls {
		cfg.Exposure = servicestest.AllInterfaces()
		cfg.TLS = services.TLSAuto
	}
	m := services.NewManager(cfg, reg, composekit.ExecRunner{Files: []string{composekit.DefaultComposeFile, services.OverrideFile}}, client, logger, logger, nil)
	project := services.InstanceRef{Engine: "mosquitto", Name: "iot"}.Project()
	dir := filepath.Join(stacks, project)
	t.Cleanup(func() {
		exec.Command("docker", "compose", "--project-name", project, "--project-directory", dir,
			"-f", filepath.Join(dir, "compose.yaml"), "-f", filepath.Join(dir, services.OverrideFile), "down").Run()
	})
	res, err := m.Create(ctx, services.CreateRequest{Engine: "mosquitto", Version: "2.0", Name: "iot"}, "it")
	if err != nil {
		out, _ := exec.Command("docker", "logs", project).CombinedOutput()
		t.Fatalf("create: %v\n%s\ncontainer logs:\n%s", err, logs.String(), out)
	}
	servicestest.CheckPinning(t, m, stacks, data, "mosquitto-iot", project)
	if tls {
		// Published on every interface: TLS is required (from every client).
		servicestest.Expose(t, m, "mosquitto-iot", 21883, false)
	}
	host, pw := credential(res.Credentials, "host"), credential(res.Credentials, "password")
	mqtt := func(tool, user, pass string, args ...string) (string, error) {
		full := []string{"run", "--rm", "--network", network}
		if tls {
			full = append(full, "-v", filepath.Join(dir, services.CertsDir)+":/certs:ro")
		}
		full = append(full, clientImage, tool, "-h", host, "-p", "1883", "-i", "it-"+tool)
		if tls {
			full = append(full, "--cafile", "/certs/"+services.CertFile, "--insecure")
		}
		if user != "" {
			full = append(full, "-u", user, "-P", pass)
		}
		out, err := exec.Command("docker", append(full, args...)...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	// A retained message, read back by another client.
	if out, err := mqtt("mosquitto_pub", "iot", pw, "-t", "sensors/t1", "-m", "21.5", "-r", "-q", "1"); err != nil {
		t.Fatalf("publish: %v %s\n%s", err, out, logs.String())
	}
	if out, err := mqtt("mosquitto_sub", "iot", pw, "-t", "sensors/#", "-C", "1", "-W", "10"); err != nil || out != "21.5" {
		t.Fatalf("subscribe: %v %q", err, out)
	}
	if out, err := mqtt("mosquitto_pub", "", "", "-t", "x", "-m", "y"); err == nil {
		t.Fatalf("anonymous client accepted: %s", out)
	}
	if out, err := mqtt("mosquitto_pub", "iot", "WrongWrongWrongWrongWrongWrong12", "-t", "x", "-m", "y"); err == nil {
		t.Fatalf("wrong password accepted: %s", out)
	}
	if tls {
		out, err := exec.Command("docker", "run", "--rm", "--network", network, clientImage, "mosquitto_pub", "-h", host, "-p", "1883", "-u", "iot", "-P", pw, "-t", "x", "-m", "y").CombinedOutput()
		if err == nil {
			t.Fatalf("plain client accepted on a TLS listener: %s", out)
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
	if json.Unmarshal(out, &info) != nil || len(info) != 1 || info[0].Config.User != "1883:1883" || !info[0].HostConfig.ReadonlyRootfs || len(info[0].HostConfig.CapDrop) != 1 {
		t.Fatalf("container hardening: %s", out)
	}
	// Resize: recreated, the retained message survives (persistence).
	if _, err := m.UpdateOptions(ctx, "mosquitto-iot", map[string]string{"memory": "192"}, "it"); err != nil {
		t.Fatalf("update: %v\n%s", err, logs.String())
	}
	if out, err := mqtt("mosquitto_sub", "iot", pw, "-t", "sensors/#", "-C", "1", "-W", "10"); err != nil || out != "21.5" {
		t.Fatalf("retained message lost by the update: %v %q", err, out)
	}
	creds, err := m.RotatePassword(ctx, "mosquitto-iot", "iot", "it")
	if err != nil {
		t.Fatalf("rotate: %v\n%s", err, logs.String())
	}
	pw2 := credential(creds, "password")
	if _, err := mqtt("mosquitto_pub", "iot", pw, "-t", "x", "-m", "y"); err == nil {
		t.Fatal("old password still accepted")
	}
	if out, err := mqtt("mosquitto_pub", "iot", pw2, "-t", "x", "-m", "y"); err != nil {
		t.Fatalf("new password refused: %v %s", err, out)
	}
	if _, err := m.Delete(ctx, "mosquitto-iot", "iot", "it"); err != nil {
		t.Fatalf("delete: %v\n%s", err, logs.String())
	}
	if strings.Contains(logs.String(), pw) || strings.Contains(logs.String(), pw2) {
		t.Fatal("password in the logs")
	}
}
