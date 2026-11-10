package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"host-panel-api/internal/cron"
	"host-panel-api/internal/docker"
	"host-panel-api/internal/functions"
	"host-panel-api/internal/middleware"
	"host-panel-api/internal/model"
)

// CronHandler handles the cron job management endpoints.
type CronHandler struct {
	Store       *cron.Store
	Scheduler   *cron.Scheduler
	Docker      *docker.Client
	Logger      *slog.Logger
	Audit       *slog.Logger
	MaxJobs     int // max number of allowed jobs (0 = unlimited)
	HTTPTimeout int // max HTTP timeout in seconds (also the default when not specified)
	// Functions checks the target of function_invoke jobs (nil: the
	// functions module is disabled).
	Functions CronFunctions

	// writeMu serializes the checks and writes of jobs (job limits).
	writeMu sync.Mutex
}

// CronFunctions is the part of functions.Manager the cron handler uses.
type CronFunctions interface {
	// Exists returns nil if the function exists, a functions error
	// (FUNCTION_NOT_FOUND, INVALID_FUNCTION_NAME, …) otherwise.
	Exists(name string) error
}

// ErrFunctionCronLimit: too many function_invoke jobs target one function.
const ErrFunctionCronLimit = "FUNCTION_CRON_LIMIT_REACHED"

// List returns the cron jobs visible to the user.
// Admin: all jobs. Restricted user: their allowed containers + HTTP jobs.
// GET /cron
func (h *CronHandler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	if !middleware.RequireScope(r.Context(), "cron") {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}

	jobs := h.Store.List()

	if user.Profile == model.ProfileAdmin {
		writeJSON(w, http.StatusOK, jobs)
		return
	}

	// Filter: HTTP jobs + Docker jobs on allowed containers
	filtered := make([]*cron.Job, 0)
	for _, job := range jobs {
		if h.userCanAccessJob(user, job) {
			filtered = append(filtered, redactJob(job))
		}
	}

	writeJSON(w, http.StatusOK, filtered)
}

// Create creates a new cron job.
// POST /cron
func (h *CronHandler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	if !middleware.RequireScope(r.Context(), "cron") {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var job cron.Job
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&job); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRequestBody, "Invalid request body")
		return
	}

	if status, code, msg := validateJobSchedule(&job); status != 0 {
		writeError(w, status, code, msg)
		return
	}
	if status, code, msg := h.applyHTTPTimeout(&job); status != 0 {
		writeError(w, status, code, msg)
		return
	}

	h.writeMu.Lock()
	defer h.writeMu.Unlock()

	job.ID = cron.GenerateID()
	if !h.checkFunctionInvoke(w, r, user, nil, &job) {
		return
	}

	// Check the job count limit
	if h.MaxJobs > 0 && len(h.Store.List()) >= h.MaxJobs {
		writeError(w, http.StatusConflict, ErrJobLimitReached, fmt.Sprintf("Maximum number of jobs reached (%d)", h.MaxJobs))
		return
	}

	// Check permissions on the target container
	if !h.userCanAccessJob(user, &job) {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	// Set server-managed fields (ID set above)
	job.LastStatus = cron.JobStatusPending
	job.LastError = ""
	job.LastOutput = ""
	job.LastRun = nil
	job.LastSkipped = nil
	job.DisabledReason = ""
	now := time.Now()
	job.CreatedAt = now
	job.UpdatedAt = now

	h.Store.Set(job.ID, &job)

	if err := h.Scheduler.AddJob(&job); err != nil {
		h.Logger.Error("failed to register cron job", "error", err)
	}

	h.Logger.Info("cron job created", "id", job.ID, "name", job.Name)
	h.Audit.Info("cron job created", "user", user.Email, "id", job.ID, "name", job.Name)
	writeJSON(w, http.StatusCreated, &job)
}

// Get returns a cron job by ID.
// GET /cron/{id}
func (h *CronHandler) Get(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	if !middleware.RequireScope(r.Context(), "cron") {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}

	id := r.PathValue("id")

	job, ok := h.Store.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, ErrJobNotFound, "Job not found")
		return
	}

	if !h.userCanAccessJob(user, job) {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	if user.Profile != model.ProfileAdmin {
		job = redactJob(job)
	}
	writeJSON(w, http.StatusOK, job)
}

// Update modifies an existing cron job.
// PUT /cron/{id}
func (h *CronHandler) Update(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	if !middleware.RequireScope(r.Context(), "cron") {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}

	id := r.PathValue("id")

	existing, ok := h.Store.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, ErrJobNotFound, "Job not found")
		return
	}

	// Check permissions on the existing job
	if !h.userCanAccessJob(user, existing) {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var updated cron.Job
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&updated); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRequestBody, "Invalid request body")
		return
	}

	if status, code, msg := validateJobSchedule(&updated); status != 0 {
		writeError(w, status, code, msg)
		return
	}
	if status, code, msg := h.applyHTTPTimeout(&updated); status != 0 {
		writeError(w, status, code, msg)
		return
	}

	// Check permissions on the new target container
	if !h.userCanAccessJob(user, &updated) {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	// The stored job again, under the lock (the check below compares with it).
	if existing, ok = h.Store.Get(id); !ok {
		writeError(w, http.StatusNotFound, ErrJobNotFound, "Job not found")
		return
	}
	if !h.userCanAccessJob(user, existing) {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}
	updated.ID = id
	if !h.checkFunctionInvoke(w, r, user, existing, &updated) {
		return
	}

	// Preserve server-managed fields (ID set above)
	updated.CreatedAt = existing.CreatedAt
	updated.UpdatedAt = time.Now()
	updated.DisabledReason = ""
	if !updated.Enabled && updated.Type == existing.Type {
		updated.DisabledReason = existing.DisabledReason
	}

	if !h.writeJob(id, &updated) {
		writeError(w, http.StatusNotFound, ErrJobNotFound, "Job not found")
		return
	}

	if err := h.Scheduler.UpdateJob(&updated); err != nil {
		h.Logger.Error("failed to update cron schedule", "error", err)
	}

	h.Logger.Info("cron job updated", "id", id, "name", updated.Name)
	h.Audit.Info("cron job updated", "user", user.Email, "id", id, "name", updated.Name)
	writeJSON(w, http.StatusOK, &updated)
}

// CronJobPatch contains the mutable fields of a cron job.
// Pointers are used to distinguish "not provided" (nil) from "zero value" (false, "").
type CronJobPatch struct {
	Name           *string                    `json:"name,omitempty"`
	Schedule       *string                    `json:"schedule,omitempty"`
	Type           *cron.JobType              `json:"type,omitempty"`
	Enabled        *bool                      `json:"enabled,omitempty"`
	Once           *bool                      `json:"once,omitempty"`
	DockerAction   *cron.DockerActionConfig   `json:"dockerAction,omitempty"`
	DockerExec     *cron.DockerExecConfig     `json:"dockerExec,omitempty"`
	HTTP           *cron.HTTPConfig           `json:"http,omitempty"`
	FunctionInvoke *cron.FunctionInvokeConfig `json:"functionInvoke,omitempty"`
}

// Patch applies a partial update to an existing cron job.
// PATCH /cron/{id}
func (h *CronHandler) Patch(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	if !middleware.RequireScope(r.Context(), "cron") {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}

	id := r.PathValue("id")

	job, ok := h.Store.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, ErrJobNotFound, "Job not found")
		return
	}

	if !h.userCanAccessJob(user, job) {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var patch CronJobPatch
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&patch); err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidRequestBody, "Invalid request body")
		return
	}

	// The patch is merged into the stored job under the lock, from the
	// read to the write.
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if job, ok = h.Store.Get(id); !ok {
		writeError(w, http.StatusNotFound, ErrJobNotFound, "Job not found")
		return
	}
	if !h.userCanAccessJob(user, job) {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}
	existing := *job

	// Apply the provided fields
	if patch.Name != nil {
		job.Name = *patch.Name
	}
	if patch.Schedule != nil {
		job.Schedule = *patch.Schedule
	}
	if patch.Type != nil {
		job.Type = *patch.Type
	}
	if patch.Enabled != nil {
		job.Enabled = *patch.Enabled
	}
	if patch.Once != nil {
		job.Once = *patch.Once
	}
	if patch.DockerAction != nil {
		job.DockerAction = patch.DockerAction
	}
	if patch.DockerExec != nil {
		job.DockerExec = patch.DockerExec
	}
	if patch.HTTP != nil {
		job.HTTP = patch.HTTP
	}
	if patch.FunctionInvoke != nil {
		job.FunctionInvoke = patch.FunctionInvoke
	}

	// Validate the merged result
	if status, code, msg := validateJobSchedule(job); status != 0 {
		writeError(w, status, code, msg)
		return
	}
	if status, code, msg := h.applyHTTPTimeout(job); status != 0 {
		writeError(w, status, code, msg)
		return
	}

	// Check permissions on the modified job
	if !h.userCanAccessJob(user, job) {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}
	if !h.checkFunctionInvoke(w, r, user, &existing, job) {
		return
	}
	if job.Enabled || job.Type != existing.Type {
		job.DisabledReason = ""
	}

	job.UpdatedAt = time.Now()
	if !h.writeJob(id, job) {
		writeError(w, http.StatusNotFound, ErrJobNotFound, "Job not found")
		return
	}

	if err := h.Scheduler.UpdateJob(job); err != nil {
		h.Logger.Error("failed to update cron schedule", "error", err)
	}

	h.Logger.Info("cron job updated (patch)", "id", id, "name", job.Name)
	h.Audit.Info("cron job updated (patch)", "user", user.Email, "id", id, "name", job.Name)
	writeJSON(w, http.StatusOK, job)
}

// Delete removes a cron job.
// DELETE /cron/{id}
func (h *CronHandler) Delete(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	if !middleware.RequireScope(r.Context(), "cron") {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}

	id := r.PathValue("id")

	job, ok := h.Store.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, ErrJobNotFound, "Job not found")
		return
	}

	if !h.userCanAccessJob(user, job) {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	h.Store.Delete(id)
	h.Scheduler.RemoveJob(id)
	h.Logger.Info("cron job deleted", "id", id)
	h.Audit.Info("cron job deleted", "user", user.Email, "id", id)
	w.WriteHeader(http.StatusNoContent)
}

// RunNow triggers the immediate execution of a job.
// POST /cron/{id}/run
func (h *CronHandler) RunNow(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	if !middleware.RequireScope(r.Context(), "cron") {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}

	id := r.PathValue("id")

	job, ok := h.Store.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, ErrJobNotFound, "Job not found")
		return
	}

	if !h.userCanAccessJob(user, job) {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	if job.Type == cron.JobTypeFunctionInvoke {
		if !middleware.RequireScope(r.Context(), ScopeFunctionsInvoke) {
			writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
			return
		}
		if h.Functions == nil {
			writeError(w, http.StatusNotFound, functions.CodeDisabled, "Functions are disabled")
			return
		}
	}

	if h.Scheduler.IsRunning(id) {
		writeError(w, http.StatusConflict, ErrJobAlreadyRunning, "Job is already running")
		return
	}

	h.Scheduler.RunNow(r.Context(), job)
	h.Logger.Info("manual cron job execution", "id", id, "name", job.Name)
	h.Audit.Info("manual cron job execution", "user", user.Email, "id", id, "name", job.Name)
	writeJSON(w, http.StatusAccepted, map[string]string{
		"message": "Job execution started",
	})
}

// validateJobSchedule validates a job's fields, cron expression, and minimum interval.
func validateJobSchedule(job *cron.Job) (int, string, string) {
	job.Normalize()
	if msg := job.Validate(); msg != "" {
		return http.StatusBadRequest, ErrInvalidRequestBody, msg
	}
	// No command nor action on a function container (by name here, by
	// container at run time: the executor refuses them as well).
	if job.Type == cron.JobTypeDockerExec && docker.IsFunctionContainerName(job.DockerExec.ContainerName) {
		return http.StatusForbidden, ErrFunctionProtected, "Commands cannot run in a function container"
	}
	if job.Type == cron.JobTypeDockerAction && docker.IsFunctionContainerName(job.DockerAction.ContainerName) {
		return http.StatusForbidden, ErrFunctionProtected, "Function containers are managed through the functions API"
	}
	if err := cron.ValidateCronExpression(job.Schedule); err != nil {
		return http.StatusBadRequest, ErrInvalidCronExpr, "Invalid cron expression"
	}
	if err := cron.ValidateMinInterval(job.Schedule); err != nil {
		return http.StatusBadRequest, ErrCronFreqTooHigh, "Minimum interval is 1 minute"
	}
	return 0, "", ""
}

// checkFunctionInvoke applies the rules of function_invoke jobs to a job
// being created (existing nil) or changed: functions:invoke on the API key
// (also to change a job that was one, or to make one), an admin owner, the
// module enabled, the function existing, and at most
// cron.MaxJobsPerFunction jobs per function. The module must be enabled
// and the function exist unless the job stays disabled on the function it
// already had (such a job can still be renamed, rescheduled or kept
// disabled while the function or the module is gone). Called with writeMu held. It writes the error and returns
// false on a refusal.
func (h *CronHandler) checkFunctionInvoke(w http.ResponseWriter, r *http.Request, user *model.User, existing, job *cron.Job) bool {
	wasInvoke := existing != nil && existing.Type == cron.JobTypeFunctionInvoke
	isInvoke := job.Type == cron.JobTypeFunctionInvoke
	if !wasInvoke && !isInvoke {
		return true
	}
	if !middleware.RequireScope(r.Context(), ScopeFunctionsInvoke) {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return false
	}
	if user.Profile != model.ProfileAdmin {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return false
	}
	if !isInvoke {
		return true
	}
	fn := job.FunctionInvoke.Function
	sameFunction := wasInvoke && existing.FunctionInvoke != nil && existing.FunctionInvoke.Function == fn
	// A job kept (or turned) disabled on the function it already had
	// needs neither the module nor the function: it can always be
	// disabled, renamed or rescheduled.
	if job.Enabled || !sameFunction {
		if h.Functions == nil {
			writeError(w, http.StatusNotFound, functions.CodeDisabled, "Functions are disabled")
			return false
		}
		if err := h.Functions.Exists(fn); err != nil {
			writeFunctionTargetError(w, h.Logger, err)
			return false
		}
	}
	count := 0
	for _, other := range h.Store.List() {
		if other.ID != job.ID && other.Type == cron.JobTypeFunctionInvoke &&
			other.FunctionInvoke != nil && other.FunctionInvoke.Function == fn {
			count++
		}
	}
	if count >= cron.MaxJobsPerFunction {
		writeError(w, http.StatusConflict, ErrFunctionCronLimit,
			fmt.Sprintf("At most %d scheduled tasks can invoke one function", cron.MaxJobsPerFunction))
		return false
	}
	return true
}

// writeJob stores a job changed by a client, keeping the execution fields
// of the live job (a run may have ended since it was read: the executor
// writes them through Store.Update too). It returns false if the job was
// deleted meanwhile; job gets the stored execution fields.
func (h *CronHandler) writeJob(id string, job *cron.Job) bool {
	return h.Store.Update(id, func(live *cron.Job) {
		job.LastRun, job.LastStatus, job.LastError = live.LastRun, live.LastStatus, live.LastError
		job.LastOutput, job.LastSkipped = live.LastOutput, live.LastSkipped
		*live = *job
	})
}

// writeFunctionTargetError maps the error of a function lookup with the
// statuses of the functions API (FUNCTION_NOT_FOUND 404, …).
func writeFunctionTargetError(w http.ResponseWriter, logger *slog.Logger, err error) {
	var fe *functions.Error
	if !errors.As(err, &fe) {
		logger.Error("cron: checking a function", "error", err)
		writeError(w, http.StatusInternalServerError, ErrInternalError, "Internal error")
		return
	}
	status, ok := functionsErrorStatus[fe.Code]
	if !ok || status == http.StatusInternalServerError || functions.InternalCodes[fe.Code] {
		logger.Error("cron: checking a function", "code", fe.Code, "error", fe.Message)
		writeError(w, http.StatusInternalServerError, ErrInternalError, "Internal error")
		return
	}
	body := map[string]string{"code": fe.Code, "error": fe.Message, "field": "functionInvoke.function"}
	writeJSON(w, status, body)
}

// FunctionDeleted disables the function_invoke jobs of a deleted function
// (they are kept, with disabledReason FUNCTION_DELETED: the user points
// them at another function or deletes them). Called by the functions
// module after a deletion or a purge.
func (h *CronHandler) FunctionDeleted(name, actor string) {
	// Serialized with the writes: a change checked against the function
	// before its deletion cannot enable a job afterwards.
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	for _, job := range h.Scheduler.DisableFunctionJobs(name, cron.DisabledReasonFunctionDeleted) {
		h.Logger.Info("cron job disabled: its function was deleted", "id", job.ID, "name", job.Name, "function", name)
		h.Audit.Info("cron job updated", "user", actor, "id", job.ID, "name", job.Name,
			"enabled", false, "reason", "function deleted", "function", name)
	}
}

// applyHTTPTimeout validates and defaults the HTTP timeout on a job.
func (h *CronHandler) applyHTTPTimeout(job *cron.Job) (int, string, string) {
	if job.Type != cron.JobTypeHTTP || job.HTTP == nil {
		return 0, "", ""
	}
	if job.HTTP.Timeout > h.HTTPTimeout && h.HTTPTimeout > 0 {
		return http.StatusBadRequest, ErrHTTPTimeoutExceeded, fmt.Sprintf("http.timeout cannot exceed %d seconds", h.HTTPTimeout)
	}
	if job.HTTP.Timeout == 0 {
		job.HTTP.Timeout = h.HTTPTimeout
	}
	return 0, "", ""
}

// userCanAccessJob checks whether a user is allowed to access a cron job.
// Admin: full access. Restricted user: HTTP jobs without restriction,
// Docker jobs only on allowed containers.
func (h *CronHandler) userCanAccessJob(user *model.User, job *cron.Job) bool {
	if user.Profile == model.ProfileAdmin {
		return true
	}

	// HTTP jobs are accessible to all authenticated users (by design).
	// The SSRF-safe transport blocks private/internal addresses, and restricted
	// users legitimately need HTTP cron jobs for monitoring, webhooks, etc.
	if job.Type == cron.JobTypeHTTP {
		return true
	}

	// Functions are admin only.
	if job.Type == cron.JobTypeFunctionInvoke {
		return false
	}

	// For Docker jobs, check container access by name
	var containerName string
	switch job.Type {
	case cron.JobTypeDockerAction:
		containerName = job.DockerAction.ContainerName
	case cron.JobTypeDockerExec:
		containerName = job.DockerExec.ContainerName
	}

	if containerName == "" {
		return false
	}

	return userCanAccessContainer(user, containerName)
}

// redactJob returns a shallow copy of the job with lastError and lastOutput
// cleared. This prevents restricted users from seeing internal error details
// (Docker socket paths, DNS resolution results, remote HTTP response bodies).
func redactJob(job *cron.Job) *cron.Job {
	copy := *job
	copy.LastError = ""
	copy.LastOutput = ""
	return &copy
}
