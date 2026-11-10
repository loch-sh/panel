package handler

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"host-panel-api/internal/functions"
	"host-panel-api/internal/middleware"
)

// FunctionsManager is the subset of functions.Manager used by the handler
// (an interface so the handler can be tested with a mock).
type FunctionsManager interface {
	Info() functions.Info
	List(ctx context.Context) ([]functions.Summary, error)
	Get(ctx context.Context, name string) (*functions.Detail, error)
	Create(ctx context.Context, req functions.CreateRequest, actor string) (*functions.Created, error)
	Update(ctx context.Context, name string, req functions.UpdateRequest, actor string) (*functions.Detail, error)
	Delete(ctx context.Context, name, actor string) (*functions.DeleteResult, error)
	Purge(ctx context.Context, name, actor string) (*functions.DeleteResult, error)
	Deploy(ctx context.Context, name string, upload io.Reader, actor string) (*functions.Deployment, error)
	Deployments(ctx context.Context, name string) ([]functions.DeploymentInfo, error)
	Activate(ctx context.Context, name, id, actor string) error
	SetSecret(ctx context.Context, name, key, value, actor string) error
	DeleteSecret(ctx context.Context, name, key, actor string) error
	RotateCredentials(ctx context.Context, name string, sigv4 *bool, actor string) (*functions.Credentials, error)
	Invoke(ctx context.Context, name string, event io.Reader, actor string) (*functions.InvokeResult, error)
	Action(ctx context.Context, name, action, actor string) error
	SetNetworks(ctx context.Context, name string, nets []string, actor string) ([]string, error)
	SetEgress(ctx context.Context, name string, enabled bool, actor string) error
}

// Scopes of the functions module (admin only). functions:invoke is implied
// by no other scope; code, secrets, credentials, variables, handler,
// networks and egress need functions:deploy (they decide what the code
// does and where its secrets can go); functions:write keeps the resources,
// start / stop / restart, creation and deletion.
const (
	ScopeFunctionsRead   = "functions:read"
	ScopeFunctionsWrite  = "functions:write"
	ScopeFunctionsDeploy = "functions:deploy"
	ScopeFunctionsInvoke = "functions:invoke"
)

// FunctionsConfigErrorCode is reported by GET /functions/config when the
// module is enabled but could not start.
const FunctionsConfigErrorCode = "FUNCTIONS_MISCONFIGURED"

// FunctionsHandler exposes the functions module.
type FunctionsHandler struct {
	Manager     FunctionsManager
	Enabled     bool
	ConfigError string
	// MaxCodeBytes bounds an uploaded zip (FUNCTIONS_MAX_CODE_SIZE).
	MaxCodeBytes int64
	Logger       *slog.Logger
	// Upload pacing (0: defaults): stall timeout per read, grace period and
	// minimum average rate (bytes per second) after it.
	UploadStall   time.Duration
	UploadGrace   time.Duration
	UploadMinRate int64
	// EventReadTimeout bounds the read of an invocation event (0: 30 s).
	EventReadTimeout time.Duration
	// FlushAudit writes the pending aggregated invocation entries (called
	// after the server shut down).
	FlushAudit func()
}

// multipartOverhead is allowed on top of the zip for the multipart framing.
const multipartOverhead = 64 << 10

type functionsConfigResponse struct {
	Enabled bool            `json:"enabled"`
	Error   string          `json:"error,omitempty"`
	Config  *functions.Info `json:"config,omitempty"`
}

func (h *FunctionsHandler) precheck(w http.ResponseWriter, r *http.Request, scope string) (string, bool) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return "", false
	}
	if !middleware.RequireScope(r.Context(), scope) {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return "", false
	}
	if !h.Enabled || h.Manager == nil {
		writeError(w, http.StatusNotFound, functions.CodeDisabled, "Functions are disabled")
		return "", false
	}
	return user.Email, true
}

// Config returns the module configuration (always reachable by admins).
// GET /functions/config
func (h *FunctionsHandler) Config(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.UserFromCtx(r.Context()); !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}
	if !middleware.RequireScope(r.Context(), ScopeFunctionsRead) {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}
	resp := functionsConfigResponse{Enabled: h.Enabled && h.Manager != nil, Error: h.ConfigError}
	if resp.Enabled {
		info := h.Manager.Info()
		resp.Config = &info
	}
	writeJSON(w, http.StatusOK, resp)
}

// List returns the functions.
// GET /functions
func (h *FunctionsHandler) List(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.precheck(w, r, ScopeFunctionsRead); !ok {
		return
	}
	list, err := h.Manager.List(r.Context())
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// Get returns one function (secret names only).
// GET /functions/{name}
func (h *FunctionsHandler) Get(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.precheck(w, r, ScopeFunctionsRead); !ok {
		return
	}
	d, err := h.Manager.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// Create reserves a function; its invoke credentials are returned once.
// POST /functions
func (h *FunctionsHandler) Create(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, ScopeFunctionsWrite)
	if !ok {
		return
	}
	var req functions.CreateRequest
	if !decodeStrict(w, r, &req, 96<<10) {
		return
	}
	// Same rule as the changes: variables, extra networks, egress and a
	// SigV4 key pair (credentials) need functions:deploy.
	// The MCP opt-in widens who can trigger the function's side effects:
	// functions:deploy too.
	if (len(req.Env) > 0 || len(req.Networks) > 0 || req.Egress || req.SigV4 || req.MCPInvocable) && !middleware.RequireScope(r.Context(), ScopeFunctionsDeploy) {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}
	res, err := h.Manager.Create(r.Context(), req, actor)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusCreated, res)
}

// Update changes the configuration of a function.
// PATCH /functions/{name}
func (h *FunctionsHandler) Update(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, ScopeFunctionsWrite)
	if !ok {
		return
	}
	var req functions.UpdateRequest
	if !decodeStrict(w, r, &req, 96<<10) {
		return
	}
	// The variables and the handler decide what the code does with its
	// secrets (a variable pointing at an attacker's host), the MCP opt-in
	// who can trigger its side effects: functions:deploy.
	if (req.Env != nil || req.Handler != nil || req.MCPInvocable != nil) && !middleware.RequireScope(r.Context(), ScopeFunctionsDeploy) {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}
	d, err := h.Manager.Update(r.Context(), r.PathValue("name"), req, actor)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// Delete removes a function (?purge=true for one modified outside the
// panel).
// DELETE /functions/{name}
func (h *FunctionsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, ScopeFunctionsWrite)
	if !ok {
		return
	}
	remove := h.Manager.Delete
	switch r.URL.Query().Get("purge") {
	case "":
	case "true":
		remove = h.Manager.Purge
	default:
		writeError(w, http.StatusBadRequest, ErrInvalidRequestBody, "Invalid purge parameter")
		return
	}
	res, err := remove(r.Context(), r.PathValue("name"), actor)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// Upload pacing defaults: the server has no read timeout (streamed
// uploads), so a stalled or trickling client would hold the function lock
// and an upload slot.
const (
	defaultUploadStall   = 15 * time.Second
	defaultUploadGrace   = 30 * time.Second
	defaultUploadMinRate = 32 << 10 // bytes per second, on average after the grace period
)

var errUploadTooSlow = errors.New("upload too slow")

// uploadReader bounds an upload: every read must make progress within the
// stall timeout (a connection read deadline, refreshed per read, so that a
// blocked read is aborted), and after the grace period the average rate
// must stay above the minimum. It remembers why it stopped (the zip reader
// only sees a read error).
type uploadReader struct {
	r        io.Reader
	rc       *http.ResponseController
	deadline bool // the connection supports read deadlines
	start    time.Time
	n        int64
	stall    time.Duration
	grace    time.Duration
	minRate  int64
	hit      bool // body limit
	slow     bool // stalled or under the minimum rate
}

func (u *uploadReader) Read(p []byte) (int, error) {
	if u.start.IsZero() {
		u.start = time.Now()
	}
	if u.deadline {
		_ = u.rc.SetReadDeadline(time.Now().Add(u.stall))
	}
	n, err := u.r.Read(p)
	u.n += int64(n)
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &mbe):
		u.hit = true
	case errors.Is(err, os.ErrDeadlineExceeded):
		u.slow = true
	case err == nil:
		if elapsed := time.Since(u.start); elapsed > u.grace && float64(u.n) < float64(u.minRate)*elapsed.Seconds() {
			u.slow = true
			return n, errUploadTooSlow
		}
	}
	return n, err
}

// Deploy uploads code: a raw zip (application/zip or
// application/octet-stream) or a multipart form with a "code" part,
// streamed to disk, never buffered in memory.
// POST /functions/{name}/code
func (h *FunctionsHandler) Deploy(w http.ResponseWriter, r *http.Request) {
	w = closeOnError{w}
	actor, ok := h.precheck(w, r, ScopeFunctionsDeploy)
	if !ok {
		return
	}
	max := h.MaxCodeBytes
	if max <= 0 {
		max = functions.DefaultMaxCodeBytes
	}
	if r.ContentLength > max+multipartOverhead {
		writeError(w, http.StatusRequestEntityTooLarge, functions.CodeCodeTooLarge, "The zip is too large")
		return
	}
	body := &uploadReader{
		r: http.MaxBytesReader(w, r.Body, max+multipartOverhead), rc: http.NewResponseController(w),
		stall: orDuration(h.UploadStall, defaultUploadStall), grace: orDuration(h.UploadGrace, defaultUploadGrace),
		minRate: h.UploadMinRate,
	}
	if body.minRate <= 0 {
		body.minRate = defaultUploadMinRate
	}
	// Deadlines start with the first read: the manager may first wait for
	// the function lock.
	if err := body.rc.SetReadDeadline(time.Time{}); err == nil {
		body.deadline = true
		defer func() {
			if !body.slow {
				_ = body.rc.SetReadDeadline(time.Time{})
			}
		}()
	}
	tooSlow := func() bool {
		if body.slow {
			// Close the connection: the server must not read the rest of
			// a trickling body after the handler.
			w.Header().Set("Connection", "close")
			writeError(w, http.StatusRequestTimeout, functions.CodeCodeTimeout, "The upload stalled or is too slow")
		}
		return body.slow
	}
	var upload io.Reader
	mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch mediaType {
	case "application/zip", "application/x-zip-compressed", "application/octet-stream":
		upload = body
	case "multipart/form-data":
		part, err := codePart(body, params["boundary"])
		if err != nil {
			if tooSlow() {
				return
			}
			if body.hit {
				writeError(w, http.StatusRequestEntityTooLarge, functions.CodeCodeTooLarge, "The zip is too large")
				return
			}
			writeError(w, http.StatusBadRequest, ErrInvalidRequestBody, "Expected a multipart form with a \"code\" file")
			return
		}
		defer part.Close()
		upload = part
	default:
		writeError(w, http.StatusUnsupportedMediaType, ErrInvalidRequestBody, "Send a zip (application/zip) or a multipart form")
		return
	}
	d, err := h.Manager.Deploy(r.Context(), r.PathValue("name"), upload, actor)
	if err != nil {
		if tooSlow() {
			return
		}
		if body.hit {
			writeError(w, http.StatusRequestEntityTooLarge, functions.CodeCodeTooLarge, "The zip is too large")
			return
		}
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

// codePart returns the "code" part of a multipart body (other parts are
// skipped, bounded by the body limit).
func codePart(body io.Reader, boundary string) (io.ReadCloser, error) {
	if boundary == "" {
		return nil, errors.New("no boundary")
	}
	mr := multipart.NewReader(body, boundary)
	for range 8 {
		p, err := mr.NextPart()
		if err != nil {
			return nil, err
		}
		if p.FormName() == "code" {
			return p, nil
		}
		p.Close()
	}
	return nil, errors.New("no code part")
}

// Deployments lists the kept deployments.
// GET /functions/{name}/deployments
func (h *FunctionsHandler) Deployments(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.precheck(w, r, ScopeFunctionsRead); !ok {
		return
	}
	list, err := h.Manager.Deployments(r.Context(), r.PathValue("name"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// Activate activates a kept deployment (rollback).
// POST /functions/{name}/deployments/{id}/activate
func (h *FunctionsHandler) Activate(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, ScopeFunctionsDeploy)
	if !ok {
		return
	}
	if err := h.Manager.Activate(r.Context(), r.PathValue("name"), r.PathValue("id"), actor); err != nil {
		h.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SetSecret sets a secret; the value (body {"value": "…"}) is never echoed.
// PUT /functions/{name}/secrets/{key}
func (h *FunctionsHandler) SetSecret(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, ScopeFunctionsDeploy)
	if !ok {
		return
	}
	var req struct {
		Value *string `json:"value"`
	}
	if !decodeStrict(w, r, &req, 16<<10) {
		return
	}
	if req.Value == nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRequestBody, "Invalid request body")
		return
	}
	if err := h.Manager.SetSecret(r.Context(), r.PathValue("name"), r.PathValue("key"), *req.Value, actor); err != nil {
		h.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteSecret removes a secret.
// DELETE /functions/{name}/secrets/{key}
func (h *FunctionsHandler) DeleteSecret(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, ScopeFunctionsDeploy)
	if !ok {
		return
	}
	if err := h.Manager.DeleteSecret(r.Context(), r.PathValue("name"), r.PathValue("key"), actor); err != nil {
		h.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RotateCredentials replaces the invoke credentials, returned once. An
// optional body {"sigv4": true|false} enables or disables the SigV4 key
// pair (off by default).
// POST /functions/{name}/credentials/rotate
func (h *FunctionsHandler) RotateCredentials(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, ScopeFunctionsDeploy)
	if !ok {
		return
	}
	var req struct {
		SigV4 *bool `json:"sigv4"`
	}
	if r.ContentLength != 0 && !decodeStrict(w, r, &req, 1<<10) {
		return
	}
	c, err := h.Manager.RotateCredentials(r.Context(), r.PathValue("name"), req.SigV4, actor)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, c)
}

// invokeResponse is the fixed envelope of an invocation. Everything in it
// was written by the function: rendered as text only, never with the
// function's headers or content type.
type invokeResponse struct {
	StatusCode    int    `json:"statusCode"`
	FunctionError string `json:"functionError,omitempty"`
	RequestID     string `json:"requestId,omitempty"`
	DurationMs    int64  `json:"durationMs"`
	// Payload is the response as text when it is valid UTF-8; otherwise
	// PayloadBase64 holds it.
	Payload       *string `json:"payload,omitempty"`
	PayloadBase64 string  `json:"payloadBase64,omitempty"`
	// LogTail is the end of the invocation's logs (at most 4 KiB), invalid
	// UTF-8 replaced.
	LogTail string `json:"logTail"`
}

// Invoke runs one invocation; the body is the event (one JSON value, at
// most 6 MiB).
// POST /functions/{name}/invoke
func (h *FunctionsHandler) Invoke(w http.ResponseWriter, r *http.Request) {
	w = closeOnError{w}
	actor, ok := h.precheck(w, r, ScopeFunctionsInvoke)
	if !ok {
		return
	}
	if r.ContentLength > functions.MaxEventBytes {
		writeError(w, http.StatusRequestEntityTooLarge, functions.CodeEventTooLarge, "The event is too large")
		return
	}
	// The manager checks the function and takes a slot before it reads
	// the body (bounded here in size and in time: the whole event within
	// EventReadTimeout of the first read).
	event := &eventReader{r: http.MaxBytesReader(w, r.Body, functions.MaxEventBytes), rc: http.NewResponseController(w),
		timeout: orDuration(h.EventReadTimeout, defaultEventReadTimeout)}
	defer event.reset()
	res, err := h.Manager.Invoke(r.Context(), r.PathValue("name"), event, actor)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	out := invokeResponse{
		StatusCode: res.StatusCode, FunctionError: res.FunctionError, RequestID: res.RequestID,
		DurationMs: res.DurationMs, LogTail: strings.ToValidUTF8(string(res.LogTail), "�"),
	}
	if utf8.Valid(res.Payload) {
		s := string(res.Payload)
		out.Payload = &s
	} else {
		out.PayloadBase64 = base64.StdEncoding.EncodeToString(res.Payload)
	}
	// Fixed headers: never a header or a content type of the function.
	noStore(w)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Disposition", "attachment")
	writeJSON(w, http.StatusOK, out)
}

// Action starts, stops or restarts a function.
// POST /functions/{name}/{action}
func (h *FunctionsHandler) Action(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, ScopeFunctionsWrite)
	if !ok {
		return
	}
	if err := h.Manager.Action(r.Context(), r.PathValue("name"), r.PathValue("action"), actor); err != nil {
		h.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SetNetworks replaces the networks of a function (functions:deploy: what
// it can reach decides where its secrets can go).
// PUT /functions/{name}/networks
func (h *FunctionsHandler) SetNetworks(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, ScopeFunctionsDeploy)
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
	nets, err := h.Manager.SetNetworks(r.Context(), r.PathValue("name"), req.Networks, actor)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{"networks": nets})
}

// SetEgress gives or removes outbound access (functions:deploy).
// PUT /functions/{name}/egress
func (h *FunctionsHandler) SetEgress(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.precheck(w, r, ScopeFunctionsDeploy)
	if !ok {
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeStrict(w, r, &req, 1<<10) {
		return
	}
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRequestBody, "Invalid request body")
		return
	}
	if err := h.Manager.SetEgress(r.Context(), r.PathValue("name"), *req.Enabled, actor); err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"egress": *req.Enabled})
}

// functionsErrorStatus maps every functions code to an HTTP status.
var functionsErrorStatus = map[string]int{
	functions.CodeInvalidName:        http.StatusBadRequest,
	functions.CodeInvalidRuntime:     http.StatusBadRequest,
	functions.CodeInvalidHandler:     http.StatusBadRequest,
	functions.CodeInvalidEnv:         http.StatusBadRequest,
	functions.CodeReservedEnv:        http.StatusBadRequest,
	functions.CodeInvalidSecret:      http.StatusBadRequest,
	functions.CodeInvalidLimits:      http.StatusBadRequest,
	functions.CodeInvalidNetwork:     http.StatusBadRequest,
	functions.CodeInvalidCode:        http.StatusBadRequest,
	functions.CodeCodeTooLarge:       http.StatusRequestEntityTooLarge,
	functions.CodeCodeTimeout:        http.StatusRequestTimeout,
	functions.CodeBusy:               http.StatusTooManyRequests,
	functions.CodeNoSpace:            http.StatusInsufficientStorage,
	functions.CodeRuntimeUntrusted:   http.StatusServiceUnavailable,
	functions.CodeRuntimeUnavailable: http.StatusServiceUnavailable,
	functions.CodeArtifactModified:   http.StatusConflict,
	functions.CodeInvalidReference:   http.StatusInternalServerError,
	functions.CodeStorage:            http.StatusInternalServerError,
	functions.CodeInvalidDeployment:  http.StatusBadRequest,
	functions.CodeBootstrapMissing:   http.StatusBadRequest,
	functions.CodeBootstrapArch:      http.StatusBadRequest,
	functions.CodeBootstrapInvalid:   http.StatusBadRequest,
	functions.CodeDisabled:           http.StatusNotFound,
	functions.CodeNotFound:           http.StatusNotFound,
	functions.CodeExists:             http.StatusConflict,
	functions.CodeNameConflict:       http.StatusConflict,
	functions.CodeLimit:              http.StatusConflict,
	functions.CodeBudget:             http.StatusConflict,
	functions.CodeInProgress:         http.StatusConflict,
	functions.CodeModified:           http.StatusConflict,
	functions.CodeNotModified:        http.StatusConflict,
	functions.CodeNotDeployed:        http.StatusConflict,
	functions.CodeNotRunning:         http.StatusConflict,
	functions.CodeVerificationFailed: http.StatusConflict,
	functions.CodeStartFailed:        http.StatusBadGateway,
	functions.CodeOutcomeUnknown:     http.StatusGatewayTimeout,
	functions.CodeDeploymentNotFound: http.StatusNotFound,
	functions.CodeDeploymentActive:   http.StatusConflict,
	functions.CodeSecretNotFound:     http.StatusNotFound,
	functions.CodeImagePullFailed:    http.StatusBadGateway,
	functions.CodeInvokeBusy:         http.StatusTooManyRequests,
	functions.CodeInvokeTimeout:      http.StatusGatewayTimeout,
	functions.CodeInvokeFailed:       http.StatusBadGateway,
	functions.CodeEventTooLarge:      http.StatusRequestEntityTooLarge,
	functions.CodeDockerError:        http.StatusBadGateway,
	functions.CodeInvalidAction:      http.StatusBadRequest,
	functions.CodeInvalidEvent:       http.StatusBadRequest,
	functions.CodeEventTimeout:       http.StatusRequestTimeout,
}

// writeErr maps a functions error to its status. Messages that may hold
// host paths are replaced by a generic one (the details are logged).
func (h *FunctionsHandler) writeErr(w http.ResponseWriter, err error) {
	var fe *functions.Error
	if !errors.As(err, &fe) {
		h.Logger.Error("functions request failed", "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternalError, "Internal error")
		return
	}
	status, ok := functionsErrorStatus[fe.Code]
	if !ok {
		h.Logger.Error("functions: unmapped error code", "code", fe.Code)
		status = http.StatusInternalServerError
	}
	message := fe.Message
	if functions.InternalCodes[fe.Code] || status == http.StatusInternalServerError {
		h.Logger.Error("functions request failed", "code", fe.Code, "error", fe.Message)
		message = "The function storage or artifacts could not be used (details in the API logs)"
	}
	body := map[string]string{"code": fe.Code, "error": message}
	if fe.Field != "" {
		body["field"] = fe.Field
	}
	writeJSON(w, status, body)
}

func orDuration(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// closeOnError closes the connection after an error response: on the
// routes whose body may be left unread (code, invoke), net/http would
// otherwise try to drain it before the client sees the status.
type closeOnError struct{ http.ResponseWriter }

func (c closeOnError) WriteHeader(status int) {
	if status >= 400 {
		c.Header().Set("Connection", "close")
	}
	c.ResponseWriter.WriteHeader(status)
}

// Unwrap lets http.ResponseController reach the connection.
func (c closeOnError) Unwrap() http.ResponseWriter { return c.ResponseWriter }

const defaultEventReadTimeout = 30 * time.Second

// eventReader bounds the read of an event in time: a connection read
// deadline set at the first read (after the manager took a slot), so a
// stalled client cannot hold the slot.
type eventReader struct {
	r       io.Reader
	rc      *http.ResponseController
	timeout time.Duration
	armed   bool
	expired bool
}

func (e *eventReader) Read(p []byte) (int, error) {
	if !e.armed {
		e.armed = true
		_ = e.rc.SetReadDeadline(time.Now().Add(e.timeout))
	}
	n, err := e.r.Read(p)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		e.expired = true
	}
	return n, err
}

// reset clears the deadline after a complete read (keep-alive).
func (e *eventReader) reset() {
	if e.armed && !e.expired {
		_ = e.rc.SetReadDeadline(time.Time{})
	}
}
