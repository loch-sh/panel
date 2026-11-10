//go:build integration

package functions

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"testing"
	"time"

	"host-panel-api/internal/docker"
	"host-panel-api/internal/releases"
)

// Image pinning against mirror.gcr.io and a real daemon: digest of the
// architecture-suffixed line tag, pull by digest (with retries), local
// image checked. Needs Docker and network access; the runtime binary is
// not needed.
// The end-to-end tests (deploy, invoke, rollback…) are in
// e2e_integration_test.go.
func TestPinRuntimeImageIntegration(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker not available")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dc, err := docker.NewClient(logger)
	if err != nil {
		t.Fatal(err)
	}
	defer dc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	arch, err := dc.HostArch(ctx)
	if err != nil || !ValidArch(arch) {
		t.Skipf("host architecture %q: %v", arch, err)
	}
	m := NewManager(ManagerConfig{Config: Config{DataDir: t.TempDir(), StacksDir: t.TempDir()}, Arch: arch},
		Deps{Docker: dc, Digests: releases.New(), Logger: logger, Audit: logger})
	rt, _ := LookupRuntime("nodejs")
	tag, digest, err := m.resolveImage(ctx, rt, "22")
	if err != nil {
		t.Fatal(err)
	}
	if tag != LineTag("22", arch) || !releases.ValidDigest(digest) {
		t.Fatalf("%s %s", tag, digest)
	}
	id, err := m.pullImage(ctx, rt, tag, digest)
	if err != nil || !releases.ValidDigest(id) {
		t.Fatalf("pull: %s %v", id, err)
	}
	img, err := dc.InspectImage(ctx, rt.ImageRepository()+"@"+digest)
	if err != nil {
		t.Fatal(err)
	}
	if perr := checkPinnedImage(img, rt, digest, arch, id); perr != nil {
		t.Fatal(perr)
	}
	if img.Config == nil || len(img.Config.Entrypoint) != 1 || img.Config.Entrypoint[0] != rt.Entrypoint[0] {
		t.Fatalf("entrypoint %v", img.Config)
	}
}
