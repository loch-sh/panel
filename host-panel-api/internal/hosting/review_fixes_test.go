package hosting

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
)

func TestNamePrefixOfAnotherStackIsFree(t *testing.T) {
	docker := &fakeDocker{containers: []container.Summary{{
		Names: []string{"/hosted-blog-staging-web-1"},
		Labels: map[string]string{
			composeProjectLabel: "hosted-blog-staging",
			"traefik.http.routers.hosted-blog-staging-https.rule":                "Host(`staging.example.com`)",
			"traefik.http.services.hosted-blog-staging.loadbalancer.server.port": "80",
		},
	}}}
	m, _ := newTestManager(t, &fakeRunner{}, docker)
	if err := m.CheckName(context.Background(), "blog"); err != nil {
		t.Fatalf("blog must be free next to blog-staging: %v", err)
	}
	if err := m.CheckName(context.Background(), "blog-staging"); codeOf(err) != CodeNameTaken {
		t.Fatalf("blog-staging must be taken: %v", err)
	}
}

// writeForeignStack plants a stack directory as another tool could: a
// compose file with an x-loch section, and optionally the panel marker.
func writeForeignStack(t *testing.T, cfg Config, name, xloch string, marker bool) {
	t.Helper()
	dir := filepath.Join(cfg.StacksDir, "hosted-"+name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	compose := "name: hosted-" + name + "\nx-loch:\n  version: 1\n  name: " + name + "\n  template: nginx\n" + xloch
	writeTestFile(t, filepath.Join(dir, ComposeFile), []byte(compose), 0o644)
	if marker {
		writeTestFile(t, filepath.Join(dir, ManagedMarker), []byte("x"), 0o644)
	}
}

func TestStoredSpecIsRevalidated(t *testing.T) {
	m, cfg := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	victim := filepath.Join(cfg.StacksDir, "ci-app")
	if err := os.Mkdir(victim, 0o755); err != nil {
		t.Fatal(err)
	}

	// A planted spec whose site escapes the sites directory is not managed.
	writeForeignStack(t, cfg, "evil", "  site: ../../ci-app\n  documentRoot: web\n", true)
	if _, err := m.Delete(context.Background(), "evil", true, "admin"); codeOf(err) != CodeStackNotFound {
		t.Fatalf("delete of a stack with a traversal site: %v", err)
	}
	// A valid-looking spec without the panel marker is not managed either.
	writeForeignStack(t, cfg, "nomarker", "  site: nomarker\n  documentRoot: web\n", false)
	if _, err := m.Get(context.Background(), "nomarker"); codeOf(err) != CodeStackNotFound {
		t.Fatalf("stack without marker: %v", err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("a directory outside the sites directory was touched")
	}
}

func TestCheckDeletableRefusesEscapingSite(t *testing.T) {
	m, cfg := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	dir := t.TempDir()
	for _, site := range []string{"..", "../x", "a/b", ""} {
		if _, err := m.checkDeletable(dir, site, true); err == nil {
			t.Errorf("site %q must be refused", site)
		}
	}
	if err := os.Mkdir(filepath.Join(cfg.Render.SitesDir, "ok"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := m.checkDeletable(dir, "ok", true); err != nil || got != filepath.Join(cfg.Render.SitesDir, "ok") {
		t.Fatalf("valid site: %q %v", got, err)
	}
}

func TestDeployModifiedRequiresForce(t *testing.T) {
	m, cfg := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	ctx := context.Background()
	if _, err := m.Create(ctx, validSpec("site"), Snippets{}, "admin"); err != nil {
		t.Fatal(err)
	}
	waitOp(t, m, "site")
	path := filepath.Join(cfg.StacksDir, "hosted-site", ComposeFile)
	data, _ := os.ReadFile(path)
	writeTestFile(t, path, append(data, []byte("# privileged: true\n")...), 0o640)

	if _, err := m.Deploy(ctx, "site", false, false, "admin"); codeOf(err) != CodeManuallyModified {
		t.Fatalf("deploy of a modified stack without force: %v", err)
	}
	if _, err := m.Deploy(ctx, "site", false, true, "admin"); err != nil {
		t.Fatalf("forced deploy: %v", err)
	}
	waitOp(t, m, "site")
}

func TestOperationErrorCodePerKind(t *testing.T) {
	runner := &fakeRunner{fail: func(string, []string) error { return errors.New("boom") }}
	m, _ := newTestManager(t, runner, &fakeDocker{})
	if _, err := m.Create(context.Background(), validSpec("site"), Snippets{}, "admin"); err != nil {
		t.Fatal(err)
	}
	if op := waitOp(t, m, "site"); op.Error != "HOSTING_CREATE_FAILED" {
		t.Fatalf("error = %q", op.Error)
	}
}

func TestPanicInOperationIsRecovered(t *testing.T) {
	runner := &fakeRunner{fail: func(string, []string) error { panic("unexpected") }}
	m, _ := newTestManager(t, runner, &fakeDocker{})
	if _, err := m.Create(context.Background(), validSpec("site"), Snippets{}, "admin"); err != nil {
		t.Fatal(err)
	}
	if op := waitOp(t, m, "site"); op.State != OpFailed {
		t.Fatalf("op = %+v", op)
	}
}

func TestInterruptedOperationIsReported(t *testing.T) {
	m, cfg := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	if _, err := m.Create(context.Background(), validSpec("site"), Snippets{}, "admin"); err != nil {
		t.Fatal(err)
	}
	waitOp(t, m, "site")
	dir := filepath.Join(cfg.StacksDir, "hosted-site")
	if _, err := os.Stat(filepath.Join(dir, operationMarker)); !os.IsNotExist(err) {
		t.Fatal("operation marker not removed after completion")
	}

	// Simulate an API stopped during an update.
	if err := writeOperationMarker(dir, &Operation{ID: "x", Kind: OpUpdate, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	restarted := NewManager(cfg, &fakeRunner{}, &fakeDocker{}, fakeModules{}, discard, discard, nil)
	op := restarted.Operation("site")
	if op == nil || op.State != OpFailed || op.Error != CodeOperationInterrupted || op.Kind != OpUpdate {
		t.Fatalf("op = %+v", op)
	}
}

func TestListOmitsOperationOutput(t *testing.T) {
	m, _ := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	if _, err := m.Create(context.Background(), validSpec("site"), Snippets{}, "admin"); err != nil {
		t.Fatal(err)
	}
	waitOp(t, m, "site")
	list, _ := m.List(context.Background())
	if len(list) != 1 || list[0].Operation == nil || list[0].Operation.Output != "" {
		t.Fatalf("list = %+v", list)
	}
	detail, _ := m.Get(context.Background(), "site")
	if detail.Operation.Output == "" {
		t.Fatal("details must keep the output")
	}
	if !strings.HasPrefix(detail.Compose, "# Generated by Loch.sh Panel") || !strings.Contains(detail.Compose, "name: hosted-site") {
		t.Fatalf("details must include the compose file:\n%s", detail.Compose)
	}
}

func TestDockerEnvDropsSecrets(t *testing.T) {
	t.Setenv("CLIENT_SECRET", "s3cret")
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	env := strings.Join(dockerEnv(), "\n")
	if strings.Contains(env, "s3cret") || !strings.Contains(env, "DOCKER_HOST=") {
		t.Fatalf("env = %q", env)
	}
}

// TestExecRunnerIgnoresDotEnv checks with the real compose CLI that a .env
// file in the stack directory cannot change the project.
func TestExecRunnerIgnoresDotEnv(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	out, _ := Render(validSpec("site"), testRender)
	dir := filepath.Join(t.TempDir(), "hosted-site")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeStackFiles(dir, out, Snippets{}, generatedFiles{}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, ".env"), []byte("COMPOSE_PROJECT_NAME=ci-app\n"), 0o644)

	res, err := ExecRunner{}.Run(context.Background(), dir, "config", "--format", "json")
	if err != nil {
		t.Fatalf("compose config: %v\n%s", err, res)
	}
	if !strings.Contains(res, `"name": "hosted-site"`) || strings.Contains(res, "ci-app") {
		t.Fatalf("project not forced to hosted-site:\n%s", res)
	}
}

func TestOlderSpecVersionIsNotModifiedAndRefreshedOnDeploy(t *testing.T) {
	m, cfg := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	ctx := context.Background()
	if _, err := m.Create(ctx, validSpec("site"), Snippets{}, "admin"); err != nil {
		t.Fatal(err)
	}
	waitOp(t, m, "site")

	// Simulate a file written by version 1 of the panel (no init/tty).
	path := filepath.Join(cfg.StacksDir, "hosted-site", ComposeFile)
	data, _ := os.ReadFile(path)
	current := fmt.Sprintf("version: %d", SpecVersion)
	old := strings.Replace(string(data), current, "version: 1", 1)
	old = strings.Replace(old, "        init: true\n", "", 1)
	writeTestFile(t, path, []byte(old), 0o640)

	if s, _ := m.Get(ctx, "site"); s.Modified {
		t.Fatal("a file from an older panel version must not be reported as modified")
	}
	if _, err := m.Deploy(ctx, "site", false, false, "admin"); err != nil {
		t.Fatal(err)
	}
	waitOp(t, m, "site")
	refreshed, _ := os.ReadFile(path)
	if !strings.Contains(string(refreshed), "init: true") || !strings.Contains(string(refreshed), current) {
		t.Fatalf("compose not regenerated on deploy:\n%s", refreshed)
	}
}
