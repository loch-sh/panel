package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORS_HeadersSet(t *testing.T) {
	origin := "https://app.example.com"
	handler := CORS(origin)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rr, req)

	expected := map[string]string{
		"Access-Control-Allow-Origin":      origin,
		"Access-Control-Allow-Credentials": "true",
		"Access-Control-Allow-Methods":     "GET, POST, PATCH, PUT, DELETE, HEAD, OPTIONS",
		"Access-Control-Allow-Headers":     "Content-Type, Authorization",
	}

	for header, want := range expected {
		got := rr.Header().Get(header)
		if got != want {
			t.Errorf("header %q = %q, expected %q", header, got, want)
		}
	}
}

func TestCORS_OriginMatchesConfig(t *testing.T) {
	origin := "https://specific-origin.example.com"
	handler := CORS(origin)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rr, req)

	got := rr.Header().Get("Access-Control-Allow-Origin")
	if got != origin {
		t.Errorf("Access-Control-Allow-Origin = %q, expected %q (no wildcard)", got, origin)
	}
	if got == "*" {
		t.Error("Access-Control-Allow-Origin must never be '*'")
	}
}

func TestCORS_PreflightReturns204(t *testing.T) {
	called := false
	handler := CORS("https://app.example.com")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, expected %d", rr.Code, http.StatusNoContent)
	}
	if called {
		t.Error("next handler must not be called during a preflight request")
	}
}

func TestCORS_NonPreflightPassesThrough(t *testing.T) {
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			called := false
			handler := CORS("https://app.example.com")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(method, "/", nil)
			handler.ServeHTTP(rr, req)

			if !called {
				t.Errorf("next handler was not called for %s", method)
			}
		})
	}
}
