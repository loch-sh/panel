package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLogging_StatusCapture(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tests := []struct {
		name       string
		statusCode int
	}{
		{"200 OK", http.StatusOK},
		{"404 Not Found", http.StatusNotFound},
		{"500 Internal Server Error", http.StatusInternalServerError},
		{"403 Forbidden", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.statusCode {
				t.Errorf("status = %d, expected %d", rr.Code, tt.statusCode)
			}
		})
	}
}

func TestStatusWriter_Unwrap(t *testing.T) {
	inner := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: inner, status: http.StatusOK}

	unwrapped := sw.Unwrap()
	if unwrapped != inner {
		t.Error("Unwrap() must return the original ResponseWriter (needed for http.Hijacker)")
	}
}
