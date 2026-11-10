package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"

	"host-panel-api/internal/middleware"
	"host-panel-api/internal/services"
)

// ServicesManager is the subset of services.Manager used by the handler. It
// is an interface so the handler can be tested with a mock.
type ServicesManager interface {
	Instances(ctx context.Context) ([]services.InstanceSummary, error)
	Resources(ctx context.Context, key string) ([]services.ResourceSummary, error)
	Create(ctx context.Context, req services.CreateRequest, actor string) (*services.CreatedResource, error)
	Delete(ctx context.Context, key, name, actor string) (*services.DeleteResult, error)
	RotatePassword(ctx context.Context, key, name, actor string) ([]services.Credential, error)
	Retire(ctx context.Context, key, actor string) error
	Purge(ctx context.Context, key, actor string) error
	UpdateOptions(ctx context.Context, key string, changes map[string]string, actor string) (services.Options, error)
	UpdateNetworks(ctx context.Context, key string, nets []string, actor string) ([]string, error)
	SetExposure(ctx context.Context, key string, exp *services.Exposure, actor string) (*services.InstanceSummary, error)
	ExposureLimits() services.ExposureLimits
	Update(ctx context.Context, key, version, actor string) (*services.UpdateResult, error)
	CheckUpdates(ctx context.Context) (services.CatalogStatus, error)
}

// ServicesHandler exposes the services module (managed database instances).
type ServicesHandler struct {
	Manager  ServicesManager
	Registry *services.Registry
	Enabled  bool
	// ConfigError is a stable code (ConfigErrorCode) set when the module is
	// enabled but could not start; the details (host paths...) are only logged.
	ConfigError string
	Network     string
	Logger      *slog.Logger
}

type serviceEngineInfo struct {
	ID          string             `json:"id"`
	Kind        services.Kind      `json:"kind"`
	DefaultPort int                `json:"defaultPort"`
	Versions    []services.Version `json:"versions"`
	// Lifecycle: "shared" (one instance per version, resources inside) or
	// "dedicated" (one instance per resource, sized by its options).
	Lifecycle     string            `json:"lifecycle"`
	Options       []services.Option `json:"options,omitempty"`
	MaxNameLength int               `json:"maxNameLength"`
	// Unauthenticated: no authentication at all (every connected site
	// reaches the instances): shown as a warning, no password.
	Unauthenticated bool `json:"unauthenticated,omitempty"`
}

type servicesConfigResponse struct {
	Enabled bool                `json:"enabled"`
	Error   string              `json:"error,omitempty"`
	Network string              `json:"network,omitempty"`
	Engines []serviceEngineInfo `json:"engines,omitempty"`
	// Exposure is the policy bounding the published ports chosen in the
	// panel (API environment only).
	Exposure *services.ExposureLimits `json:"exposure,omitempty"`
	// Catalog is the state of the version discovery (registries,
	// endoflife.date).
	Catalog *services.CatalogStatus `json:"catalog,omitempty"`
}

type createResourceRequest struct {
	Engine  string            `json:"engine"`
	Version string            `json:"version"`
	Name    string            `json:"name"`
	Options map[string]string `json:"options,omitempty"`
	// LegacyAuth: MySQL 8.4 mysql_native_password, PostgreSQL md5.
	LegacyAuth bool `json:"legacyAuth,omitempty"`
	// Networks of a new instance (none: the shared network).
	Networks []string `json:"networks,omitempty"`
}

// maxRequestNetworks bounds the networks of a request.
const maxRequestNetworks = 16

// maxCreateOptions bounds the options of a request (engines offer a few).
const maxCreateOptions = 16

type credentialsResponse struct {
	Credentials []services.Credential `json:"credentials"`
}

// Config returns the module configuration. It is always reachable (admins)
// so the frontend can decide whether to show the module.
// GET /services/config
func (h *ServicesHandler) Config(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.UserFromCtx(r.Context()); !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}
	if !middleware.RequireScope(r.Context(), "services:read") {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}
	resp := servicesConfigResponse{Enabled: h.Enabled, Error: h.ConfigError}
	if h.Enabled && h.Registry != nil {
		resp.Network = h.Network
		status := h.Registry.Status()
		resp.Catalog = &status
		if h.Manager != nil {
			limits := h.Manager.ExposureLimits()
			resp.Exposure = &limits
		}
		for _, e := range h.Registry.Engines() {
			versions := slices.Clone(h.Registry.Versions(e.ID()))
			for i := range versions {
				versions[i].LegacyAuth = services.SupportsLegacyAuth(e, versions[i])
				versions[i].TLS = services.SupportsTLS(e, versions[i])
				versions[i].TLSOptional = services.SupportsOptionalTLS(e, versions[i])
			}
			info := serviceEngineInfo{
				ID: e.ID(), Kind: e.Kind(), DefaultPort: e.DefaultPort(), Versions: versions,
				Lifecycle: e.Lifecycle().String(), MaxNameLength: services.MaxNameLength,
				Unauthenticated: services.IsUnauthenticated(e),
			}
			if d, ok := e.(services.DedicatedEngine); ok && e.Lifecycle() == services.Dedicated {
				info.Options = d.Options()
				info.MaxNameLength = services.MaxDedicatedNameLength
			}
			resp.Engines = append(resp.Engines, info)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// ListInstances returns the instances with their state.
// GET /services/instances
func (h *ServicesHandler) ListInstances(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.precheck(w, r, "services:read"); !ok {
		return
	}
	list, err := h.Manager.Instances(r.Context())
	if err != nil {
		h.writeServicesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// ListResources returns the resources of an instance.
// GET /services/instances/{instance}/resources
func (h *ServicesHandler) ListResources(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.precheck(w, r, "services:read"); !ok {
		return
	}
	list, err := h.Manager.Resources(r.Context(), r.PathValue("instance"))
	if err != nil {
		h.writeServicesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// CreateResource provisions a resource (database and user), creating the
// instance when needed. The password is returned once.
// POST /services/resources
func (h *ServicesHandler) CreateResource(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, "services:write")
	if !ok {
		return
	}
	var req createResourceRequest
	if !decodeStrict(w, r, &req, 4<<10) {
		return
	}
	if len(req.Options) > maxCreateOptions || len(req.Networks) > maxRequestNetworks {
		writeError(w, http.StatusBadRequest, ErrInvalidRequestBody, "Too many options or networks")
		return
	}
	res, err := h.Manager.Create(r.Context(), services.CreateRequest{Engine: req.Engine, Version: req.Version, Name: req.Name,
		Options: req.Options, LegacyAuth: req.LegacyAuth, Networks: req.Networks}, actor)
	if err != nil {
		h.writeServicesError(w, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusCreated, res)
}

// RotatePassword sets a new password on the user of a resource, returned once.
// POST /services/instances/{instance}/resources/{name}/password
func (h *ServicesHandler) RotatePassword(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, "services:write")
	if !ok {
		return
	}
	creds, err := h.Manager.RotatePassword(r.Context(), r.PathValue("instance"), r.PathValue("name"), actor)
	if err != nil {
		h.writeServicesError(w, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, credentialsResponse{Credentials: creds})
}

// DeleteResource removes a resource; the instance is retired with its last one.
// DELETE /services/instances/{instance}/resources/{name}
func (h *ServicesHandler) DeleteResource(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, "services:write")
	if !ok {
		return
	}
	res, err := h.Manager.Delete(r.Context(), r.PathValue("instance"), r.PathValue("name"), actor)
	if err != nil {
		h.writeServicesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// UpdateInstance changes the mutable options of a dedicated instance, which
// is recreated with them.
// PATCH /services/instances/{instance}
func (h *ServicesHandler) UpdateInstance(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, "services:write")
	if !ok {
		return
	}
	var req struct {
		Options map[string]string `json:"options"`
	}
	if !decodeStrict(w, r, &req, 4<<10) {
		return
	}
	if len(req.Options) == 0 || len(req.Options) > maxCreateOptions {
		writeError(w, http.StatusBadRequest, ErrInvalidRequestBody, "Invalid request body")
		return
	}
	opts, err := h.Manager.UpdateOptions(r.Context(), r.PathValue("instance"), req.Options, actor)
	if err != nil {
		h.writeServicesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]services.Options{"options": opts})
}

// UpdateInstanceImage updates a dedicated instance to the latest image of
// its version, or to a newer version (body {"version": "8.4"}, optional).
// POST /services/instances/{instance}/update
func (h *ServicesHandler) UpdateInstanceImage(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, "services:write")
	if !ok {
		return
	}
	var req struct {
		Version string `json:"version"`
	}
	if r.ContentLength != 0 && !decodeStrict(w, r, &req, 1<<10) {
		return
	}
	res, err := h.Manager.Update(r.Context(), r.PathValue("instance"), req.Version, actor)
	if err != nil {
		h.writeServicesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// CheckUpdates discovers the versions now (registries, endoflife.date):
// the "check for updates" action. Throttled by the manager.
// POST /services/updates/check
func (h *ServicesHandler) CheckUpdates(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.precheck(w, r, "services:write"); !ok {
		return
	}
	status, err := h.Manager.CheckUpdates(r.Context())
	if err != nil {
		h.writeServicesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// UpdateInstanceNetworks replaces the networks of an instance, which is
// recreated on them.
// PUT /services/instances/{instance}/networks
func (h *ServicesHandler) UpdateInstanceNetworks(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, "networks:write")
	if !ok {
		return
	}
	var req struct {
		Networks []string `json:"networks"`
	}
	if !decodeStrict(w, r, &req, 4<<10) {
		return
	}
	if len(req.Networks) > maxRequestNetworks {
		writeError(w, http.StatusBadRequest, ErrInvalidRequestBody, "Invalid request body")
		return
	}
	nets, err := h.Manager.UpdateNetworks(r.Context(), r.PathValue("instance"), req.Networks, actor)
	if err != nil {
		h.writeServicesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{"networks": nets})
}

// SetExposure publishes the port of an instance, within the policy of the
// API configuration. Dedicated scope: publishing a database is the most
// sensitive change of the module.
// PUT /services/instances/{instance}/exposure
func (h *ServicesHandler) SetExposure(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, "services:expose")
	if !ok {
		return
	}
	var req struct {
		Address     string `json:"address"`
		Port        int    `json:"port"`
		TLSOptional bool   `json:"tlsOptional"`
	}
	if !decodeStrict(w, r, &req, 1<<10) {
		return
	}
	exp, perr := services.ParseExposure(req.Address, req.Port)
	if perr != nil {
		h.writeServicesError(w, perr)
		return
	}
	exp.TLSOptional = req.TLSOptional
	s, err := h.Manager.SetExposure(r.Context(), r.PathValue("instance"), &exp, actor)
	if err != nil {
		h.writeServicesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// DeleteExposure closes the published port of an instance.
// DELETE /services/instances/{instance}/exposure
func (h *ServicesHandler) DeleteExposure(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, "services:expose")
	if !ok {
		return
	}
	s, err := h.Manager.SetExposure(r.Context(), r.PathValue("instance"), nil, actor)
	if err != nil {
		h.writeServicesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// RetireInstance removes an empty instance.
// DELETE /services/instances/{instance}
func (h *ServicesHandler) RetireInstance(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, "services:write")
	if !ok {
		return
	}
	retire := h.Manager.Retire
	// ?purge=true: an instance edited by hand, deleted with its data without
	// running its files (only such an instance is accepted).
	switch r.URL.Query().Get("purge") {
	case "":
	case "true":
		retire = h.Manager.Purge
	default:
		writeError(w, http.StatusBadRequest, ErrInvalidRequestBody, "Invalid purge parameter")
		return
	}
	if err := retire(r.Context(), r.PathValue("instance"), actor); err != nil {
		h.writeServicesError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ServicesHandler) precheck(w http.ResponseWriter, r *http.Request, scope string) (string, bool) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return "", false
	}
	if !h.Enabled || h.Manager == nil {
		writeError(w, http.StatusNotFound, services.CodeDisabled, "Services are disabled")
		return "", false
	}
	if !middleware.RequireScope(r.Context(), scope) {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return "", false
	}
	return user.Email, true
}

// ConfigErrorCode is reported by GET /services/config when the module is
// enabled but could not start.
const ConfigErrorCode = "SERVICES_MISCONFIGURED"

// noStore keeps responses holding credentials out of every cache.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

var servicesErrorStatus = map[string]int{
	services.CodeDisabled:              http.StatusNotFound,
	services.CodeInstanceNotFound:      http.StatusNotFound,
	services.CodeResourceNotFound:      http.StatusNotFound,
	services.CodeInvalidEngine:         http.StatusBadRequest,
	services.CodeInvalidVersion:        http.StatusBadRequest,
	services.CodeInvalidName:           http.StatusBadRequest,
	services.CodeNameReserved:          http.StatusConflict,
	services.CodeResourceExists:        http.StatusConflict,
	services.CodeResourceNotManaged:    http.StatusConflict,
	services.CodeResourceLimit:         http.StatusConflict,
	services.CodeInstanceLimit:         http.StatusConflict,
	services.CodeInvalidOption:         http.StatusBadRequest,
	services.CodeOptionImmutable:       http.StatusBadRequest,
	services.CodeNoCredentials:         http.StatusBadRequest,
	services.CodePasswordResetRequired: http.StatusConflict,
	services.CodeInstanceNotModified:   http.StatusConflict,
	services.CodeInProgress:            http.StatusConflict,
	services.CodeDirectoryConflict:     http.StatusConflict,
	services.CodeDataDirNotEmpty:       http.StatusConflict,
	services.CodeInstanceModified:      http.StatusConflict,
	services.CodeInstanceNotEmpty:      http.StatusConflict,
	services.CodeNotReady:              http.StatusServiceUnavailable,
	services.CodeStartFailed:           http.StatusBadGateway,
	services.CodeEngineError:           http.StatusBadGateway,
	services.CodeOutcomeUnknown:        http.StatusGatewayTimeout,
	services.CodeStorage:               http.StatusInternalServerError,
	services.CodeInvalidNetwork:        http.StatusBadRequest,
	services.CodeLastNetwork:           http.StatusBadRequest,
	services.CodeInvalidExposure:       http.StatusBadRequest,
	services.CodePortInUse:             http.StatusConflict,
	services.CodeExposureFailed:        http.StatusBadGateway,
	services.CodeUpdateNotSupported:    http.StatusBadRequest,
	services.CodeVersionDowngrade:      http.StatusBadRequest,
	services.CodePullFailed:            http.StatusBadGateway,
	services.CodeReleasesUnavailable:   http.StatusServiceUnavailable,
}

func (h *ServicesHandler) writeServicesError(w http.ResponseWriter, err error) {
	writeServicesError(w, h.Logger, err)
}

// writeServicesError maps a services error to its status (also used for
// the members changed by the networks routes).
func writeServicesError(w http.ResponseWriter, logger *slog.Logger, err error) {
	var serr *services.Error
	if !errors.As(err, &serr) {
		logger.Error("services request failed", "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternalError, "Internal error")
		return
	}
	status, ok := servicesErrorStatus[serr.Code]
	if !ok {
		logger.Error("services: unmapped error code", "code", serr.Code)
		status = http.StatusInternalServerError
	}
	if status == http.StatusInternalServerError {
		writeError(w, status, serr.Code, "Storage error")
		return
	}
	body := map[string]string{"code": serr.Code, "error": serr.Message}
	if serr.Field != "" {
		body["field"] = serr.Field
	}
	writeJSON(w, status, body)
}
