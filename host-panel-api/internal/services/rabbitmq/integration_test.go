//go:build integration

package rabbitmq

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

const clientImage = "mirror.gcr.io/pivotalrabbitmq/perf-test:latest"

func TestEndToEnd(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	for _, v := range []string{"4.4", "4.2", "4.0"} {
		t.Run(v, func(t *testing.T) { endToEnd(t, v, false) })
	}
	t.Run("4.2-tls", func(t *testing.T) { endToEnd(t, "4.2", true) })
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
	network := "loch-services-it-rabbit"
	exec.Command("docker", "network", "create", "--internal", "--label", "sh.loch.managed=true", network).Run()
	t.Cleanup(func() { exec.Command("docker", "network", "rm", network).Run() })

	reg, err := services.NewRegistry([]services.Engine{New()}, nil, map[string][]string{"rabbitmq": {version}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := docker.NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	logs := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	ref := services.InstanceRef{Engine: "rabbitmq", Version: version}
	cfg := services.Config{StacksDir: stacks, Render: services.RenderConfig{DataDir: data, Networks: servicestest.Registry(t, client, network)}}
	if tls {
		cfg.Exposure = servicestest.AllInterfaces()
	}
	m := services.NewManager(cfg, reg, composekit.ExecRunner{Files: []string{composekit.DefaultComposeFile, services.OverrideFile}}, client, logger, logger, nil)
	project, dir := ref.Project(), filepath.Join(stacks, ref.Project())
	t.Cleanup(func() {
		exec.Command("docker", "compose", "--project-name", project, "--project-directory", dir,
			"-f", filepath.Join(dir, "compose.yaml"), "-f", filepath.Join(dir, services.OverrideFile), "down").Run()
	})

	shop, err := m.Create(ctx, services.CreateRequest{Engine: "rabbitmq", Version: version, Name: "shop"}, "it")
	if err != nil || !shop.InstanceCreated {
		t.Fatalf("create shop: %v\n%s", err, logs.String())
	}
	if tls {
		servicestest.Expose(t, m, ref.Key(), 25672+1, false)
		// The credentials shown after the exposure require TLS.
		creds, err := m.RotatePassword(ctx, ref.Key(), "shop", "it")
		if err != nil {
			t.Fatalf("password: %v\n%s", err, logs.String())
		}
		shop.Credentials = creds
	}
	blog, err := m.Create(ctx, services.CreateRequest{Engine: "rabbitmq", Version: version, Name: "blog"}, "it")
	if err != nil {
		t.Fatalf("create blog: %v\n%s", err, logs.String())
	}
	pw, blogPW := credential(shop.Credentials, "password"), credential(blog.Credentials, "password")
	scheme := "amqp"
	if tls {
		scheme = "amqps"
		if !strings.HasPrefix(credential(shop.Credentials, "uri"), "amqps://") {
			t.Fatal("TLS required: amqps:// expected")
		}
	}
	// amqp exchanges a few messages as a site would (perf-test exits 0
	// when every message got through).
	amqp := func(uri string) (string, error) {
		out, err := exec.Command("docker", "run", "--rm", "--network", network, clientImage,
			"--uri", uri, "-x", "1", "-y", "1", "-C", "5", "-D", "5", "-z", "20").CombinedOutput()
		return string(out), err
	}
	uri := func(user, pass, vhost string) string {
		return scheme + "://" + user + ":" + pass + "@" + project + ":5672/" + vhost
	}
	if out, err := amqp(uri("shop", pw, "shop")); err != nil {
		t.Fatalf("tenant on its vhost: %v\n%s\n%s", err, out, logs.String())
	}
	if _, err := amqp(uri("shop", pw, "blog")); err == nil {
		t.Fatal("a tenant must not reach another vhost")
	}
	if _, err := amqp(uri("guest", "guest", "shop")); err == nil {
		t.Fatal("guest must not exist")
	}
	if tls {
		if _, err := amqp("amqp://shop:" + pw + "@" + project + ":5672/shop"); err == nil {
			t.Fatal("plain AMQP must be refused once TLS is required")
		}
	}

	exec1 := func(args ...string) string {
		out, _ := exec.Command("docker", append([]string{"exec", "-u", "rabbitmq", project}, args...)...).CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	if users := exec1("rabbitmqctl", "-q", "list_users", "--formatter", "json"); strings.Contains(users, "guest") {
		t.Fatalf("guest user present: %s", users)
	}
	if vhosts := exec1("rabbitmqctl", "-q", "list_vhosts", "--formatter", "json"); strings.Contains(vhosts, `"/"`) {
		t.Fatalf("default vhost present: %s", vhosts)
	}
	if plugins := exec1("rabbitmq-plugins", "list", "-e", "-m"); plugins != "" && !strings.HasPrefix(plugins, "Listing") {
		t.Fatalf("plugins enabled: %s", plugins)
	} else if strings.Count(plugins, "\n") > 0 {
		t.Fatalf("plugins enabled: %s", plugins)
	}
	// Listening sockets: only AMQP outside the loopback (4369 epmd and
	// 25672 distribution on 127.0.0.1).
	tcp := exec1("sh", "-c", "cat /proc/net/tcp /proc/net/tcp6 | awk 'NR>1 && $4==\"0A\" {print $2}'")
	for _, l := range strings.Fields(tcp) {
		addr, port, _ := strings.Cut(l, ":")
		// 127.0.0.0/8 (Docker DNS included) and ::1.
		loopback := (len(addr) == 8 && strings.HasSuffix(addr, "7F")) || addr == "00000000000000000000000001000000"
		if port != "1628" && !loopback {
			t.Fatalf("port %s listens outside the loopback: %s", port, tcp)
		}
	}

	creds, err := m.RotatePassword(ctx, ref.Key(), "shop", "it")
	if err != nil {
		t.Fatalf("rotate: %v\n%s", err, logs.String())
	}
	pw2 := credential(creds, "password")
	if _, err := amqp(uri("shop", pw, "shop")); err == nil {
		t.Fatal("old password still accepted")
	}
	if out, err := amqp(uri("shop", pw2, "shop")); err != nil {
		t.Fatalf("new password refused: %v %s", err, out)
	}

	if _, err := m.Delete(ctx, ref.Key(), "shop", "it"); err != nil {
		t.Fatalf("delete shop: %v\n%s", err, logs.String())
	}
	res, err := m.Delete(ctx, ref.Key(), "blog", "it")
	if err != nil || !res.InstanceRetired {
		t.Fatalf("delete blog (last): %+v %v\n%s", res, err, logs.String())
	}
	for _, secret := range []string{pw, pw2, blogPW} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("password in the logs")
		}
	}
}
