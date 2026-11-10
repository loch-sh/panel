package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameOrigin(t *testing.T) {
	h := SameOrigin("https://Panel.example.com/app/")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	cases := []struct {
		name, method, origin, fetchSite string
		want                            int
	}{
		{"frontend", "POST", "https://panel.example.com", "same-site", http.StatusNoContent},
		{"frontend, other subdomain API", "DELETE", "https://panel.example.com", "same-site", http.StatusNoContent},
		{"CLI / MCP / API key (no browser headers)", "POST", "", "", http.StatusNoContent},
		{"CLI PUT stream", "PUT", "", "", http.StatusNoContent},
		{"other site", "POST", "https://evil.example", "cross-site", http.StatusForbidden},
		{"other site, no fetch metadata", "PATCH", "https://evil.example", "", http.StatusForbidden},
		{"other scheme", "POST", "http://panel.example.com", "", http.StatusForbidden},
		{"other port", "POST", "https://panel.example.com:8443", "", http.StatusForbidden},
		{"null origin", "POST", "null", "cross-site", http.StatusForbidden},
		{"cross-site without Origin", "POST", "", "cross-site", http.StatusForbidden},
		{"read from another site (CORS protects it)", "GET", "https://evil.example", "cross-site", http.StatusNoContent},
		{"preflight", "OPTIONS", "https://evil.example", "cross-site", http.StatusNoContent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "/cron", nil)
			if c.origin != "" {
				req.Header.Set("Origin", c.origin)
			}
			if c.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", c.fetchSite)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("%d, want %d", rec.Code, c.want)
			}
		})
	}
}

func TestSameOriginWithoutFrontURL(t *testing.T) {
	h := SameOrigin("")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest("POST", "/cron", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatal("non-browser clients must pass")
	}
	req.Header.Set("Origin", "https://panel.example.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatal("without FRONT_URL, no browser origin is trusted")
	}
}

func TestNormalizeOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"https://Panel.Example.com/app/": "https://panel.example.com",
		"https://panel.example.com:443":  "https://panel.example.com",
		"http://panel.example.com:80/":   "http://panel.example.com",
		"http://localhost:5173":          "http://localhost:5173",
		"https://panel.example.com.":     "https://panel.example.com",
		"http://[::1]:8080":              "http://[::1]:8080",
		"null":                           "",
		"":                               "",
		"panel.example.com":              "",
	} {
		if got := NormalizeOrigin(in); got != want {
			t.Errorf("NormalizeOrigin(%q) = %q, want %q", in, got, want)
		}
	}
	// FRONT_URL with the default port still matches what browsers send.
	h := SameOrigin("https://panel.example.com:443")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest("POST", "/cron", nil)
	req.Header.Set("Origin", "https://panel.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("default port: %d", rec.Code)
	}
}

// The full chain: the preflight is answered by CORS, a refusal carries the
// CORS headers so that the frontend can read it.
func TestCORSThenSameOrigin(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		CORS("https://panel.example.com"), SameOrigin("https://panel.example.com"))
	req := httptest.NewRequest("OPTIONS", "/cron", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight: %d", rec.Code)
	}
	req = httptest.NewRequest("POST", "/cron", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || rec.Header().Get("Access-Control-Allow-Origin") != "https://panel.example.com" {
		t.Fatalf("refusal: %d %v", rec.Code, rec.Header())
	}
}
