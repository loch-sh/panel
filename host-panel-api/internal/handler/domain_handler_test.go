package handler

import (
	"testing"
)

func TestParseTraefikLabels_FullRouter(t *testing.T) {
	labels := map[string]string{
		"traefik.enable":                                       "true",
		"traefik.http.routers.myapp.rule":                      "Host(`example.com`)",
		"traefik.http.routers.myapp.entrypoints":               "websecure",
		"traefik.http.routers.myapp.tls":                       "true",
		"traefik.http.routers.myapp.tls.certresolver":          "letsencrypt",
		"traefik.http.routers.myapp.middlewares":               "auth,compress",
		"traefik.http.services.myapp.loadbalancer.server.port": "8080",
	}

	routers, services := parseTraefikLabels(labels)

	if len(routers) != 1 {
		t.Fatalf("routers len = %d, want 1", len(routers))
	}

	r := routers[0]
	if r.Name != "myapp" {
		t.Errorf("Name = %q, want %q", r.Name, "myapp")
	}
	if r.Rule != "Host(`example.com`)" {
		t.Errorf("Rule = %q", r.Rule)
	}
	if len(r.Domains) != 1 || r.Domains[0] != "example.com" {
		t.Errorf("Domains = %v, want [example.com]", r.Domains)
	}
	if len(r.Entrypoints) != 1 || r.Entrypoints[0] != "websecure" {
		t.Errorf("Entrypoints = %v", r.Entrypoints)
	}
	if !r.TLS {
		t.Error("TLS should be true")
	}
	if r.CertResolver != "letsencrypt" {
		t.Errorf("CertResolver = %q", r.CertResolver)
	}
	if len(r.Middlewares) != 2 {
		t.Errorf("Middlewares = %v, want [auth compress]", r.Middlewares)
	}

	if len(services) != 1 {
		t.Fatalf("services len = %d, want 1", len(services))
	}
	if services[0].Port != 8080 {
		t.Errorf("Port = %d, want 8080", services[0].Port)
	}
}

func TestParseTraefikLabels_NoTraefikLabels(t *testing.T) {
	labels := map[string]string{
		"com.docker.compose.project": "mystack",
		"maintainer":                 "me",
	}

	routers, services := parseTraefikLabels(labels)

	if len(routers) != 0 {
		t.Errorf("routers len = %d, want 0", len(routers))
	}
	if len(services) != 0 {
		t.Errorf("services len = %d, want 0", len(services))
	}
}

func TestParseTraefikLabels_CertResolverImpliesTLS(t *testing.T) {
	labels := map[string]string{
		"traefik.http.routers.app.rule":             "Host(`app.com`)",
		"traefik.http.routers.app.tls.certresolver": "le",
	}

	routers, _ := parseTraefikLabels(labels)

	if len(routers) != 1 {
		t.Fatalf("routers len = %d, want 1", len(routers))
	}
	if !routers[0].TLS {
		t.Error("CertResolver should imply TLS=true")
	}
}

func TestParseTraefikLabels_CustomServiceName(t *testing.T) {
	labels := map[string]string{
		"traefik.http.routers.myrouter.rule":    "Host(`app.com`)",
		"traefik.http.routers.myrouter.service": "custom-svc",
	}

	routers, _ := parseTraefikLabels(labels)

	if len(routers) != 1 {
		t.Fatalf("routers len = %d, want 1", len(routers))
	}
	if routers[0].ServiceName != "custom-svc" {
		t.Errorf("ServiceName = %q, want %q", routers[0].ServiceName, "custom-svc")
	}
}

func TestExtractDomainsFromRule(t *testing.T) {
	tests := []struct {
		name string
		rule string
		want []string
	}{
		{
			"single host",
			"Host(`example.com`)",
			[]string{"example.com"},
		},
		{
			"multiple Host() with OR",
			"Host(`a.com`) || Host(`b.com`)",
			[]string{"a.com", "b.com"},
		},
		{
			"no host",
			"PathPrefix(`/api`)",
			nil,
		},
		{
			"host with path",
			"Host(`example.com`) && PathPrefix(`/api`)",
			[]string{"example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDomainsFromRule(tt.rule)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d (%v)", len(got), len(tt.want), got)
			}
			for i, d := range tt.want {
				if got[i] != d {
					t.Errorf("domains[%d] = %q, want %q", i, got[i], d)
				}
			}
		})
	}
}

func TestSplitTrim(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b , c ", []string{"a", "b", "c"}},
		{"single", []string{"single"}},
		{"", nil},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitTrim(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d (%v)", len(got), len(tt.want), got)
			}
			for i, v := range tt.want {
				if got[i] != v {
					t.Errorf("[%d] = %q, want %q", i, got[i], v)
				}
			}
		})
	}
}
