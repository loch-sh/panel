package functions

import (
	"context"
	"slices"
	"time"

	"github.com/moby/moby/api/types/image"

	"host-panel-api/internal/registry"
	"host-panel-api/internal/releases"
)

// Runtime images are pinned at creation: the digest of the
// architecture-suffixed line tag ("22-arm64") is read on the mirror the
// image is pulled from, the image pulled by that digest and its local ID
// recorded in the reference. mirror.gcr.io answers "not found" or 429 on
// the first requests of a tag (decision log §20, phase 0): both steps are
// retried. Moving to a newer image (dated tags, discovery) is the runtime
// update of a later phase.

// RuntimeVersions are the runtime versions offered at creation, newest
// first (the AWS Lambda base images with live, architecture-suffixed
// builds). Their discovery comes with the runtime updates.
var RuntimeVersions = map[string][]string{
	"nodejs": {"26", "24", "22"},
	"python": {"3.14", "3.13", "3.12"},
	// OS-only image for compiled code (a "bootstrap" executable); al2 is
	// out of support.
	"provided": {"al2023"},
}

const (
	imageAttempts = 5
	pullTimeout   = 4 * time.Minute
)

// retryDelay is the wait before the attempt i+1 (2, 4, 8, 16 s).
func retryDelay(i int) time.Duration { return time.Duration(2<<i) * time.Second }

// resolveImage reads the digest of a runtime image on the mirror.
func (m *Manager) resolveImage(ctx context.Context, rt Runtime, version string) (tag, digest string, err error) {
	if m.digests == nil {
		return "", "", newError(CodeImagePullFailed, "", "the image registry is not configured")
	}
	tag = LineTag(version, m.cfg.Arch)
	ref := registry.Hub(rt.Repository + ":" + tag)
	for i := range imageAttempts {
		d, derr := m.digests.Digest(ctx, ref)
		if derr == nil && releases.ValidDigest(d) {
			return tag, d, nil
		}
		m.logger.Warn("functions: reading an image digest failed", "image", ref, "attempt", i+1, "error", derr)
		if i == imageAttempts-1 || m.sleep(ctx, retryDelay(i)) != nil {
			break
		}
	}
	return "", "", newError(CodeImagePullFailed, "", "the digest of %s could not be read on the mirror", ref)
}

// pullImage pulls a pinned image by digest (with retries) and returns its
// local ID, checked against the digest and the architecture.
func (m *Manager) pullImage(ctx context.Context, rt Runtime, tag, digest string) (string, error) {
	ref := rt.ImageRef(tag, digest)
	for i := range imageAttempts {
		pctx, cancel := context.WithTimeout(ctx, pullTimeout)
		err := m.docker.PullImage(pctx, ref, "linux/"+m.cfg.Arch)
		cancel()
		if err == nil {
			break
		}
		m.logger.Warn("functions: pulling an image failed", "image", ref, "attempt", i+1, "error", err)
		if i == imageAttempts-1 || m.sleep(ctx, retryDelay(i)) != nil {
			return "", newError(CodeImagePullFailed, "", "the image %s could not be pulled", rt.Repository+":"+tag)
		}
	}
	img, err := m.docker.InspectImage(ctx, rt.ImageRepository()+"@"+digest)
	if err != nil {
		return "", m.dockerError("inspecting a pulled image", err)
	}
	if perr := checkPinnedImage(img, rt, digest, m.cfg.Arch, ""); perr != nil {
		return "", perr
	}
	return img.ID, nil
}

// checkPinnedImage verifies a local image: pulled by its digest, of the
// architecture, with the recorded ID (when known).
func checkPinnedImage(img image.InspectResponse, rt Runtime, digest, arch, id string) *Error {
	if !releases.ValidDigest(img.ID) || !slices.Contains(img.RepoDigests, rt.ImageRepository()+"@"+digest) {
		return newError(CodeVerificationFailed, "", "the runtime image was not pulled by its pinned digest")
	}
	if id != "" && img.ID != id {
		return newError(CodeVerificationFailed, "", "the runtime image is not the one recorded")
	}
	if CheckImageArch(img, arch) != nil {
		return newError(CodeVerificationFailed, "", "the runtime image is not of the host architecture")
	}
	return nil
}

// ensureImage makes the pinned image of a reference present (pulled again
// by digest if it was removed) and checks it.
func (m *Manager) ensureImage(ctx context.Context, ref *Reference) error {
	rt := ref.runtime()
	img, err := m.docker.InspectImage(ctx, rt.ImageRepository()+"@"+ref.ImageDigest)
	if err != nil {
		if _, perr := m.pullImage(ctx, rt, ref.ImageTag, ref.ImageDigest); perr != nil {
			return perr
		}
		if img, err = m.docker.InspectImage(ctx, rt.ImageRepository()+"@"+ref.ImageDigest); err != nil {
			return m.dockerError("inspecting an image", err)
		}
	}
	if perr := checkPinnedImage(img, rt, ref.ImageDigest, ref.Arch, ref.ImageID); perr != nil {
		n, _ := ref.FunctionName()
		return m.refused(n, "", "image", perr.Message, perr.Code, perr.Message)
	}
	return nil
}
