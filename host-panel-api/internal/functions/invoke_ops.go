package functions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"host-panel-api/internal/docker"
)

// InvokeCommand is run in the function container (event on stdin, one
// JSON envelope on stdout). The invoke subcommand bounds itself with the
// function timeout + the runtime queue wait + a margin.
// With --watch-stdin the event is the first JSON value on stdin, which the
// panel keeps open: the process exits as soon as stdin closes (the exec
// connection closed: the panel is done, or gone), and in any case at its
// own deadline. Docker never closes an exec's stdout when its client goes
// away, but closes its stdin within half a second.
var InvokeCommand = []string{RuntimeMount, "invoke", "--watch-stdin", "--log-tail"}

// Errors of an Invoker, mapped by the Manager.
var (
	ErrInvokeTimeout   = errors.New("invoke: deadline exceeded")
	ErrInvokeTransport = errors.New("invoke: the runtime did not deliver the invocation")
)

// ExecInvoker invokes through `docker exec … /loch/lambda-runtime invoke`,
// never through a network bridge: the panel never joins a function
// network.
type ExecInvoker struct {
	Docker interface {
		ExecInvoke(ctx context.Context, id string, req docker.InvokeRequest) (docker.ExecResult, error)
	}
}

// Invoke runs one invocation in a verified container.
func (e ExecInvoker) Invoke(ctx context.Context, containerID string, event []byte, timeout time.Duration) (InvokeResult, error) {
	res, err := e.Docker.ExecInvoke(ctx, containerID, docker.InvokeRequest{
		Cmd: InvokeCommand, User: functionUser, Stdin: event, KeepStdinOpen: true,
		MaxStdout: maxEnvelopeBytes, Timeout: timeout,
	})
	switch {
	case errors.Is(err, docker.ErrExecTimeout):
		return InvokeResult{}, ErrInvokeTimeout
	case err != nil:
		return InvokeResult{}, errors.Join(ErrInvokeTransport, err)
	case res.ExitCode != 0:
		return InvokeResult{}, ErrInvokeTransport
	}
	out, perr := ParseEnvelope(res.Stdout)
	if perr != nil {
		return InvokeResult{}, errors.Join(ErrInvokeTransport, perr)
	}
	return out, nil
}

// invokeDeadline bounds an invocation from the panel: the function
// timeout, the runtime's queue wait, the invoke subcommand's margin and
// the panel's.
func invokeDeadline(timeoutSeconds int) time.Duration {
	return time.Duration(timeoutSeconds)*time.Second + runtimeQueueWait + invokeMargin + panelMargin
}

// slot takes one of the invocation slots of a function, waiting a little.
func (m *Manager) slot(ctx context.Context, n Name) (func(), *Error) {
	m.invokeMu.Lock()
	ch := m.invokeSlots[n.String()]
	if ch == nil {
		ch = make(chan struct{}, m.cfg.InvokeConcurrency)
		m.invokeSlots[n.String()] = ch
	}
	m.invokeMu.Unlock()
	t := time.NewTimer(m.invokeWait)
	defer t.Stop()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-t.C:
	case <-ctx.Done():
	}
	return nil, newError(CodeInvokeBusy, "", "too many invocations of this function are running, retry later")
}

// slotReleaseDelay: after an exec cut short (client gone, deadline), the
// runtime's invoke process notices its stdin closed and exits within about
// a second; the slot is kept until then.
var slotReleaseDelay = time.Second

// Invoke runs one invocation of a function with an event read from
// event (at most MaxEventBytes, one JSON value). The function's existence
// and state and a panel slot are checked before the body is read. The
// result is untrusted data written by the function: callers render it as
// text only. Invocations are audited aggregated per function and minute,
// never with their content.
func (m *Manager) Invoke(ctx context.Context, name string, event io.Reader, actor string) (*InvokeResult, error) {
	n, nerr := parse(name)
	if nerr != nil {
		return nil, nerr
	}
	ref, err := m.load(n)
	if err != nil {
		return nil, err
	}
	switch {
	case m.modified(ref):
		return nil, m.refused(n, actor, "invoke", "compose project modified outside the panel", CodeModified,
			"the function was modified outside the panel: it is never invoked by the panel (purge it)")
	case ref.Deployment == nil:
		return nil, newError(CodeNotDeployed, "", "the function has no code yet: deploy it first")
	case ref.Stopped:
		return nil, newError(CodeNotRunning, "", "the function is stopped")
	}
	release, serr := m.slot(ctx, n)
	if serr != nil {
		return nil, serr
	}
	cut := false
	defer func() {
		if cut {
			time.AfterFunc(slotReleaseDelay, release)
			return
		}
		release()
	}()
	body, rerr := io.ReadAll(io.LimitReader(event, MaxEventBytes+1))
	switch {
	case rerr != nil:
		var mbe *http.MaxBytesError
		if errors.As(rerr, &mbe) {
			return nil, newError(CodeEventTooLarge, "event", "the event is at most %d bytes", MaxEventBytes)
		}
		if errors.Is(rerr, os.ErrDeadlineExceeded) {
			return nil, newError(CodeEventTimeout, "event", "the event was not received in time")
		}
		return nil, newError(CodeInvalidEvent, "event", "the event could not be read")
	case len(body) > MaxEventBytes:
		return nil, newError(CodeEventTooLarge, "event", "the event is at most %d bytes", MaxEventBytes)
	case !json.Valid(body):
		// The runtime reads the event as the first JSON value on stdin.
		return nil, newError(CodeInvalidEvent, "event", "the event must be one JSON value")
	}
	id, problem := m.verifyRunning(ctx, ref, m.renderConfig(ref))
	switch problem {
	case "":
	case problemNotFound, problemNotRunning:
		return nil, newError(CodeNotRunning, "", "the function is not running")
	default:
		return nil, m.refused(n, actor, "invoke", problem, CodeVerificationFailed,
			"the container does not match the function reference: it is not invoked")
	}
	// Into the verified container ID only. The exec is bound to the
	// request: a client gone closes the exec connection, hence the
	// runtime's stdin, and the invoke process exits.
	// A newline ends the event: the runtime's JSON decoder cannot finish a
	// top-level scalar (42, "hi", true, null) before the next byte, and
	// stdin stays open.
	res, err := m.invoker.Invoke(ctx, id, append(body, '\n'), invokeDeadline(ref.TimeoutSeconds))
	if err != nil {
		cut = true
		m.invocations.record(n.String(), actor, outcomeFailed)
		if errors.Is(err, ErrInvokeTimeout) {
			return nil, newError(CodeInvokeTimeout, "", "the invocation did not complete in time")
		}
		m.logger.Warn("functions: invocation failed", "function", n.String(), "error", err)
		return nil, newError(CodeInvokeFailed, "", "the runtime did not deliver the invocation")
	}
	outcome := outcomeOK
	if res.FunctionError != "" || res.StatusCode >= 400 {
		outcome = outcomeFunctionError
	}
	m.invocations.record(n.String(), actor, outcome)
	return &res, nil
}

// forgetSlots drops the invocation slots of a deleted function.
func (m *Manager) forgetSlots(n Name) {
	m.invokeMu.Lock()
	delete(m.invokeSlots, n.String())
	m.invokeMu.Unlock()
}
