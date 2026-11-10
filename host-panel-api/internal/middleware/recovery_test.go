package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecovery_NoPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, expected %d", rr.Code, http.StatusOK)
	}
	if rr.Body.String() != "ok" {
		t.Errorf("body = %q, expected %q", rr.Body.String(), "ok")
	}
}

func TestRecovery_CatchesPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("unexpected error")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, expected %d", rr.Code, http.StatusInternalServerError)
	}
}

func TestRecovery_NoStackTraceInResponse(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("secret internal error details")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rr, req)

	body := rr.Body.String()

	// The body must contain neither the stack trace nor the panic message
	dangerousPatterns := []string{
		"goroutine",
		"runtime",
		"secret internal error details",
		".go:",
		"panic",
	}
	for _, pattern := range dangerousPatterns {
		if strings.Contains(body, pattern) {
			t.Errorf("response body contains %q — security information leak", pattern)
		}
	}
}
