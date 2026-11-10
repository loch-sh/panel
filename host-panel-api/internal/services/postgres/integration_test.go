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

// TestEndToEnd drives the real Manager against real PostgreSQL containers:
// docker compose up, provisioning through docker exec, connections from a
// client container on the shared network, retirement.
func TestEndToEnd(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	for _, version := range []string{"15", "16", "18"} {
		t.Run(version, func(t *testing.T) { endToEnd(t, version) })
	}
}

func endToEnd(t *testing.T, version string) {
	ctx := context.Background()
	// Under $HOME: shared with Docker Desktop at the same path. Symlinks
	// resolved: compose and the daemon report resolved paths.
	root, err := os.MkdirTemp(os.Getenv("HOME"), ".loch-services-it-")
	if err != nil {
		t.Fatal(err)
	}
	root, _ = filepath.EvalSymlinks(root)
	t.Cleanup(func() { os.RemoveAll(root) })
	stacks, data := filepath.Join(root, "compose"), filepath.Join(root, "services")
	os.Mkdir(stacks, 0o755)
	os.Mkdir(data, 0o755)

	suffix := make([]byte, 4)
	rand.Read(suffix)
	network := "loch-services-it-" + hex.EncodeToString(suffix)
	if out, err := exec.Command("docker", "network", "create", "--internal", "--label", "sh.loch.managed=true", network).CombinedOutput(); err != nil {
		t.Fatalf("network: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "network", "rm", network).Run() })

	reg, err := services.NewRegistry([]services.Engine{New()}, nil, map[string][]string{"postgres": {version}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := docker.NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	logs := &strings.Builder{}
	m := services.NewManager(services.Config{
		StacksDir: stacks,
		Render:    services.RenderConfig{DataDir: data, Networks: servicestest.Registry(t, client, network)},
	}, reg, composekit.ExecRunner{}, client, slog.New(slog.NewTextHandler(logs, nil)), slog.New(slog.NewTextHandler(logs, nil)), nil)
	key := "postgres-" + version
	project := "svc-" + key
	t.Cleanup(func() {
		exec.Command("docker", "compose", "--project-name", project, "--project-directory", filepath.Join(stacks, project), "down").Run()
	})

	shop, err := m.Create(ctx, services.CreateRequest{Engine: "postgres", Version: version, Name: "shop"}, "it")
	if err != nil {
		t.Fatalf("create shop: %v\n%s", err, logs)
	}
	if !shop.InstanceCreated {
		t.Fatal("instance must be created")
	}
	blog, err := m.Create(ctx, services.CreateRequest{Engine: "postgres", Version: version, Name: "blog"}, "it")
	if err != nil || blog.InstanceCreated {
		t.Fatalf("create blog: %v %+v", err, blog)
	}
	image := "mirror.gcr.io/library/postgres:" + version
	psql := func(uri, sql string) error {
		out, err := exec.Command("docker", "run", "--rm", "--network", network, image, "psql", uri, "-Atc", sql).CombinedOutput()
		if err != nil {
			t.Logf("psql: %s", strings.TrimSpace(string(out)))
		}
		return err
	}
	cred := func(c []services.Credential, k string) string {
		for _, x := range c {
			if x.Key == k {
				return x.Value
			}
		}
		return ""
	}
	shopURI := cred(shop.Credentials, "uri")
	if err := psql(shopURI, "CREATE TABLE t(i int); INSERT INTO t VALUES (1); SELECT count(*) FROM t"); err != nil {
		t.Fatal("tenant cannot use its database")
	}
	// Isolation: shop cannot open blog's database nor the maintenance one.
	for _, db := range []string{"blog", "postgres", "template1"} {
		if err := psql(strings.TrimSuffix(shopURI, "/shop")+"/"+db, "SELECT 1"); err == nil {
			t.Fatalf("shop must not connect to %s", db)
		}
	}
	// The superuser is refused over the network, even with its password.
	admin, _ := os.ReadFile(filepath.Join(stacks, project, "secrets", "admin_password"))
	if err := psql("postgresql://postgres:"+string(admin)+"@"+project+":5432/postgres", "SELECT 1"); err == nil {
		t.Fatal("superuser must be refused over the network")
	}

	// Legacy md5 password, for clients without SCRAM; SCRAM users keep
	// SCRAM (the hba "md5" method uses SCRAM for SCRAM passwords).
	method := func(role string) string {
		out, _ := exec.Command("docker", "exec", project, "psql", "-U", "postgres", "-Atc",
			"SELECT left(rolpassword, 3) FROM pg_authid WHERE rolname = '"+role+"'").Output()
		return strings.TrimSpace(string(out))
	}
	old, err := m.Create(ctx, services.CreateRequest{Engine: "postgres", Version: version, Name: "oldcms", LegacyAuth: true}, "it")
	if err != nil {
		t.Fatalf("create legacy: %v\n%s", err, logs)
	}
	if method("oldcms") != "md5" || method("shop") != "SCR" {
		t.Fatalf("stored mechanisms: oldcms=%q shop=%q", method("oldcms"), method("shop"))
	}
	if err := psql(cred(old.Credentials, "uri"), "SELECT 1"); err != nil {
		t.Fatal("the md5 user must connect")
	}
	rot, err := m.RotatePassword(ctx, key, "oldcms", "it")
	if err != nil || method("oldcms") != "md5" || psql(cred(rot, "uri"), "SELECT 1") != nil {
		t.Fatalf("rotation must keep md5: %v %q", err, method("oldcms"))
	}
	if _, err := m.Delete(ctx, key, "oldcms", "it"); err != nil {
		t.Fatal(err)
	}

	list, err := m.Resources(ctx, key)
	if err != nil || len(list) != 2 || !list[0].Managed || !list[1].Managed {
		t.Fatalf("resources = %+v %v", list, err)
	}

	rotated, err := m.RotatePassword(ctx, key, "shop", "it")
	if err != nil {
		t.Fatal(err)
	}
	if err := psql(shopURI, "SELECT 1"); err == nil {
		t.Fatal("old password must be refused")
	}
	if err := psql(cred(rotated, "uri"), "SELECT count(*) FROM t"); err != nil {
		t.Fatal("new password refused")
	}

	// A duplicate is refused through the engine path too (SQLSTATE mapping).
	if _, err := m.Create(ctx, services.CreateRequest{Engine: "postgres", Version: version, Name: "shop"}, "it"); services.CodeOf(err) != services.CodeResourceExists {
		t.Fatalf("duplicate: %v", err)
	}

	// No password, verifier nor SQL in the engine logs.
	out, _ := exec.Command("docker", "logs", project).CombinedOutput()
	for _, secret := range []string{cred(shop.Credentials, "password"), cred(rotated, "password"), cred(blog.Credentials, "password"), "SCRAM-SHA-256$"} {
		if strings.Contains(string(out), secret) || strings.Contains(logs.String(), secret) {
			t.Fatalf("secret %q found in logs", secret)
		}
	}

	// blog grants CONNECT on its database to shop: a shared dependency that
	// would block DROP ROLE shop without DROP OWNED.
	blogURI := cred(blog.Credentials, "uri")
	if err := psql(blogURI, "GRANT CONNECT ON DATABASE blog TO shop"); err != nil {
		t.Fatal("grant failed")
	}
	if res, err := m.Delete(ctx, key, "shop", "it"); err != nil || res.InstanceRetired {
		t.Fatalf("delete shop (granted on blog): %+v %v\n%s", res, err, logs)
	}
	if list, err := m.Resources(ctx, key); err != nil || len(list) != 1 || list[0].Name != "blog" {
		t.Fatalf("no orphan role may remain: %+v %v", list, err)
	}
	if err := psql(cred(rotated, "uri"), "SELECT 1"); err == nil {
		t.Fatal("deleted user can still connect")
	}
	if res, err := m.Delete(ctx, key, "blog", "it"); err != nil || !res.InstanceRetired {
		t.Fatalf("delete blog: %+v %v\n%s", res, err, logs)
	}
	if _, err := os.Stat(filepath.Join(stacks, project)); !os.IsNotExist(err) {
		t.Fatal("instance directory left")
	}
	if _, err := os.Stat(filepath.Join(data, project)); !os.IsNotExist(err) {
		t.Fatal("data directory left")
	}
	if out, _ := exec.Command("docker", "ps", "-aq", "--filter", "name="+project).Output(); len(strings.TrimSpace(string(out))) > 0 {
		t.Fatal("container left")
	}
}
