package cron

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"

	"host-panel-api/internal/docker"
	"host-panel-api/internal/functions"
)

const maxOutputLen = 10 * 1024 // 10 KB max for LastOutput

// Executor runs cron jobs and records the results.
type Executor struct {
	docker      *docker.Client
	httpClient  *http.Client
	store       *Store
	logger      *slog.Logger
	audit       *slog.Logger
	httpTimeout time.Duration // max and default timeout for HTTP jobs
	// functions invokes the function_invoke jobs (nil: the functions
	// module is disabled). Set once the module is built, before the
	// scheduler starts (atomic: it may also be changed later).
	functions atomic.Pointer[functionInvokerHolder]
}

// FunctionInvoker is the part of functions.Manager the executor uses:
// invocations go through docker exec in the verified container, never over
// HTTP nor a network bridge.
type FunctionInvoker interface {
	Invoke(ctx context.Context, name string, event io.Reader, actor string) (*functions.InvokeResult, error)
}

type functionInvokerHolder struct{ FunctionInvoker }

// SetFunctions gives the executor the functions module (nil: disabled).
func (e *Executor) SetFunctions(inv FunctionInvoker) {
	if inv == nil {
		e.functions.Store(nil)
		return
	}
	e.functions.Store(&functionInvokerHolder{inv})
}

// HasFunctions reports whether the functions module is attached.
func (e *Executor) HasFunctions() bool { return e.functions.Load() != nil }

// functionGone reports whether a function is known to be gone: true
// only when the module answers FUNCTION_NOT_FOUND (any other answer, or a
// module without the check, keeps the job).
func (e *Executor) functionGone(name string) bool {
	h := e.functions.Load()
	if h == nil {
		return false
	}
	ex, ok := h.FunctionInvoker.(interface{ Exists(string) error })
	if !ok {
		return false
	}
	return functions.CodeOf(ex.Exists(name)) == functions.CodeNotFound
}

// FunctionInvokeActor is the actor of a cron invocation in the aggregated
// "function invoked" audit entry: the source (cron) and the job.
func FunctionInvokeActor(jobID string) string { return "cron:" + jobID }

// NewExecutor creates an Executor with an SSRF-safe HTTP transport.
func NewExecutor(dockerClient *docker.Client, store *Store, logger, audit *slog.Logger, httpTimeout time.Duration) *Executor {
	return &Executor{
		docker:      dockerClient,
		httpClient:  &http.Client{Transport: newSafeTransport()},
		store:       store,
		logger:      logger,
		audit:       audit,
		httpTimeout: httpTimeout,
	}
}

// isPrivateIP checks whether an IP address is private, loopback, or link-local.
func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// newSafeTransport creates an HTTP transport that blocks connections to private addresses.
// Protects against SSRF by checking resolved IPs before establishing the connection.
func newSafeTransport() *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("invalid address: %s", addr)
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("DNS resolution: %w", err)
			}
			for _, ip := range ips {
				if isPrivateIP(ip.IP) {
					return nil, fmt.Errorf("access to private addresses is forbidden: %s resolved to %s", host, ip.IP)
				}
			}
			// Dial using the resolved IP to pin the address and prevent DNS rebinding
			dialer := &net.Dialer{Timeout: 10 * time.Second}
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
			if err != nil {
				return nil, err
			}
			// Post-dial verification: re-check the actual remote IP to defend against
			// OS-level rebinding or multi-record DNS responses
			remoteHost, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
			if isPrivateIP(net.ParseIP(remoteHost)) {
				conn.Close()
				return nil, fmt.Errorf("connection to private address blocked (post-dial check)")
			}
			return conn, nil
		},
	}
}

// Run executes a job and updates its metadata in the store.
func (e *Executor) Run(ctx context.Context, job *Job) {
	e.logger.Info("executing cron job", "id", job.ID, "name", job.Name, "type", job.Type)

	var output string
	var execErr error

	switch job.Type {
	case JobTypeDockerAction:
		execErr = e.runDockerAction(ctx, job.DockerAction)
	case JobTypeDockerExec:
		output, execErr = e.runDockerExec(ctx, job.DockerExec)
	case JobTypeHTTP:
		output, execErr = e.runHTTP(ctx, job.HTTP)
	case JobTypeFunctionInvoke:
		output, execErr = e.runFunctionInvoke(ctx, job)
	default:
		execErr = fmt.Errorf("unknown job type: %s", job.Type)
	}

	// Update execution metadata
	now := time.Now()
	job.LastRun = &now
	if execErr != nil {
		job.LastStatus = JobStatusError
		job.LastError = execErr.Error()
		job.LastOutput = truncate(output, maxOutputLen)
		e.logger.Error("cron job failed", "id", job.ID, "name", job.Name, "error", execErr)
		// The audit trail is durable and readable from the API: never the
		// URL of an HTTP job (it may carry a token or credentials).
		e.audit.Error("cron job failed", "id", job.ID, "name", job.Name, "type", job.Type, "error", auditError(execErr))
	} else {
		job.LastStatus = JobStatusSuccess
		job.LastError = ""
		job.LastOutput = truncate(output, maxOutputLen)
		e.logger.Info("cron job completed successfully", "id", job.ID, "name", job.Name)
		e.audit.Info("cron job completed", "id", job.ID, "name", job.Name, "type", job.Type)
	}

	// Only the execution fields: the job may have been changed, disabled
	// (its function deleted) or deleted while it ran.
	e.store.Update(job.ID, func(j *Job) {
		j.LastRun = job.LastRun
		j.LastStatus = job.LastStatus
		j.LastError = job.LastError
		j.LastOutput = job.LastOutput
	})
}

// functionInvokeOutput is what LastOutput keeps of an invocation: never
// the payload nor the log tail (untrusted data written by the function,
// possibly secrets), only their outline.
type functionInvokeOutput struct {
	StatusCode    int    `json:"statusCode"`
	FunctionError string `json:"functionError,omitempty"`
	DurationMs    int64  `json:"durationMs"`
	PayloadBytes  int    `json:"payloadBytes"`
}

// runFunctionInvoke invokes a function through the functions module. The
// output is a JSON object (functionInvokeOutput); a function error or a
// status >= 400 fails the run.
func (e *Executor) runFunctionInvoke(ctx context.Context, job *Job) (string, error) {
	cfg := job.FunctionInvoke
	if cfg == nil {
		return "", errors.New("functionInvoke config is missing")
	}
	h := e.functions.Load()
	if h == nil {
		return "", fmt.Errorf("functions are disabled (%s)", functions.CodeDisabled)
	}
	event := cfg.Event
	if strings.TrimSpace(event) == "" {
		event = DefaultFunctionEvent
	}
	res, err := h.Invoke(ctx, cfg.Function, strings.NewReader(event), FunctionInvokeActor(job.ID))
	if err != nil {
		return "", invokeError(err)
	}
	out, _ := json.Marshal(functionInvokeOutput{
		StatusCode: res.StatusCode, FunctionError: res.FunctionError,
		DurationMs: res.DurationMs, PayloadBytes: len(res.Payload),
	})
	switch {
	case res.FunctionError != "":
		return string(out), fmt.Errorf("function error (%s), status %d", res.FunctionError, res.StatusCode)
	case res.StatusCode >= 400:
		return string(out), fmt.Errorf("function returned status %d", res.StatusCode)
	}
	return string(out), nil
}

// invokeError describes a failed invocation by its code and client-facing
// message (never the details of an internal error: they may hold host
// paths, and are logged by the module).
func invokeError(err error) error {
	var fe *functions.Error
	if !errors.As(err, &fe) {
		return errors.New("function invocation failed")
	}
	if functions.InternalCodes[fe.Code] {
		return fmt.Errorf("function invocation failed (%s)", fe.Code)
	}
	return fmt.Errorf("%s (%s)", fe.Message, fe.Code)
}

func (e *Executor) runDockerAction(ctx context.Context, cfg *DockerActionConfig) error {
	// Functions are started and stopped by the functions module only.
	if docker.IsFunctionContainerName(cfg.ContainerName) || e.docker.IsFunction(ctx, cfg.ContainerName) {
		return fmt.Errorf("function containers are managed through the functions API (FUNCTION_CONTAINER_PROTECTED)")
	}
	switch cfg.Action {
	case "start":
		return e.docker.StartContainer(ctx, cfg.ContainerName)
	case "stop":
		return e.docker.StopContainer(ctx, cfg.ContainerName)
	case "restart":
		return e.docker.RestartContainer(ctx, cfg.ContainerName)
	case "kill":
		return e.docker.KillContainer(ctx, cfg.ContainerName)
	default:
		return fmt.Errorf("unknown action: %s", cfg.Action)
	}
}

func (e *Executor) runDockerExec(ctx context.Context, cfg *DockerExecConfig) (string, error) {
	if e.docker.IsSelf(ctx, cfg.ContainerName) {
		return "", fmt.Errorf("commands cannot run in the API container (audit trail)")
	}
	if e.docker.IsFunction(ctx, cfg.ContainerName) || docker.IsFunctionContainerName(cfg.ContainerName) {
		return "", fmt.Errorf("commands cannot run in a function container (FUNCTION_CONTAINER_PROTECTED)")
	}
	execID, err := e.docker.ContainerExecCreate(ctx, cfg.ContainerName, cfg.Command, false)
	if err != nil {
		return "", fmt.Errorf("exec create: %w", err)
	}

	hijacked, err := e.docker.ContainerExecAttach(ctx, execID, false)
	if err != nil {
		return "", fmt.Errorf("exec attach: %w", err)
	}
	defer hijacked.Close()

	// Demux stdout/stderr (tty=false)
	var stdoutBuf, stderrBuf bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdoutBuf, &stderrBuf, hijacked.Reader); err != nil {
		return "", fmt.Errorf("exec read: %w", err)
	}

	var sb strings.Builder
	if stdoutBuf.Len() > 0 {
		sb.WriteString(stdoutBuf.String())
	}
	if stderrBuf.Len() > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n--- stderr ---\n")
		}
		sb.WriteString(stderrBuf.String())
	}

	return sb.String(), nil
}

func (e *Executor) runHTTP(ctx context.Context, cfg *HTTPConfig) (string, error) {
	timeout := e.httpTimeout
	if timeout == 0 {
		timeout = 15 * time.Minute // fallback if not configured
	}
	if cfg.Timeout > 0 {
		timeout = time.Duration(cfg.Timeout) * time.Second
	}
	// Defense-in-depth: cap at the configured max timeout
	if e.httpTimeout > 0 && timeout > e.httpTimeout {
		timeout = e.httpTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var bodyReader io.Reader
	if cfg.Body != "" {
		bodyReader = strings.NewReader(cfg.Body)
	}

	req, err := http.NewRequestWithContext(ctx, cfg.Method, cfg.URL, bodyReader)
	if err != nil {
		return "", fmt.Errorf("creating HTTP request: %w", err)
	}

	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxOutputLen)))
	if err != nil {
		return "", fmt.Errorf("reading HTTP response: %w", err)
	}

	output := fmt.Sprintf("HTTP %d %s\n%s", resp.StatusCode, resp.Status, string(body))

	if resp.StatusCode >= 400 {
		return output, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return output, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "... [truncated]"
}

// auditError describes an error without the URL that net/http errors embed
// (*url.Error, possibly wrapped). The executor never puts the job URL in its
// own error messages.
func auditError(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return fmt.Sprintf("http request failed (%s): %v", uerr.Op, uerr.Err)
	}
	return err.Error()
}
