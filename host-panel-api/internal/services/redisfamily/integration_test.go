//go:build integration

package redisfamily

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
	"host-panel-api/internal/releases"
	"host-panel-api/internal/services"
	"host-panel-api/internal/services/servicestest"
)

func TestEndToEnd(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	for _, c := range []struct {
		e       *Engine
		version string
	}{{NewRedis(), "8.10"}, {NewRedis(), "8.4"}, {NewRedis(), "7.4"}, {NewValkey(), "9.2"}, {NewValkey(), "9.0"}, {NewValkey(), "7.2"}} {
		t.Run(c.e.ID()+"-"+c.version, func(t *testing.T) { endToEnd(t, c.e, c.version, false) })
	}
	t.Run("redis-8.4-tls", func(t *testing.T) { endToEnd(t, NewRedis(), "8.4", true) })
}

type env struct {
	t       *testing.T
	m       *services.Manager
	network string
	image   string
	cli     string
	logs    *strings.Builder
	tls     bool
	stacks  string
	data    string
}

func newEnv(t *testing.T, e *Engine, version string, tls bool) *env {
	t.Helper()
	root, _ := os.MkdirTemp(os.Getenv("HOME"), ".loch-services-it-")
	root, _ = filepath.EvalSymlinks(root)
	t.Cleanup(func() { os.RemoveAll(root) })
	stacks, data := filepath.Join(root, "compose"), filepath.Join(root, "services")
	os.Mkdir(stacks, 0o755)
	os.Mkdir(data, 0o755)
	network := "loch-services-it-" + e.ID()
	exec.Command("docker", "network", "create", "--internal", "--label", "sh.loch.managed=true", network).Run()
	t.Cleanup(func() { exec.Command("docker", "network", "rm", network).Run() })

	reg, err := services.NewRegistry([]services.Engine{e}, nil, map[string][]string{e.ID(): {version}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := docker.NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	logs := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	// Version discovery (Update, CheckUpdates only: no periodic check).
	cfg := services.Config{StacksDir: stacks, Render: services.RenderConfig{DataDir: data, Networks: servicestest.Registry(t, client, network)}, Releases: releases.New()}
	if tls {
		// Published on a non-loopback address: TLS is required (from every client).
		cfg.Exposure = servicestest.AllInterfaces()
		cfg.TLS = services.TLSAuto
	}
	m := services.NewManager(cfg, reg, composekit.ExecRunner{Files: []string{composekit.DefaultComposeFile, services.OverrideFile}}, client, logger, logger, nil)
	project := services.InstanceRef{Engine: e.ID(), Name: "cache"}.Project()
	t.Cleanup(func() {
		exec.Command("docker", "compose", "--project-name", project, "--project-directory", filepath.Join(stacks, project),
			"-f", filepath.Join(stacks, project, "compose.yaml"), "-f", filepath.Join(stacks, project, services.OverrideFile), "down").Run()
	})
	img, _ := reg.Version(e.ID(), version)
	return &env{t: t, m: m, network: network, image: img.Image, cli: e.cli, logs: logs, tls: tls, stacks: stacks, data: data}
}

// cli runs a command from another container of the shared network, like a site.
func (x *env) run(args ...string) string {
	full := append([]string{"run", "--rm", "--network", x.network, "--entrypoint", x.cli, x.image}, args...)
	out, _ := exec.Command("docker", full...).CombinedOutput()
	return strings.TrimSpace(string(out))
}

func credential(creds []services.Credential, key string) string {
	for _, c := range creds {
		if c.Key == key {
			return c.Value
		}
	}
	return ""
}

func endToEnd(t *testing.T, e *Engine, version string, tls bool) {
	ctx := context.Background()
	x := newEnv(t, e, version, tls)
	res, err := x.m.Create(ctx, services.CreateRequest{Engine: e.ID(), Version: version, Name: "cache", Options: map[string]string{"memory": "128"}}, "it")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, x.logs.String())
	}
	servicestest.CheckPinning(t, x.m, x.stacks, x.data, e.ID()+"-cache", services.InstanceRef{Engine: e.ID(), Name: "cache"}.Project())
	if x.tls {
		servicestest.Expose(t, x.m, e.ID()+"-cache", 26379, false)
		// The credentials shown after the exposure require TLS.
		creds, err := x.m.RotatePassword(ctx, e.ID()+"-cache", "cache", "it")
		if err != nil {
			t.Fatalf("password: %v\n%s", err, x.logs.String())
		}
		res.Credentials = creds
	}
	host := credential(res.Credentials, "host")
	pw := credential(res.Credentials, "password")
	base := []string{"-h", host, "--no-auth-warning"}
	if tls {
		base = append(base, "--tls", "--insecure")
		if !strings.HasPrefix(credential(res.Credentials, "uri"), "rediss://") {
			t.Fatal("TLS required: rediss:// expected")
		}
		if out := x.run("-h", host, "ping"); strings.Contains(out, "PONG") || strings.Contains(out, "NOAUTH") {
			t.Fatalf("plain connections must be refused once TLS is required: %s", out)
		}
	}
	as := func(user, pass string, cmd ...string) string {
		return x.run(append(append(append([]string{}, base...), "--user", user, "--pass", pass), cmd...)...)
	}

	if out := as("cache", pw, "set", "k", "v"); out != "OK" {
		t.Fatalf("tenant write: %q\n%s", out, x.logs.String())
	}
	if out := x.run(append(append([]string{}, base...), "ping")...); !strings.Contains(out, "NOAUTH") {
		t.Fatalf("anonymous access must be refused: %q", out)
	}
	if out := as("default", "", "ping"); !strings.Contains(out, "WRONGPASS") && !strings.Contains(out, "NOAUTH") && !strings.Contains(out, "AUTH failed") {
		t.Fatalf("default user must be disabled: %q", out)
	}
	for _, cmd := range [][]string{{"config", "set", "dir", "/tmp"}, {"replicaof", "10.0.0.1", "6379"}, {"migrate", "10.0.0.1", "6379", "k", "0", "1000"}, {"acl", "list"}, {"module", "list"}, {"restore", "x", "0", "y"}} {
		if out := as("cache", pw, cmd...); !strings.Contains(out, "NOPERM") {
			t.Fatalf("%v must be refused: %q", cmd, out)
		}
	}
	if e.modules && hasModules(services.Version{Version: version}) {
		if out := as("cache", pw, "json.set", "j", "$", "1"); !strings.Contains(out, "NOPERM") {
			t.Fatalf("modules are off by default: %q", out)
		}
	}
	for _, cmd := range [][]string{{"info", "server"}, {"flushdb"}, {"eval", "return 1", "0"}} {
		if out := as("cache", pw, cmd...); strings.Contains(out, "NOPERM") || strings.Contains(out, "ERR") {
			t.Fatalf("%v is allowed: %q", cmd, out)
		}
	}
	as("cache", pw, "set", "k", "v")

	// Container: unprivileged, read-only, limited.
	var info []struct {
		Config     struct{ User string }
		HostConfig struct {
			ReadonlyRootfs bool
			CapDrop        []string
			Memory         int64
		}
	}
	out, _ := exec.Command("docker", "inspect", host).Output()
	if json.Unmarshal(out, &info) != nil || len(info) != 1 || info[0].Config.User != "999:1000" || !info[0].HostConfig.ReadonlyRootfs ||
		len(info[0].HostConfig.CapDrop) != 1 || info[0].HostConfig.Memory != 128<<20 {
		t.Fatalf("container hardening: %s", out)
	}

	// Resize to a custom size (384 MB, not one of the offered values): the
	// container is recreated with the new limit, data is kept.
	if _, err := x.m.UpdateOptions(ctx, e.ID()+"-cache", map[string]string{"memory": "384", "policy": "allkeys-lru"}, "it"); err != nil {
		t.Fatalf("update: %v\n%s", err, x.logs.String())
	}
	out, _ = exec.Command("docker", "inspect", "-f", "{{.HostConfig.Memory}}", host).Output()
	if strings.TrimSpace(string(out)) != "402653184" {
		t.Fatalf("memory limit after the update: %s", out)
	}
	if got := as("cache", pw, "get", "k"); got != "v" {
		t.Fatalf("data lost by the update: %q", got)
	}
	// An option that changes only a configuration file reaches the server.
	if _, err := x.m.UpdateOptions(ctx, e.ID()+"-cache", map[string]string{"scripting": "off"}, "it"); err != nil {
		t.Fatalf("update: %v\n%s", err, x.logs.String())
	}
	if got := as("cache", pw, "eval", "return 1", "0"); !strings.Contains(strings.ToUpper(got), "NOPERM") {
		t.Fatalf("Lua still allowed after scripting=off: %q", got)
	}
	if _, err := x.m.UpdateOptions(ctx, e.ID()+"-cache", map[string]string{"persistence": "none"}, "it"); services.CodeOf(err) != services.CodeOptionImmutable {
		t.Fatalf("persistence is immutable: %v", err)
	}

	// Rotation: the old password stops working, data is kept.
	creds, err := x.m.RotatePassword(ctx, e.ID()+"-cache", "cache", "it")
	if err != nil {
		t.Fatalf("rotate: %v\n%s", err, x.logs.String())
	}
	pw2 := credential(creds, "password")
	if out := as("cache", pw, "ping"); strings.Contains(out, "PONG") {
		t.Fatal("old password still accepted")
	}
	if out := as("cache", pw2, "get", "k"); out != "v" {
		t.Fatalf("data lost or new password refused: %q", out)
	}

	// Deletion: container and data gone.
	if _, err := x.m.Delete(ctx, e.ID()+"-cache", "cache", "it"); err != nil {
		t.Fatalf("delete: %v\n%s", err, x.logs.String())
	}
	if err := exec.Command("docker", "inspect", host).Run(); err == nil {
		t.Fatal("container still present")
	}
	if strings.Contains(x.logs.String(), pw) || strings.Contains(x.logs.String(), pw2) {
		t.Fatal("password in the logs")
	}
}

// An instance edited by hand is deleted without running its files.
func TestPurgeModified(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	ctx := context.Background()
	e := NewValkey()
	x := newEnv(t, e, "9.0", false)
	res, err := x.m.Create(ctx, services.CreateRequest{Engine: e.ID(), Version: "9.0", Name: "cache"}, "it")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, x.logs.String())
	}
	host := credential(res.Credentials, "host")
	inspect, _ := exec.Command("docker", "inspect", "-f", `{{index .Config.Labels "com.docker.compose.project.working_dir"}}`, host).Output()
	dir := strings.TrimSpace(string(inspect))
	compose := filepath.Join(dir, "compose.yaml")
	data, _ := os.ReadFile(compose)
	// An edit that must never run (it would mount the host root).
	os.WriteFile(compose, []byte(strings.Replace(string(data), "volumes:", "volumes:\n      - /:/host", 1)), 0o640)
	if _, err := x.m.RotatePassword(ctx, e.ID()+"-cache", "cache", "it"); services.CodeOf(err) != services.CodeInstanceModified {
		t.Fatalf("edited instance: %v", err)
	}
	if err := x.m.Purge(ctx, e.ID()+"-cache", "it"); err != nil {
		t.Fatalf("purge: %v\n%s", err, x.logs.String())
	}
	if err := exec.Command("docker", "inspect", host).Run(); err == nil {
		t.Fatal("container still present")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("instance directory still present: %v", err)
	}
}

// An update to a newer version, on a real registry: the image is pulled by
// digest (no tag is moved) and the instance runs "repo:tag@digest"; the
// data is kept.
func TestUpdateByDigest(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	ctx := context.Background()
	e := NewValkey()
	x := newEnv(t, e, "8.1", false)
	key := e.ID() + "-cache"
	res, err := x.m.Create(ctx, services.CreateRequest{Engine: e.ID(), Version: "8.1", Name: "cache"}, "it")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, x.logs.String())
	}
	host, pw := credential(res.Credentials, "host"), credential(res.Credentials, "password")
	as := func(cmd ...string) string {
		return x.run(append([]string{"-h", host, "--no-auth-warning", "--user", "cache", "--pass", pw}, cmd...)...)
	}
	if out := as("set", "k", "v"); out != "OK" {
		t.Fatalf("write: %q", out)
	}
	if _, err := x.m.CheckUpdates(ctx); err != nil {
		t.Fatal(err)
	}
	var target string
	list, _ := x.m.Instances(ctx)
	for _, s := range list {
		if s.Key == key && s.Update != nil && len(s.Update.Versions) > 0 {
			target = s.Update.Versions[len(s.Update.Versions)-1].Version // the closest newer version
		}
	}
	if target == "" {
		t.Skip("no newer version discovered (registry unreachable?)")
	}
	up, err := x.m.Update(ctx, key, target, "it")
	if err != nil || !up.Updated {
		t.Fatalf("update to %s: %+v %v\n%s", target, up, err, x.logs.String())
	}
	project := services.InstanceRef{Engine: e.ID(), Name: "cache"}.Project()
	compose, _ := os.ReadFile(filepath.Join(x.stacks, project, services.ComposeFile))
	if !strings.Contains(string(compose), "image: "+up.Image+"@sha256:") {
		t.Fatalf("compose not pinned to %s:\n%s", up.Image, compose)
	}
	if got := as("get", "k"); got != "v" {
		t.Fatalf("data lost by the update: %q", got)
	}
	servicestest.CheckPinning(t, x.m, x.stacks, x.data, key, project)
}
