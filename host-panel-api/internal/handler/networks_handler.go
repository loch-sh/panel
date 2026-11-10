package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"host-panel-api/internal/hosting"
	"host-panel-api/internal/middleware"
	"host-panel-api/internal/networks"
	"host-panel-api/internal/services"
)

// NetworksManager is the subset of networks.Manager used by the handler.
type NetworksManager interface {
	List(ctx context.Context) ([]networks.Summary, error)
	Get(ctx context.Context, name string) (*networks.Detail, error)
	Create(ctx context.Context, req networks.CreateRequest, actor string) (*networks.Network, error)
	Update(name, description, actor string) (*networks.Network, error)
	Delete(ctx context.Context, name, actor string) error
	Attach(ctx context.Context, name, kind, member, actor string) (string, error)
	Detach(ctx context.Context, name, kind, member, actor string) (string, error)
	Disconnect(ctx context.Context, name, containerID, actor string) error
	Limits() networks.Limits
	ListExternal(ctx context.Context) ([]networks.External, error)
	GetExternal(ctx context.Context, id string) (*networks.ExternalDetail, error)
}

// NetworksHandler exposes the private networks (admins only).
type NetworksHandler struct {
	Manager NetworksManager
	Logger  *slog.Logger
}

// maxNetworkBody bounds the JSON bodies of the networks routes.
const maxNetworkBody = 4 << 10

type networkMemberRequest struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// memberResponse: Operation is set for a site (asynchronous redeploy).
type memberResponse struct {
	Operation string `json:"operation,omitempty"`
}

func (h *NetworksHandler) precheck(w http.ResponseWriter, r *http.Request, scope string) (string, bool) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return "", false
	}
	if !middleware.RequireScope(r.Context(), scope) {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return "", false
	}
	return user.Email, true
}

// List returns the networks.
// GET /networks
func (h *NetworksHandler) List(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.precheck(w, r, "networks:read"); !ok {
		return
	}
	list, err := h.Manager.List(r.Context())
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// Get returns a network with its members and their addresses.
// GET /networks/{name}
func (h *NetworksHandler) Get(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.precheck(w, r, "networks:read"); !ok {
		return
	}
	d, err := h.Manager.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// Config returns the creation rules (limit, reserved and private ranges).
// GET /networks/config
func (h *NetworksHandler) Config(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.precheck(w, r, "networks:read"); !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.Manager.Limits())
}

// ListDocker returns the Docker networks the panel did not create
// (read-only).
// GET /networks/docker
func (h *NetworksHandler) ListDocker(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.precheck(w, r, "networks:read"); !ok {
		return
	}
	list, err := h.Manager.ListExternal(r.Context())
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// GetDocker returns a Docker network the panel did not create, with its
// containers (read-only).
// GET /networks/docker/{id}
func (h *NetworksHandler) GetDocker(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.precheck(w, r, "networks:read"); !ok {
		return
	}
	d, err := h.Manager.GetExternal(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// Create creates a network.
// POST /networks
func (h *NetworksHandler) Create(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, "networks:write")
	if !ok {
		return
	}
	var req networks.CreateRequest
	if !decodeStrict(w, r, &req, maxNetworkBody) {
		return
	}
	n, err := h.Manager.Create(r.Context(), req, actor)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

// Update changes the description of a network.
// PATCH /networks/{name}
func (h *NetworksHandler) Update(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, "networks:write")
	if !ok {
		return
	}
	var req struct {
		Description string `json:"description"`
	}
	if !decodeStrict(w, r, &req, maxNetworkBody) {
		return
	}
	n, err := h.Manager.Update(r.PathValue("name"), req.Description, actor)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// Delete removes a network without members.
// DELETE /networks/{name}
func (h *NetworksHandler) Delete(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, "networks:write")
	if !ok {
		return
	}
	if err := h.Manager.Delete(r.Context(), r.PathValue("name"), actor); err != nil {
		h.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Attach adds a site or an instance to a network.
// POST /networks/{name}/members
func (h *NetworksHandler) Attach(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, "networks:write")
	if !ok {
		return
	}
	var req networkMemberRequest
	if !decodeStrict(w, r, &req, maxNetworkBody) {
		return
	}
	if !functionMemberScope(w, r, req.Kind) {
		return
	}
	op, err := h.Manager.Attach(r.Context(), r.PathValue("name"), req.Kind, req.Name, actor)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, memberResponse{Operation: op})
}

// Detach removes a site or an instance from a network.
// DELETE /networks/{name}/members/{kind}/{member}
func (h *NetworksHandler) Detach(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, "networks:write")
	if !ok {
		return
	}
	if !functionMemberScope(w, r, r.PathValue("kind")) {
		return
	}
	op, err := h.Manager.Detach(r.Context(), r.PathValue("name"), r.PathValue("kind"), r.PathValue("member"), actor)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, memberResponse{Operation: op})
}

// Disconnect removes a container the panel does not manage.
// POST /networks/{name}/disconnect
func (h *NetworksHandler) Disconnect(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, "networks:write")
	if !ok {
		return
	}
	var req struct {
		ContainerID string `json:"containerId"`
	}
	if !decodeStrict(w, r, &req, maxNetworkBody) {
		return
	}
	if req.ContainerID == "" {
		writeError(w, http.StatusBadRequest, ErrInvalidRequestBody, "Invalid request body")
		return
	}
	if err := h.Manager.Disconnect(r.Context(), r.PathValue("name"), req.ContainerID, actor); err != nil {
		h.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var networksErrorStatus = map[string]int{
	networks.CodeNotFound:        http.StatusNotFound,
	networks.CodeInvalidName:     http.StatusBadRequest,
	networks.CodeNameTaken:       http.StatusConflict,
	networks.CodeInvalidSubnet:   http.StatusBadRequest,
	networks.CodeInUse:           http.StatusConflict,
	networks.CodeLimit:           http.StatusConflict,
	networks.CodeSystem:          http.StatusConflict,
	networks.CodeCompromised:     http.StatusConflict,
	networks.CodeMemberNotFound:  http.StatusNotFound,
	networks.CodeMemberManaged:   http.StatusConflict,
	networks.CodeInvalidKind:     http.StatusBadRequest,
	networks.CodeDockerError:     http.StatusBadGateway,
	networks.CodeStorage:         http.StatusInternalServerError,
	networks.CodeAlreadyAttached: http.StatusConflict,
	networks.CodeNotAttached:     http.StatusConflict,
	networks.CodeBusy:            http.StatusConflict,
}

// writeError maps the errors of the networks module, and those of the
// member it changed (a site or an instance).
func (h *NetworksHandler) writeError(w http.ResponseWriter, err error) {
	if nerr, ok := networks.AsError(err); ok {
		status, known := networksErrorStatus[nerr.Code]
		if !known {
			status = http.StatusInternalServerError
		}
		if status == http.StatusInternalServerError {
			writeError(w, status, nerr.Code, "Storage error")
			return
		}
		body := map[string]string{"code": nerr.Code, "error": nerr.Message}
		if nerr.Field != "" {
			body["field"] = nerr.Field
		}
		writeJSON(w, status, body)
		return
	}
	var serr *services.Error
	if errors.As(err, &serr) {
		writeServicesError(w, h.Logger, err)
		return
	}
	var herr *hosting.Error
	if errors.As(err, &herr) {
		writeHostingError(w, h.Logger, err)
		return
	}
	h.Logger.Error("networks request failed", "error", err)
	writeError(w, http.StatusInternalServerError, ErrInternalError, "Internal error")
}

// functionMemberScope: changing the networks of a function can expose its
// secrets (a variable pointing at a host it can now reach): it needs
// functions:deploy besides networks:write.
func functionMemberScope(w http.ResponseWriter, r *http.Request, kind string) bool {
	if kind == networks.KindFunction && !middleware.RequireScope(r.Context(), ScopeFunctionsDeploy) {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return false
	}
	return true
}
