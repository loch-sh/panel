package middleware

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type visitor struct {
	count       int
	windowStart time.Time
}

// RateLimit implements a per-IP fixed-window rate limiter.
// limit is the maximum number of requests allowed per window.
func RateLimit(limit int, window time.Duration) func(http.Handler) http.Handler {
	var (
		mu       sync.Mutex
		visitors = make(map[string]*visitor)
	)

	// Periodic cleanup of expired entries
	go func() {
		ticker := time.NewTicker(window)
		defer ticker.Stop()
		for range ticker.C {
			mu.Lock()
			now := time.Now()
			for ip, v := range visitors {
				if now.Sub(v.windowStart) > window*2 {
					delete(visitors, ip)
				}
			}
			mu.Unlock()
		}
	}()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := ClientIP(r)

			mu.Lock()
			v, ok := visitors[ip]
			now := time.Now()

			if !ok || now.Sub(v.windowStart) > window {
				visitors[ip] = &visitor{count: 1, windowStart: now}
				mu.Unlock()
				next.ServeHTTP(w, r)
				return
			}

			v.count++
			if v.count > limit {
				mu.Unlock()
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}
			mu.Unlock()

			next.ServeHTTP(w, r)
		})
	}
}

// trustedProxies are the proxies allowed to set X-Forwarded-For besides
// the loopback (TRUSTED_PROXIES), set once at startup.
var trustedProxies []netip.Prefix

// SetTrustedProxies sets the proxies allowed to set X-Forwarded-For (the
// reverse proxy in front of the API, e.g. the subnet of the Traefik
// network). Called once at startup, before serving.
func SetTrustedProxies(prefixes []netip.Prefix) {
	trustedProxies = append([]netip.Prefix(nil), prefixes...)
}

// ClientIP returns the client IP in a secure way. X-Forwarded-For is only
// used when the request comes from a trusted proxy (the loopback, or
// TRUSTED_PROXIES); it is read from the right, skipping trusted
// proxies: the first other address is the one the nearest trusted proxy saw
// (entries further left are written by the client and could be forged).
// Only valid IP addresses are returned.
func ClientIP(r *http.Request) string {
	remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteHost = r.RemoteAddr
	}
	if !isTrustedProxy(remoteHost) {
		return remoteHost
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(hops[i])
		if net.ParseIP(hop) == nil {
			return remoteHost // malformed: not trusted
		}
		if !isTrustedProxy(hop) {
			return hop
		}
		remoteHost = hop
	}
	return remoteHost
}

func isTrustedProxy(ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	if a.IsLoopback() {
		return true
	}
	for _, p := range trustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
