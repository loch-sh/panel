//go:build integration

package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// startAlpine runs a throwaway container and returns its ID.
func startAlpine(t *testing.T) string {
	t.Helper()
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker not available")
	}
	// Output (not CombinedOutput): a first run prints the pull progress on stderr.
	out, err := exec.Command("docker", "run", "-d", "--rm", "mirror.gcr.io/library/alpine:3", "sleep", "300").Output()
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", id).Run() })
	return id
}

func TestExecRunIntegration(t *testing.T) {
	id := startAlpine(t)
	c, err := NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()

	t.Run("stdin round trip without deadlock", func(t *testing.T) {
		in := bytes.Repeat([]byte("0123456789abcdef"), MaxExecStdin/16)
		res, err := c.ExecRun(ctx, id, ExecRequest{Cmd: []string{"cat"}, Stdin: in})
		if err != nil || res.ExitCode != 0 || !bytes.Equal(res.Stdout, in) {
			t.Fatalf("err=%v code=%d stdout=%d bytes", err, res.ExitCode, len(res.Stdout))
		}
	})
	t.Run("exit code and stderr", func(t *testing.T) {
		res, err := c.ExecRun(ctx, id, ExecRequest{Cmd: []string{"sh", "-c", "echo oops >&2; exit 3"}})
		if err != nil || res.ExitCode != 3 || strings.TrimSpace(string(res.Stderr)) != "oops" || len(res.Stdout) != 0 {
			t.Fatalf("err=%v res=%+v", err, res)
		}
	})
	t.Run("large output is bounded", func(t *testing.T) {
		res, err := c.ExecRun(ctx, id, ExecRequest{Cmd: []string{"head", "-c", "3000000", "/dev/zero"}})
		if err != nil || res.ExitCode != 0 || len(res.Stdout) != maxExecStdout || !res.Truncated {
			t.Fatalf("err=%v code=%d stdout=%d truncated=%v", err, res.ExitCode, len(res.Stdout), res.Truncated)
		}
	})
	t.Run("user", func(t *testing.T) {
		res, err := c.ExecRun(ctx, id, ExecRequest{Cmd: []string{"id", "-un"}, User: "nobody"})
		if err != nil || strings.TrimSpace(string(res.Stdout)) != "nobody" {
			t.Fatalf("err=%v stdout=%q", err, res.Stdout)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		start := time.Now()
		_, err := c.ExecRun(ctx, id, ExecRequest{Cmd: []string{"sleep", "30"}, Timeout: 500 * time.Millisecond})
		if !errors.Is(err, ErrExecTimeout) || time.Since(start) > 5*time.Second {
			t.Fatalf("err=%v after %s", err, time.Since(start))
		}
	})
	t.Run("missing binary", func(t *testing.T) {
		res, err := c.ExecRun(ctx, id, ExecRequest{Cmd: []string{"definitely-not-here"}})
		if err == nil && res.ExitCode == 0 {
			t.Fatal("a missing binary must fail")
		}
	})
}
