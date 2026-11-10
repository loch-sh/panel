package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"host-panel-api/internal/model"
)

func TestAuditRequests(t *testing.T) {
	var buf bytes.Buffer
	audit := slog.New(slog.NewJSONHandler(&buf, nil))
	auth := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Like APIKeyOrJWT: the user is set by a middleware inside the route.
		ctx := WithUser(r.Context(), &model.User{Email: "alice@x.fr"})
		ctx = WithAPIKey(ctx, &model.APIKey{ID: "key1"})
		r = r.WithContext(ctx)
		switch r.URL.Path {
		case "/denied":
			w.WriteHeader(http.StatusForbidden)
		case "/boom":
			panic("handler failure")
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	// The mux sets the pattern of known routes.
	mux := http.NewServeMux()
	for _, p := range []string{"GET /docker", "GET /docker/{id}", "POST /cron", "GET /denied", "POST /boom"} {
		mux.Handle(p, auth)
	}
	for _, p := range []string{"POST /unauthorized"} {
		mux.Handle(p, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	}
	// Like SameOrigin: refuses before the mux.
	deny := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Test-Deny") != "" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	h := AuditRequests(audit)(deny(mux))

	run := func(method, path string, hdr map[string]string) map[string]any {
		t.Helper()
		buf.Reset()
		req := httptest.NewRequest(method, path+"?password=secret", nil)
		for k, v := range hdr {
			if k == "remote" {
				req.RemoteAddr = v
				continue
			}
			req.Header.Set(k, v)
		}
		func() {
			defer func() {
				if v := recover(); v != nil && path != "/boom" {
					t.Fatalf("unexpected panic: %v", v)
				}
			}()
			h.ServeHTTP(httptest.NewRecorder(), req)
			if path == "/boom" {
				t.Fatal("the panic must be propagated to Recovery")
			}
		}()
		if buf.Len() == 0 {
			return nil
		}
		if strings.Contains(buf.String(), "secret") {
			t.Fatalf("query string logged: %s", buf.String())
		}
		var m map[string]any
		if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	if m := run("GET", "/docker", nil); m != nil {
		t.Fatalf("plain reads are not audited: %v", m)
	}
	m := run("POST", "/cron", nil)
	if m["msg"] != "api request" || m["user"] != "alice@x.fr" || m["api_key"] != "key1" || m["status"] != float64(204) || m["path"] != "/cron" {
		t.Fatalf("write = %v", m)
	}
	m = run("GET", "/denied", nil)
	if m["msg"] != "access denied" || m["level"] != "WARN" {
		t.Fatalf("refused read = %v", m)
	}
	if m := run("GET", "/docker/abc", nil); m["msg"] != "api request" {
		t.Fatalf("sensitive read = %v", m)
	}
	if m := run("POST", "/boom", nil); m["status"] != float64(500) {
		t.Fatalf("a panicking handler must still be audited: %v", m)
	}
	if m := run("POST", "/unknown/"+strings.Repeat("x", 5000), nil); m != nil {
		t.Fatalf("unknown routes are not audited: %v", m)
	}
	if m := run("POST", "/cron/"+strings.Repeat("x", 5000), nil); m != nil {
		t.Fatalf("unknown routes are not audited: %v", m)
	}
	// Refusals are audited whatever the route (CSRF refusals happen before
	// routing).
	if m := run("POST", "/denied-before-routing", map[string]string{"X-Test-Deny": "1", "remote": "198.51.100.1:1"}); m["msg"] != "access denied" {
		t.Fatalf("refusal on unknown route = %v", m)
	}
	if m := run("POST", "/unauthorized", map[string]string{"X-API-Key": "bad", "remote": "198.51.100.2:1"}); m["msg"] != "access denied" {
		t.Fatalf("401 with an API key = %v", m)
	}
	if m := run("POST", "/unauthorized", map[string]string{"remote": "198.51.100.3:1"}); m["msg"] != "api request" || m["status"] != float64(401) {
		t.Fatalf("401 write without key = %v", m)
	}
	// Anonymous refusals from one IP: one entry per minute.
	if m := run("POST", "/unauthorized", map[string]string{"X-API-Key": "bad", "remote": "198.51.100.2:1"}); m != nil {
		t.Fatalf("anonymous refusals must be aggregated: %v", m)
	}
}

func TestAuditPathTruncated(t *testing.T) {
	if got := truncatePath(strings.Repeat("a", 2000)); len(got) > maxAuditPath+4 {
		t.Fatalf("path not truncated: %d", len(got))
	}
}

func TestAuditRateLimitedOncePerMinute(t *testing.T) {
	var buf bytes.Buffer
	audit := slog.New(slog.NewJSONHandler(&buf, nil))
	h := AuditRequests(audit)(RateLimit(1, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))
	for range 5 {
		req := httptest.NewRequest("POST", "/cron", nil)
		req.RemoteAddr = "203.0.113.9:1234"
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if n := strings.Count(buf.String(), `"rate limited"`); n != 1 {
		t.Fatalf("%d rate limited entries, want 1: %s", n, buf.String())
	}
}

func TestTruncateUTF8(t *testing.T) {
	if got := TruncateUTF8("ééé", 3); got != "é…" {
		t.Fatalf("%q", got)
	}
	if got := TruncateUTF8("abc", 3); got != "abc" {
		t.Fatalf("%q", got)
	}
}

func TestRateLimitedIPs(t *testing.T) {
	l := &rateLimitedIPs{seen: map[string]time.Time{}}
	now := time.Now()
	if !l.first("a", now) || l.first("a", now.Add(30*time.Second)) || !l.first("a", now.Add(61*time.Second)) {
		t.Fatal("one report per IP and minute")
	}
	for i := range maxRateLimitedIPs {
		l.first(fmt.Sprint(i), now)
	}
	if l.first("new", now.Add(time.Second)) {
		t.Fatal("bounded number of tracked IPs")
	}
	if !l.first("new", now.Add(3*time.Minute)) {
		t.Fatal("expired IPs are swept")
	}
}

// Invocations are audited aggregated by the functions module: a successful
// one writes no "api request" entry, a refused one still does.
func TestAuditRequestsSkipsAggregatedInvocations(t *testing.T) {
	var buf bytes.Buffer
	audit := slog.New(slog.NewJSONHandler(&buf, nil))
	status := http.StatusOK
	mux := http.NewServeMux()
	mux.Handle("POST /functions/{name}/invoke", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WithUser(r.Context(), &model.User{Email: "alice@x.fr"})
		w.WriteHeader(status)
	}))
	h := AuditRequests(audit)(mux)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/functions/resize/invoke", nil))
	if buf.Len() != 0 {
		t.Fatalf("invocation audited per request: %s", buf.String())
	}
	status = http.StatusForbidden
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/functions/resize/invoke", nil))
	if !strings.Contains(buf.String(), "access denied") {
		t.Fatalf("refusal not audited: %s", buf.String())
	}
	for _, status = range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusRequestEntityTooLarge, http.StatusBadGateway} {
		buf.Reset()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/functions/resize/invoke", nil))
		if !strings.Contains(buf.String(), `"status":`) {
			t.Fatalf("%d not audited: %s", status, buf.String())
		}
	}
}
