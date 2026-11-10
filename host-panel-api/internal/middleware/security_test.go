package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeaders_AllPresent(t *testing.T) {
	handler := SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rr, req)

	expected := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"X-Xss-Protection":          "0",
		"Referrer-Policy":           "strict-origin-when-cross-origin",
		"Strict-Transport-Security": "max-age=63072000; includeSubDomains",
	}

	for header, want := range expected {
		got := rr.Header().Get(header)
		if got != want {
			t.Errorf("header %q = %q, expected %q", header, got, want)
		}
	}
}

func TestSecurityHeaders_PassesThrough(t *testing.T) {
	called := false
	handler := SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("next handler was not called")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, expected %d", rr.Code, http.StatusOK)
	}
}
