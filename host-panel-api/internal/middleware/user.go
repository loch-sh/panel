package middleware

import (
	"log/slog"
	"net/http"

	"host-panel-api/internal/model"
	"host-panel-api/internal/store"
)

// User loads or creates the user from JWT claims.
// The first user created automatically becomes admin.
func User(userStore store.UserStore, logger, audit *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := ClaimsFromCtx(r.Context())
			if !ok {
				writeErrorJSON(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
				return
			}

			email, _ := claims["email"].(string)
			if email == "" {
				// Some providers use "preferred_username"
				email, _ = claims["preferred_username"].(string)
			}
			if email == "" {
				writeErrorJSON(w, http.StatusForbidden, "FORBIDDEN", "Forbidden")
				return
			}

			user, existed := userStore.GetOrCreate(email, func(currentCount int) *model.User {
				// Auto-provisioning: first user = admin (atomic under write lock)
				profile := model.ProfileRestricted
				if currentCount == 0 {
					profile = model.ProfileAdmin
				}
				name, _ := claims["name"].(string)
				return &model.User{
					Email:       email,
					Name:        name,
					Profile:     profile,
					Permissions: []string{},
				}
			})
			if !existed {
				logger.Info("new user created",
					"email", email,
					"profile", user.Profile,
				)
				audit.Info("user auto-provisioned", "user", email, "profile", user.Profile)
			}

			ctx := WithUser(r.Context(), user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
