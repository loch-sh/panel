package middleware

import (
	"net/http"
	"net/url"
	"strings"
)

// SameOrigin refuses state-changing requests sent by a browser from another
// origin than the frontend: the session cookie is SameSite=None, so a page
// on any site could otherwise post forms to the API with it (CSRF).
//
// Only browsers are concerned: they always send Origin on cross-origin
// POST/PUT/PATCH/DELETE (and Sec-Fetch-Site). Requests without these headers
// (CLI, MCP server, API keys, server to server) pass unchanged; they cannot
// carry the browser session cookie of a victim anyway.
func SameOrigin(frontURL string) func(http.Handler) http.Handler {
	allowed := NormalizeOrigin(frontURL)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !safeMethod(r.Method) && crossOrigin(r, allowed) {
				writeErrorJSON(w, http.StatusForbidden, "FORBIDDEN_ORIGIN", "Cross-origin request refused")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func crossOrigin(r *http.Request, allowed string) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		// "null" (sandboxed frame, file://) is never the frontend.
		return allowed == "" || NormalizeOrigin(origin) != allowed
	}
	// No Origin: a browser marks cross-site requests anyway.
	return r.Header.Get("Sec-Fetch-Site") == "cross-site"
}

// NormalizeOrigin reduces a URL to its origin as browsers send it
// ("scheme://host[:port]"): lowercase, default port and trailing dot of the
// host removed, path ignored. "" when it is not an absolute URL.
func NormalizeOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if strings.Contains(host, ":") {
		host = "[" + host + "]" // IPv6
	}
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host
}
