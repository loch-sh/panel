package functions

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"host-panel-api/internal/docker"
)

type fakeExec struct {
	req docker.InvokeRequest
	res docker.ExecResult
	err error
}

func (f *fakeExec) ExecInvoke(_ context.Context, _ string, req docker.InvokeRequest) (docker.ExecResult, error) {
	f.req = req
	return f.res, f.err
}

func TestExecInvoker(t *testing.T) {
	env := `{"statusCode":200,"functionError":"","requestId":"8f0c2a6e-1b2c-4d5e-9f00-1234567890ab","durationMs":3,"payload":"` +
		base64.StdEncoding.EncodeToString([]byte(`"hi"`)) + `","logTail":""}`
	f := &fakeExec{res: docker.ExecResult{Stdout: []byte(env)}}
	res, err := ExecInvoker{Docker: f}.Invoke(context.Background(), "c", []byte(`{}`), time.Minute)
	if err != nil || string(res.Payload) != `"hi"` {
		t.Fatalf("%+v %v", res, err)
	}
	// The command: the runtime's invoke subcommand, as the function user,
	// event on stdin, no credential (loopback).
	// --watch-stdin: stdin stays open, the process exits when it closes.
	if !slices.Equal(f.req.Cmd, []string{"/loch/lambda-runtime", "invoke", "--watch-stdin", "--log-tail"}) || !f.req.KeepStdinOpen || f.req.User != "10001:10001" ||
		string(f.req.Stdin) != `{}` || f.req.MaxStdout != maxEnvelopeBytes || f.req.Timeout != time.Minute {
		t.Fatalf("request: %+v", f.req)
	}
	for name, c := range map[string]struct {
		res  docker.ExecResult
		err  error
		want error
	}{
		"timeout":    {err: docker.ErrExecTimeout, want: ErrInvokeTimeout},
		"too large":  {err: docker.ErrExecOutputTooLarge, want: ErrInvokeTransport},
		"exit code":  {res: docker.ExecResult{ExitCode: 1}, want: ErrInvokeTransport},
		"bad output": {res: docker.ExecResult{Stdout: []byte("<html>")}, want: ErrInvokeTransport},
	} {
		f := &fakeExec{res: c.res, err: c.err}
		if _, err := (ExecInvoker{Docker: f}).Invoke(context.Background(), "c", nil, time.Second); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if d := invokeDeadline(60); d != 60*time.Second+runtimeQueueWait+invokeMargin+panelMargin {
		t.Fatalf("deadline %v", d)
	}
}

func TestInvocationAuditRollsOverEachMinute(t *testing.T) {
	var buf bytes.Buffer
	now := time.Date(2026, 11, 6, 12, 0, 10, 0, time.UTC)
	a := newInvocationAudit(slog.New(slog.NewJSONHandler(&buf, nil)), func() time.Time { return now })
	for i := range 7 {
		a.record("resize", "user"+string(rune('a'+i))+"@x", outcomeOK)
	}
	a.flushOld()
	if buf.Len() != 0 {
		t.Fatal("the current minute is not flushed")
	}
	now = now.Add(time.Minute)
	a.record("resize", "usera@x", outcomeFailed) // writes the previous minute first
	out := buf.String()
	if strings.Count(out, "function invoked") != 1 || !strings.Contains(out, `"count":7`) || !strings.Contains(out, ",…") {
		t.Fatalf("%s", out)
	}
	now = now.Add(time.Minute)
	a.flushOld()
	if strings.Count(buf.String(), "function invoked") != 2 || !strings.Contains(buf.String(), `"failures":1`) {
		t.Fatalf("%s", buf.String())
	}
}
