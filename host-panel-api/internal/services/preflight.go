package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"host-panel-api/internal/composekit"
)

// PreflightResult reports what the installation check found.
type PreflightResult struct {
	ComposeVersion string
}

// ComposeVersioner reports the docker compose version (composekit.ExecRunner).
type ComposeVersioner interface {
	Version(ctx context.Context) (string, error)
}

// Preflight verifies the installation before enabling the module:
//   - both directories are absolute, distinct, writable directories;
//   - the docker compose plugin is available;
//   - inside Docker, both directories are bind mounts at the very same path
//     as on the host (compose reads the paths from the API container, the
//     daemon resolves them on the host), and the API runs as root (it chowns
//     and deletes the engines' data).
//
// The shared network is registered by the networks module
// (networks.Manager.EnsureShared) before the module is enabled.
func Preflight(ctx context.Context, cfg Config, runner ComposeVersioner, self composekit.SelfInspector) (PreflightResult, error) {
	var res PreflightResult
	dirs := []struct{ env, dir string }{
		{"HOSTING_STACKS_DIRECTORY", cfg.StacksDir},
		{"SERVICES_DATA_DIRECTORY", cfg.Render.DataDir},
	}
	for _, d := range dirs {
		if err := composekit.CheckWritableDir(d.dir); err != nil {
			return res, fmt.Errorf("%s=%q: %w", d.env, d.dir, err)
		}
		// Paths are compared and mounted as written: a symlink would make
		// them point elsewhere than they look.
		if real, err := filepath.EvalSymlinks(d.dir); err != nil || real != d.dir {
			return res, fmt.Errorf("%s=%q must not go through a symlink", d.env, d.dir)
		}
	}
	if cfg.StacksDir == cfg.Render.DataDir || isWithin(cfg.Render.DataDir, cfg.StacksDir) || isWithin(cfg.StacksDir, cfg.Render.DataDir) {
		return res, fmt.Errorf("SERVICES_DATA_DIRECTORY must be outside HOSTING_STACKS_DIRECTORY (and the other way round)")
	}
	version, err := runner.Version(ctx)
	if err != nil {
		return res, err
	}
	res.ComposeVersion = version

	if id := self.SelfID(); id != "" {
		inspect, err := self.InspectContainer(ctx, id)
		if err != nil {
			return res, fmt.Errorf("inspecting own container: %w", err)
		}
		for _, d := range dirs {
			if err := composekit.CheckSamePathMount(inspect.Mounts, d.dir); err != nil {
				return res, fmt.Errorf("%s=%q: %w", d.env, d.dir, err)
			}
		}
		if os.Geteuid() != 0 {
			return res, fmt.Errorf("the API container must run as root to manage the services data")
		}
		// uid 0 in a user namespace may still be unable to chown: try it.
		if err := checkChown(cfg.Render.DataDir); err != nil {
			return res, fmt.Errorf("SERVICES_DATA_DIRECTORY=%q: cannot give files to the engines' user: %w", cfg.Render.DataDir, err)
		}
	}
	return res, nil
}

// checkChown creates a temporary file and gives it to uid 999 (the engines).
func checkChown(dir string) error {
	f, err := os.CreateTemp(dir, ".loch-chown-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	defer os.Remove(name)
	return os.Lchown(name, 999, 999)
}

// isWithin reports whether p is inside dir.
func isWithin(p, dir string) bool {
	return len(p) > len(dir) && p[:len(dir)] == dir && p[len(dir)] == '/'
}
