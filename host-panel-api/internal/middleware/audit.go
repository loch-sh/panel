package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// requestAudit collects, along the chain, who made a request: the
// authentication middlewares run inside the route handlers, after the global
// middlewares, and fill it through WithUser / WithAPIKey. Only the request
// goroutine may write it (read after ServeHTTP returns, in the same one).
type requestAudit struct {
	user   string
	apiKey string
}

type ctxKeyAudit struct{}

func auditInfo(ctx context.Context) *requestAudit {
	a, _ := ctx.Value(ctxKeyAudit{}).(*requestAudit)
	return a
}

// maxAuditPath bounds the logged path (an unauthenticated client controls it).
const maxAuditPath = 512

// sensitiveReads are reads audited like writes: they expose secrets
// (container environment, site configuration).
var sensitiveReads = map[string]bool{
	"GET /docker/{id}":           true,
	"GET /hosting/stacks/{name}": true,
	"GET /docker/{id}/cp":        true, // file download, audited even if interrupted
	"GET /logs/{id}/file":        true, // file tail, failures included
}

// aggregatedWrites are writes audited by their module, aggregated, instead
// of one "api request" entry each.
var aggregatedWrites = map[string]bool{
	"POST /functions/{name}/invoke": true,
}

// AuditRequests writes an audit entry for every request that may change
// something (any method but GET/HEAD/OPTIONS, "api request") whatever its
// outcome, for sensitive reads, and for every refused request ("access
// denied": 403, or 401 with an API key), reads included. Rate-limited
// clients produce one "rate limited" entry per IP and minute. Successful or
// failed requests to unknown routes are not audited (an anonymous client
// could fill the log); refusals are, whatever the route (a cross-origin
// write is refused before routing), bounded by the rate limit. Only the path
// is logged, truncated: query strings and bodies may carry secrets. It must
// run before RateLimit, and the middlewares between it and the mux must not
// copy the request (the mux sets r.Pattern on it).
func AuditRequests(audit *slog.Logger) func(http.Handler) http.Handler {
	limited := &rateLimitedIPs{seen: map[string]time.Time{}}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info := &requestAudit{}
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			req := r.WithContext(context.WithValue(r.Context(), ctxKeyAudit{}, info))
			panicked := true
			defer func() {
				status := sw.status
				if panicked {
					status = http.StatusInternalServerError
				}
				logRequest(audit, limited, req, info, status, time.Since(start))
			}()
			next.ServeHTTP(sw, req)
			panicked = false
		})
	}
}

func logRequest(audit *slog.Logger, limited *rateLimitedIPs, r *http.Request, info *requestAudit, status int, d time.Duration) {
	ip := ClientIP(r)
	if status == http.StatusTooManyRequests {
		if limited.first(ip, time.Now()) {
			audit.Warn("rate limited", "method", r.Method, "path", truncatePath(r.URL.Path), "ip", ip)
		}
		return
	}
	denied := status == http.StatusForbidden ||
		(status == http.StatusUnauthorized && r.Header.Get("X-API-Key") != "")
	// Anonymous refusals and failures are aggregated per IP and minute: an
	// unauthenticated client must not be able to fill the trail.
	anonymous := info.user == "" && (denied || status == http.StatusUnauthorized)
	if anonymous && !limited.first("anon "+ip, time.Now()) {
		return
	}
	// r.Pattern is set by the mux on this request: empty for unknown routes.
	if r.Pattern == "" && !denied {
		return
	}
	if safeMethod(r.Method) && !denied && !sensitiveReads[r.Pattern] {
		return
	}
	// Successful invocations are audited by the functions module,
	// aggregated per function and minute (a cron job or a script must not
	// flood the trail); refusals and failures (4xx, 5xx) still are, one by
	// one (429s once per IP and minute, as every rate limit).
	if aggregatedWrites[r.Pattern] && !denied && status < 400 {
		return
	}
	event, level := "api request", slog.LevelInfo
	if denied {
		event, level = "access denied", slog.LevelWarn
	}
	attrs := []any{
		"method", r.Method,
		"path", truncatePath(r.URL.Path),
		"status", status,
		"ip", ip,
		"duration_ms", d.Milliseconds(),
	}
	if info.user != "" {
		attrs = append(attrs, "user", info.user)
	}
	if info.apiKey != "" {
		attrs = append(attrs, "api_key", info.apiKey)
	}
	audit.Log(r.Context(), level, event, attrs...)
}

func truncatePath(p string) string {
	return TruncateUTF8(p, maxAuditPath)
}

// TruncateUTF8 cuts s to at most n bytes without splitting a character,
// marking the cut with "…".
func TruncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}

// rateLimitedIPs remembers when an IP was last reported as rate limited.
// Expired IPs are swept at most once a minute; beyond maxRateLimitedIPs
// recent ones, new IPs are not reported (bounded memory and CPU).
type rateLimitedIPs struct {
	mu        sync.Mutex
	seen      map[string]time.Time
	lastSweep time.Time
}

const maxRateLimitedIPs = 10000

func (l *rateLimitedIPs) first(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t, ok := l.seen[ip]; ok && now.Sub(t) < time.Minute {
		return false
	}
	if now.Sub(l.lastSweep) >= time.Minute {
		for k, t := range l.seen {
			if now.Sub(t) >= time.Minute {
				delete(l.seen, k)
			}
		}
		l.lastSweep = now
	}
	if len(l.seen) >= maxRateLimitedIPs {
		return false
	}
	l.seen[ip] = now
	return true
}
