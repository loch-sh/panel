package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recovery catches panics and returns a 500 error
// without exposing the stack trace to the client.
func Recovery(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					logger.Error("panic recovered",
						"error", err,
						"stack", string(debug.Stack()),
						"method", r.Method,
						"path", r.URL.Path,
					)
					writeErrorJSON(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
