package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"host-panel-api/internal/middleware"
	"host-panel-api/internal/sftpgo"
)

// SFTPClient is the subset of the SFTPGo client used by the handler.
// It is an interface so the handler can be tested with a mock.
type SFTPClient interface {
	ListUsers(ctx context.Context) ([]sftpgo.User, error)
	GetUser(ctx context.Context, username string) (*sftpgo.User, error)
	CreateUser(ctx context.Context, user *sftpgo.User) (*sftpgo.User, error)
	UpdateUser(ctx context.Context, username string, user *sftpgo.User) error
	DeleteUser(ctx context.Context, username string) error
	UserToken(ctx context.Context, username, password string) (string, error)
	CreateUserDir(ctx context.Context, token, dirPath string) error
}

// SFTPHandler manages SFTPGo users (the "web agency" plugin). SFTPGo is the
// single source of truth; this handler is a thin, validated pass-through.
type SFTPHandler struct {
	Client           SFTPClient
	Enabled          bool
	Directory        string   // base directory, e.g. /data/websites/hosted
	Domain           string   // SFTP connection domain (display only)
	Port             string   // SFTP connection port (display only)
	WebUserInterface string   // SFTPGo web client URL (display only)
	InitDirs         []string // directories created in a new user's home when requested
	Logger           *slog.Logger
	Audit            *slog.Logger
}

// --- DTOs ---

type sftpConfigResponse struct {
	Enabled          bool     `json:"enabled"`
	Directory        string   `json:"directory,omitempty"`
	Domain           string   `json:"domain,omitempty"`
	Port             string   `json:"port,omitempty"`
	WebUserInterface string   `json:"webUserInterface,omitempty"`
	InitDirs         []string `json:"initDirs,omitempty"`
}

type sftpUserResponse struct {
	Username              string   `json:"username"`
	Email                 string   `json:"email,omitempty"`
	Site                  string   `json:"site"`
	SubPath               string   `json:"subPath"`
	HomeDir               string   `json:"homeDir"`
	Status                int      `json:"status"`
	QuotaSize             int64    `json:"quotaSize"`
	QuotaFiles            int      `json:"quotaFiles"`
	UploadBandwidth       int64    `json:"uploadBandwidth"`
	DownloadBandwidth     int64    `json:"downloadBandwidth"`
	UsedQuotaSize         int64    `json:"usedQuotaSize"`
	UsedQuotaFiles        int      `json:"usedQuotaFiles"`
	RequirePasswordChange bool     `json:"requirePasswordChange"`
	LastLogin             int64    `json:"lastLogin"`
	CreatedAt             int64    `json:"createdAt"`
	PublicKeys            []string `json:"publicKeys"`
	// InitializedDirs lists the directories successfully created in the home on
	// creation; InitDirsError describes a (non-fatal) failure. Set on POST only.
	InitializedDirs []string `json:"initializedDirs,omitempty"`
	InitDirsError   string   `json:"initDirsError,omitempty"`
}

type sftpSiteResponse struct {
	Name               string `json:"name"`
	HomeDir            string `json:"homeDir"`
	UserCount          int    `json:"userCount"`
	TotalUsedQuotaSize int64  `json:"totalUsedQuotaSize"`
}

type sftpCreateRequest struct {
	Username              string `json:"username"`
	Site                  string `json:"site"`
	SubPath               string `json:"subPath"`
	Password              string `json:"password"`
	PublicKey             string `json:"publicKey"`
	QuotaSizeBytes        int64  `json:"quotaSizeBytes"`
	QuotaFiles            int    `json:"quotaFiles"`
	BandwidthKbps         int64  `json:"bandwidthKbps"`
	RequirePasswordChange bool   `json:"requirePasswordChange"`
	// InitializeDirs requests the creation of the configured directories (e.g.
	// web, logs) in the new user's home. A nil pointer defaults to true.
	InitializeDirs *bool `json:"initializeDirs"`
}

type sftpUpdateRequest struct {
	PublicKey             string `json:"publicKey"`
	QuotaSizeBytes        int64  `json:"quotaSizeBytes"`
	QuotaFiles            int    `json:"quotaFiles"`
	BandwidthKbps         int64  `json:"bandwidthKbps"`
	RequirePasswordChange bool   `json:"requirePasswordChange"`
}

// Config returns the SFTP plugin configuration. It is always reachable so the
// frontend can decide whether to show the plugin.
// GET /sftp/config
func (h *SFTPHandler) Config(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.UserFromCtx(r.Context()); !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}
	resp := sftpConfigResponse{Enabled: h.Enabled}
	if h.Enabled {
		resp.Directory = h.Directory
		resp.Domain = h.Domain
		resp.Port = h.Port
		resp.WebUserInterface = h.WebUserInterface
		resp.InitDirs = h.InitDirs
	}
	writeJSON(w, http.StatusOK, resp)
}

// ListUsers returns all SFTP users hosted under SFTP_DIRECTORY.
// GET /sftp/users
func (h *SFTPHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	if !h.precheck(w, r, "sftp:read") {
		return
	}
	users, err := h.Client.ListUsers(r.Context())
	if err != nil {
		h.writeUpstreamError(w, err)
		return
	}
	result := make([]sftpUserResponse, 0, len(users))
	for i := range users {
		site, ok := h.siteOf(users[i].HomeDir)
		if !ok {
			continue // never expose SFTPGo users outside the managed directory
		}
		result = append(result, toSFTPUserResponse(&users[i], site, h.subPathOf(users[i].HomeDir, site)))
	}
	writeJSON(w, http.StatusOK, result)
}

// ListSites returns the sites (directories) derived from the users' home dirs.
// GET /sftp/sites
func (h *SFTPHandler) ListSites(w http.ResponseWriter, r *http.Request) {
	if !h.precheck(w, r, "sftp:read") {
		return
	}
	users, err := h.Client.ListUsers(r.Context())
	if err != nil {
		h.writeUpstreamError(w, err)
		return
	}

	order := make([]string, 0)
	sites := make(map[string]*sftpSiteResponse)
	for i := range users {
		site, ok := h.siteOf(users[i].HomeDir)
		if !ok {
			continue
		}
		s, exists := sites[site]
		if !exists {
			s = &sftpSiteResponse{Name: site, HomeDir: path.Join(h.Directory, site)}
			sites[site] = s
			order = append(order, site)
		}
		s.UserCount++
		s.TotalUsedQuotaSize += users[i].UsedQuotaSize
	}

	result := make([]sftpSiteResponse, 0, len(order))
	for _, name := range order {
		result = append(result, *sites[name])
	}
	writeJSON(w, http.StatusOK, result)
}

// CreateUser creates a new SFTP user for a site.
// POST /sftp/users
func (h *SFTPHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	if !h.precheck(w, r, "sftp:write") {
		return
	}
	actingUser, _ := middleware.UserFromCtx(r.Context())

	var req sftpCreateRequest
	if !readJSONBody(w, r, &req) {
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	if !validUsername(req.Username) {
		writeError(w, http.StatusBadRequest, ErrUsernameRequired, "A valid username is required")
		return
	}
	if !validPassword(w, req.Password) {
		return
	}
	if !validLimits(w, req.QuotaSizeBytes, req.QuotaFiles, req.BandwidthKbps) {
		return
	}
	homeDir, ok := h.buildHomeDir(req.Site, req.SubPath)
	if !ok {
		writeError(w, http.StatusBadRequest, ErrInvalidSite, "Invalid site or subdirectory name")
		return
	}

	user := &sftpgo.User{
		Username:          req.Username,
		Password:          req.Password,
		HomeDir:           homeDir,
		Status:            1,
		Permissions:       map[string][]string{"/": {"*"}},
		QuotaSize:         req.QuotaSizeBytes,
		QuotaFiles:        req.QuotaFiles,
		UploadBandwidth:   req.BandwidthKbps,
		DownloadBandwidth: req.BandwidthKbps,
		Filters:           sftpgo.UserFilters{RequirePasswordChange: req.RequirePasswordChange},
	}
	if key := strings.TrimSpace(req.PublicKey); key != "" {
		user.PublicKeys = []string{key}
	}

	created, err := h.Client.CreateUser(r.Context(), user)
	if err != nil {
		h.writeUpstreamError(w, err)
		return
	}

	site, _ := h.siteOf(created.HomeDir)
	subPath := h.subPathOf(created.HomeDir, site)
	h.Logger.Info("sftp user created", "username", created.Username, "site", site, "subPath", subPath)
	h.Audit.Info("sftp user created", "user", actingUser.Email, "username", created.Username, "site", site, "subPath", subPath)

	resp := toSFTPUserResponse(created, site, subPath)
	// Optionally initialize the home directory structure. This is best-effort:
	// the user already exists, so a failure here must not fail the request.
	if req.InitializeDirs == nil || *req.InitializeDirs {
		dirs, err := h.initUserDirs(r.Context(), req.Username, req.Password)
		resp.InitializedDirs = dirs
		if err != nil {
			resp.InitDirsError = "Some directories could not be initialized"
			h.Logger.Warn("sftp dir initialization failed", "username", created.Username, "created", dirs, "error", err)
		}
	}
	writeJSON(w, http.StatusCreated, resp)
}

// UpdateUser updates the editable fields of an SFTP user.
// PUT /sftp/users/{username}
func (h *SFTPHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	if !h.precheck(w, r, "sftp:write") {
		return
	}
	actingUser, _ := middleware.UserFromCtx(r.Context())
	username := r.PathValue("username")

	existing, ok := h.fetchManagedUser(w, r.Context(), username)
	if !ok {
		return
	}

	var req sftpUpdateRequest
	if !readJSONBody(w, r, &req) {
		return
	}
	if !validLimits(w, req.QuotaSizeBytes, req.QuotaFiles, req.BandwidthKbps) {
		return
	}

	existing.QuotaSize = req.QuotaSizeBytes
	existing.QuotaFiles = req.QuotaFiles
	existing.UploadBandwidth = req.BandwidthKbps
	existing.DownloadBandwidth = req.BandwidthKbps
	existing.Filters.RequirePasswordChange = req.RequirePasswordChange
	existing.Password = "" // never resend the password on a generic update
	if key := strings.TrimSpace(req.PublicKey); key != "" {
		existing.PublicKeys = []string{key}
	} else {
		existing.PublicKeys = nil
	}

	if err := h.Client.UpdateUser(r.Context(), username, existing); err != nil {
		h.writeUpstreamError(w, err)
		return
	}

	site, _ := h.siteOf(existing.HomeDir)
	h.Logger.Info("sftp user updated", "username", username, "site", site)
	h.Audit.Info("sftp user updated", "user", actingUser.Email, "username", username, "site", site)
	writeJSON(w, http.StatusOK, toSFTPUserResponse(existing, site, h.subPathOf(existing.HomeDir, site)))
}

// DeleteUser removes an SFTP user.
// DELETE /sftp/users/{username}
func (h *SFTPHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	if !h.precheck(w, r, "sftp:write") {
		return
	}
	actingUser, _ := middleware.UserFromCtx(r.Context())
	username := r.PathValue("username")

	// Ensure the user belongs to the managed directory before deleting.
	if _, ok := h.fetchManagedUser(w, r.Context(), username); !ok {
		return
	}

	if err := h.Client.DeleteUser(r.Context(), username); err != nil {
		h.writeUpstreamError(w, err)
		return
	}

	h.Logger.Info("sftp user deleted", "username", username)
	h.Audit.Info("sftp user deleted", "user", actingUser.Email, "username", username)
	w.WriteHeader(http.StatusNoContent)
}

// ResetPassword sets a new password for an SFTP user.
// POST /sftp/users/{username}/password
func (h *SFTPHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	if !h.precheck(w, r, "sftp:write") {
		return
	}
	actingUser, _ := middleware.UserFromCtx(r.Context())
	username := r.PathValue("username")

	existing, ok := h.fetchManagedUser(w, r.Context(), username)
	if !ok {
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if !readJSONBody(w, r, &req) {
		return
	}
	if !validPassword(w, req.Password) {
		return
	}

	existing.Password = req.Password
	if err := h.Client.UpdateUser(r.Context(), username, existing); err != nil {
		h.writeUpstreamError(w, err)
		return
	}

	h.Logger.Info("sftp user password reset", "username", username)
	h.Audit.Info("sftp user password reset", "user", actingUser.Email, "username", username)
	w.WriteHeader(http.StatusNoContent)
}

// SetEnabled enables or disables an SFTP user.
// POST /sftp/users/{username}/status
func (h *SFTPHandler) SetEnabled(w http.ResponseWriter, r *http.Request) {
	if !h.precheck(w, r, "sftp:write") {
		return
	}
	actingUser, _ := middleware.UserFromCtx(r.Context())
	username := r.PathValue("username")

	existing, ok := h.fetchManagedUser(w, r.Context(), username)
	if !ok {
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !readJSONBody(w, r, &req) {
		return
	}

	existing.Status = 0
	if req.Enabled {
		existing.Status = 1
	}
	existing.Password = ""
	if err := h.Client.UpdateUser(r.Context(), username, existing); err != nil {
		h.writeUpstreamError(w, err)
		return
	}

	h.Logger.Info("sftp user status changed", "username", username, "enabled", req.Enabled)
	h.Audit.Info("sftp user status changed", "user", actingUser.Email, "username", username, "enabled", req.Enabled)
	site, _ := h.siteOf(existing.HomeDir)
	writeJSON(w, http.StatusOK, toSFTPUserResponse(existing, site, h.subPathOf(existing.HomeDir, site)))
}

// --- helpers ---

// precheck enforces authentication, the enabled flag, and the API key scope.
func (h *SFTPHandler) precheck(w http.ResponseWriter, r *http.Request, scope string) bool {
	if _, ok := middleware.UserFromCtx(r.Context()); !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return false
	}
	if !h.Enabled {
		writeError(w, http.StatusNotFound, ErrSFTPDisabled, "SFTP management is disabled")
		return false
	}
	if !middleware.RequireScope(r.Context(), scope) {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return false
	}
	return true
}

// fetchManagedUser loads a user and verifies it lives under SFTP_DIRECTORY.
// It writes the appropriate error and returns ok=false on failure.
func (h *SFTPHandler) fetchManagedUser(w http.ResponseWriter, ctx context.Context, username string) (*sftpgo.User, bool) {
	user, err := h.Client.GetUser(ctx, username)
	if err != nil {
		h.writeUpstreamError(w, err)
		return nil, false
	}
	if _, ok := h.siteOf(user.HomeDir); !ok {
		// The user exists in SFTPGo but outside the managed directory: treat as not found.
		writeError(w, http.StatusNotFound, ErrSFTPUserNotFound, "SFTP user not found")
		return nil, false
	}
	return user, true
}

// initUserDirs creates the configured directories in a freshly created user's
// home. SFTPGo only exposes user-scoped filesystem APIs, so it impersonates the
// user with the password just set. It returns the directories created and the
// first error encountered (best-effort: it keeps going after a failure).
func (h *SFTPHandler) initUserDirs(ctx context.Context, username, password string) ([]string, error) {
	if len(h.InitDirs) == 0 {
		return nil, nil
	}
	token, err := h.Client.UserToken(ctx, username, password)
	if err != nil {
		return nil, fmt.Errorf("authenticating as user: %w", err)
	}

	created := make([]string, 0, len(h.InitDirs))
	var firstErr error
	for _, dir := range h.InitDirs {
		if err := h.Client.CreateUserDir(ctx, token, "/"+dir); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("creating %q: %w", dir, err)
			}
			continue
		}
		created = append(created, dir)
	}
	return created, firstErr
}

// writeUpstreamError maps an SFTPGo client error to an HTTP response.
func (h *SFTPHandler) writeUpstreamError(w http.ResponseWriter, err error) {
	if sftpgo.NotFound(err) {
		writeError(w, http.StatusNotFound, ErrSFTPUserNotFound, "SFTP user not found")
		return
	}
	h.Logger.Error("sftpgo upstream error", "error", err)
	writeError(w, http.StatusBadGateway, ErrSFTPUpstream, "SFTP backend error")
}

// buildHomeDir builds and validates a home directory from a site name and an
// optional sub-path, guarding against path traversal. The site must be a single
// path segment directly under SFTP_DIRECTORY; the sub-path, when present, may
// span several segments but stays confined under the site directory. An empty
// sub-path grants access to the whole site.
func (h *SFTPHandler) buildHomeDir(site, subPath string) (string, bool) {
	site = strings.TrimSpace(site)
	if !validPathSegment(site) {
		return "", false
	}
	home := path.Join(h.Directory, site)
	if home != h.Directory+"/"+site {
		return "", false
	}

	subPath = strings.Trim(strings.TrimSpace(subPath), "/")
	if subPath == "" {
		return home, true
	}
	for _, seg := range strings.Split(subPath, "/") {
		if !validPathSegment(seg) {
			return "", false
		}
	}
	full := path.Join(home, subPath)
	if full != home+"/"+subPath {
		return "", false
	}
	return full, true
}

// validPathSegment reports whether s is a safe single path segment. It rejects
// the empty segment and the "." / ".." relative references (which would allow
// escaping or escalating out of the target directory), as well as path
// separators and control characters. A leading dot is allowed, so hidden
// directories such as ".config" remain valid.
func validPathSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// siteOf derives the site name (first path segment under SFTP_DIRECTORY) from a
// home directory. It returns ok=false when the path is outside the directory.
func (h *SFTPHandler) siteOf(homeDir string) (string, bool) {
	clean := path.Clean(homeDir)
	prefix := h.Directory + "/"
	if !strings.HasPrefix(clean, prefix) {
		return "", false
	}
	rel := strings.TrimPrefix(clean, prefix)
	if rel == "" {
		return "", false
	}
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		rel = rel[:i]
	}
	if rel == "" {
		return "", false
	}
	return rel, true
}

// subPathOf returns the home directory's path relative to its site directory,
// i.e. the part below SFTP_DIRECTORY/<site>. It is empty when the user owns the
// whole site.
func (h *SFTPHandler) subPathOf(homeDir, site string) string {
	if site == "" {
		return ""
	}
	clean := path.Clean(homeDir)
	base := h.Directory + "/" + site
	if clean == base {
		return ""
	}
	if rest := strings.TrimPrefix(clean, base+"/"); rest != clean {
		// A user created directly in SFTPGo (outside this API) could carry a
		// traversing home_dir; never surface such a value to clients.
		if strings.Contains(rest, "..") {
			return ""
		}
		return rest
	}
	return ""
}

func toSFTPUserResponse(u *sftpgo.User, site, subPath string) sftpUserResponse {
	keys := u.PublicKeys
	if keys == nil {
		keys = []string{}
	}
	return sftpUserResponse{
		Username:              u.Username,
		Email:                 u.Email,
		Site:                  site,
		SubPath:               subPath,
		HomeDir:               u.HomeDir,
		Status:                u.Status,
		QuotaSize:             u.QuotaSize,
		QuotaFiles:            u.QuotaFiles,
		UploadBandwidth:       u.UploadBandwidth,
		DownloadBandwidth:     u.DownloadBandwidth,
		UsedQuotaSize:         u.UsedQuotaSize,
		UsedQuotaFiles:        u.UsedQuotaFiles,
		RequirePasswordChange: u.Filters.RequirePasswordChange,
		LastLogin:             u.LastLogin,
		CreatedAt:             u.CreatedAt,
		PublicKeys:            keys,
	}
}

// decodeJSON reads a JSON body with the standard limits and rejects unknown fields.
func readJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRequestBody, "Invalid request body")
		return false
	}
	return true
}

// minPasswordLength is the minimum accepted length for SFTP passwords. These
// accounts are exposed over the network, so we reject trivially weak passwords.
const minPasswordLength = 8

// validPassword writes the appropriate error and returns false when the
// password is empty or shorter than minPasswordLength.
func validPassword(w http.ResponseWriter, password string) bool {
	if password == "" {
		writeError(w, http.StatusBadRequest, ErrPasswordRequired, "Password is required")
		return false
	}
	if len(password) < minPasswordLength {
		writeError(w, http.StatusBadRequest, ErrPasswordRequired, "Password must be at least 8 characters")
		return false
	}
	return true
}

// validLimits rejects negative quota or bandwidth values before they reach
// SFTPGo (where 0 means unlimited and negatives are undefined behavior).
func validLimits(w http.ResponseWriter, quotaSize int64, quotaFiles int, bandwidth int64) bool {
	if quotaSize < 0 || quotaFiles < 0 || bandwidth < 0 {
		writeError(w, http.StatusBadRequest, ErrInvalidRequestBody, "Quota and bandwidth values must be non-negative")
		return false
	}
	return true
}

// validUsername rejects empty, overly long, or path/control-character usernames.
func validUsername(u string) bool {
	if u == "" || len(u) > 255 {
		return false
	}
	for _, r := range u {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}
