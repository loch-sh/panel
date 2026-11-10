package hosting

import (
	"context"
	"fmt"

	"github.com/moby/moby/api/types/container"

	"host-panel-api/internal/composekit"
)

// SelfInspector exposes the API's own container (empty ID outside Docker).
type SelfInspector = composekit.SelfInspector

// Preflight verifies the installation before enabling the feature:
//   - both directories exist and are writable;
//   - the docker compose plugin is available;
//   - inside Docker, both directories are bind mounts at the very same path
//     as on the host. Compose resolves the paths written in compose files from
//     the API container but the daemon reads them on the host: any difference
//     would silently mount the wrong directories.
func Preflight(ctx context.Context, cfg Config, runner ExecRunner, self SelfInspector) (string, error) {
	dirs := map[string]string{
		"HOSTING_STACKS_DIRECTORY": cfg.StacksDir,
		"HOSTING_SITES_DIRECTORY":  cfg.Render.SitesDir,
	}
	for env, dir := range dirs {
		if err := checkWritableDir(dir); err != nil {
			return "", fmt.Errorf("%s=%q: %w", env, dir, err)
		}
	}

	version, err := runner.Version(ctx)
	if err != nil {
		return "", err
	}

	id := self.SelfID()
	if id == "" {
		return version, nil // not running in Docker: paths are host paths
	}
	inspect, err := self.InspectContainer(ctx, id)
	if err != nil {
		return "", fmt.Errorf("inspecting own container: %w", err)
	}
	for env, dir := range dirs {
		if err := checkSamePathMount(inspect.Mounts, dir); err != nil {
			return "", fmt.Errorf("%s=%q: %w", env, dir, err)
		}
	}
	return version, nil
}

func checkWritableDir(dir string) error { return composekit.CheckWritableDir(dir) }

func checkSamePathMount(mounts []container.MountPoint, dir string) error {
	return composekit.CheckSamePathMount(mounts, dir)
}
