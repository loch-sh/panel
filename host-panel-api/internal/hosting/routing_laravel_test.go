package hosting

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLaravelRoutingValidation(t *testing.T) {
	spec := validSpec("site")
	spec.Routing = RoutingLaravel
	if err := Validate(spec, Snippets{}, testLimits); err != nil {
		t.Fatalf("laravel on php-nginx: %v", err)
	}
	spec.AssetDirs = []string{"bundles"}
	if err := Validate(spec, Snippets{}, testLimits); codeOf(err) != CodeInvalidRouting {
		t.Fatalf("asset dirs with laravel: %v", err)
	}
	static := validSpec("site")
	static.Template, static.PHP, static.PHPVersion = TemplateNginx, nil, ""
	static.Routing = RoutingLaravel
	if err := Validate(static, Snippets{}, testLimits); codeOf(err) != CodeInvalidRouting {
		t.Fatalf("laravel on a static template: %v", err)
	}
}

func TestLaravelRules(t *testing.T) {
	nginx := LaravelRules(TemplatePHPNginx, "public", 90)
	for _, want := range []string{"try_files $uri $uri/ /index.php?$query_string;", "fastcgi_pass php;", "fastcgi_read_timeout 90s;", `location ~ /\.(?!well-known(/|$))`, "return 404;"} {
		if !strings.Contains(nginx, want) {
			t.Errorf("nginx rules miss %q:\n%s", want, nginx)
		}
	}
	apache := LaravelRules(TemplatePHPApache, "public", 90)
	for _, want := range []string{`<Directory "/app/public">`, "AllowOverride All", "FallbackResource /index.php", "Require all denied", "CGIPassAuth On", `<FilesMatch "^(?!index\.php$).*\.php$">`} {
		if !strings.Contains(apache, want) {
			t.Errorf("apache rules miss %q:\n%s", want, apache)
		}
	}
	spec := validSpec("site")
	spec.Routing = RoutingLaravel
	if got, err := PreviewRules(spec); err != nil || got != RoutingContent(spec, Snippets{}) {
		t.Fatalf("preview: %v", err)
	}
}

// TestLaravelRulesInRealContainer runs the generated rules in the real
// webdevops images. Opt-in (HOSTING_E2E=1).
func TestLaravelRulesInRealContainer(t *testing.T) {
	if os.Getenv("HOSTING_E2E") == "" {
		t.Skip("set HOSTING_E2E=1 to run")
	}
	site := t.TempDir()
	os.MkdirAll(filepath.Join(site, "public", "css"), 0o755)
	os.WriteFile(filepath.Join(site, "public", "index.php"), []byte(`<?php echo "FRONT ".$_SERVER['REQUEST_URI'];`), 0o644)
	os.WriteFile(filepath.Join(site, "public", "other.php"), []byte(`<?php echo "OTHER";`), 0o644)
	os.WriteFile(filepath.Join(site, "public", ".env"), []byte("APP_KEY=secret"), 0o644)
	os.WriteFile(filepath.Join(site, "public", ".well-known.env"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(site, "public", "auth.php"), []byte(`<?php echo $_SERVER["HTTP_AUTHORIZATION"] ?? "noauth";`), 0o644)
	os.WriteFile(filepath.Join(site, "public", "css", "app.css"), []byte("asset"), 0o644)

	for i, tt := range []struct {
		template Template
		image    string
		want     map[string]int
	}{
		{TemplatePHPNginx, "webdevops/php-nginx:8.4", map[string]int{
			"/": 200, "/users/42?x=1": 200, "/css/app.css": 200, "/.env": 403, "/other.php": 404,
			"/.well-known.env": 403,
		}},
		{TemplatePHPApache, "webdevops/php-apache:8.4", map[string]int{
			"/": 200, "/users/42?x=1": 200, "/css/app.css": 200, "/.env": 403,
			"/other.php": 403, "/.well-known.env": 403,
		}},
	} {
		t.Run(string(tt.template), func(t *testing.T) {
			rules := filepath.Join(t.TempDir(), RoutingFile)
			os.WriteFile(rules, []byte(LaravelRules(tt.template, "public", 90)), 0o644)
			name := fmt.Sprintf("loch-laravel-e2e-%d", i)
			port := 18110 + i
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
			for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
				if resp, err := client.Get(base + "/"); err == nil {
					resp.Body.Close()
					if resp.StatusCode == 200 {
						break
					}
				}
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
