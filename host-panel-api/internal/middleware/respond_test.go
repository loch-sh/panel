package middleware

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestWriteErrorJSON(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		code    string
		message string
	}{
		{"unauthorized", 401, "unauthorized", "missing token"},
		{"forbidden", 403, "forbidden", "admin access required"},
		{"not found", 404, "not_found", "resource not found"},
		{"rate limited", 429, "rate_limited", "too many requests"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeErrorJSON(w, tt.status, tt.code, tt.message)

			resp := w.Result()
			if resp.StatusCode != tt.status {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.status)
			}

			ct := resp.Header.Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("Content-Type = %q, want %q", ct, "application/json")
			}

			var body map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}

			if body["code"] != tt.code {
				t.Errorf("code = %q, want %q", body["code"], tt.code)
			}
			if body["error"] != tt.message {
				t.Errorf("error = %q, want %q", body["error"], tt.message)
			}
		})
	}
}
