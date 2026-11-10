package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// fakeProcess simulates a container process on the daemon side of conn: it
// handles stdin and writes a multiplexed stdout/stderr stream.
type fakeProcess func(stdin io.Reader, stdout, stderr io.Writer)

// pipeExec wires pumpExec to a fakeProcess through synchronous, unbuffered
// in-memory pipes (the worst case for deadlocks): one for stdin (closed by
// CloseWrite) and one for the multiplexed output.
func pipeExec(t *testing.T, ctx context.Context, stdin []byte, proc fakeProcess) (ExecResult, error) {
	t.Helper()
	stdinR, stdinW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		proc(stdinR, &muxWriter{w: outW, stream: stdcopy.Stdout}, &muxWriter{w: outW, stream: stdcopy.Stderr})
		outW.Close() // the process exited: end of the output stream
	}()
	closeConn := func() {
		stdinW.CloseWithError(net.ErrClosed)
		outR.CloseWithError(net.ErrClosed)
		stdinR.CloseWithError(net.ErrClosed)
	}
	before := runtime.NumGoroutine()
	res, err := pumpExec(ctx, stdin, stdinW, stdinW.Close, outR, closeConn)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fake process still blocked after pumpExec returned")
	}
	// pumpExec's goroutines (and the fake process) must have ended.
	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() >= before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n >= before {
		t.Fatalf("goroutine leak: %d running, %d before the call (fake process included)", n, before)
	}
	return res, err
}

// muxWriter frames writes like the daemon does for a non-TTY exec:
// [stream, 0, 0, 0, size (uint32 big endian)] + payload.
type muxWriter struct {
	w      io.Writer
	stream stdcopy.StdType
}

func (m *muxWriter) Write(p []byte) (int, error) {
	const chunk = 32 << 10
	for off := 0; off < len(p); off += chunk {
		end := min(off+chunk, len(p))
		hdr := make([]byte, 8)
		hdr[0] = byte(m.stream)
		binary.BigEndian.PutUint32(hdr[4:], uint32(end-off))
		if _, err := m.w.Write(append(hdr, p[off:end]...)); err != nil {
			return off, err
		}
	}
	return len(p), nil
}

func TestPumpExecSeparatesStreams(t *testing.T) {
	res, err := pipeExec(t, context.Background(), []byte("hello"), func(in io.Reader, out, errw io.Writer) {
		data, _ := io.ReadAll(in)
		out.Write(bytes.ToUpper(data))
		errw.Write([]byte("warn"))
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "HELLO" || string(res.Stderr) != "warn" || res.Truncated {
		t.Fatalf("res = %+v", res)
	}
}

// A process that echoes its input while reading it (like cat) deadlocks a
// client that writes all of stdin before reading any output.
func TestPumpExecNoDeadlockWithLargeEcho(t *testing.T) {
	stdin := bytes.Repeat([]byte("x"), MaxExecStdin)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := pipeExec(t, ctx, stdin, func(in io.Reader, out, _ io.Writer) {
		io.Copy(out, in)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stdout) != len(stdin) {
		t.Fatalf("stdout = %d bytes, want %d", len(res.Stdout), len(stdin))
	}
}

func TestPumpExecBoundsOutputAndDrains(t *testing.T) {
	big := strings.Repeat("y", maxExecStdout+10)
	res, err := pipeExec(t, context.Background(), nil, func(_ io.Reader, out, errw io.Writer) {
		out.Write([]byte(big)) // returns only once fully drained by the client
		errw.Write([]byte(strings.Repeat("e", maxExecStderr+1)))
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stdout) != maxExecStdout || len(res.Stderr) != maxExecStderr || !res.Truncated {
		t.Fatalf("stdout=%d stderr=%d truncated=%v", len(res.Stdout), len(res.Stderr), res.Truncated)
	}
}

// A process that exits without reading its input must not block the writer.
func TestPumpExecProcessIgnoresStdin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := pipeExec(t, ctx, bytes.Repeat([]byte("z"), MaxExecStdin), func(_ io.Reader, out, _ io.Writer) {
		out.Write([]byte("bye"))
	})
	if err != nil || string(res.Stdout) != "bye" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// A process that never ends (here: endless output) is stopped by the timeout
// and the connection is closed so that nothing stays blocked.
func TestPumpExecTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := pipeExec(t, ctx, nil, func(_ io.Reader, out, _ io.Writer) {
		for {
			if _, err := out.Write([]byte("tick")); err != nil {
				return
			}
		}
	})
	if !errors.Is(err, ErrExecTimeout) {
		t.Fatalf("err = %v, want ErrExecTimeout", err)
	}
}

func TestBoundedBuffer(t *testing.T) {
	b := &boundedBuffer{max: 4}
	if n, _ := b.Write([]byte("ab")); n != 2 {
		t.Fatal("short write")
	}
	if n, _ := b.Write([]byte("cdef")); n != 4 {
		t.Fatal("a bounded buffer must report full writes to keep draining")
	}
	if string(b.Bytes()) != "abcd" || !b.truncated {
		t.Fatalf("buf=%q truncated=%v", b.Bytes(), b.truncated)
	}
}

func TestExecRunValidatesRequest(t *testing.T) {
	c := &Client{}
	if _, err := c.ExecRun(context.Background(), "x", ExecRequest{}); err == nil {
		t.Fatal("empty command must be refused")
	}
	if _, err := c.ExecRun(context.Background(), "x", ExecRequest{Cmd: []string{"cat"}, Stdin: make([]byte, MaxExecStdin+1)}); err == nil {
		t.Fatal("oversized stdin must be refused")
	}
}

// A process that neither reads stdin nor writes anything must not keep the
// writer blocked after the timeout.
func TestPumpExecTimeoutInertProcessWithStdin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := pipeExec(t, ctx, bytes.Repeat([]byte("s"), MaxExecStdin), func(_ io.Reader, out, _ io.Writer) {
		<-ctx.Done()
		out.Write([]byte("late")) // fails: the connection was closed
	})
	if !errors.Is(err, ErrExecTimeout) {
		t.Fatalf("err = %v", err)
	}
}

// A corrupt stream is a read error. (A frame cut by EOF is not: stdcopy
// accepts it, and the exit code then tells whether the command succeeded.)
func TestPumpExecCorruptStream(t *testing.T) {
	outR, outW := io.Pipe()
	go func() {
		hdr := []byte{9, 0, 0, 0, 0, 0, 0, 3} // unknown stream type
		outW.Write(append(hdr, "abc"...))
		outW.Close()
	}()
	stdinR, stdinW := io.Pipe()
	go io.Copy(io.Discard, stdinR)
	_, err := pumpExec(context.Background(), []byte("x"), stdinW, stdinW.Close, outR, func() {
		stdinW.CloseWithError(net.ErrClosed)
		outR.CloseWithError(net.ErrClosed)
	})
	if err == nil {
		t.Fatal("a corrupt stream must be reported")
	}
}

// After a failed write the connection is dropped: no EOF, which would let
// the process run a truncated script.
func TestPumpExecWriteErrorDropsConnection(t *testing.T) {
	outR, outW := io.Pipe()
	var once sync.Once
	dropped := make(chan struct{})
	closeConn := func() {
		once.Do(func() { close(dropped) })
		outR.CloseWithError(net.ErrClosed)
	}
	go func() { <-dropped; outW.Close() }()
	var closedWrite atomic.Bool
	failing := writerFunc(func([]byte) (int, error) { return 0, errors.New("broken pipe") })
	pumpExec(context.Background(), []byte("SECRET"), failing,
		func() error { closedWrite.Store(true); return nil }, outR, closeConn)
	if closedWrite.Load() {
		t.Fatal("CloseWrite must not be called after a failed write")
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

type fakeInspector struct {
	running int // number of calls that report Running
	code    int
	calls   int
}

func (f *fakeInspector) ExecInspect(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error) {
	f.calls++
	return client.ExecInspectResult{Running: f.calls <= f.running, ExitCode: f.code}, nil
}

func TestWaitExecExit(t *testing.T) {
	code, err := waitExecExit(context.Background(), &fakeInspector{running: 2, code: 7}, "e", time.Second)
	if err != nil || code != 7 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	_, err = waitExecExit(context.Background(), &fakeInspector{running: 1 << 30}, "e", 60*time.Millisecond)
	if !errors.Is(err, ErrExecStillRunning) {
		t.Fatalf("err = %v, want ErrExecStillRunning", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = waitExecExit(ctx, &fakeInspector{running: 1 << 30}, "e", time.Minute)
	if !errors.Is(err, ErrExecTimeout) {
		t.Fatalf("err = %v, want ErrExecTimeout", err)
	}
}

func TestExecResultFormattingHidesOutput(t *testing.T) {
	r := ExecResult{ExitCode: 1, Stdout: []byte("SECRET"), Stderr: []byte("LINE 1: PASSWORD 'SECRET'")}
	if out := fmt.Sprintf("%v %+v %#v %s", r, r, r, r); strings.Contains(out, "SECRET") {
		t.Fatalf("output leaked: %s", out)
	}
}
