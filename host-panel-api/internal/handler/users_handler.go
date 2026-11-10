package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"host-panel-api/internal/docker"
	"host-panel-api/internal/middleware"
	"host-panel-api/internal/model"
	"host-panel-api/internal/store"
)

// UsersHandler handles the user management endpoints.
type UsersHandler struct {
	Store       store.UserStore
	APIKeyStore store.APIKeyStore
	Logger      *slog.Logger
	Audit       *slog.Logger
}

// userResponse is the public representation of a user (excludes sensitive fields).
type userResponse struct {
	Email       string        `json:"email"`
	Name        string        `json:"name"`
	Profile     model.Profile `json:"profile"`
	Permissions []string      `json:"permissions"`
}

func toUserResponse(u *model.User) userResponse {
	return userResponse{
		Email:       u.Email,
		Name:        u.Name,
		Profile:     u.Profile,
		Permissions: u.Permissions,
	}
}

// Me returns the authenticated user's profile.
func (h *UsersHandler) Me(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(user))
}

// List returns all users.
func (h *UsersHandler) List(w http.ResponseWriter, r *http.Request) {
	users := h.Store.List()
	result := make([]userResponse, 0, len(users))
	for _, u := range users {
		result = append(result, toUserResponse(u))
	}
	writeJSON(w, http.StatusOK, result)
}

// Switch toggles a user's profile between admin and restricted.
func (h *UsersHandler) Switch(w http.ResponseWriter, r *http.Request) {
	actingUser, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}
	email := r.PathValue("email")

	user, ok := h.Store.Get(email)
	if !ok {
		writeError(w, http.StatusNotFound, ErrUserNotFound, "User not found")
		return
	}

	if user.Profile == model.ProfileAdmin {
		user.Profile = model.ProfileRestricted
	} else {
		user.Profile = model.ProfileAdmin
	}

	h.Store.Set(email, user)
	h.Logger.Info("user profile toggled", "email", email, "profile", user.Profile)
	h.Audit.Info("user profile toggled", "user", actingUser.Email, "target", email, "profile", user.Profile)
	w.WriteHeader(http.StatusNoContent)
}

// Restrict sets a user to restricted and clears their permissions.
func (h *UsersHandler) Restrict(w http.ResponseWriter, r *http.Request) {
	actingUser, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}
	email := r.PathValue("email")

	user, ok := h.Store.Get(email)
	if !ok {
		writeError(w, http.StatusNotFound, ErrUserNotFound, "User not found")
		return
	}

	user.Profile = model.ProfileRestricted
	user.Permissions = []string{}

	h.Store.Set(email, user)
	h.Logger.Info("user restricted", "email", email)
	h.Audit.Info("user restricted", "user", actingUser.Email, "target", email)
	w.WriteHeader(http.StatusNoContent)
}

// UpdateACL updates a user's permissions.
func (h *UsersHandler) UpdateACL(w http.ResponseWriter, r *http.Request) {
	actingUser, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}
	email := r.PathValue("email")

	user, ok := h.Store.Get(email)
	if !ok {
		writeError(w, http.StatusNotFound, ErrUserNotFound, "User not found")
		return
	}

	// Limit the body size
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB

	var body struct {
		Permissions []string `json:"permissions"`
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRequestBody, "Invalid request body")
		return
	}

	if body.Permissions == nil {
		body.Permissions = []string{}
	}
	for _, p := range body.Permissions {
		if docker.IsFunctionContainerName(p) {
			writeError(w, http.StatusBadRequest, ErrFunctionProtected, "Function containers cannot be granted to a restricted profile")
			return
		}
	}

	user.Permissions = body.Permissions
	h.Store.Set(email, user)
	h.Logger.Info("permissions updated", "email", email, "permissions", body.Permissions)
	h.Audit.Info("permissions updated", "user", actingUser.Email, "target", email, "permissions", body.Permissions)
	w.WriteHeader(http.StatusNoContent)
}

// Delete removes a user.
func (h *UsersHandler) Delete(w http.ResponseWriter, r *http.Request) {
	actingUser, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}
	email := r.PathValue("email")

	if !h.Store.Delete(email) {
		writeError(w, http.StatusNotFound, ErrUserNotFound, "User not found")
		return
	}

	// Clean up associated API keys
	if h.APIKeyStore != nil {
		h.APIKeyStore.DeleteByEmail(email)
	}

	h.Logger.Info("user deleted", "email", email)
	h.Audit.Info("user deleted", "user", actingUser.Email, "target", email)
	w.WriteHeader(http.StatusNoContent)
}
