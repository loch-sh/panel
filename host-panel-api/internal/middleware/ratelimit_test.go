package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestRateLimit_AllowsUnderLimit(t *testing.T) {
	mw := RateLimit(5, time.Minute)(okHandler)

	for i := 0; i < 5; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		mw.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("request %d: status = %d, expected %d", i+1, rr.Code, http.StatusOK)
		}
	}
}

func TestRateLimit_BlocksOverLimit(t *testing.T) {
	mw := RateLimit(3, time.Minute)(okHandler)

	for i := 0; i < 3; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.2:12345"
		mw.ServeHTTP(rr, req)
	}

	// The 4th request should be blocked
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:12345"
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, expected %d", rr.Code, http.StatusTooManyRequests)
	}
}

func TestRateLimit_ResetsAfterWindow(t *testing.T) {
	window := 50 * time.Millisecond
	mw := RateLimit(2, window)(okHandler)

	// Exhaust the limit
	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.3:12345"
		mw.ServeHTTP(rr, req)
	}

	// Blocked
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.3:12345"
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("should be blocked, status = %d", rr.Code)
	}

	// Wait for the window to expire
	time.Sleep(window + 10*time.Millisecond)

	// The next request should pass
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.3:12345"
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("after window reset: status = %d, expected %d", rr.Code, http.StatusOK)
	}
}

func TestRateLimit_PerIPIsolation(t *testing.T) {
	mw := RateLimit(1, time.Minute)(okHandler)

	// IP 1: one OK request
	rr1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "10.0.0.10:12345"
	mw.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Errorf("IP 1, req 1 : status = %d", rr1.Code)
	}

	// IP 1: blocked
	rr1b := httptest.NewRecorder()
	req1b := httptest.NewRequest(http.MethodGet, "/", nil)
	req1b.RemoteAddr = "10.0.0.10:12345"
	mw.ServeHTTP(rr1b, req1b)
	if rr1b.Code != http.StatusTooManyRequests {
		t.Errorf("IP 1, req 2: status = %d, expected 429", rr1b.Code)
	}

	// IP 2: should still be allowed through
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "10.0.0.11:12345"
	mw.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Errorf("IP 2, req 1: status = %d, expected 200 (per-IP isolation)", rr2.Code)
	}
}

func TestRateLimit_XForwardedFor_TrustedProxy(t *testing.T) {
	mw := RateLimit(1, time.Minute)(okHandler)

	// From localhost (trusted proxy), X-Forwarded-For is honored
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.50, 10.0.0.1")
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("first request from trusted proxy: status = %d", rr.Code)
	}

	// Second request with the same XFF -> blocked (same extracted client IP)
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "127.0.0.1:54321"
	req2.Header.Set("X-Forwarded-For", "203.0.113.50, 10.0.0.1")
	mw.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Errorf("same XFF from trusted proxy: status = %d, expected 429", rr2.Code)
	}
}

func TestRateLimit_XForwardedFor_UntrustedProxy(t *testing.T) {
	mw := RateLimit(1, time.Minute)(okHandler)

	// From an external IP, X-Forwarded-For must be IGNORED
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.5:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("first request: status = %d", rr.Code)
	}

	// Second request from the same external IP -> blocked (XFF ignored, RemoteAddr used)
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "198.51.100.5:54321"
	req2.Header.Set("X-Forwarded-For", "192.0.2.99")
	mw.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Errorf("XFF from untrusted proxy: status = %d, expected 429 (XFF must be ignored)", rr2.Code)
	}
}

func TestRateLimit_XForwardedFor_DockerNetwork(t *testing.T) {
	trustDocker(t)
	mw := RateLimit(1, time.Minute)(okHandler)

	// From a Docker network (172.17.0.1), X-Forwarded-For is honored
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.17.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("first request from Docker: status = %d", rr.Code)
	}

	// Second request with the same XFF -> blocked
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "172.17.0.1:54321"
	req2.Header.Set("X-Forwarded-For", "203.0.113.99")
	mw.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Errorf("same XFF from Docker: status = %d, expected 429", rr2.Code)
	}
}

// trustDocker trusts the Docker range for the duration of a test.
func trustDocker(t *testing.T) {
	SetTrustedProxies([]netip.Prefix{netip.MustParsePrefix("172.16.0.0/12")})
	t.Cleanup(func() { SetTrustedProxies(nil) })
}

func TestClientIPForwardedFor(t *testing.T) {
	trustDocker(t)
	cases := []struct{ remote, xff, want string }{
		{"203.0.113.7:1", "10.1.1.1", "203.0.113.7"},               // untrusted peer: header ignored
		{"127.0.0.1:1", "", "127.0.0.1"},                           // no header
		{"172.17.0.1:1", "10.1.1.1, 203.0.113.9", "203.0.113.9"},   // rightmost untrusted hop, not the forged left one
		{"172.17.0.1:1", "203.0.113.9, 172.18.0.5", "203.0.113.9"}, // trusted hops skipped
		{"172.17.0.1:1", "not-an-ip", "172.17.0.1"},                // malformed: peer
		{"127.0.0.1:1", strings.Repeat("9", 70000), "127.0.0.1"},   // oversized garbage: peer
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = c.remote
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := ClientIP(req); got != c.want {
			t.Errorf("ClientIP(%s, %.30q) = %q, want %q", c.remote, c.xff, got, c.want)
		}
	}
}

// Only the loopback is trusted by default: a container cannot forge its
// address (rate limiting, audit trail) through X-Forwarded-For.
func TestClientIPDefaultTrustsOnlyTheLoopback(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "172.17.0.5:1"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := ClientIP(req); got != "172.17.0.5" {
		t.Fatalf("ClientIP = %q", got)
	}
	req.RemoteAddr = "[::ffff:127.0.0.1]:1"
	if got := ClientIP(req); got != "203.0.113.9" {
		t.Fatalf("mapped loopback: ClientIP = %q", got)
	}
}
