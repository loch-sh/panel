package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"

	"host-panel-api/internal/model"
)

func functionContainer() container.InspectResponse {
	return container.InspectResponse{
		Name: "/fn-resize",
		Config: &container.Config{
			Env:    []string{"PATH=/usr/bin", "AWS_SECRET_ACCESS_KEY=s3cr3t", "LOCH_RUNTIME_TOKEN=tok=en", "EMPTY="},
			Labels: map[string]string{FunctionLabel: "resize"},
		},
	}
}

func TestMaskFunctionEnv(t *testing.T) {
	info := functionContainer()
	masked := MaskFunctionEnv(info)
	want := []string{"PATH=***", "AWS_SECRET_ACCESS_KEY=***", "LOCH_RUNTIME_TOKEN=***", "EMPTY=***"}
	if !slices.Equal(masked.Config.Env, want) {
		t.Fatalf("masked env = %v", masked.Config.Env)
	}
	// The original (possibly cached) response is not modified.
	if info.Config.Env[1] != "AWS_SECRET_ACCESS_KEY=s3cr3t" {
		t.Fatal("the original configuration was modified")
	}
	// Other containers are left alone.
	site := container.InspectResponse{Name: "/hosted-shop", Config: &container.Config{Env: []string{"DB_PASSWORD=x"}}}
	if got := MaskFunctionEnv(site); got.Config.Env[0] != "DB_PASSWORD=x" {
		t.Fatal("a non-function container must not be masked")
	}
	// No configuration: unchanged, no panic.
	MaskFunctionEnv(container.InspectResponse{Name: "/fn-x"})
}

func TestIsFunctionContainer(t *testing.T) {
	for name, info := range map[string]container.InspectResponse{
		"by name":    {Name: "/fn-resize"},
		"by label":   {Name: "/renamed", Config: &container.Config{Labels: map[string]string{FunctionLabel: "resize"}}},
		"by project": {Name: "/x", Config: &container.Config{Labels: map[string]string{"com.docker.compose.project": "fn-resize"}}},
	} {
		if !IsFunctionContainer(info) {
			t.Errorf("%s: must be protected", name)
		}
	}
	if IsFunctionContainer(container.InspectResponse{Name: "/svc-postgres-17", Config: &container.Config{}}) {
		t.Error("a service container is not a function")
	}
	if !IsFunctionContainerName("fn-resize") || !IsFunctionContainerName("/fn-resize") || IsFunctionContainerName("hosted-fn-x") {
		t.Error("IsFunctionContainerName")
	}
}

func TestGoArch(t *testing.T) {
	for in, want := range map[string]string{"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64", "s390x": "s390x"} {
		if got := GoArch(in); got != want {
			t.Errorf("GoArch(%q) = %q", in, got)
		}
	}
}

// pumpWith runs pumpExecLimits against a fake process (see pipeExec).
func pumpWith(t *testing.T, limits outputLimits, stdin []byte, proc fakeProcess) (ExecResult, error) {
	t.Helper()
	stdinR, stdinW := io.Pipe()
	outR, outW := io.Pipe()
	go func() {
		proc(stdinR, &muxWriter{w: outW, stream: stdcopy.Stdout}, &muxWriter{w: outW, stream: stdcopy.Stderr})
		outW.Close()
	}()
	closeConn := func() {
		stdinW.CloseWithError(net.ErrClosed)
		outR.CloseWithError(net.ErrClosed)
		stdinR.CloseWithError(net.ErrClosed)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return pumpExecLimits(ctx, limits, stdin, stdinW, stdinW.Close, outR, closeConn)
}

func TestInvokeOutputLimit(t *testing.T) {
	event := bytes.Repeat([]byte("e"), MaxExecStdin*4) // above ExecRun's stdin limit
	echo := func(stdin io.Reader, stdout, _ io.Writer) {
		data, _ := io.ReadAll(stdin)
		stdout.Write([]byte(`{"n":` + strings.Repeat("1", len(data)%7+1) + `}`))
	}
	res, err := pumpWith(t, outputLimits{stdout: 1 << 10, stderr: 64, failOnStdout: true}, event, echo)
	if err != nil || !strings.HasPrefix(string(res.Stdout), `{"n":`) {
		t.Fatalf("%v %v", res, err)
	}
	// Over the envelope limit: an error, nothing buffered beyond it.
	flood := func(stdin io.Reader, stdout, _ io.Writer) {
		io.Copy(io.Discard, stdin)
		for range 64 {
			if _, err := stdout.Write(bytes.Repeat([]byte("x"), 1<<10)); err != nil {
				return
			}
		}
	}
	res, err = pumpWith(t, outputLimits{stdout: 4 << 10, stderr: 64, failOnStdout: true}, nil, flood)
	if !errors.Is(err, ErrExecOutputTooLarge) || len(res.Stdout) > 4<<10 {
		t.Fatalf("flood: %d bytes, %v", len(res.Stdout), err)
	}
}

func TestExecInvokeValidatesTheRequest(t *testing.T) {
	c := &Client{}
	if _, err := c.ExecInvoke(context.Background(), "x", InvokeRequest{}); err == nil {
		t.Fatal("an empty request must be refused")
	}
	_, err := c.ExecInvoke(context.Background(), "x", InvokeRequest{Cmd: []string{"a"}, MaxStdout: 1, Timeout: time.Second, Stdin: make([]byte, MaxInvokeStdin+1)})
	if err == nil || !strings.Contains(err.Error(), "stdin exceeds") {
		t.Fatalf("oversized stdin: %v", err)
	}
}

func TestUserCanAccess(t *testing.T) {
	admin := &model.User{Profile: model.ProfileAdmin}
	legacy := &model.User{Profile: model.ProfileRestricted, Permissions: []string{"fn-resize", "renamed", "web"}}
	fn := functionContainer()
	renamed := container.InspectResponse{Name: "/renamed", Config: &container.Config{Labels: map[string]string{FunctionLabel: "resize"}}}
	web := container.InspectResponse{Name: "/web", Config: &container.Config{}}
	if !UserCanAccess(admin, fn) || UserCanAccess(legacy, fn) || UserCanAccess(legacy, renamed) || !UserCanAccess(legacy, web) {
		t.Fatal("a restricted user never reaches a function container, even with a stored grant")
	}
	if UserCanAccess(nil, web) || UserCanAccess(&model.User{Profile: "other", Permissions: []string{"web"}}, web) {
		t.Fatal("unknown profiles get nothing")
	}
}
