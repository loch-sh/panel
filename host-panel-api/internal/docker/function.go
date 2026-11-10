package docker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"host-panel-api/internal/model"
)

// Function containers (internal/functions) hold secrets in their
// environment and run client code: the Docker layer masks their
// environment in every inspect served to clients and refuses the
// operations that would reach inside them (shell, exec, cp, file tail,
// proxy), as for the panel's own container.

// FunctionLabel is set on every function container (its value is the
// function name). It is defined here, not imported from internal/functions,
// so that the Docker layer has no dependency on the module.
const FunctionLabel = "sh.loch.function"

// FunctionNamePrefix prefixes the container (and compose project) of a
// function.
const FunctionNamePrefix = model.FunctionContainerPrefix

// MaskedValue replaces the value of a masked environment variable.
const MaskedValue = "***"

// IsFunctionContainer reports whether a container is (or claims to be) a
// function container: its label, or its name or compose project with the
// function prefix. A container wrongly matched only loses features (masked
// environment, no shell), never gains any.
func IsFunctionContainer(info container.InspectResponse) bool {
	if strings.HasPrefix(strings.TrimPrefix(info.Name, "/"), FunctionNamePrefix) {
		return true
	}
	if info.Config == nil {
		return false
	}
	_, labeled := info.Config.Labels[FunctionLabel]
	return labeled || strings.HasPrefix(info.Config.Labels["com.docker.compose.project"], FunctionNamePrefix)
}

// IsFunctionSummary is IsFunctionContainer for a container list entry.
func IsFunctionSummary(c container.Summary) bool {
	if _, labeled := c.Labels[FunctionLabel]; labeled || strings.HasPrefix(c.Labels["com.docker.compose.project"], FunctionNamePrefix) {
		return true
	}
	for _, n := range c.Names {
		if IsFunctionContainerName(n) {
			return true
		}
	}
	return false
}

// IsFunctionContainerName reports whether a container name is a function
// container's (for the checks that only have a name: restricted profiles).
func IsFunctionContainerName(name string) bool { return model.IsFunctionContainerName(name) }

// UserCanAccess is the single container permission check of the
// client-facing paths (HTTP and WebSocket): admins see every container;
// a restricted user only the containers granted by name, never a function
// container (by name, label or project), whatever is stored.
func UserCanAccess(user *model.User, info container.InspectResponse) bool {
	switch {
	case user == nil:
		return false
	case user.Profile == model.ProfileAdmin:
		return true
	case user.Profile != model.ProfileRestricted || IsFunctionContainer(info):
		return false
	}
	return user.GrantsContainer(info.Name)
}

// MaskFunctionEnv returns info with the values of its environment masked
// when it is a function container (names kept). The configuration is
// copied, never modified in place (the response may be cached).
func MaskFunctionEnv(info container.InspectResponse) container.InspectResponse {
	if !IsFunctionContainer(info) || info.Config == nil {
		return info
	}
	cfg := *info.Config
	cfg.Env = make([]string, len(info.Config.Env))
	for i, e := range info.Config.Env {
		name, _, _ := strings.Cut(e, "=")
		cfg.Env[i] = name + "=" + MaskedValue
	}
	// The healthcheck, the command and the labels carry no secret (they are
	// rendered by the panel from validated values).
	info.Config = &cfg
	return info
}

// IsFunction reports whether ref (id, short id or name) is a function
// container. A failed lookup reports false (the operation then fails on
// its own).
func (c *Client) IsFunction(ctx context.Context, ref string) bool {
	if ref == "" {
		return false
	}
	res, err := c.cli.ContainerInspect(ctx, ref, client.ContainerInspectOptions{})
	return err == nil && IsFunctionContainer(res.Container)
}

// InspectContainerRaw returns the details of a container without masking:
// only for the panel's own verifications (internal/functions compares the
// environment with what it rendered), never for a client response.
func (c *Client) InspectContainerRaw(ctx context.Context, id string) (container.InspectResponse, error) {
	res, err := c.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	return res.Container, err
}

// HostArch returns the architecture of the Docker daemon, as a Go name
// ("amd64", "arm64"; another value as reported).
func (c *Client) HostArch(ctx context.Context) (string, error) {
	res, err := c.cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		return "", err
	}
	return GoArch(res.Info.Architecture), nil
}

// GoArch maps the architecture names of the daemon (uname) to Go's.
func GoArch(arch string) string {
	switch arch {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	}
	return arch
}

// Bounds of ExecInvoke.
const (
	// MaxInvokeStdin is the largest event (the runtime's payload limit).
	MaxInvokeStdin  = 6<<20 + 1 // the event and the newline that ends it
	maxInvokeStderr = 4 << 10
)

// InvokeRequest is a function invocation run in its container: Cmd reads
// the event on Stdin and writes one bounded envelope on stdout.
type InvokeRequest struct {
	Cmd   []string
	User  string
	Stdin []byte // at most MaxInvokeStdin
	// KeepStdinOpen leaves stdin open after the event: the command exits
	// when the exec connection closes (done, deadline or caller gone),
	// which Docker propagates to its stdin, never to its stdout.
	KeepStdinOpen bool
	// MaxStdout bounds the envelope: a larger output is an error
	// (ErrExecOutputTooLarge), never buffered beyond the limit.
	MaxStdout int
	// Timeout is the deadline of the whole exec; the connection is closed
	// when it expires, which closes the command's stdin and stdout.
	Timeout time.Duration
}

// ExecInvoke runs an invocation in a container. Like ExecRun, it adds no
// shell and never returns the input nor the output in its errors. Docker
// cannot kill an exec: when the call returns (output read, deadline, or
// the caller's context done) the connection is closed, which closes the
// command's stdin (KeepStdinOpen: the runtime's invoke --watch-stdin exits
// then); the command also bounds itself with its own deadline.
func (c *Client) ExecInvoke(ctx context.Context, containerID string, req InvokeRequest) (ExecResult, error) {
	if len(req.Cmd) == 0 || req.MaxStdout <= 0 || req.Timeout <= 0 {
		return ExecResult{}, errors.New("exec: invalid invoke request")
	}
	if len(req.Stdin) > MaxInvokeStdin {
		return ExecResult{}, fmt.Errorf("exec: stdin exceeds %d bytes", MaxInvokeStdin)
	}
	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()
	created, err := c.cli.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		Cmd: slices.Clone(req.Cmd), User: req.User,
		AttachStdin: true, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return ExecResult{}, fmt.Errorf("exec create: %w", wrapTimeout(ctx, err))
	}
	attached, err := c.cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return ExecResult{}, fmt.Errorf("exec attach: %w", wrapTimeout(ctx, err))
	}
	hijacked := attached.HijackedResponse
	defer hijacked.Close()
	if _, ok := hijacked.Conn.(client.CloseWriter); !ok && !req.KeepStdinOpen {
		return ExecResult{}, ErrExecNoHalfClose
	}
	limits := outputLimits{stdout: req.MaxStdout, stderr: maxInvokeStderr, failOnStdout: true, keepStdin: req.KeepStdinOpen}
	res, err := pumpExecLimits(ctx, limits, req.Stdin, hijacked.Conn, hijacked.CloseWrite, hijacked.Reader, hijacked.Close)
	if err != nil {
		return res, err
	}
	code, err := waitExecExit(ctx, c.cli, created.ID, exitPollTimeout)
	if err != nil {
		return res, err
	}
	res.ExitCode = code
	return res, nil
}
