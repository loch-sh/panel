//go:build integration

package mongodb

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

func TestEndToEnd(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	for _, v := range []string{"9.0", "8.3", "8.0", "7.0"} {
		t.Run(v, func(t *testing.T) { endToEnd(t, v) })
	}
}

func endToEnd(t *testing.T, version string) {
	ctx := context.Background()
	root, _ := os.MkdirTemp(os.Getenv("HOME"), ".loch-services-it-")
	root, _ = filepath.EvalSymlinks(root)
	t.Cleanup(func() { os.RemoveAll(root) })
	stacks, data := filepath.Join(root, "compose"), filepath.Join(root, "services")
	os.Mkdir(stacks, 0o755)
	os.Mkdir(data, 0o755)
	network := "loch-services-it-mongo"
	exec.Command("docker", "network", "create", "--internal", "--label", "sh.loch.managed=true", network).Run()
	t.Cleanup(func() { exec.Command("docker", "network", "rm", network).Run() })

	e := New()
	reg, _ := services.NewRegistry([]services.Engine{e}, nil, map[string][]string{"mongodb": {version}}, nil)
	client, err := docker.NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	logs := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	m := services.NewManager(services.Config{StacksDir: stacks, Render: services.RenderConfig{DataDir: data, Networks: servicestest.Registry(t, client, network)}},
		reg, composekit.ExecRunner{Files: []string{composekit.DefaultComposeFile, services.OverrideFile}}, client, logger, logger, nil)
	ref := services.InstanceRef{Engine: "mongodb", Version: version}
	key, project := ref.Key(), ref.Project()
	t.Cleanup(func() {
		exec.Command("docker", "compose", "--project-name", project, "--project-directory", filepath.Join(stacks, project),
			"-f", filepath.Join(stacks, project, "compose.yaml"), "-f", filepath.Join(stacks, project, services.OverrideFile), "down").Run()
	})

	shop, err := m.Create(ctx, services.CreateRequest{Engine: "mongodb", Version: version, Name: "shop"}, "it")
	if err != nil || !shop.InstanceCreated {
		t.Fatalf("create shop: %v\n%s", err, logs.String())
	}
	blog, err := m.Create(ctx, services.CreateRequest{Engine: "mongodb", Version: version, Name: "blog"}, "it")
	if err != nil {
		t.Fatalf("create blog: %v\n%s", err, logs)
	}
	cred := func(c []services.Credential, k string) string {
		for _, x := range c {
			if x.Key == k {
				return x.Value
			}
		}
		return ""
	}
	mongosh := func(uri, eval string) error {
		out, err := exec.Command("docker", "run", "--rm", "--network", network, "mongo:"+version, "mongosh", uri, "--quiet", "--norc", "--eval", eval).CombinedOutput()
		if err != nil {
			t.Logf("mongosh: %s", strings.TrimSpace(string(out)))
		}
		return err
	}
	shopURI := cred(shop.Credentials, "uri")
	if err := mongosh(shopURI, "db.items.insertOne({a: 1}); if (db.items.countDocuments() !== 1) quit(1)"); err != nil {
		t.Fatal("tenant cannot use its database")
	}
	if err := mongosh(shopURI, "db.getSiblingDB('blog').x.insertOne({a: 1})"); err == nil {
		t.Fatal("tenant writes another database")
	}
	if err := mongosh(shopURI, "db.createUser({user: 'backdoor', pwd: 'x', roles: ['readWrite']})"); err == nil {
		t.Fatal("a tenant must not create users (they would survive the deletion)")
	}
	if err := mongosh(shopURI, "db.getSiblingDB('admin').system.users.find().toArray()"); err == nil {
		t.Fatal("tenant reads the users")
	}
	admin, _ := os.ReadFile(filepath.Join(stacks, project, "secrets", "admin_password"))
	if err := mongosh("mongodb://root:"+string(admin)+"@"+project+":27017/admin", "db.adminCommand({listDatabases: 1})"); err == nil {
		t.Fatal("root must be refused over the network")
	}
	list, err := m.Resources(ctx, key)
	if err != nil || len(list) != 2 || !list[0].Managed || !list[1].Managed {
		t.Fatalf("resources = %+v %v\n%s", list, err, logs)
	}
	if _, err := m.Create(ctx, services.CreateRequest{Engine: "mongodb", Version: version, Name: "shop"}, "it"); services.CodeOf(err) != services.CodeResourceExists {
		t.Fatalf("duplicate: %v", err)
	}
	rotated, err := m.RotatePassword(ctx, key, "shop", "it")
	if err != nil {
		t.Fatalf("rotate: %v\n%s", err, logs)
	}
	if err := mongosh(shopURI, "db.items.countDocuments()"); err == nil {
		t.Fatal("old password accepted")
	}
	if err := mongosh(cred(rotated, "uri"), "if (db.items.countDocuments() !== 1) quit(1)"); err != nil {
		t.Fatal("new password refused")
	}
	out, _ := exec.Command("docker", "logs", project).CombinedOutput()
	for _, s := range []string{cred(shop.Credentials, "password"), cred(rotated, "password"), cred(blog.Credentials, "password")} {
		if strings.Contains(string(out), s) || strings.Contains(logs.String(), s) {
			t.Fatal("password in the logs")
		}
	}
	if res, err := m.Delete(ctx, key, "shop", "it"); err != nil || res.InstanceRetired {
		t.Fatalf("delete shop: %+v %v\n%s", res, err, logs)
	}
	if err := mongosh(cred(rotated, "uri"), "db.items.countDocuments()"); err == nil {
		t.Fatal("deleted user can still connect")
	}
	if res, err := m.Delete(ctx, key, "blog", "it"); err != nil || !res.InstanceRetired {
		t.Fatalf("delete blog: %+v %v\n%s", res, err, logs)
	}
	if _, err := os.Stat(filepath.Join(data, project)); !os.IsNotExist(err) {
		t.Fatal("data directory left")
	}
}
