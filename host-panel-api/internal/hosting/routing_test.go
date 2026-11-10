package hosting

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateRouting(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Spec, *Snippets)
		ok     bool
	}{
		{"default", func(*Spec, *Snippets) {}, true},
		{"symfony", func(s *Spec, _ *Snippets) {
			s.Routing = RoutingSymfony
			s.AssetDirs = []string{"bundles", "build/assets"}
		}, true},
		{"symfony on static", func(s *Spec, _ *Snippets) {
			s.Template = TemplateNginx
			s.PHP = nil
			s.PHPVersion = ""
			s.Routing = RoutingSymfony
		}, false},
		{"custom", func(s *Spec, sn *Snippets) { s.Routing = RoutingCustom; sn.RoutingRules = "location / {}" }, true},
		{"custom on static", func(s *Spec, sn *Snippets) {
			s.Template = TemplateNginx
			s.PHP = nil
			s.PHPVersion = ""
			s.Routing = RoutingCustom
			sn.RoutingRules = "location / {}"
		}, true},
		{"unknown", func(s *Spec, _ *Snippets) { s.Routing = "drupal" }, false},
		{"asset dirs without symfony", func(s *Spec, _ *Snippets) { s.AssetDirs = []string{"bundles"} }, false},
		{"rules without custom", func(_ *Spec, sn *Snippets) { sn.RoutingRules = "x" }, false},
		{"asset dir traversal", func(s *Spec, _ *Snippets) { s.Routing = RoutingSymfony; s.AssetDirs = []string{"../etc"} }, false},
		{"asset dir injection", func(s *Spec, _ *Snippets) { s.Routing = RoutingSymfony; s.AssetDirs = []string{"a { return 200; }"} }, false},
		{"asset dir duplicate", func(s *Spec, _ *Snippets) { s.Routing = RoutingSymfony; s.AssetDirs = []string{"a", "a"} }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validSpec("site")
			var snippets Snippets
			tt.mutate(spec, &snippets)
			if err := Validate(spec, snippets, testLimits); (err == nil) != tt.ok {
				t.Fatalf("err = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestRenderRoutingMount(t *testing.T) {
	spec := validSpec("site")
	out, _ := Render(spec, testRender)
	if strings.Contains(string(out), RoutingFile) {
		t.Fatal("default routing must not mount a routing file")
	}
	spec.Routing = RoutingSymfony
	out, _ = Render(spec, testRender)
	if !strings.Contains(string(out), "target: "+nginxRoutingTarget) {
		t.Fatalf("nginx routing not mounted:\n%s", out)
	}
	spec.Template = TemplatePHPApache
	out, _ = Render(spec, testRender)
	if !strings.Contains(string(out), "target: "+apacheRoutingTarget) {
		t.Fatalf("apache routing not mounted:\n%s", out)
	}
}

func TestSymfonyRules(t *testing.T) {
	nginx := SymfonyRules(TemplatePHPNginx, "web/public", []string{"bundles"}, 90)
	for _, want := range []string{"try_files $uri /index.php$is_args$args;", "location /bundles {", "fastcgi_pass php;", "fastcgi_read_timeout 90s;", "internal;", "return 404;"} {
		if !strings.Contains(nginx, want) {
			t.Errorf("nginx rules miss %q:\n%s", want, nginx)
		}
	}
	apache := SymfonyRules(TemplatePHPApache, "web/public", []string{"bundles"}, 90)
	for _, want := range []string{`<Directory "/app/web/public">`, "FallbackResource /index.php", `<Directory "/app/web/public/bundles">`, "FallbackResource disabled"} {
		if !strings.Contains(apache, want) {
			t.Errorf("apache rules miss %q:\n%s", want, apache)
		}
	}
}

func TestCreateWritesRoutingFile(t *testing.T) {
	m, cfg := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	spec := validSpec("site")
	spec.Routing = RoutingSymfony
	spec.AssetDirs = []string{"bundles"}
	if _, err := m.Create(context.Background(), spec, Snippets{}, "admin"); err != nil {
		t.Fatal(err)
	}
	waitOp(t, m, "site")
	data, _ := os.ReadFile(filepath.Join(cfg.StacksDir, "hosted-site", ConfigDir, RoutingFile))
	if !strings.Contains(string(data), "FallbackResource") && !strings.Contains(string(data), "try_files") {
		t.Fatalf("routing file not generated:\n%s", data)
	}
	d, _ := m.Get(context.Background(), "site")
	if d.RoutingFile != string(data) || d.Snippets.RoutingRules != "" {
		t.Fatalf("details: routingFile=%q rules=%q", d.RoutingFile, d.Snippets.RoutingRules)
	}

	// Switching to custom rules keeps the user's content.
	spec = validSpec("site")
	spec.Routing = RoutingCustom
	custom := "location / {\n    try_files $uri /app.php$is_args$args;\n}\n"
	if _, err := m.Update(context.Background(), "site", spec, Snippets{RoutingRules: custom}, false, "admin"); err != nil {
		t.Fatal(err)
	}
	waitOp(t, m, "site")
	if d, _ := m.Get(context.Background(), "site"); d.Snippets.RoutingRules != custom || d.RoutingFile != custom {
		t.Fatalf("custom rules not kept: %+v", d.Snippets)
	}
}

// TestSymfonyRulesInRealContainer runs the generated rules in the real
// webdevops images. Opt-in (HOSTING_E2E=1): it pulls images and binds ports.
func TestSymfonyRulesInRealContainer(t *testing.T) {
	if os.Getenv("HOSTING_E2E") == "" {
		t.Skip("set HOSTING_E2E=1 to run")
	}
	site := t.TempDir()
	os.MkdirAll(filepath.Join(site, "public", "bundles"), 0o755)
	os.WriteFile(filepath.Join(site, "public", "index.php"), []byte(`<?php echo "FRONT ".$_SERVER['REQUEST_URI'];`), 0o644)
	os.WriteFile(filepath.Join(site, "public", "other.php"), []byte(`<?php echo "OTHER";`), 0o644)
	os.WriteFile(filepath.Join(site, "public", "bundles", "app.css"), []byte("asset"), 0o644)

	for i, tt := range []struct {
		template Template
		image    string
		want     map[string]int // path -> status
	}{
		{TemplatePHPNginx, "webdevops/php-nginx:8.4", map[string]int{
			"/": 200, "/some/route?x=1": 200, "/index.php": 404, "/other.php": 404,
			"/bundles/app.css": 200, "/bundles/missing.css": 404,
		}},
		{TemplatePHPApache, "webdevops/php-apache:8.4", map[string]int{
			"/": 200, "/some/route?x=1": 200, "/bundles/app.css": 200, "/bundles/missing.css": 404,
			"/other.php": 403,
		}},
	} {
		t.Run(string(tt.template), func(t *testing.T) {
			rules := filepath.Join(t.TempDir(), RoutingFile)
			os.WriteFile(rules, []byte(SymfonyRules(tt.template, "public", []string{"bundles"}, 90)), 0o644)
			name := fmt.Sprintf("loch-routing-e2e-%d", i)
			port := 18090 + i
			exec.Command("docker", "rm", "-f", name).Run()
			out, err := exec.Command("docker", "run", "-d", "--name", name, "--init", "-t",
				"-e", "WEB_DOCUMENT_ROOT=/app/public", "-p", fmt.Sprintf("127.0.0.1:%d:80", port),
				"-v", site+":/app", "-v", rules+":"+routingTarget(tt.template)+":ro", tt.image).CombinedOutput()
			if err != nil {
				t.Fatalf("docker run: %v\n%s", err, out)
			}
			defer exec.Command("docker", "rm", "-f", name).Run()

			client := &http.Client{Timeout: 3 * time.Second}
			base := fmt.Sprintf("http://127.0.0.1:%d", port)
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				if resp, err := client.Get(base + "/"); err == nil && resp.StatusCode == 200 {
					resp.Body.Close()
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
			for p, want := range tt.want {
				resp, err := client.Get(base + p)
				if err != nil {
					t.Errorf("%s: %v", p, err)
					continue
				}
				resp.Body.Close()
				if resp.StatusCode != want {
					t.Errorf("%s: status %d, want %d", p, resp.StatusCode, want)
				}
			}
		})
	}
}

func TestWebPathAllowList(t *testing.T) {
	for p, ok := range map[string]bool{
		"": true, "web": true, "web/public": true, ".well-known": true, "build~2/assets@v1": true,
		"a b": false, `a"b`: false, "*": false, "a/*": false, "a;b": false, "a{b": false,
		"a/../b": false, "a//b": false, "[x]": false,
	} {
		if validWebPath(p) != ok {
			t.Errorf("validWebPath(%q) = %v, want %v", p, !ok, ok)
		}
	}
}

func TestCustomRoutingNeedsRules(t *testing.T) {
	spec := validSpec("site")
	spec.Routing = RoutingCustom
	if err := Validate(spec, Snippets{RoutingRules: "  \n"}, testLimits); codeOf(err) != CodeInvalidRouting {
		t.Fatalf("empty custom rules: %v", err)
	}
}

func TestCreateDefaultsAssetDirs(t *testing.T) {
	m, _ := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	spec := validSpec("site")
	spec.Routing = RoutingSymfony
	m.Create(context.Background(), spec, Snippets{}, "admin")
	waitOp(t, m, "site")
	d, _ := m.Get(context.Background(), "site")
	if len(d.Spec.AssetDirs) != 1 || d.Spec.AssetDirs[0] != "bundles" {
		t.Fatalf("asset dirs = %v", d.Spec.AssetDirs)
	}
}

func TestSymfonyRoutingDriftAndRegeneration(t *testing.T) {
	m, cfg := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	ctx := context.Background()
	spec := validSpec("site")
	spec.Routing = RoutingSymfony
	m.Create(ctx, spec, Snippets{}, "admin")
	waitOp(t, m, "site")
	routing := filepath.Join(cfg.StacksDir, "hosted-site", ConfigDir, RoutingFile)

	os.WriteFile(routing, []byte("location / { return 200; }\n"), 0o640)
	if d, _ := m.Get(ctx, "site"); !d.Modified {
		t.Fatal("a hand-edited Symfony routing file must be reported as modified")
	}
	// A deleted file is regenerated on deploy instead of breaking compose.
	os.Remove(routing)
	if d, _ := m.Get(ctx, "site"); d.Modified {
		t.Fatal("a missing routing file is not a manual edit")
	}
	if _, err := m.Deploy(ctx, "site", false, false, "admin"); err != nil {
		t.Fatal(err)
	}
	waitOp(t, m, "site")
	if data, _ := os.ReadFile(routing); !strings.Contains(string(data), "FallbackResource") && !strings.Contains(string(data), "try_files") {
		t.Fatalf("routing file not regenerated:\n%s", data)
	}
}

func TestDefaultRoutingHidesLeftoverFile(t *testing.T) {
	m, cfg := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	m.Create(context.Background(), validSpec("site"), Snippets{}, "admin")
	waitOp(t, m, "site")
	os.WriteFile(filepath.Join(cfg.StacksDir, "hosted-site", ConfigDir, RoutingFile), []byte("leftover"), 0o640)
	if d, _ := m.Get(context.Background(), "site"); d.RoutingFile != "" {
		t.Fatalf("routingFile = %q for the default routing", d.RoutingFile)
	}
}

func TestCustomRulesMustFollowWebServer(t *testing.T) {
	m, _ := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	ctx := context.Background()
	rules := "location / {\n    try_files $uri /index.php$is_args$args;\n}\n"
	spec := validSpec("site")
	spec.Routing = RoutingCustom
	m.Create(ctx, spec, Snippets{RoutingRules: rules}, "admin")
	waitOp(t, m, "site")

	apache := validSpec("site")
	apache.Template = TemplatePHPApache
	apache.Routing = RoutingCustom
	if _, err := m.Update(ctx, "site", apache, Snippets{RoutingRules: rules}, false, "admin"); codeOf(err) != CodeInvalidRouting {
		t.Fatalf("nginx rules kept for apache: %v", err)
	}
	if _, err := m.Update(ctx, "site", apache, Snippets{RoutingRules: "FallbackResource /index.php\n"}, false, "admin"); err != nil {
		t.Fatalf("rewritten rules: %v", err)
	}
	waitOp(t, m, "site")
}

func TestPreviewRejectsBadTimeout(t *testing.T) {
	spec := &Spec{Name: "p", Template: TemplatePHPNginx, Routing: RoutingSymfony, PHP: &PHPSettings{MaxExecutionTime: -5}}
	if _, err := PreviewRules(spec); err == nil {
		t.Fatal("negative timeout accepted")
	}
}

func TestSymfonyToCustomAcrossServersNeedsNewRules(t *testing.T) {
	m, cfg := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	ctx := context.Background()
	spec := validSpec("site")
	spec.Routing = RoutingSymfony
	m.Create(ctx, spec, Snippets{}, "admin")
	waitOp(t, m, "site")
	generated, _ := os.ReadFile(filepath.Join(cfg.StacksDir, "hosted-site", ConfigDir, RoutingFile))

	// Seeding custom rules with the nginx Symfony rules for an Apache stack.
	apache := validSpec("site")
	apache.Template = TemplatePHPApache
	apache.Routing = RoutingCustom
	if _, err := m.Update(ctx, "site", apache, Snippets{RoutingRules: string(generated)}, false, "admin"); codeOf(err) != CodeInvalidRouting {
		t.Fatalf("nginx rules for apache: %v", err)
	}
}

func TestDeployMissingCustomRoutingFile(t *testing.T) {
	m, cfg := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	ctx := context.Background()
	spec := validSpec("site")
	spec.Routing = RoutingCustom
	m.Create(ctx, spec, Snippets{RoutingRules: "location / {}\n"}, "admin")
	waitOp(t, m, "site")
	os.Remove(filepath.Join(cfg.StacksDir, "hosted-site", ConfigDir, RoutingFile))
	if _, err := m.Deploy(ctx, "site", false, false, "admin"); codeOf(err) != CodeInvalidRouting {
		t.Fatalf("deploy without custom rules file: %v", err)
	}
}
