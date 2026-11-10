package elasticmq

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"host-panel-api/internal/services"
	"host-panel-api/internal/services/servicestest"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func TestUnauthenticated(t *testing.T) {
	e := New()
	if !services.IsUnauthenticated(e) {
		t.Fatal("ElasticMQ has no authentication: it must be marked so")
	}
	creds := e.ConnectionInfo(services.ConnParams{Host: "svc-elasticmq-jobs", Port: 9324, Name: "jobs", Password: "Abcdefghijklmnopqrstuvwxyz012345"})
	for _, c := range creds {
		if c.Secret || strings.Contains(c.Value, "Abcdefghij") {
			t.Fatalf("no credential to show: %+v", c)
		}
	}
	if creds[0].Value != "http://svc-elasticmq-jobs:9324" {
		t.Fatalf("%+v", creds)
	}
}

func TestConfig(t *testing.T) {
	conf := string(config(services.InstanceRef{Engine: "elasticmq", Name: "jobs"}, "strict"))
	if !strings.Contains(string(config(services.InstanceRef{Engine: "elasticmq", Name: "jobs"}, "relaxed")), "sqs-limits = relaxed") {
		t.Fatal("relaxed limits")
	}
	for _, want := range []string{`host = "svc-elasticmq-jobs"`, "rest-stats { enabled = false }", "messages-storage { enabled = true", `bind-hostname = "0.0.0.0"`} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q in\n%s", want, conf)
		}
	}
}

func TestRenderGolden(t *testing.T) {
	e := New()
	o, _ := services.ResolveOptions(e.Options(), nil)
	out, err := services.Render(e, services.InstanceRef{Engine: "elasticmq", Name: "jobs"}, e.Catalog()[0], o, []string{"services"}, services.RenderConfig{DataDir: "/data/services", Networks: servicestest.Networks()})
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "compose-elasticmq-jobs.golden.yaml")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil || string(out) != string(want) {
		t.Fatalf("compose differs from %s (run with -update):\n%s", golden, out)
	}
}
