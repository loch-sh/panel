package composekit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
)

// SelfInspector exposes the API's own container (empty ID outside Docker).
type SelfInspector interface {
	SelfID() string
	InspectContainer(ctx context.Context, id string) (container.InspectResponse, error)
}

// CheckWritableDir verifies that dir is an absolute, clean path to an existing,
// writable directory.
func CheckWritableDir(dir string) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return fmt.Errorf("must be an absolute, clean path")
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("not a directory")
	}
	f, err := os.CreateTemp(dir, ".loch-write-test-*")
	if err != nil {
		return fmt.Errorf("not writable: %w", err)
	}
	f.Close()
	return os.Remove(f.Name())
}

// CheckSamePathMount finds the mount containing dir and checks that it is a
// bind mount whose host path equals the container path. Compose resolves the
// paths written in compose files from the API container but the daemon reads
// them on the host: any difference would silently mount the wrong directories.
func CheckSamePathMount(mounts []container.MountPoint, dir string) error {
	var best *container.MountPoint
	for i := range mounts {
		m := &mounts[i]
		if (dir == m.Destination || strings.HasPrefix(dir, strings.TrimRight(m.Destination, "/")+"/")) &&
			(best == nil || len(m.Destination) > len(best.Destination)) {
			best = m
		}
	}
	if best == nil {
		return fmt.Errorf("not mounted in the API container (mount it at the same path, e.g. -v %s:%s)", dir, dir)
	}
	if best.Type != mount.TypeBind {
		return fmt.Errorf("must be a bind mount (found %s)", best.Type)
	}
	hostPath := filepath.Join(best.Source, strings.TrimPrefix(dir, best.Destination))
	if hostPath != dir {
		return fmt.Errorf("mounted from host path %q: the host and container paths must be identical (-v %s:%s)", hostPath, dir, dir)
	}
	return nil
}
