package hosting

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
)

var testLimits = Limits{MaxMemoryMB: 2048, MaxCPUs: 2, MaxDomains: 20, MaxEnvVars: 100, MaxSnippetBytes: 64 << 10}

func validSpec(name string) *Spec {
	return &Spec{
		Name:          name,
		Site:          name,
		Template:      TemplatePHPNginx,
		PHPVersion:    "8.4",
		DocumentRoot:  "web",
		Domains:       []string{name + ".example.com"},
		HTTPS:         true,
		RedirectHTTPS: true,
		PHP: &PHPSettings{
			MemoryLimit: "256M", UploadMaxFilesize: "64M", PostMaxSize: "64M",
			MaxExecutionTime: 60, Timezone: "Europe/Paris",
		},
		Resources: Resources{MemoryMB: 512, CPUs: 1},
	}
}

func codeOf(err error) string {
	var herr *Error
	if errors.As(err, &herr) {
		return herr.Code
	}
	return ""
}

// --- validation ---

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Spec, *Snippets)
		code   string
	}{
		{"valid", func(*Spec, *Snippets) {}, ""},
		{"uppercase name", func(s *Spec, _ *Snippets) { s.Name = "Site" }, CodeInvalidName},
		{"leading dash", func(s *Spec, _ *Snippets) { s.Name = "-site" }, CodeInvalidName},
		{"too long", func(s *Spec, _ *Snippets) { s.Name = strings.Repeat("a", 33) }, CodeInvalidName},
		{"reserved", func(s *Spec, _ *Snippets) { s.Name = "traefik" }, CodeNameTaken},
		{"route name reserved", func(s *Spec, _ *Snippets) { s.Name = "check-name" }, CodeNameTaken},
		{"unknown template", func(s *Spec, _ *Snippets) { s.Template = "caddy" }, CodeInvalidTemplate},
		{"free php tag", func(s *Spec, _ *Snippets) { s.PHPVersion = "latest" }, CodeInvalidPHPVersion},
		{"missing php settings", func(s *Spec, _ *Snippets) { s.PHP = nil }, CodeInvalidPHPSetting},
		{"bad memory limit", func(s *Spec, _ *Snippets) { s.PHP.MemoryLimit = "lots" }, CodeInvalidPHPSetting},
		{"unlimited memory", func(s *Spec, _ *Snippets) { s.PHP.MemoryLimit = "-1" }, ""},
		{"post smaller than upload", func(s *Spec, _ *Snippets) { s.PHP.PostMaxSize = "8M" }, CodeInvalidPHPSetting},
		{"post in G", func(s *Spec, _ *Snippets) { s.PHP.PostMaxSize = "1G" }, ""},
		{"bad timezone", func(s *Spec, _ *Snippets) { s.PHP.Timezone = "Mars/Olympus" }, CodeInvalidPHPSetting},
		{"site traversal", func(s *Spec, _ *Snippets) { s.Site = ".." }, CodeInvalidSite},
		{"site with slash", func(s *Spec, _ *Snippets) { s.Site = "a/b" }, CodeInvalidSite},
		{"site with dollar", func(s *Spec, _ *Snippets) { s.Site = "a$b" }, CodeInvalidSite},
		{"hidden site allowed", func(s *Spec, _ *Snippets) { s.Site = ".site" }, ""},
		{"docroot traversal", func(s *Spec, _ *Snippets) { s.DocumentRoot = "web/../.." }, CodeInvalidDocRoot},
		{"nested docroot", func(s *Spec, _ *Snippets) { s.DocumentRoot = "web/public" }, ""},
		{"site root", func(s *Spec, _ *Snippets) { s.DocumentRoot = "" }, ""},
		{"wildcard domain", func(s *Spec, _ *Snippets) { s.Domains = []string{"*.example.com"} }, CodeInvalidDomain},
		{"domain injection", func(s *Spec, _ *Snippets) { s.Domains = []string{"a.com`) || Host(`b.com"} }, CodeInvalidDomain},
		{"duplicate domain", func(s *Spec, _ *Snippets) { s.Domains = []string{"a.com", "a.com"} }, CodeInvalidDomain},
		{"no domain", func(s *Spec, _ *Snippets) { s.Domains = nil }, ""},
		{"redirect without https", func(s *Spec, _ *Snippets) { s.HTTPS = false }, CodeInvalidDomain},
		{"too much memory", func(s *Spec, _ *Snippets) { s.Resources.MemoryMB = 4096 }, CodeInvalidResources},
		{"too few cpus", func(s *Spec, _ *Snippets) { s.Resources.CPUs = 0.05 }, CodeInvalidResources},
		{"too little memory", func(s *Spec, _ *Snippets) { s.Resources.MemoryMB = 32 }, CodeInvalidResources},
		{"negative memory", func(s *Spec, _ *Snippets) { s.Resources.MemoryMB = -1 }, CodeInvalidResources},
		{"unlimited resources", func(s *Spec, _ *Snippets) { s.Resources = Resources{} }, ""},
		{"env ok", func(s *Spec, _ *Snippets) { s.Env = []EnvVar{{"APP_ENV", "prod"}, {"DSN", "mysql://u:$ecret@db/x"}} }, ""},
		{"env bad name", func(s *Spec, _ *Snippets) { s.Env = []EnvVar{{"1X", "a"}} }, CodeInvalidEnvVar},
		{"env newline", func(s *Spec, _ *Snippets) { s.Env = []EnvVar{{"X", "a\nb"}} }, CodeInvalidEnvVar},
		{"env duplicate", func(s *Spec, _ *Snippets) { s.Env = []EnvVar{{"X", "a"}, {"X", "b"}} }, CodeInvalidEnvVar},
		{"env managed", func(s *Spec, _ *Snippets) { s.Env = []EnvVar{{"PHP_MEMORY_LIMIT", "1G"}} }, CodeReservedEnvVar},
		{"env run user", func(s *Spec, _ *Snippets) { s.Env = []EnvVar{{"APPLICATION_UID", "0"}} }, CodeReservedEnvVar},
		{"env preload", func(s *Spec, _ *Snippets) { s.Env = []EnvVar{{"ld_preload", "/x.so"}} }, CodeReservedEnvVar},
		{"env dismod", func(s *Spec, _ *Snippets) { s.Env = []EnvVar{{"PHP_DISMOD", "x"}} }, CodeReservedEnvVar},
		{"module glob", func(s *Spec, _ *Snippets) { s.PHP.DisabledModules = []string{"*"} }, CodeInvalidPHPModule},
		{"module duplicate", func(s *Spec, _ *Snippets) { s.PHP.DisabledModules = []string{"gd", "gd"} }, CodeInvalidPHPModule},
		{"snippet too large", func(_ *Spec, sn *Snippets) { sn.Vhost = strings.Repeat("a", 65<<10) }, CodeInvalidSnippet},
		{"snippet nul", func(_ *Spec, sn *Snippets) { sn.Vhost = "a\x00" }, CodeInvalidSnippet},
		{"php.ini on static", func(s *Spec, sn *Snippets) { s.Template = TemplateNginx; sn.PHPIni = "x=1" }, CodeInvalidSnippet},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validSpec("site")
			var snippets Snippets
			tt.mutate(spec, &snippets)
			if spec.Site == "" {
				spec.Site = spec.Name
			}
			err := Validate(spec, snippets, testLimits)
			if got := codeOf(err); got != tt.code {
				t.Fatalf("code = %q, want %q (err: %v)", got, tt.code, err)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	spec := &Spec{Name: " site ", Template: TemplateNginx, PHPVersion: "8.4", PHP: &PHPSettings{},
		DocumentRoot: "/web/", Domains: []string{" WWW.Example.COM "}}
	Normalize(spec)
	if spec.Name != "site" || spec.Site != "site" || spec.DocumentRoot != "web" ||
		spec.Domains[0] != "www.example.com" || spec.PHP != nil || spec.PHPVersion != "" {
		t.Fatalf("unexpected normalized spec: %+v", spec)
	}
}

func TestPHPVersionsSupportDates(t *testing.T) {
	at := func(date string) map[string]PHPVersion {
		now, _ := time.Parse(time.DateOnly, date)
		out := map[string]PHPVersion{}
		for _, v := range PHPVersions(now) {
			if v.ActiveSupportUntil == "" || v.SecuritySupportUntil == "" {
				t.Fatalf("%s has no support dates", v.Tag)
			}
			out[v.Tag] = v
		}
		return out
	}
	v := at("2026-11-03")
	if !v["8.1"].Deprecated || !v["8.1-alpine"].Deprecated || !v["5.6"].Deprecated {
		t.Error("8.1 and 5.6 must be deprecated in November 2026")
	}
	if v["8.2"].Deprecated || v["8.4"].Deprecated || v["8.4-alpine"].SecuritySupportUntil != "2028-12-31" {
		t.Error("8.2/8.4 are still supported in November 2026")
	}
	// Security support lasts through its end date.
	if at("2026-12-31")["8.2"].Deprecated || !at("2027-01-01")["8.2"].Deprecated {
		t.Error("8.2 deprecation boundary is wrong")
	}
}

// --- rendering ---

var testRender = RenderConfig{
	SitesDir: "/data/websites/hosted", TraefikNetwork: "traefik",
	HTTPEntrypoint: "web", HTTPSEntrypoint: "websecure", CertResolver: "letsencrypt",
	Networks: testNetworks{},
}

func TestRenderContent(t *testing.T) {
	spec := validSpec("site")
	spec.Env = []EnvVar{{"DSN", "mysql://u:$ecret@db/x"}}
	out, err := Render(spec, testRender)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"name: hosted-site",
		"image: mirror.gcr.io/webdevops/php-nginx:8.4",
		"source: /data/websites/hosted/site",
		"target: /app",
		"source: ./config/php.ini",
		"target: /usr/local/etc/php/conf.d/zz-loch.ini",
		"source: ./config/vhost.conf",
		"target: /opt/docker/etc/nginx/vhost.common.d/99-loch.conf",
		"read_only: true",
		"create_host_path: false",
		"WEB_DOCUMENT_ROOT: /app/web",
		"PHP_DATE_TIMEZONE: Europe/Paris",
		"DSN: mysql://u:$$ecret@db/x",
		"traefik.http.routers.hosted-site-https.tls.certresolver: letsencrypt",
		"traefik.http.routers.hosted-site-http.middlewares: hosted-site-redirect",
		"traefik.http.middlewares.hosted-site-redirect.redirectscheme.scheme: https",
		"traefik.http.services.hosted-site.loadbalancer.server.port: \"80\"",
		"external: true",
		"no-new-privileges:true",
		"init: true",
		"tty: true",
		"stdin_open: true",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("compose does not contain %q\n%s", want, s)
		}
	}
	if strings.Contains(s, "$ecret") && !strings.Contains(s, "$$ecret") {
		t.Error("env value with $ is not escaped")
	}
}

func TestRenderResourceLimits(t *testing.T) {
	spec := validSpec("site")
	out, _ := Render(spec, testRender)
	if !strings.Contains(string(out), "mem_limit: 512m") || !strings.Contains(string(out), "cpus: \"1\"") {
		t.Fatalf("limits not rendered:\n%s", out)
	}
	spec.Resources = Resources{}
	out, _ = Render(spec, testRender)
	if strings.Contains(string(out), "mem_limit") || strings.Contains(string(out), "cpus: \"") {
		t.Fatalf("unlimited stack must not have limits:\n%s", out)
	}
}

func TestRenderHTTPOnlyAndStatic(t *testing.T) {
	spec := validSpec("site")
	spec.Template = TemplateApache
	spec.HTTPS, spec.RedirectHTTPS = false, false
	Normalize(spec)
	out, err := Render(spec, testRender)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "-https.") || strings.Contains(s, "redirectscheme") || strings.Contains(s, "PHP_") || strings.Contains(s, "php.ini") {
		t.Fatalf("unexpected HTTPS/PHP content in static HTTP-only stack:\n%s", s)
	}
	if !strings.Contains(s, "webdevops/apache:alpine") || !strings.Contains(s, "/opt/docker/etc/httpd/vhost.common.d/99-loch.conf") {
		t.Fatalf("unexpected apache rendering:\n%s", s)
	}
}

func TestRenderParseRoundTrip(t *testing.T) {
	spec := validSpec("site")
	spec.Env = []EnvVar{{"A", "x$y$$z"}, {"B", "plain"}}
	out, err := Render(spec, testRender)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseSpec(out)
	if err != nil || parsed == nil {
		t.Fatalf("ParseSpec: %v %v", parsed, err)
	}
	if parsed.Env[0].Value != "x$y$$z" || parsed.Env[1].Value != "plain" {
		t.Fatalf("env not restored: %+v", parsed.Env)
	}
	again, _ := Render(parsed, testRender)
	if string(again) != string(out) {
		t.Fatal("rendering is not deterministic / round trip differs")
	}
}

func TestParseSpecRejectsForeignCompose(t *testing.T) {
	for _, data := range []string{
		"services:\n  app:\n    image: nginx\n",
		"name: ci-app\nx-loch:\n  name: site\n",       // project/name mismatch
		"name: hosted-site\nx-loch:\n  name: other\n", // idem
	} {
		if spec, err := ParseSpec([]byte(data)); err != nil || spec != nil {
			t.Errorf("ParseSpec(%q) = %v, %v; want nil, nil", data, spec, err)
		}
	}
}

// TestRenderComposeConfig validates generated files with the real compose
// parser, when available.
func TestRenderComposeConfig(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	for _, tpl := range Templates {
		spec := validSpec("site")
		spec.Template = tpl
		spec.Env = []EnvVar{{"DSN", "a$(b)${c}"}}
		Normalize(spec)
		out, err := Render(spec, testRender)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if err := writeStackFiles(dir, out, Snippets{}, generatedFiles{}); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("docker", "compose", "-f", filepath.Join(dir, ComposeFile), "config", "--format", "json")
		res, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: docker compose config failed: %v\n%s", tpl, err, res)
		}
		// `compose config` re-escapes "$" in its output: "$$" means the value
		// was kept verbatim (an interpolated value would be empty or fail).
		if !strings.Contains(string(res), `a$$(b)$${c}`) {
			t.Errorf("%s: env value was interpolated: %s", tpl, res)
		}
	}
}

// --- files ---

func TestWriteRefusesSymlinkDir(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "hosted-site")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := writeStackFiles(link, []byte("x"), Snippets{}, generatedFiles{}); err == nil {
		t.Fatal("write through a symlinked stack directory must fail")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatal("files were written outside the stacks directory")
	}
}

func TestEnsureSiteDirsRefusesSymlink(t *testing.T) {
	sites := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(sites, "site")); err != nil {
		t.Fatal(err)
	}
	if err := ensureSiteDirs(sites, "site", "web", os.Getuid(), os.Getgid()); err == nil {
		t.Fatal("ensureSiteDirs must refuse a symlinked site")
	}
}

func TestReadRefusesSymlinkFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(target, []byte("secret"), 0o600)
	os.Symlink(target, filepath.Join(dir, ComposeFile))
	if _, err := readFileNoFollow(filepath.Join(dir, ComposeFile)); err == nil {
		t.Fatal("reading through a symlink must fail")
	}
}

func TestBackupRestore(t *testing.T) {
	dir := t.TempDir()
	if err := writeStackFiles(dir, []byte("v1"), Snippets{Vhost: "a"}, generatedFiles{}); err != nil {
		t.Fatal(err)
	}
	if err := backupStack(dir); err != nil {
		t.Fatal(err)
	}
	writeStackFiles(dir, []byte("v2"), Snippets{Vhost: "b"}, generatedFiles{})
	if err := restoreStack(dir); err != nil {
		t.Fatal(err)
	}
	compose, _ := os.ReadFile(filepath.Join(dir, ComposeFile))
	vhost, _ := os.ReadFile(filepath.Join(dir, ConfigDir, VhostFile))
	if string(compose) != "v1" || string(vhost) != "a" {
		t.Fatalf("restore failed: %q %q", compose, vhost)
	}
}

// --- preflight ---

func TestCheckSamePathMount(t *testing.T) {
	bind := func(src, dst string) container.MountPoint {
		return container.MountPoint{Type: mount.TypeBind, Source: src, Destination: dst}
	}
	tests := []struct {
		name   string
		mounts []container.MountPoint
		ok     bool
	}{
		{"same path", []container.MountPoint{bind("/data/compose", "/data/compose")}, true},
		{"parent same path", []container.MountPoint{bind("/data", "/data")}, true},
		{"different path", []container.MountPoint{bind("/srv/compose", "/data/compose")}, false},
		{"longest wins", []container.MountPoint{bind("/data", "/data"), bind("/srv/x", "/data/compose")}, false},
		{"volume", []container.MountPoint{{Type: mount.TypeVolume, Source: "/var/lib/docker/volumes/x", Destination: "/data/compose"}}, false},
		{"not mounted", []container.MountPoint{bind("/app/data", "/app/data")}, false},
		{"prefix is not parent", []container.MountPoint{bind("/data/comp", "/data/comp")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkSamePathMount(tt.mounts, "/data/compose")
			if (err == nil) != tt.ok {
				t.Fatalf("err = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

// --- PHP modules ---

// Real conf.d listing of webdevops/php-nginx:8.4 (trimmed).
var confFiles84 = []string{
	"00-ioncube.ini", "98-webdevops.ini", "99-docker.ini", "apcu.ini", "docker-fpm.ini",
	"docker-php-ext-gd.ini", "docker-php-ext-mysqli.ini", "docker-php-ext-pdo_mysql.ini",
	"docker-php-ext-pdo_pgsql.ini", "docker-php-ext-pgsql.ini", "docker-php-ext-opcache.ini",
	"redis.ini",
}

type fakeModules struct{}

func (fakeModules) ConfFiles(context.Context, string) ([]string, error) { return confFiles84, nil }

func TestModulesFromFiles(t *testing.T) {
	var names []string
	for _, m := range ModulesFromFiles(append(confFiles84, "zz-loch.ini")) {
		names = append(names, m.Name)
	}
	want := "apcu gd ioncube mysqli opcache pdo_mysql pdo_pgsql pgsql redis"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("modules = %q, want %q", got, want)
	}
}

func TestCheckDisabledModules(t *testing.T) {
	tests := []struct {
		disabled []string
		ok       bool
	}{
		{[]string{"ioncube"}, true},
		{[]string{"ioncube", "redis", "gd"}, true},
		{[]string{"pdo_pgsql"}, true},
		{[]string{"pgsql"}, false},             // also removes pdo_pgsql
		{[]string{"pgsql", "pdo_pgsql"}, true}, // explicit
		{[]string{"xdebug"}, true},             // not in the image and matches no file: no-op
		{[]string{"docker"}, false},            // not a module (would wipe config files)
		{[]string{"pdo"}, false},               // absent, but would remove pdo_mysql and pdo_pgsql
	}
	for _, tt := range tests {
		err := checkDisabledModules(confFiles84, tt.disabled)
		if (err == nil) != tt.ok {
			t.Errorf("%v: err = %v, want ok=%v", tt.disabled, err, tt.ok)
		}
	}
}

func TestRenderDisabledModules(t *testing.T) {
	spec := validSpec("site")
	spec.PHP.DisabledModules = []string{"ioncube", "redis"}
	out, _ := Render(spec, testRender)
	if !strings.Contains(string(out), "PHP_DISMOD: ioncube,redis") {
		t.Fatalf("PHP_DISMOD not rendered:\n%s", out)
	}
}

func TestCreateRejectsUnsafeModuleList(t *testing.T) {
	m, _ := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	spec := validSpec("site")
	spec.PHP.DisabledModules = []string{"pgsql"}
	_, err := m.Create(context.Background(), spec, Snippets{}, "admin")
	var herr *Error
	if !errors.As(err, &herr) || herr.Code != CodeInvalidPHPModule || herr.Reason != "pdo_pgsql" {
		t.Fatalf("err = %+v", err)
	}
}

// --- manager ---

type fakeRunner struct {
	mu    sync.Mutex
	calls [][]string
	fail  func(dir string, args []string) error
	block chan struct{}
}

func (f *fakeRunner) Run(_ context.Context, dir string, args ...string) (string, error) {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{dir}, args...))
	fail := f.fail
	f.mu.Unlock()
	if fail != nil {
		if err := fail(dir, args); err != nil {
			return "boom", err
		}
	}
	return "ok", nil
}

type fakeDocker struct{ containers []container.Summary }

func (f *fakeDocker) ListContainers(context.Context) ([]container.Summary, error) {
	return f.containers, nil
}

func newTestManager(t *testing.T, runner *fakeRunner, docker *fakeDocker) (*Manager, Config) {
	t.Helper()
	render := testRender
	render.SitesDir = t.TempDir()
	cfg := Config{StacksDir: t.TempDir(), Render: render, RunUID: os.Getuid(), RunGID: os.Getgid(), Limits: testLimits}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewManager(cfg, runner, docker, fakeModules{}, discard, discard, nil), cfg
}

func waitOp(t *testing.T, m *Manager, name string) *Operation {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if op := m.Operation(name); op != nil && op.State != OpRunning {
			return op
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("operation did not finish")
	return nil
}

func TestCreateWritesStackAndDeploys(t *testing.T) {
	runner := &fakeRunner{}
	m, cfg := newTestManager(t, runner, &fakeDocker{})
	if _, err := m.Create(context.Background(), validSpec("site"), Snippets{PHPIni: "x=1", Vhost: "# vhost"}, "admin@x"); err != nil {
		t.Fatal(err)
	}
	if op := waitOp(t, m, "site"); op.State != OpSucceeded {
		t.Fatalf("op = %+v", op)
	}
	dir := filepath.Join(cfg.StacksDir, "hosted-site")
	for _, f := range []string{ComposeFile, "config/php.ini", "config/vhost.conf"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(cfg.Render.SitesDir, "site", "web")); err != nil || !fi.IsDir() {
		t.Errorf("site document root not created: %v", err)
	}
	if len(runner.calls) != 1 || runner.calls[0][1] != "up" {
		t.Fatalf("calls = %v", runner.calls)
	}

	detail, err := m.Get(context.Background(), "site")
	if err != nil || detail.Modified || detail.Snippets.PHPIni != "x=1" || detail.Project != "hosted-site" {
		t.Fatalf("Get = %+v, %v", detail, err)
	}
}

func TestCreateCollisions(t *testing.T) {
	ctx := context.Background()
	docker := &fakeDocker{containers: []container.Summary{
		{Names: []string{"/ci-app-1"}, Labels: map[string]string{
			composeProjectLabel:           "hosted-ci",
			"traefik.http.routers.x.rule": "Host(`taken.example.com`)",
		}},
		{Names: []string{"/hosted-manual-web-1"}, Labels: map[string]string{}},
		{Names: []string{"/other"}, Labels: map[string]string{"traefik.http.routers.hosted-router-https.rule": "Host(`r.example.com`)"}},
	}}
	m, cfg := newTestManager(t, &fakeRunner{}, docker)
	os.Mkdir(filepath.Join(cfg.StacksDir, "hosted-dir"), 0o755)

	tests := []struct {
		name   string
		mutate func(*Spec)
		code   string
		reason string
	}{
		{"existing directory", func(s *Spec) { s.Name = "dir" }, CodeNameTaken, ReasonDirectory},
		{"existing project", func(s *Spec) { s.Name = "ci" }, CodeNameTaken, ReasonProject},
		{"existing container", func(s *Spec) { s.Name = "manual" }, CodeNameTaken, ReasonContainer},
		{"existing router", func(s *Spec) { s.Name = "router" }, CodeNameTaken, ReasonTraefikRouter},
		{"routed domain", func(s *Spec) { s.Domains = []string{"taken.example.com"} }, CodeDomainInUse, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validSpec("fresh")
			tt.mutate(spec)
			_, err := m.Create(ctx, spec, Snippets{}, "admin")
			var herr *Error
			if !errors.As(err, &herr) || herr.Code != tt.code || herr.Reason != tt.reason {
				t.Fatalf("err = %+v, want %s/%s", err, tt.code, tt.reason)
			}
		})
	}
	// Nothing was written for rejected requests.
	if _, err := os.Stat(filepath.Join(cfg.StacksDir, "hosted-fresh")); !os.IsNotExist(err) {
		t.Fatal("rejected stack left a directory behind")
	}
}

func TestCreateConflictsWithOtherStacks(t *testing.T) {
	m, _ := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	ctx := context.Background()
	if _, err := m.Create(ctx, validSpec("one"), Snippets{}, "admin"); err != nil {
		t.Fatal(err)
	}
	waitOp(t, m, "one")

	sameDomain := validSpec("two")
	sameDomain.Domains = []string{"one.example.com"}
	if _, err := m.Create(ctx, sameDomain, Snippets{}, "admin"); codeOf(err) != CodeDomainInUse {
		t.Fatalf("same domain: %v", err)
	}
	sameSite := validSpec("three")
	sameSite.Site = "one"
	if _, err := m.Create(ctx, sameSite, Snippets{}, "admin"); codeOf(err) != CodeSiteInUse {
		t.Fatalf("same site: %v", err)
	}
	if err := m.CheckName(ctx, "one"); codeOf(err) != CodeNameTaken {
		t.Fatalf("CheckName(existing) = %v", err)
	}
	if err := m.CheckName(ctx, "free"); err != nil {
		t.Fatalf("CheckName(free) = %v", err)
	}
}

func TestConcurrentCreateSameName(t *testing.T) {
	m, _ := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0
	for i := 0; i < 10; i++ {
		wg.Go(func() {
			spec := validSpec("race")
			if _, err := m.Create(context.Background(), spec, Snippets{}, "admin"); err == nil {
				mu.Lock()
				succeeded++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if succeeded != 1 {
		t.Fatalf("%d concurrent creations succeeded, want 1", succeeded)
	}
	waitOp(t, m, "race")
}

func TestUpdateRollsBackOnFailure(t *testing.T) {
	runner := &fakeRunner{}
	m, cfg := newTestManager(t, runner, &fakeDocker{})
	ctx := context.Background()
	m.Create(ctx, validSpec("site"), Snippets{Vhost: "v1"}, "admin")
	waitOp(t, m, "site")
	dir := filepath.Join(cfg.StacksDir, "hosted-site")
	before, _ := os.ReadFile(filepath.Join(dir, ComposeFile))

	// The first `up` after the update fails, the rollback `up` succeeds.
	var n int
	runner.fail = func(string, []string) error {
		n++
		if n == 1 {
			return errors.New("container exited")
		}
		return nil
	}
	spec := validSpec("site")
	spec.PHPVersion = "8.5"
	if _, err := m.Update(ctx, "site", spec, Snippets{Vhost: "v2"}, false, "admin"); err != nil {
		t.Fatal(err)
	}
	op := waitOp(t, m, "site")
	if op.State != OpFailed || !op.RolledBack || op.Kind != OpUpdate {
		t.Fatalf("op = %+v", op)
	}
	after, _ := os.ReadFile(filepath.Join(dir, ComposeFile))
	vhost, _ := os.ReadFile(filepath.Join(dir, ConfigDir, VhostFile))
	if string(after) != string(before) || string(vhost) != "v1" {
		t.Fatal("files were not restored after a failed update")
	}
}

func TestUpdateRefusesManualEditsUnlessForced(t *testing.T) {
	m, cfg := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	ctx := context.Background()
	m.Create(ctx, validSpec("site"), Snippets{}, "admin")
	waitOp(t, m, "site")

	path := filepath.Join(cfg.StacksDir, "hosted-site", ComposeFile)
	data, _ := os.ReadFile(path)
	os.WriteFile(path, append(data, []byte("# manual edit\n")...), 0o640)

	if s, _ := m.Get(ctx, "site"); !s.Modified {
		t.Fatal("manual edit not detected")
	}
	if _, err := m.Update(ctx, "site", validSpec("site"), Snippets{}, false, "admin"); codeOf(err) != CodeManuallyModified {
		t.Fatalf("update without force: %v", err)
	}
	if _, err := m.Update(ctx, "site", validSpec("site"), Snippets{}, true, "admin"); err != nil {
		t.Fatalf("forced update: %v", err)
	}
	waitOp(t, m, "site")
	if s, _ := m.Get(ctx, "site"); s.Modified {
		t.Fatal("forced update did not overwrite the manual edit")
	}
}

func TestUpdateSiteIsImmutable(t *testing.T) {
	m, _ := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	ctx := context.Background()
	m.Create(ctx, validSpec("site"), Snippets{}, "admin")
	waitOp(t, m, "site")
	spec := validSpec("site")
	spec.Site = "elsewhere"
	if _, err := m.Update(ctx, "site", spec, Snippets{}, false, "admin"); codeOf(err) != CodeInvalidSite {
		t.Fatalf("err = %v", err)
	}
}

func TestOperationInProgress(t *testing.T) {
	runner := &fakeRunner{block: make(chan struct{})}
	m, _ := newTestManager(t, runner, &fakeDocker{})
	ctx := context.Background()
	if _, err := m.Create(ctx, validSpec("site"), Snippets{}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Deploy(ctx, "site", false, false, "admin"); codeOf(err) != CodeOperationRunning {
		t.Fatalf("deploy during create: %v", err)
	}
	close(runner.block)
	waitOp(t, m, "site")
}

func TestDeleteKeepsOrPurgesData(t *testing.T) {
	for _, purge := range []bool{false, true} {
		m, cfg := newTestManager(t, &fakeRunner{}, &fakeDocker{})
		ctx := context.Background()
		m.Create(ctx, validSpec("site"), Snippets{}, "admin")
		waitOp(t, m, "site")
		if _, err := m.Delete(ctx, "site", purge, "admin"); err != nil {
			t.Fatal(err)
		}
		if op := waitOp(t, m, "site"); op.State != OpSucceeded {
			t.Fatalf("delete op = %+v", op)
		}
		if _, err := os.Stat(filepath.Join(cfg.StacksDir, "hosted-site")); !os.IsNotExist(err) {
			t.Fatal("stack directory not removed")
		}
		_, err := os.Stat(filepath.Join(cfg.Render.SitesDir, "site"))
		if purge != os.IsNotExist(err) {
			t.Fatalf("purge=%v but site exists=%v", purge, err == nil)
		}
	}
}

func TestDeleteIgnoresForeignStacks(t *testing.T) {
	m, cfg := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	dir := filepath.Join(cfg.StacksDir, "hosted-ci")
	os.Mkdir(dir, 0o755)
	os.WriteFile(filepath.Join(dir, ComposeFile), []byte("services:\n  app:\n    image: nginx\n"), 0o644)

	if _, err := m.Delete(context.Background(), "ci", true, "admin"); codeOf(err) != CodeStackNotFound {
		t.Fatalf("deleting a foreign compose project: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("foreign stack directory was touched")
	}
	list, _ := m.List(context.Background())
	if len(list) != 0 {
		t.Fatalf("foreign stack listed: %+v", list)
	}
}

func TestSites(t *testing.T) {
	m, cfg := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	os.Mkdir(filepath.Join(cfg.Render.SitesDir, "legacy"), 0o755)
	os.Symlink(t.TempDir(), filepath.Join(cfg.Render.SitesDir, "link"))
	m.Create(context.Background(), validSpec("site"), Snippets{}, "admin")
	waitOp(t, m, "site")

	sites, err := m.Sites()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range sites {
		got[s.Name] = s.Stack
	}
	if len(got) != 2 || got["site"] != "site" || got["legacy"] != "" {
		t.Fatalf("sites = %+v", sites)
	}
}

// TestDockerModuleInspector runs against a real image when it is already
// present locally (it never pulls).
func TestDockerModuleInspector(t *testing.T) {
	const image = "webdevops/php-nginx:8.4"
	if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
		t.Skip(image + " not present locally")
	}
	files, err := (&DockerModuleInspector{}).ConfFiles(context.Background(), image)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkDisabledModules(files, DefaultDisabledModules); err != nil {
		t.Fatalf("default disabled modules are not valid for %s: %v", image, err)
	}
	if len(ModulesFromFiles(files)) < 20 {
		t.Fatalf("unexpectedly few modules: %v", files)
	}
}

func TestImagesFromTheMirror(t *testing.T) {
	cases := map[string]*Spec{
		"mirror.gcr.io/webdevops/php-nginx:8.4": {Template: TemplatePHPNginx, PHPVersion: "8.4"},
		"mirror.gcr.io/webdevops/apache:alpine": {Template: TemplateApache},
		// Missing from the mirror: still from Docker Hub.
		"webdevops/php-apache:8.4": {Template: TemplatePHPApache, PHPVersion: "8.4"},
		"webdevops/nginx:alpine":   {Template: TemplateNginx},
	}
	for want, spec := range cases {
		if got := Image(spec); got != want {
			t.Errorf("Image(%s %s) = %q, want %q", spec.Template, spec.PHPVersion, got, want)
		}
	}
}
