package hosting

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWebMemoryLimitRendersPoolFile(t *testing.T) {
	spec := validSpec("site")
	out, _ := Render(spec, testRender)
	if strings.Contains(string(out), fpmPoolTarget) {
		t.Fatal("no pool file without a web memory limit")
	}
	if FPMPoolContent(spec) != "" {
		t.Fatal("pool content without a web memory limit")
	}

	spec.PHP.WebMemoryLimit = "96M"
	out, _ = Render(spec, testRender)
	if !strings.Contains(string(out), "target: "+fpmPoolTarget) {
		t.Fatalf("pool file not mounted:\n%s", out)
	}
	if got := FPMPoolContent(spec); !strings.Contains(got, "[www]\nphp_admin_value[memory_limit] = 96M\n") {
		t.Fatalf("pool content:\n%s", got)
	}
}

func TestWebMemoryLimitValidation(t *testing.T) {
	for v, ok := range map[string]bool{"": true, "96M": true, "-1": true, "1G": true, "lots": false, "96": false} {
		spec := validSpec("site")
		spec.PHP.WebMemoryLimit = v
		if err := Validate(spec, Snippets{}, testLimits); (err == nil) != ok {
			t.Errorf("webMemoryLimit %q: err = %v", v, err)
		}
	}
}

func TestWebPHPTimeout(t *testing.T) {
	for met, want := range map[int]int{60: 600, 900: 930, 0: maxExecutionTime + 30} {
		spec := validSpec("site")
		spec.PHP.MaxExecutionTime = met
		if got := webPHPTimeout(spec); got != want {
			t.Errorf("max_execution_time %d: timeout %d, want %d", met, got, want)
		}
	}
	out, _ := Render(validSpec("site"), testRender)
	if !strings.Contains(string(out), `WEB_PHP_TIMEOUT: "600"`) {
		t.Fatalf("WEB_PHP_TIMEOUT not rendered:\n%s", out)
	}
}

func TestPoolFileWrittenAndRegenerated(t *testing.T) {
	m, cfg := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	ctx := context.Background()
	spec := validSpec("site")
	spec.PHP.WebMemoryLimit = "128M"
	m.Create(ctx, spec, Snippets{}, "admin")
	waitOp(t, m, "site")
	pool := filepath.Join(cfg.StacksDir, "hosted-site", ConfigDir, FPMPoolFile)
	if data, _ := os.ReadFile(pool); !strings.Contains(string(data), "128M") {
		t.Fatalf("pool file:\n%s", data)
	}
	os.Remove(pool)
	if _, err := m.Deploy(ctx, "site", false, false, "admin"); err != nil {
		t.Fatal(err)
	}
	waitOp(t, m, "site")
	if data, _ := os.ReadFile(pool); !strings.Contains(string(data), "128M") {
		t.Fatalf("pool file not regenerated:\n%s", data)
	}
}

// TestPoolFileInRealContainer checks with the real images that the generated
// pool file limits web requests only. Opt-in (HOSTING_E2E=1).
func TestPoolFileInRealContainer(t *testing.T) {
	if os.Getenv("HOSTING_E2E") == "" {
		t.Skip("set HOSTING_E2E=1 to run")
	}
	site := t.TempDir()
	os.WriteFile(filepath.Join(site, "index.php"), []byte(`<?php echo ini_get("memory_limit");`), 0o644)
	spec := validSpec("site")
	spec.PHP.WebMemoryLimit = "96M"
	pool := filepath.Join(t.TempDir(), FPMPoolFile)
	os.WriteFile(pool, []byte(FPMPoolContent(spec)), 0o644)

	for i, image := range []string{"webdevops/php-nginx:8.4", "webdevops/php-apache:8.4"} {
		t.Run(image, func(t *testing.T) {
			name := fmt.Sprintf("loch-fpm-e2e-%d", i)
			port := 18100 + i
			exec.Command("docker", "rm", "-f", name).Run()
			out, err := exec.Command("docker", "run", "-d", "--name", name, "--init", "-t",
				"-e", "PHP_MEMORY_LIMIT=1G", "-p", fmt.Sprintf("127.0.0.1:%d:80", port),
				"-v", site+":/app", "-v", pool+":"+fpmPoolTarget+":ro", image).CombinedOutput()
			if err != nil {
				t.Fatalf("docker run: %v\n%s", err, out)
			}
			defer exec.Command("docker", "rm", "-f", name).Run()

			var web string
			for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
				if resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port)); err == nil {
					body, _ := io.ReadAll(resp.Body)
					resp.Body.Close()
					if resp.StatusCode == 200 {
						web = string(body)
						break
					}
				}
			}
			cli, _ := exec.Command("docker", "exec", name, "php", "-r", `echo ini_get("memory_limit");`).Output()
			if web != "96M" || string(cli) != "1G" {
				t.Fatalf("web memory_limit %q (want 96M), cli %q (want 1G)", web, cli)
			}
		})
	}
}

func TestPoolFileEditIsDetected(t *testing.T) {
	m, cfg := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	ctx := context.Background()
	spec := validSpec("site")
	spec.PHP.WebMemoryLimit = "128M"
	m.Create(ctx, spec, Snippets{}, "admin")
	waitOp(t, m, "site")
	os.WriteFile(filepath.Join(cfg.StacksDir, "hosted-site", ConfigDir, FPMPoolFile), []byte("[www]\npm.max_children = 500\n"), 0o640)
	if d, _ := m.Get(ctx, "site"); !d.Modified {
		t.Fatal("a hand-edited pool file must be reported as modified")
	}
}

// A forced deploy regenerating a missing pool file must keep a hand-edited
// Symfony routing file.
func TestForcedDeployKeepsEditedRouting(t *testing.T) {
	m, cfg := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	ctx := context.Background()
	spec := validSpec("site")
	spec.Routing = RoutingSymfony
	spec.PHP.WebMemoryLimit = "128M"
	m.Create(ctx, spec, Snippets{}, "admin")
	waitOp(t, m, "site")
	cfgDir := filepath.Join(cfg.StacksDir, "hosted-site", ConfigDir)
	edited := "location / { return 418; }\n"
	os.WriteFile(filepath.Join(cfgDir, RoutingFile), []byte(edited), 0o640)
	os.Remove(filepath.Join(cfgDir, FPMPoolFile))

	if _, err := m.Deploy(ctx, "site", false, true, "admin"); err != nil {
		t.Fatal(err)
	}
	waitOp(t, m, "site")
	if data, _ := os.ReadFile(filepath.Join(cfgDir, RoutingFile)); string(data) != edited {
		t.Fatalf("hand-edited routing overwritten:\n%s", data)
	}
	if data, _ := os.ReadFile(filepath.Join(cfgDir, FPMPoolFile)); !strings.Contains(string(data), "128M") {
		t.Fatalf("missing pool file not regenerated:\n%s", data)
	}
}
