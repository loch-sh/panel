//go:build integration

package mysqlfamily

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"host-panel-api/internal/composekit"
	"host-panel-api/internal/docker"
	"host-panel-api/internal/services"
	"host-panel-api/internal/services/servicestest"
)

// TestEndToEnd drives the real Manager against real MariaDB and MySQL
// containers. MySQL 5.6 runs emulated on arm64 (slow).
func TestEndToEnd(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	for _, c := range []struct {
		e       *Engine
		version string
	}{{NewMariaDB(), "10.11"}, {NewMariaDB(), "11.8"}, {NewMariaDB(), "12.3"}, {NewMariaDB(), "13.0"},
		{NewMySQL(), "8.4"}, {NewMySQL(), "9.7"}, {NewMySQL(), "26.10"}, {NewMySQL(), "5.6"}} {
		t.Run(c.e.ID()+"-"+c.version, func(t *testing.T) { endToEnd(t, c.e, c.version) })
	}
}

func endToEnd(t *testing.T, e *Engine, version string) {
	ctx := context.Background()
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
	m := services.NewManager(services.Config{StacksDir: stacks, Render: services.RenderConfig{DataDir: data, Networks: servicestest.Registry(t, client, network)}},
		reg, composekit.ExecRunner{}, client, logger, logger, nil)
	ref := services.InstanceRef{Engine: e.ID(), Version: version}
	key, project := ref.Key(), ref.Project()
	t.Cleanup(func() {
		exec.Command("docker", "compose", "--project-name", project, "--project-directory", filepath.Join(stacks, project), "down").Run()
	})

	shop, err := m.Create(ctx, services.CreateRequest{Engine: e.ID(), Version: version, Name: "my_ql"}, "it")
	if err != nil || !shop.InstanceCreated {
		t.Fatalf("create my_ql: %v\n%s", err, logs)
	}
	blog, err := m.Create(ctx, services.CreateRequest{Engine: e.ID(), Version: version, Name: "blog"}, "it")
	if err != nil {
		t.Fatalf("create blog: %v\n%s", err, logs)
	}
	v := e.VersionOf(version, version)
	d := dialectFor(e.flavor, version)
	cred := func(c []services.Credential, k string) string {
		for _, x := range c {
			if x.Key == k {
				return x.Value
			}
		}
		return ""
	}
	sql := func(user, password, db, query string) error {
		args := []string{"run", "--rm", "--network", network}
		if v.Platform != "" {
			args = append(args, "--platform", v.Platform)
		}
		args = append(args, "-e", "MYSQL_PWD="+password, v.Image, d.client, "-h", project, "-u"+user, "-N", "-B")
		if db != "" {
			args = append(args, db)
		}
		out, err := exec.Command("docker", append(args, "-e", query)...).CombinedOutput()
		if err != nil {
			t.Logf("%s: %s", user, strings.TrimSpace(string(out)))
		}
		return err
	}
	pw := cred(shop.Credentials, "password")
	if d.plainAuth && !d.nativePlugin {
		// MySQL 9+: mysql_native_password no longer exists.
		if _, err := m.Create(ctx, services.CreateRequest{Engine: e.ID(), Version: version, Name: "oldcms", LegacyAuth: true}, "it"); services.CodeOf(err) != services.CodeInvalidOption {
			t.Fatalf("legacy authentication must be refused on MySQL %s: %v", version, err)
		}
	}
	if d.nativePlugin {
		// Legacy mechanism (MySQL 8.4): a client without TLS nor RSA key
		// exchange connects; rotation keeps the mechanism.
		legacy, err := m.Create(ctx, services.CreateRequest{Engine: e.ID(), Version: version, Name: "oldcms", LegacyAuth: true}, "it")
		if err != nil {
			t.Fatalf("create legacy: %v\n%s", err, logs)
		}
		plain := func(password string) error {
			out, err := exec.Command("docker", "run", "--rm", "--network", network, "-e", "MYSQL_PWD="+password, v.Image,
				d.client, "-h", project, "-uoldcms", "--ssl-mode=DISABLED", "--default-auth=mysql_native_password", "-N", "-B", "-e", "SELECT 1").CombinedOutput()
			if err != nil {
				t.Logf("plain connection: %s", strings.TrimSpace(string(out)))
			}
			return err
		}
		if err := plain(cred(legacy.Credentials, "password")); err != nil {
			t.Fatal("the legacy user must connect without TLS")
		}
		plugin := func() string {
			out, _ := exec.Command("docker", "exec", project, "sh", "-c",
				`MYSQL_PWD="$(cat /run/secrets/admin_password)" mysql -uroot -N -B -e "SELECT plugin FROM mysql.user WHERE User='oldcms'"`).Output()
			return strings.TrimSpace(string(out))
		}
		if p := plugin(); p != "mysql_native_password" {
			t.Fatalf("plugin = %q", p)
		}
		rot, err := m.RotatePassword(ctx, key, "oldcms", "it")
		if err != nil || plugin() != "mysql_native_password" || plain(cred(rot, "password")) != nil {
			t.Fatalf("rotation must keep the legacy mechanism: %v %q", err, plugin())
		}
		if _, err := m.Delete(ctx, key, "oldcms", "it"); err != nil {
			t.Fatal(err)
		}
	} else if _, err := m.Create(ctx, services.CreateRequest{Engine: e.ID(), Version: version, Name: "oldcms", LegacyAuth: true}, "it"); services.CodeOf(err) != services.CodeInvalidOption {
		t.Fatalf("no legacy option where native already is the default: %v", err)
	}
	// A CMS needs tables, LOCK TABLES and triggers on its own database.
	if err := sql("my_ql", pw, "my_ql", "CREATE TABLE t(v VARCHAR(191) PRIMARY KEY) DEFAULT CHARSET utf8mb4; LOCK TABLES t WRITE; UNLOCK TABLES; CREATE TRIGGER tr BEFORE INSERT ON t FOR EACH ROW SET NEW.v = NEW.v;"); err != nil {
		t.Fatal("tenant cannot use its database")
	}
	// The "_" wildcard must not grant the system database mysql.
	if err := sql("my_ql", pw, "", "SELECT COUNT(*) FROM mysql.user"); err == nil {
		t.Fatal("my_ql can read mysql.user: the GRANT wildcard is not escaped")
	}
	if err := sql("my_ql", pw, "blog", "SELECT 1"); err == nil {
		t.Fatal("tenant reaches another database")
	}
	if err := sql("my_ql", pw, "", "SELECT * FROM loch_meta.resources"); err == nil {
		t.Fatal("tenant reads the panel registry")
	}
	admin, _ := os.ReadFile(filepath.Join(stacks, project, "secrets", "admin_password"))
	if err := sql("root", string(admin), "", "SELECT 1"); err == nil {
		t.Fatal("root must be refused over the network")
	}

	list, err := m.Resources(ctx, key)
	if err != nil || len(list) != 2 || !list[0].Managed || !list[1].Managed {
		t.Fatalf("resources = %+v %v", list, err)
	}
	if _, err := m.Create(ctx, services.CreateRequest{Engine: e.ID(), Version: version, Name: "blog"}, "it"); services.CodeOf(err) != services.CodeResourceExists {
		t.Fatalf("duplicate: %v", err)
	}
	rotated, err := m.RotatePassword(ctx, key, "my_ql", "it")
	if err != nil {
		t.Fatalf("rotate: %v\n%s", err, logs)
	}
	if err := sql("my_ql", pw, "my_ql", "SELECT 1"); err == nil {
		t.Fatal("old password still accepted")
	}
	if err := sql("my_ql", cred(rotated, "password"), "my_ql", "SELECT COUNT(*) FROM t"); err != nil {
		t.Fatal("new password refused")
	}
	out, _ := exec.Command("docker", "logs", project).CombinedOutput()
	for _, secret := range []string{pw, cred(rotated, "password"), cred(blog.Credentials, "password")} {
		if strings.Contains(string(out), secret) || strings.Contains(logs.String(), secret) {
			t.Fatal("password found in the logs")
		}
	}
	// An open transaction holds metadata locks: the deletion closes the
	// session instead of waiting for it.
	holdArgs := []string{"run", "-d", "--network", network}
	if v.Platform != "" {
		holdArgs = append(holdArgs, "--platform", v.Platform)
	}
	holdArgs = append(holdArgs, "-e", "MYSQL_PWD="+cred(rotated, "password"), v.Image, d.client, "-h", project, "-umy_ql", "my_ql",
		"-e", "START TRANSACTION; SELECT * FROM t; SELECT SLEEP(120);")
	holder, err := exec.Command("docker", holdArgs...).Output()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", strings.TrimSpace(string(holder))).Run() })
	time.Sleep(3 * time.Second) // let the transaction start
	if res, err := m.Delete(ctx, key, "my_ql", "it"); err != nil || res.InstanceRetired {
		t.Fatalf("delete my_ql (with an open transaction): %+v %v\n%s", res, err, logs)
	}
	if err := sql("my_ql", cred(rotated, "password"), "", "SELECT 1"); err == nil {
		t.Fatal("deleted user can still connect")
	}
	if list, err := m.Resources(ctx, key); err != nil || len(list) != 1 {
		t.Fatalf("no orphan may remain: %+v %v", list, err)
	}
	// A database created outside the panel, named like a system schema in
	// another case, must keep the instance (and its data) alive.
	// On macOS the data directory is case-insensitive: "Sys" would collide
	// with "sys" there (never on a Linux server).
	external := "Sys"
	if runtime.GOOS == "darwin" {
		external = "Legacy"
	}
	rootSQL := `MYSQL_PWD="$(cat /run/secrets/admin_password)" exec ` + d.client + ` --protocol=socket -uroot -e 'CREATE DATABASE ` + external + `'`
	if out, err := exec.Command("docker", "exec", project, "sh", "-c", rootSQL).CombinedOutput(); err != nil {
		t.Fatalf("create Sys: %v %s", err, out)
	}
	if res, err := m.Delete(ctx, key, "blog", "it"); err != nil || res.InstanceRetired {
		t.Fatalf("an instance holding an unmanaged database must be kept: %+v %v\n%s", res, err, logs)
	}
	if list, err := m.Resources(ctx, key); err != nil || len(list) != 1 || list[0].Name != external || list[0].Managed {
		t.Fatalf("resources = %+v %v", list, err)
	}
	if _, err := os.Stat(filepath.Join(data, project)); err != nil {
		t.Fatal("data deleted")
	}
}
