package functions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"host-panel-api/internal/composekit"
)

// ComposeVersioner reports the docker compose version (composekit.ExecRunner).
type ComposeVersioner interface {
	Version(ctx context.Context) (string, error)
}

// Preflight verifies the installation before enabling the module (a
// failure disables the module, never the API):
//   - both directories are writable, not reached through a symlink, and
//     the data directory is owned by the panel and writable by nobody
//     else (its private and runtime directories are created 0700);
//   - the docker compose plugin is available;
//   - inside Docker, both are bind mounts at the very same path as on the
//     host (compose reads the paths from the API container, the daemon
//     resolves them on the host).
//
// The configuration lint already refused a data directory inside another
// tree of the panel.
func Preflight(ctx context.Context, cfg Config, runner ComposeVersioner, self composekit.SelfInspector) (string, error) {
	dirs := []struct{ env, dir string }{
		{"HOSTING_STACKS_DIRECTORY", cfg.StacksDir},
		{"FUNCTIONS_DATA_DIRECTORY", cfg.DataDir},
	}
	for _, d := range dirs {
		if err := composekit.CheckWritableDir(d.dir); err != nil {
			return "", fmt.Errorf("%s=%q: %w", d.env, d.dir, err)
		}
		if real, err := filepath.EvalSymlinks(d.dir); err != nil || real != d.dir {
			return "", fmt.Errorf("%s=%q must not go through a symlink", d.env, d.dir)
		}
	}
	if err := checkPanelDir(cfg.DataDir); err != nil {
		return "", fmt.Errorf("FUNCTIONS_DATA_DIRECTORY=%q: %w", cfg.DataDir, err)
	}
	p := cfg.Paths()
	for _, d := range []string{p.PrivateRoot(), p.Runtime()} {
		created, err := composekit.EnsureDir(d, privatePerm)
		if err != nil {
			return "", fmt.Errorf("FUNCTIONS_DATA_DIRECTORY: %w", err)
		}
		if created {
			if err := os.Chmod(d, privatePerm); err != nil {
				return "", err
			}
		}
		if err := checkPanelDir(d); err != nil {
			return "", fmt.Errorf("FUNCTIONS_DATA_DIRECTORY: %w", err)
		}
	}
	version, err := runner.Version(ctx)
	if err != nil {
		return "", err
	}
	if id := self.SelfID(); id != "" {
		inspect, err := self.InspectContainer(ctx, id)
		if err != nil {
			return "", fmt.Errorf("inspecting own container: %w", err)
		}
		for _, d := range dirs {
			if err := composekit.CheckSamePathMount(inspect.Mounts, d.dir); err != nil {
				return "", fmt.Errorf("%s=%q: %w", d.env, d.dir, err)
			}
		}
	}
	return version, nil
}
