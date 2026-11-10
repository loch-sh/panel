package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"host-panel-api/internal/middleware"
	"host-panel-api/internal/model"
	"host-panel-api/internal/store"
)

const (
	apiKeyPrefix    = "loch_"
	apiKeyRandBytes = 32
	maxExpiryDays   = 365
	maxKeysPerUser  = 20
	maxNameLength   = 100
)

// APIKeyHandler handles the API key management endpoints.
type APIKeyHandler struct {
	Store  store.APIKeyStore
	Logger *slog.Logger
	Audit  *slog.Logger
}

type createAPIKeyRequest struct {
	Name          string   `json:"name"`
	ExpiresInDays int      `json:"expiresInDays"`
	Scopes        []string `json:"scopes"`
}

type createAPIKeyResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Token     string    `json:"token"`
	Scopes    []string  `json:"scopes"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type listAPIKeyResponse struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"keyPrefix"`
	Scopes     []string   `json:"scopes"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

// Create generates a new API key for the authenticated user.
func (h *APIKeyHandler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var body createAPIKeyRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRequestBody, "Invalid request body")
		return
	}

	if body.Name == "" {
		writeError(w, http.StatusBadRequest, ErrNameRequired, "Name is required")
		return
	}

	if len(body.Name) > maxNameLength {
		writeError(w, http.StatusBadRequest, ErrNameTooLong, fmt.Sprintf("Name must be at most %d characters", maxNameLength))
		return
	}

	// Check key count limit
	existing := h.Store.List(user.Email)
	if len(existing) >= maxKeysPerUser {
		writeError(w, http.StatusConflict, ErrAPIKeyLimitReached, fmt.Sprintf("Maximum %d API keys per user", maxKeysPerUser))
		return
	}

	if body.ExpiresInDays < 1 || body.ExpiresInDays > maxExpiryDays {
		writeError(w, http.StatusBadRequest, ErrInvalidExpiry, fmt.Sprintf("expiresInDays must be between 1 and %d", maxExpiryDays))
		return
	}

	// Default to wildcard if no scopes provided (backward compat with old CLIs)
	if len(body.Scopes) == 0 {
		body.Scopes = []string{model.ScopeAll}
	}
	if err := model.ValidateScopes(body.Scopes); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidScopes, err.Error())
		return
	}
	// A key may only create keys within its own scopes: a limited key must
	// not be able to mint a full one.
	if caller, ok := middleware.APIKeyFromCtx(r.Context()); ok {
		for _, s := range body.Scopes {
			if !caller.HasScope(s) || (s == model.ScopeAll && !caller.HasScope(model.ScopeAll)) {
				writeError(w, http.StatusForbidden, ErrInsufficientScope, "An API key can only create keys within its own scopes")
				return
			}
		}
	}

	// Generate cryptographically secure random token
	randomBytes := make([]byte, apiKeyRandBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		h.Logger.Error("failed to generate random bytes", "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternalError, "Internal server error")
		return
	}

	token := apiKeyPrefix + hex.EncodeToString(randomBytes)
	hash := sha256.Sum256([]byte(token))
	hashHex := hex.EncodeToString(hash[:])

	// Generate UUID v4 for the key ID
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		h.Logger.Error("failed to generate UUID", "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternalError, "Internal server error")
		return
	}
	idBytes[6] = (idBytes[6] & 0x0f) | 0x40 // version 4
	idBytes[8] = (idBytes[8] & 0x3f) | 0x80 // variant 2
	id := fmt.Sprintf("%x-%x-%x-%x-%x", idBytes[0:4], idBytes[4:6], idBytes[6:8], idBytes[8:10], idBytes[10:16])

	now := time.Now()
	key := &model.APIKey{
		ID:        id,
		Name:      body.Name,
		KeyHash:   hashHex,
		KeyPrefix: token[:len(apiKeyPrefix)+8],
		UserEmail: user.Email,
		Scopes:    body.Scopes,
		ExpiresAt: now.AddDate(0, 0, body.ExpiresInDays),
		CreatedAt: now,
	}

	h.Store.Create(key)
	h.Logger.Info("api key created", "id", id, "user", user.Email, "name", body.Name)
	h.Audit.Info("api key created", "user", user.Email, "id", id, "name", body.Name)

	writeJSON(w, http.StatusCreated, createAPIKeyResponse{
		ID:        id,
		Name:      body.Name,
		Token:     token,
		Scopes:    body.Scopes,
		ExpiresAt: key.ExpiresAt,
	})
}

// List returns all API keys for the authenticated user (without hashes).
func (h *APIKeyHandler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	keys := h.Store.List(user.Email)

	result := make([]listAPIKeyResponse, 0, len(keys))
	for _, k := range keys {
		result = append(result, listAPIKeyResponse{
			ID:         k.ID,
			Name:       k.Name,
			KeyPrefix:  k.KeyPrefix,
			Scopes:     k.Scopes,
			ExpiresAt:  k.ExpiresAt,
			CreatedAt:  k.CreatedAt,
			LastUsedAt: k.LastUsedAt,
		})
	}

	writeJSON(w, http.StatusOK, result)
}

// Delete revokes an API key belonging to the authenticated user.
func (h *APIKeyHandler) Delete(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	id := r.PathValue("id")
	if !h.Store.Delete(id, user.Email) {
		writeError(w, http.StatusNotFound, ErrAPIKeyNotFound, "API key not found")
		return
	}

	h.Logger.Info("api key revoked", "id", id, "user", user.Email)
	h.Audit.Info("api key revoked", "user", user.Email, "id", id)
	w.WriteHeader(http.StatusNoContent)
}
