package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"host-panel-api/internal/docker"
	"host-panel-api/internal/middleware"
	"host-panel-api/internal/model"

	"github.com/moby/moby/api/types/container"
)

// DockerHandler handles the container management endpoints.
type DockerHandler struct {
	Cache  *docker.ContainerCache
	Client *docker.Client
	Logger *slog.Logger
	Audit  *slog.Logger
}

// List returns the list of containers filtered by user profile.
func (h *DockerHandler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	if !middleware.RequireScope(r.Context(), "containers:read") {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}

	containers, err := h.Cache.List(r.Context())
	if err != nil {
		h.Logger.Error("failed to list containers", "error", err)
		writeError(w, http.StatusInternalServerError, ErrContainerListFailed, "Failed to list containers")
		return
	}

	filtered := filterNonSystem(containers)

	if user.Profile == model.ProfileRestricted {
		filtered = filterByPermissions(filtered, user.Permissions)
	}

	writeJSON(w, http.StatusOK, filtered)
}

// Inspect returns the details of a container.
func (h *DockerHandler) Inspect(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	if !middleware.RequireScope(r.Context(), "containers:read") {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}

	id := r.PathValue("id")

	info, err := h.Client.InspectContainer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrContainerNotFound, "Container not found")
		return
	}

	if !docker.UserCanAccess(user, info) {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	writeJSON(w, http.StatusOK, info)
}

// Action executes an action on a container (start, stop, kill, restart, delete).
func (h *DockerHandler) Action(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	if !middleware.RequireScope(r.Context(), "containers:actions") {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}

	id := r.PathValue("id")
	action := r.PathValue("action")

	// Check permissions before executing the action
	info, err := h.Client.InspectContainer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrContainerNotFound, "Container not found")
		return
	}

	if !docker.UserCanAccess(user, info) {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	// Started, stopped and deleted by the functions module only (it
	// verifies the container first); their logs stay readable by admins.
	if docker.IsFunctionContainer(info) {
		writeError(w, http.StatusForbidden, ErrFunctionProtected, "Function containers are managed through the functions API (/functions/{name}/{action})")
		return
	}

	switch action {
	case "start":
		err = h.Client.StartContainer(r.Context(), id)
	case "stop":
		err = h.Client.StopContainer(r.Context(), id)
	case "kill":
		err = h.Client.KillContainer(r.Context(), id)
	case "restart":
		err = h.Client.RestartContainer(r.Context(), id)
	case "delete":
		err = h.Client.RemoveContainer(r.Context(), id)
	default:
		writeError(w, http.StatusNotAcceptable, ErrUnknownAction, "Unknown action")
		return
	}

	if err != nil {
		h.Logger.Error("container action failed",
			"id", id,
			"action", action,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrActionFailed, "Failed to perform action")
		return
	}

	h.Cache.Invalidate()
	h.Audit.Info("container action", "user", user.Email, "container", info.Name, "action", action)
	w.WriteHeader(http.StatusOK)
}

// UserCanAccessContainerByID checks user permissions on a container identified
// by its ID. Used by both HTTP and WebSocket handlers.
func UserCanAccessContainerByID(ctx context.Context, client *docker.Client, user *model.User, containerID string) bool {
	if user.Profile == model.ProfileAdmin {
		return true
	}
	info, err := client.InspectContainer(ctx, containerID)
	if err != nil {
		return false
	}
	return docker.UserCanAccess(user, info)
}

// userCanAccessContainer checks whether a user has access to a given container.
// Function containers are never granted to a restricted profile (the module
// is admin only; their environment holds secrets). Paths that have the
// inspected container use docker.UserCanAccess, which also matches by
// label and project.
func userCanAccessContainer(user *model.User, containerName string) bool {
	if user.Profile == model.ProfileAdmin {
		return true
	}
	return user.Profile == model.ProfileRestricted && user.GrantsContainer(containerName)
}

// filterNonSystem excludes containers marked with a "system" label.
func filterNonSystem(containers []container.Summary) []container.Summary {
	result := make([]container.Summary, 0, len(containers))
	for _, c := range containers {
		if _, isSystem := c.Labels["system"]; !isSystem {
			result = append(result, c)
		}
	}
	return result
}

// filterByPermissions keeps only the containers the user is allowed to access.
func filterByPermissions(containers []container.Summary, permissions []string) []container.Summary {
	allowed := make(map[string]bool, len(permissions))
	for _, p := range permissions {
		if !model.IsFunctionContainerName(p) {
			allowed[p] = true
		}
	}

	result := make([]container.Summary, 0)
	for _, c := range containers {
		// Never a function container (label, name or project), whatever
		// the permissions.
		if docker.IsFunctionSummary(c) {
			continue
		}
		for _, name := range c.Names {
			cleanName := strings.TrimPrefix(name, "/")
			if allowed[cleanName] {
				result = append(result, c)
				break
			}
		}
	}
	return result
}
