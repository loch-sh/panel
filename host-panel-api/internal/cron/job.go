package cron

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	robfigcron "github.com/robfig/cron/v3"

	"host-panel-api/internal/functions"
)

// MinCronInterval is the minimum allowed interval for @every expressions.
const MinCronInterval = 60 * time.Second

// allowedHTTPMethods lists the allowed HTTP methods for cron jobs.
var allowedHTTPMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "HEAD": true, "OPTIONS": true,
}

// Bounds of a function_invoke job.
const (
	// MaxFunctionEventBytes bounds the event of a function_invoke job
	// (the invocation API accepts up to functions.MaxEventBytes; a
	// scheduled event is kept small: it is stored in cron.json, rewritten
	// whole at every run, and the jobs are bounded by CRON_MAX_JOBS).
	MaxFunctionEventBytes = 64 << 10
	// MaxJobsPerFunction bounds the function_invoke jobs targeting one
	// function (the panel runs at most 2 invocations of a function at a
	// time: more jobs would only queue and fail with FUNCTION_INVOKE_BUSY).
	MaxJobsPerFunction = 5
	// DefaultFunctionEvent is the event of a job that sets none.
	DefaultFunctionEvent = "{}"
)

// DisabledReasonFunctionDeleted is the DisabledReason of the
// function_invoke jobs whose function was deleted.
const DisabledReasonFunctionDeleted = "FUNCTION_DELETED"

// JobType distinguishes the types of scheduled tasks.
type JobType string

const (
	JobTypeDockerAction   JobType = "docker_action"
	JobTypeDockerExec     JobType = "docker_exec"
	JobTypeHTTP           JobType = "http"
	JobTypeFunctionInvoke JobType = "function_invoke"
)

// JobStatus represents the execution state of a job.
type JobStatus string

const (
	JobStatusPending JobStatus = "pending"
	JobStatusRunning JobStatus = "running"
	JobStatusSuccess JobStatus = "success"
	JobStatusError   JobStatus = "error"
)

// DockerActionConfig holds the parameters for a container lifecycle action.
type DockerActionConfig struct {
	ContainerName string `json:"containerName"`
	Action        string `json:"action"` // start | stop | restart | kill
}

// DockerExecConfig holds the parameters for executing a command in a container.
type DockerExecConfig struct {
	ContainerName string   `json:"containerName"`
	Command       []string `json:"command"`
	WorkingDir    string   `json:"workingDir,omitempty"`
}

// HTTPConfig holds the parameters for an HTTP request.
type HTTPConfig struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	Timeout int               `json:"timeout,omitempty"` // seconds; default and maximum: CRON_HTTP_TIMEOUT
}

// FunctionInvokeConfig holds the parameters of a function invocation.
// Event is the JSON text of the event (one JSON value, "{}" by default).
type FunctionInvokeConfig struct {
	Function string `json:"function"`
	Event    string `json:"event"`
}

// Job represents a scheduled task.
type Job struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Schedule string  `json:"schedule"` // cron expression (robfig/cron v3 format)
	Type     JobType `json:"type"`
	Enabled  bool    `json:"enabled"`
	Once     bool    `json:"once"` // one-time execution: disables itself after the first run

	// Type-specific configuration (only one non-nil depending on Type)
	DockerAction   *DockerActionConfig   `json:"dockerAction,omitempty"`
	DockerExec     *DockerExecConfig     `json:"dockerExec,omitempty"`
	HTTP           *HTTPConfig           `json:"http,omitempty"`
	FunctionInvoke *FunctionInvokeConfig `json:"functionInvoke,omitempty"`

	// DisabledReason says why the panel disabled the job (e.g. its
	// function was deleted); cleared when the job is enabled again.
	// Server-managed.
	DisabledReason string `json:"disabledReason,omitempty"`

	// Execution state
	LastRun    *time.Time `json:"lastRun,omitempty"`
	LastStatus JobStatus  `json:"lastStatus"`
	LastError  string     `json:"lastError,omitempty"`
	LastOutput string     `json:"lastOutput,omitempty"`
	// LastSkipped is the last time a run was skipped because the previous
	// one was still running. Server-managed.
	LastSkipped *time.Time `json:"lastSkipped,omitempty"`

	// Timestamps
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Validate checks that a Job is well-formed.
// Returns a human-readable error message if invalid, or an empty string if valid.
func (j *Job) Validate() string {
	if j.Name == "" {
		return "name is required"
	}
	if j.Schedule == "" {
		return "schedule is required"
	}

	switch j.Type {
	case JobTypeDockerAction:
		if j.DockerAction == nil {
			return "dockerAction config is required for type docker_action"
		}
		if j.DockerAction.ContainerName == "" {
			return "dockerAction.containerName is required"
		}
		switch j.DockerAction.Action {
		case "start", "stop", "restart", "kill":
		default:
			return "dockerAction.action must be start, stop, restart, or kill"
		}

	case JobTypeDockerExec:
		if j.DockerExec == nil {
			return "dockerExec config is required for type docker_exec"
		}
		if j.DockerExec.ContainerName == "" {
			return "dockerExec.containerName is required"
		}
		if len(j.DockerExec.Command) == 0 {
			return "dockerExec.command is required"
		}

	case JobTypeHTTP:
		if j.HTTP == nil {
			return "http config is required for type http"
		}
		if j.HTTP.URL == "" {
			return "http.url is required"
		}
		if j.HTTP.Method == "" {
			return "http.method is required"
		}
		if !allowedHTTPMethods[strings.ToUpper(j.HTTP.Method)] {
			return "http.method must be GET, POST, PUT, PATCH, DELETE, HEAD, or OPTIONS"
		}
		u, err := url.Parse(j.HTTP.URL)
		if err != nil || u.Scheme == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return "http.url must use http or https scheme"
		}

	case JobTypeFunctionInvoke:
		if j.FunctionInvoke == nil {
			return "functionInvoke config is required for type function_invoke"
		}
		if j.FunctionInvoke.Function == "" {
			return "functionInvoke.function is required"
		}
		if functions.ValidateName(j.FunctionInvoke.Function) != nil {
			return "functionInvoke.function is not a valid function name"
		}
		if len(j.FunctionInvoke.Event) > MaxFunctionEventBytes {
			return fmt.Sprintf("functionInvoke.event is at most %d bytes", MaxFunctionEventBytes)
		}
		if !json.Valid([]byte(j.FunctionInvoke.Event)) {
			return "functionInvoke.event must be one JSON value"
		}

	default:
		return "type must be docker_action, docker_exec, http, or function_invoke"
	}

	return ""
}

// Normalize prepares a job written by a client before validation: only
// the config block of its type is kept (a job changing type drops the
// previous one), and the event of a function_invoke job defaults to "{}".
// It never writes through a config pointer (they are shared with the
// stored job): it replaces them.
func (j *Job) Normalize() {
	if j.Type != JobTypeDockerAction {
		j.DockerAction = nil
	}
	if j.Type != JobTypeDockerExec {
		j.DockerExec = nil
	}
	if j.Type != JobTypeHTTP {
		j.HTTP = nil
	}
	if j.Type != JobTypeFunctionInvoke {
		j.FunctionInvoke = nil
	}
	if j.Type == JobTypeFunctionInvoke && j.FunctionInvoke != nil && strings.TrimSpace(j.FunctionInvoke.Event) == "" {
		cfg := *j.FunctionInvoke
		cfg.Event = DefaultFunctionEvent
		j.FunctionInvoke = &cfg
	}
}

// cronParser is the shared parser for cron expressions.
var cronParser = robfigcron.NewParser(
	robfigcron.Minute | robfigcron.Hour | robfigcron.Dom | robfigcron.Month | robfigcron.Dow | robfigcron.Descriptor,
)

// ValidateCronExpression checks that a cron expression is valid.
func ValidateCronExpression(expr string) error {
	_, err := cronParser.Parse(expr)
	return err
}

// ValidateMinInterval checks that the @every expression respects the minimum interval of 60 seconds.
func ValidateMinInterval(expr string) error {
	expr = strings.TrimSpace(expr)
	if !strings.HasPrefix(expr, "@every ") {
		return nil
	}
	durStr := strings.TrimPrefix(expr, "@every ")
	d, err := time.ParseDuration(durStr)
	if err != nil {
		return fmt.Errorf("invalid @every duration: %w", err)
	}
	if d < MinCronInterval {
		return fmt.Errorf("minimum allowed interval: %s", MinCronInterval)
	}
	return nil
}
