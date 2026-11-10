package middleware

import (
	"net/http"

	"host-panel-api/internal/model"
)

// Admin blocks requests from non-admin users.
func Admin() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFromCtx(r.Context())
			if !ok || user.Profile != model.ProfileAdmin {
				writeErrorJSON(w, http.StatusForbidden, "FORBIDDEN", "Forbidden")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
