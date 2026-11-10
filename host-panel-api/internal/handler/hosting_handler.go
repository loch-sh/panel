package handler

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"host-panel-api/internal/hosting"
	"host-panel-api/internal/middleware"
)

// HostingManager is the subset of hosting.Manager used by the handler. It is
// an interface so the handler can be tested with a mock.
type HostingManager interface {
	List(ctx context.Context) ([]hosting.StackSummary, error)
	Get(ctx context.Context, name string) (*hosting.StackDetail, error)
	Sites() ([]hosting.SiteInfo, error)
	PHPModules(ctx context.Context, template hosting.Template, version string) ([]hosting.PHPModule, error)
	CheckName(ctx context.Context, name string) error
	Create(ctx context.Context, spec *hosting.Spec, snippets hosting.Snippets, actor string) (*hosting.Operation, error)
	Update(ctx context.Context, name string, spec *hosting.Spec, snippets hosting.Snippets, force bool, actor string) (*hosting.Operation, error)
	Deploy(ctx context.Context, name string, pull, force bool, actor string) (*hosting.Operation, error)
	Delete(ctx context.Context, name string, purgeData bool, actor string) (*hosting.Operation, error)
}

// HostingDefaults are the values preselected in the creation form.
type HostingDefaults struct {
	PHPVersion   string              `json:"phpVersion"`
	DocumentRoot string              `json:"documentRoot"`
	PHP          hosting.PHPSettings `json:"php"`
	Resources    hosting.Resources   `json:"resources"`
}

// HostingHandler exposes the hosting plugin (self-service web stacks).
type HostingHandler struct {
	Manager  HostingManager
	Enabled  bool
	SitesDir string
	Defaults HostingDefaults
	Limits   hosting.Limits
	Logger   *slog.Logger
	// DefaultNetworks are preselected for a new stack (the shared network
	// of the services module, when enabled).
	DefaultNetworks []string
}

type hostingConfigResponse struct {
	Enabled        bool                 `json:"enabled"`
	SitesDirectory string               `json:"sitesDirectory,omitempty"`
	Templates      []hosting.Template   `json:"templates,omitempty"`
	PHPVersions    []hosting.PHPVersion `json:"phpVersions,omitempty"`
	Defaults       *HostingDefaults     `json:"defaults,omitempty"`
	Limits         *hosting.Limits      `json:"limits,omitempty"`
	// DefaultNetworks are preselected for a new stack.
	DefaultNetworks []string `json:"defaultNetworks"`
}

// hostingStackRequest is the body of create and update requests.
type hostingStackRequest struct {
	hosting.Spec
	hosting.Snippets
}

type hostingOperationResponse struct {
	Name      string             `json:"name"`
	Operation *hosting.Operation `json:"operation"`
}

type checkNameResponse struct {
	Available bool   `json:"available"`
	Code      string `json:"code,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Error     string `json:"error,omitempty"`
}

// hostingStackDetail adds the masking flag to the stack details.
type hostingStackDetail struct {
	*hosting.StackDetail
	SecretsMasked bool `json:"secretsMasked,omitempty"`
}

// Config returns the plugin configuration. It is always reachable (admins) so
// the frontend can decide whether to show the plugin.
// GET /hosting/config
func (h *HostingHandler) Config(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.UserFromCtx(r.Context()); !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}
	resp := hostingConfigResponse{Enabled: h.Enabled}
	if h.Enabled {
		resp.SitesDirectory = h.SitesDir
		resp.Templates = hosting.Templates
		resp.PHPVersions = hosting.PHPVersions(time.Now())
		resp.Defaults = &h.Defaults
		resp.Limits = &h.Limits
		resp.DefaultNetworks = h.DefaultNetworks
		if resp.DefaultNetworks == nil {
			resp.DefaultNetworks = []string{}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// List returns the hosted stacks.
// GET /hosting/stacks
func (h *HostingHandler) List(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.precheck(w, r, "hosting:read"); !ok {
		return
	}
	stacks, err := h.Manager.List(r.Context())
	if err != nil {
		h.writeHostingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stacks)
}

// Get returns a hosted stack with its settings.
// GET /hosting/stacks/{name}
func (h *HostingHandler) Get(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.precheck(w, r, "hosting:read"); !ok {
		return
	}
	stack, err := h.Manager.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		h.writeHostingError(w, err)
		return
	}
	// Env values and snippets may hold secrets (database passwords, tokens):
	// a read-only API key only sees the variable names. Browser sessions and
	// keys allowed to write the stack see everything.
	resp := hostingStackDetail{StackDetail: stack}
	if !middleware.RequireScope(r.Context(), "hosting:write") {
		resp.SecretsMasked = true
		maskSecrets(stack)
	}
	writeJSON(w, http.StatusOK, resp)
}

func maskSecrets(stack *hosting.StackDetail) {
	if stack.Spec != nil {
		env := make([]hosting.EnvVar, len(stack.Spec.Env))
		for i, v := range stack.Spec.Env {
			env[i] = hosting.EnvVar{Name: v.Name}
		}
		stack.Spec.Env = env
	}
	stack.Snippets = hosting.Snippets{}
	stack.Compose = "" // holds the env values too
	stack.RoutingFile = ""
}

// Sites lists the site directories and the stack using each of them.
// GET /hosting/sites
func (h *HostingHandler) Sites(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.precheck(w, r, "hosting:read"); !ok {
		return
	}
	sites, err := h.Manager.Sites()
	if err != nil {
		h.writeHostingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sites)
}

type phpModulesResponse struct {
	Modules []hosting.PHPModule `json:"modules"`
}

// PHPModules lists the PHP modules of the image of a template and version.
// The first call for an image may pull it (slow).
// GET /hosting/php-modules?template=php-nginx&version=8.4
func (h *HostingHandler) PHPModules(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.precheck(w, r, "hosting:read"); !ok {
		return
	}
	q := r.URL.Query()
	modules, err := h.Manager.PHPModules(r.Context(), hosting.Template(q.Get("template")), q.Get("version"))
	if err != nil {
		h.writeHostingError(w, err)
		return
	}
	if modules == nil {
		modules = []hosting.PHPModule{}
	}
	writeJSON(w, http.StatusOK, phpModulesResponse{Modules: modules})
}

type routingTemplateResponse struct {
	Rules string `json:"rules"`
}

// RoutingTemplate returns the framework rules the panel would generate, as a
// starting point for custom routing rules.
// GET /hosting/routing-template?framework=symfony|laravel&template=php-nginx&documentRoot=web/public&assetDirs=bundles,build&maxExecutionTime=60
// (framework defaults to symfony; assetDirs only apply to symfony).
func (h *HostingHandler) RoutingTemplate(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.precheck(w, r, "hosting:read"); !ok {
		return
	}
	q := r.URL.Query()
	spec := &hosting.Spec{
		Name:         "preview",
		Template:     hosting.Template(q.Get("template")),
		DocumentRoot: q.Get("documentRoot"),
		Routing:      cmp.Or(q.Get("framework"), hosting.RoutingSymfony),
		PHP:          &hosting.PHPSettings{},
	}
	if dirs := q.Get("assetDirs"); dirs != "" && spec.Routing == hosting.RoutingSymfony {
		spec.AssetDirs = strings.Split(dirs, ",")
	}
	if v := q.Get("maxExecutionTime"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, hosting.CodeInvalidPHPSetting, "maxExecutionTime must be an integer")
			return
		}
		spec.PHP.MaxExecutionTime = n
	}
	rules, err := hosting.PreviewRules(spec)
	if err != nil {
		h.writeHostingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, routingTemplateResponse{Rules: rules})
}

// CheckName tells whether a stack name is available (live form validation).
// GET /hosting/stacks/check-name?name=
func (h *HostingHandler) CheckName(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.precheck(w, r, "hosting:read"); !ok {
		return
	}
	err := h.Manager.CheckName(r.Context(), r.URL.Query().Get("name"))
	var herr *hosting.Error
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, checkNameResponse{Available: true})
	case errors.As(err, &herr) && (herr.Code == hosting.CodeNameTaken || herr.Code == hosting.CodeInvalidName):
		writeJSON(w, http.StatusOK, checkNameResponse{Code: herr.Code, Reason: herr.Reason, Error: herr.Message})
	default:
		h.writeHostingError(w, err)
	}
}

// Create creates a stack and deploys it asynchronously (202).
// POST /hosting/stacks
func (h *HostingHandler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := h.precheck(w, r, "hosting:write")
	if !ok {
		return
	}
	var req hostingStackRequest
	if !readJSONBody(w, r, &req) {
		return
	}
	op, err := h.Manager.Create(r.Context(), &req.Spec, req.Snippets, user)
	if err != nil {
		h.writeHostingError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, hostingOperationResponse{Name: req.Name, Operation: op})
}

// Update replaces the settings of a stack and redeploys it (202).
// PUT /hosting/stacks/{name}?force=true overwrites a manually modified stack.
func (h *HostingHandler) Update(w http.ResponseWriter, r *http.Request) {
	user, ok := h.precheck(w, r, "hosting:write")
	if !ok {
		return
	}
	var req hostingStackRequest
	if !readJSONBody(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	op, err := h.Manager.Update(r.Context(), name, &req.Spec, req.Snippets, queryBool(r, "force"), user)
	if err != nil {
		h.writeHostingError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, hostingOperationResponse{Name: name, Operation: op})
}

// Deploy redeploys a stack as it is on disk (202).
// POST /hosting/stacks/{name}/deploy?pull=true pulls the image first;
// force=true is required when the compose file was edited outside the panel.
func (h *HostingHandler) Deploy(w http.ResponseWriter, r *http.Request) {
	user, ok := h.precheck(w, r, "hosting:write")
	if !ok {
		return
	}
	name := r.PathValue("name")
	op, err := h.Manager.Deploy(r.Context(), name, queryBool(r, "pull"), queryBool(r, "force"), user)
	if err != nil {
		h.writeHostingError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, hostingOperationResponse{Name: name, Operation: op})
}

// Delete removes a stack (202). The site data is kept unless purgeData=true.
// DELETE /hosting/stacks/{name}
func (h *HostingHandler) Delete(w http.ResponseWriter, r *http.Request) {
	user, ok := h.precheck(w, r, "hosting:write")
	if !ok {
		return
	}
	name := r.PathValue("name")
	op, err := h.Manager.Delete(r.Context(), name, queryBool(r, "purgeData"), user)
	if err != nil {
		h.writeHostingError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, hostingOperationResponse{Name: name, Operation: op})
}

// --- helpers ---

// precheck enforces authentication, the enabled flag and the API key scope.
// It returns the acting user's email.
func (h *HostingHandler) precheck(w http.ResponseWriter, r *http.Request, scope string) (string, bool) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return "", false
	}
	if !h.Enabled {
		writeError(w, http.StatusNotFound, ErrHostingDisabled, "Hosting is disabled")
		return "", false
	}
	if !middleware.RequireScope(r.Context(), scope) {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return "", false
	}
	return user.Email, true
}

func queryBool(r *http.Request, key string) bool {
	v, _ := strconv.ParseBool(r.URL.Query().Get(key))
	return v
}

// hostingErrorStatus maps hosting error codes to HTTP statuses.
var hostingErrorStatus = map[string]int{
	hosting.CodeInvalidNetwork:   http.StatusBadRequest,
	hosting.CodeStackNotFound:    http.StatusNotFound,
	hosting.CodeNameTaken:        http.StatusConflict,
	hosting.CodeDomainInUse:      http.StatusConflict,
	hosting.CodeSiteInUse:        http.StatusConflict,
	hosting.CodeOperationRunning: http.StatusConflict,
	hosting.CodeManuallyModified: http.StatusConflict,
	hosting.CodeStorage:          http.StatusInternalServerError,
	hosting.CodeImageInspect:     http.StatusBadGateway,
	hosting.CodeInvalidPHPModule: http.StatusBadRequest,
}

func (h *HostingHandler) writeHostingError(w http.ResponseWriter, err error) {
	writeHostingError(w, h.Logger, err)
}

// writeHostingError maps a hosting error to its status (also used for the
// members changed by the networks routes).
func writeHostingError(w http.ResponseWriter, logger *slog.Logger, err error) {
	var herr *hosting.Error
	if !errors.As(err, &herr) {
		logger.Error("hosting request failed", "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternalError, "Internal error")
		return
	}
	status, ok := hostingErrorStatus[herr.Code]
	if !ok {
		status = http.StatusBadRequest
	}
	if status == http.StatusInternalServerError {
		// Storage errors may contain host paths: log them, return a generic message.
		logger.Error("hosting storage error", "error", herr.Message)
		writeError(w, status, herr.Code, "Storage error")
		return
	}
	body := map[string]string{"code": herr.Code, "error": herr.Message}
	if herr.Field != "" {
		body["field"] = herr.Field
	}
	if herr.Reason != "" {
		body["reason"] = herr.Reason
	}
	writeJSON(w, status, body)
}
