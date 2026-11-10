package composekit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DefaultComposeFile is the compose file name used when ExecRunner.Files is empty.
const DefaultComposeFile = "compose.yaml"

// ComposeRunner runs Docker Compose commands for a project directory. It is
// an interface so managers can be tested without Docker.
type ComposeRunner interface {
	// Run executes `docker compose` in dir and returns its combined output.
	Run(ctx context.Context, dir string, args ...string) (string, error)
}

// ExecRunner runs the docker CLI (with the compose plugin) as a subprocess.
// Arguments are passed as a slice: no shell is involved.
type ExecRunner struct {
	Binary string // default "docker"
	// Files are the compose files, relative to the project directory (no
	// absolute path, no ".."); default [DefaultComposeFile].
	Files []string
}

const maxOutputBytes = 32 << 10

func (r ExecRunner) binary() string {
	if r.Binary == "" {
		return "docker"
	}
	return r.Binary
}

// Run executes `docker compose` for the project in dir.
func (r ExecRunner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	full, err := r.args(dir, args...)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, r.binary(), full...)
	cmd.Env = DockerEnv()
	out := &tailBuffer{max: maxOutputBytes}
	cmd.Stdout = out
	cmd.Stderr = out
	err = cmd.Run()
	return out.String(), err
}

// args builds the docker CLI arguments for a compose command in dir.
func (r ExecRunner) args(dir string, extra ...string) ([]string, error) {
	full := []string{
		"compose",
		// The project is always the directory name: neither
		// COMPOSE_PROJECT_NAME nor a .env file dropped in the directory can
		// redirect up/down to another project.
		"--project-name", filepath.Base(dir),
		"--project-directory", dir,
	}
	files := r.Files
	if len(files) == 0 {
		files = []string{DefaultComposeFile}
	}
	for _, f := range files {
		if !filepath.IsLocal(f) {
			return nil, fmt.Errorf("compose file %q must be a relative path inside the project directory", f)
		}
		full = append(full, "-f", filepath.Join(dir, f))
	}
	full = append(full,
		"--env-file", os.DevNull,
		"--ansi", "never",
		"--progress", "plain",
	)
	return append(full, extra...), nil
}

// RunStdin executes `docker compose` for the project in dir with the
// compose file read from stdin (`-f -`): the bytes the caller verified are
// the bytes compose runs, whatever happens to the files of dir meanwhile
// (no time-of-check / time-of-use window). Paths in the file must be
// absolute.
func (r ExecRunner) RunStdin(ctx context.Context, dir string, compose []byte, args ...string) (string, error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return "", fmt.Errorf("project directory %q must be an absolute, clean path", dir)
	}
	full := []string{
		"compose",
		"--project-name", filepath.Base(dir),
		"--project-directory", dir,
		"-f", "-",
		"--env-file", os.DevNull,
		"--ansi", "never",
		"--progress", "plain",
	}
	cmd := exec.CommandContext(ctx, r.binary(), append(full, args...)...)
	cmd.Env = DockerEnv()
	cmd.Stdin = bytes.NewReader(compose)
	out := &tailBuffer{max: maxOutputBytes}
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	return out.String(), err
}

// Version checks that the compose plugin is available.
func (r ExecRunner) Version(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, r.binary(), "compose", "version", "--short")
	cmd.Env = DockerEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker compose is not available: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// dockerEnvAllowlist are the only variables passed to the docker CLI: the
// API's own secrets (OIDC, cookies, SFTPGo key...) must not reach compose,
// which would also use them for interpolation.
var dockerEnvAllowlist = []string{
	"PATH", "HOME", "DOCKER_HOST", "DOCKER_CONFIG", "DOCKER_CONTEXT",
	"DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "DOCKER_API_VERSION",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
}

// DockerEnv returns the environment passed to docker CLI subprocesses.
func DockerEnv() []string {
	var env []string
	for _, k := range dockerEnvAllowlist {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	max       int
	buf       []byte
	truncated bool
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
		t.truncated = true
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	if t.truncated {
		return "…" + string(t.buf)
	}
	return string(t.buf)
}
