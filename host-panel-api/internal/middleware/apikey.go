package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"

	"host-panel-api/internal/store"
)

// APIKeyOrJWT returns a handler that first checks for an X-API-Key header.
// If present and valid, the associated user is injected into the context and
// the handler is called directly (skipping JWT verification).
// If the header is absent, the fallback handler is invoked (typically the JWT chain).
func APIKeyOrJWT(apiKeyStore store.APIKeyStore, userStore store.UserStore, logger *slog.Logger, handler, fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawKey := r.Header.Get("X-API-Key")
		if rawKey == "" {
			fallback.ServeHTTP(w, r)
			return
		}

		hash := sha256.Sum256([]byte(rawKey))
		hashHex := hex.EncodeToString(hash[:])

		apiKey, ok := apiKeyStore.GetByHash(hashHex)
		if !ok {
			writeErrorJSON(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
			return
		}

		// Note: the hash lookup via GetByHash is the real discriminator.
		// A constant-time comparison here would be redundant since the map
		// lookup already reveals whether the key exists. The SHA-256 hash
		// itself provides the security: an attacker cannot derive the token
		// from timing the lookup.

		if apiKey.IsExpired() {
			writeErrorJSON(w, http.StatusUnauthorized, "API_KEY_EXPIRED", "API key expired")
			return
		}

		user, ok := userStore.Get(apiKey.UserEmail)
		if !ok {
			writeErrorJSON(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
			return
		}

		// Update last used timestamp asynchronously
		go apiKeyStore.UpdateLastUsed(apiKey.ID)

		ctx := WithUser(r.Context(), user)
		ctx = WithAPIKey(ctx, apiKey)
		handler.ServeHTTP(w, r.WithContext(ctx))
	})
}
