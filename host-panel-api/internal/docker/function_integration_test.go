//go:build integration

package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

// ExecInvoke against a real daemon, with cat standing for the runtime's
// invoke subcommand (event on stdin, envelope on stdout).
func TestExecInvokeIntegration(t *testing.T) {
	id := startAlpine(t)
	c, err := NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	event := bytes.Repeat([]byte("e"), 5<<20) // far above ExecRun's 64 KiB
	res, err := c.ExecInvoke(ctx, id, InvokeRequest{Cmd: []string{"cat"}, Stdin: event, MaxStdout: 6 << 20, Timeout: 30 * time.Second})
	if err != nil || res.ExitCode != 0 || !bytes.Equal(res.Stdout, event) {
		t.Fatalf("echo: %d bytes, exit %d, %v", len(res.Stdout), res.ExitCode, err)
	}
	// Output over the limit: an error.
	_, err = c.ExecInvoke(ctx, id, InvokeRequest{Cmd: []string{"cat"}, Stdin: event, MaxStdout: 1 << 20, Timeout: 30 * time.Second})
	if !errors.Is(err, ErrExecOutputTooLarge) {
		t.Fatalf("over the limit: %v", err)
	}
	// Deadline.
	_, err = c.ExecInvoke(ctx, id, InvokeRequest{Cmd: []string{"sleep", "30"}, MaxStdout: 1024, Timeout: time.Second})
	if !errors.Is(err, ErrExecTimeout) {
		t.Fatalf("deadline: %v", err)
	}
	// Stdin kept open (invoke --watch-stdin): the command reads what it
	// needs and exits on its own; the connection is closed afterwards.
	start := time.Now()
	res, err = c.ExecInvoke(ctx, id, InvokeRequest{Cmd: []string{"head", "-c", "2"}, Stdin: []byte(`{}`), KeepStdinOpen: true,
		MaxStdout: 1024, Timeout: 30 * time.Second})
	if err != nil || string(res.Stdout) != `{}` || time.Since(start) > 10*time.Second {
		t.Fatalf("keep stdin open: %q %v", res.Stdout, err)
	}
	// Masking through the client-facing inspect.
	info, err := c.InspectContainer(ctx, id)
	if err != nil || IsFunctionContainer(info) {
		t.Fatalf("alpine is not a function: %v", err)
	}
}
