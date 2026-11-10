package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// Limits of ExecRun. Output beyond the limits is drained and discarded (the
// process must never block on a full pipe) and reported as truncated.
// MaxExecStdin is exported because callers build the input and must bound it.
const (
	MaxExecStdin       = 64 << 10
	maxExecStdout      = 1 << 20
	maxExecStderr      = 64 << 10
	DefaultExecTimeout = 30 * time.Second
	// exitPollInterval and exitPollTimeout bound the wait for the exit code
	// after the output stream ended (the daemon may report the exec as
	// running for a short while).
	exitPollInterval = 20 * time.Millisecond
	exitPollTimeout  = 5 * time.Second
)

var (
	// ErrExecTimeout is returned when the command did not finish in time. The
	// outcome is UNKNOWN: Docker does not kill an exec, so the command may
	// still complete afterwards (e.g. a password change). Callers must check
	// the real state instead of assuming failure, and should bound the
	// command themselves (`timeout -s KILL`, statement_timeout...).
	ErrExecTimeout = errors.New("exec timed out")
	// ErrExecStillRunning is returned when the output stream ended but the
	// daemon still reports the process as running (it closed its outputs or
	// left a background child). The outcome is unknown, as for a timeout.
	ErrExecStillRunning = errors.New("exec still running after its output ended")
	// ErrExecNoHalfClose is returned when the connection to the daemon cannot
	// signal end-of-input: a command reading stdin would never terminate.
	ErrExecNoHalfClose = errors.New("exec: connection to the daemon cannot half-close stdin")
	// ErrExecOutputTooLarge is returned by ExecInvoke when the command
	// wrote more than its stdout limit (nothing beyond it is buffered).
	ErrExecOutputTooLarge = errors.New("exec: output exceeds its limit")
)

// ExecRequest is a non-interactive command run in a container. ExecRun adds
// no shell: Cmd is passed to the daemon as is. Secrets must go through Stdin (never Cmd, which
// is visible in `docker inspect`, the daemon logs and the process list).
type ExecRequest struct {
	Cmd     []string
	User    string        // empty = the image user
	Stdin   []byte        // closed (EOF) once written; at most MaxExecStdin
	Timeout time.Duration // 0 = DefaultExecTimeout
}

// ExecResult is the outcome of a command. When ExecRun also returns an error,
// ExitCode is 0 and the outputs are partial.
//
// Stdout and Stderr may echo the input (psql quotes the failing statement,
// MySQL its "near '...'" part): never log nor return them as is. String and
// GoString omit them so that %v or slog.Any cannot leak them by accident.
type ExecResult struct {
	ExitCode  int
	Stdout    []byte
	Stderr    []byte
	Truncated bool // stdout and/or stderr exceeded its limit
}

func (r ExecResult) String() string {
	return fmt.Sprintf("ExecResult{ExitCode:%d Stdout:%dB Stderr:%dB Truncated:%v}",
		r.ExitCode, len(r.Stdout), len(r.Stderr), r.Truncated)
}

func (r ExecResult) GoString() string { return r.String() }

// ExecRun runs req in the container and waits for its exit code. A non-zero
// exit code is not an error: callers inspect ExitCode.
//
// On timeout (ErrExecTimeout) the connection is closed, which closes the
// process stdin and outputs, but Docker does not kill an exec: a process that
// ignores both keeps running in the container until it ends by itself. Errors
// never contain the input nor the output.
func (c *Client) ExecRun(ctx context.Context, containerID string, req ExecRequest) (ExecResult, error) {
	if len(req.Cmd) == 0 {
		return ExecResult{}, errors.New("exec: empty command")
	}
	if len(req.Stdin) > MaxExecStdin {
		return ExecResult{}, fmt.Errorf("exec: stdin exceeds %d bytes", MaxExecStdin)
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultExecTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	created, err := c.cli.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		Cmd:          req.Cmd,
		User:         req.User,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		TTY:          false,
	})
	if err != nil {
		return ExecResult{}, fmt.Errorf("exec create: %w", wrapTimeout(ctx, err))
	}
	attached, err := c.cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return ExecResult{}, fmt.Errorf("exec attach: %w", wrapTimeout(ctx, err))
	}
	hijacked := attached.HijackedResponse
	defer hijacked.Close() // idempotent with the close done by pumpExec
	if _, ok := hijacked.Conn.(client.CloseWriter); !ok {
		return ExecResult{}, ErrExecNoHalfClose
	}

	res, err := pumpExec(ctx, req.Stdin, hijacked.Conn, hijacked.CloseWrite, hijacked.Reader, hijacked.Close)
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

// execInspector is the part of the SDK client used to read an exit code.
type execInspector interface {
	ExecInspect(ctx context.Context, execID string, options client.ExecInspectOptions) (client.ExecInspectResult, error)
}

// waitExecExit polls the exec until the daemon reports it finished.
func waitExecExit(ctx context.Context, insp execInspector, execID string, pollTimeout time.Duration) (int, error) {
	deadline := time.Now().Add(pollTimeout)
	ticker := time.NewTicker(exitPollInterval)
	defer ticker.Stop()
	for {
		info, err := insp.ExecInspect(ctx, execID, client.ExecInspectOptions{})
		if err != nil {
			return 0, fmt.Errorf("exec inspect: %w", wrapTimeout(ctx, err))
		}
		if !info.Running {
			return info.ExitCode, nil
		}
		if time.Now().After(deadline) {
			return 0, ErrExecStillRunning
		}
		select {
		case <-ctx.Done():
			return 0, wrapTimeout(ctx, ctx.Err())
		case <-ticker.C:
		}
	}
}

// pumpExec writes stdin (then half-closes it) while it demultiplexes the
// output stream into bounded buffers. Writing and reading run concurrently: a
// process that writes a lot before reading all of its input would otherwise
// deadlock. closeConn closes the whole connection (it must be idempotent and
// safe for concurrent use); it is always called before pumpExec returns and
// both goroutines have ended by then.
func pumpExec(ctx context.Context, stdin []byte, w io.Writer, closeWrite func() error,
	r io.Reader, closeConn func()) (ExecResult, error) {
	return pumpExecLimits(ctx, outputLimits{stdout: maxExecStdout, stderr: maxExecStderr}, stdin, w, closeWrite, r, closeConn)
}

// outputLimits bound the buffered outputs of an exec. failOnStdout makes a
// stdout over its limit an error (the output is useless when truncated)
// and stops reading at once.
type outputLimits struct {
	stdout, stderr int
	failOnStdout   bool
	// keepStdin leaves stdin open once written: the command watches it and
	// exits when the connection closes (closeConn).
	keepStdin bool
}

// pumpExecLimits is pumpExec with explicit output limits.
func pumpExecLimits(ctx context.Context, limits outputLimits, stdin []byte, w io.Writer, closeWrite func() error,
	r io.Reader, closeConn func()) (ExecResult, error) {

	writeErr := make(chan error, 1)
	go func() {
		var err error
		if len(stdin) > 0 {
			_, err = w.Write(stdin)
		}
		switch {
		case err != nil:
			// Never send EOF after a partial write: the process would run a
			// truncated script. Drop the connection instead.
			closeConn()
		case !limits.keepStdin:
			err = closeWrite()
		}
		writeErr <- err
	}()

	stdout := &boundedBuffer{max: limits.stdout, fail: limits.failOnStdout}
	stderr := &boundedBuffer{max: limits.stderr}
	readErr := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(stdout, stderr, r)
		readErr <- err
	}()

	var err error
	select {
	case rerr := <-readErr:
		switch {
		case errors.Is(rerr, ErrExecOutputTooLarge):
			err = ErrExecOutputTooLarge
		case rerr != nil:
			err = fmt.Errorf("exec output: %w", rerr)
		}
	case <-ctx.Done():
		closeConn()
		<-readErr //nolint:errcheck // superseded by the timeout
		err = wrapTimeout(ctx, ctx.Err())
	}
	// The output ended (the process exited or closed its outputs) or failed:
	// the rest of stdin is useless and a pending write could block forever.
	// A write error here only means the process stopped reading early, which
	// its exit code reports better.
	closeConn()
	<-writeErr //nolint:errcheck // see above: the exit code reports it better
	return ExecResult{
		Stdout:    stdout.Bytes(),
		Stderr:    stderr.Bytes(),
		Truncated: stdout.truncated || stderr.truncated,
	}, err
}

func wrapTimeout(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", ErrExecTimeout, err)
	}
	return err
}

// boundedBuffer keeps the first max bytes and silently drains the rest
// (or, with fail, refuses them: the reader stops at once).
type boundedBuffer struct {
	max       int
	buf       []byte
	truncated bool
	fail      bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.fail && len(b.buf)+len(p) > b.max {
		b.truncated = true
		return 0, ErrExecOutputTooLarge
	}
	if room := b.max - len(b.buf); room > 0 {
		if len(p) <= room {
			b.buf = append(b.buf, p...)
			return len(p), nil
		}
		b.buf = append(b.buf, p[:room]...)
	}
	b.truncated = true
	return len(p), nil
}

func (b *boundedBuffer) Bytes() []byte { return b.buf }
