package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
)

type fakeVersioner struct{ err error }

func (f fakeVersioner) Version(context.Context) (string, error) { return "v5", f.err }

type fakeSelf struct {
	id     string
	mounts []container.MountPoint
}

func (f fakeSelf) SelfID() string { return f.id }
func (f fakeSelf) InspectContainer(context.Context, string) (container.InspectResponse, error) {
	return container.InspectResponse{Mounts: f.mounts}, nil
}

func preflightConfig(t *testing.T) Config {
	// Resolved: on macOS the temporary directory is behind a symlink.
	root, _ := filepath.EvalSymlinks(t.TempDir())
	cfg := Config{StacksDir: filepath.Join(root, "compose"), Render: RenderConfig{DataDir: filepath.Join(root, "services")}}
	os.Mkdir(cfg.StacksDir, 0o755)
	os.Mkdir(cfg.Render.DataDir, 0o755)
	return cfg
}

func TestPreflight(t *testing.T) {
	ctx := context.Background()
	cfg := preflightConfig(t)
	res, err := Preflight(ctx, cfg, fakeVersioner{}, fakeSelf{})
	if err != nil || res.ComposeVersion != "v5" {
		t.Fatalf("res=%+v err=%v", res, err)
	}

	bad := map[string]func(c *Config){
		"nested data dir": func(c *Config) {
			c.Render.DataDir = filepath.Join(c.StacksDir, "data")
			os.Mkdir(c.Render.DataDir, 0o755)
		},
		"same dirs":    func(c *Config) { c.Render.DataDir = c.StacksDir },
		"missing dir":  func(c *Config) { c.Render.DataDir = filepath.Join(c.Render.DataDir, "nope") },
		"relative dir": func(c *Config) { c.StacksDir = "compose" },
	}
	for name, spoil := range bad {
		t.Run(name, func(t *testing.T) {
			c := preflightConfig(t)
			spoil(&c)
			if _, err := Preflight(ctx, c, fakeVersioner{}, fakeSelf{}); err == nil {
				t.Fatal("must fail")
			}
		})
	}
	if _, err := Preflight(ctx, cfg, fakeVersioner{err: errors.New("no compose")}, fakeSelf{}); err == nil {
		t.Fatal("compose missing must fail")
	}
	// In a container, both directories must be mounted at the same path.
	bind := func(p string) container.MountPoint {
		return container.MountPoint{Type: mount.TypeBind, Source: p, Destination: p}
	}
	if _, err := Preflight(ctx, cfg, fakeVersioner{}, fakeSelf{id: "self", mounts: []container.MountPoint{bind(cfg.StacksDir)}}); err == nil {
		t.Fatal("data directory not mounted must fail")
	}
	_, err = Preflight(ctx, cfg, fakeVersioner{}, fakeSelf{id: "self", mounts: []container.MountPoint{bind(cfg.StacksDir), bind(cfg.Render.DataDir)}})
	if os.Geteuid() != 0 && err == nil {
		t.Fatal("a non-root API container must be refused")
	}
}
