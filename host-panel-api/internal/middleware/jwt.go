package middleware

import (
	"net/http"
	"strings"

	"host-panel-api/internal/auth"
)

// JWT extracts and verifies the JWT token from the Authorization header
// or the signed "authorization" cookie. Claims are stored in the context.
func JWT(verifier *auth.JWKSVerifier, cookies *auth.CookieManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractToken(r, cookies)
			if token == "" {
				writeErrorJSON(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
				return
			}

			claims, err := verifier.VerifyToken(r.Context(), token)
			if err != nil {
				writeErrorJSON(w, http.StatusForbidden, "FORBIDDEN", "Forbidden")
				return
			}

			ctx := WithClaims(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// extractToken attempts to obtain the JWT from the Bearer header then the signed cookie.
func extractToken(r *http.Request, cookies *auth.CookieManager) string {
	// 1. Authorization header: Bearer <token>
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		if strings.HasPrefix(authHeader, "Bearer ") {
			return authHeader[7:]
		}
	}

	// 2. Signed cookie "authorization"
	if token, err := cookies.GetSigned(r, "authorization"); err == nil {
		return token
	}

	return ""
}
