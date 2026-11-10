package functions

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"host-panel-api/internal/composekit"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestRenderGolden(t *testing.T) {
	for name, ref := range map[string]*Reference{"nodejs": sampleRef(), "python-egress": pythonRef(), "provided": providedRef()} {
		t.Run(name, func(t *testing.T) {
			if err := ref.Validate(testConfig); err != nil {
				t.Fatal(err)
			}
			out, err := Render(ref, testRenderConfig(ref))
			if err != nil {
				t.Fatal(err)
			}
			again, _ := Render(ref, testRenderConfig(ref))
			if !bytes.Equal(out, again) {
				t.Fatal("rendering is not deterministic")
			}
			golden := filepath.Join("testdata", "compose-"+name+".golden.yaml")
			if *update {
				os.WriteFile(golden, out, 0o644)
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out, want) {
				t.Fatalf("compose differs from %s:\n%s", golden, out)
			}
			m, project, err := ParseManifest(out)
			if err != nil || project != nm(ref.Name).Project() || m == nil || m.Version != ManifestVersion ||
				m.Name != ref.Name || m.Deployment != ref.Deployment.ID {
				t.Fatalf("manifest = %+v %q %v", m, project, err)
			}
		})
	}
}

func TestRenderEscapesAndNeverWritesSecrets(t *testing.T) {
	ref := sampleRef()
	out, err := Render(ref, testRenderConfig(ref))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	env := doc.Services[serviceName].Environment
	if env["PRICE"] != "costs $$5" {
		t.Fatalf("$ not escaped: %q", env["PRICE"])
	}
	for _, s := range ref.Secrets {
		if _, ok := env[s]; ok {
			t.Fatalf("secret %s rendered in compose.yaml", s)
		}
	}
}

func TestRenderRefuses(t *testing.T) {
	cases := map[string]func(*Reference, *RenderConfig){
		"no deployment":       func(r *Reference, _ *RenderConfig) { r.Deployment = nil },
		"no runtime":          func(_ *Reference, c *RenderConfig) { c.RuntimePath = "" },
		"no network":          func(r *Reference, _ *RenderConfig) { r.Networks = nil },
		"unknown network":     func(r *Reference, _ *RenderConfig) { r.Networks = []string{"nope"} },
		"services network":    func(r *Reference, _ *RenderConfig) { r.Networks = []string{"services"} },
		"duplicate network":   func(r *Reference, _ *RenderConfig) { r.Networks = []string{"images", "images"} },
		"no network registry": func(_ *Reference, c *RenderConfig) { c.Networks = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			ref := sampleRef()
			cfg := testRenderConfig(ref)
			mutate(ref, &cfg)
			if _, err := Render(ref, cfg); err == nil {
				t.Fatal("must be refused")
			}
		})
	}
}

func TestComposeIntact(t *testing.T) {
	ref := sampleRef()
	cfg := testRenderConfig(ref)
	cfg.Paths.StacksDir = t.TempDir()
	dir := cfg.Paths.ProjectDir(nm(ref.Name))
	os.Mkdir(dir, 0o750)
	out, _ := Render(ref, cfg)
	os.WriteFile(filepath.Join(dir, ComposeFile), out, 0o640)
	if ComposeIntact(ref, cfg) {
		t.Fatal("intact without the managed marker")
	}
	os.WriteFile(filepath.Join(dir, composekit.ManagedMarker), []byte("x"), 0o640)
	if !ComposeIntact(ref, cfg) {
		t.Fatal("rendered file not intact")
	}
	edited := strings.Replace(string(out), "read_only: true", "read_only: false", 1)
	os.WriteFile(filepath.Join(dir, ComposeFile), []byte(edited), 0o640)
	if ComposeIntact(ref, cfg) {
		t.Fatal("edited file reported intact")
	}
}
