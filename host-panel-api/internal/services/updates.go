package services

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"time"

	distref "github.com/distribution/reference"

	"host-panel-api/internal/composekit"
	"host-panel-api/internal/releases"
)

// Updates of dedicated instances (decision log §19). A dedicated instance
// can move, in one operation, to:
//   - a newer image of its version: a new release of its floating tag (new
//     digest on the mirror) or a newer patch tag (exact-tag engines);
//   - a newer version among the UpdateTargets newest supported ones.
//
// Shared instances are never updated this way: they serve several sites and
// a version change is a migration of every database.

const (
	// checkCooldown throttles manual checks (each one queries the
	// registries).
	checkCooldown = 30 * time.Second
	// checkTimeout bounds one discovery.
	checkTimeout = 3 * time.Minute
	// firstCheckDelay leaves the API start before the first discovery.
	firstCheckDelay = time.Minute
)

// UpdateInfo tells what a dedicated instance can be updated to.
type UpdateInfo struct {
	// Image: a newer image of the same version is available (new release of
	// its tag, or newer patch tag); LatestImage is that image.
	Image       bool   `json:"image"`
	LatestImage string `json:"latestImage,omitempty"`
	// Versions are the newer versions the instance can move to, newest
	// first (with their end of support).
	Versions []Version `json:"versions,omitempty"`
}

// Available reports whether an update of any kind exists.
func (u *UpdateInfo) Available() bool { return u != nil && (u.Image || len(u.Versions) > 0) }

// updateInfo compares the instance with the catalog (nil for shared and
// modified instances: they cannot be updated by the panel).
func (m *Manager) updateInfo(inst *instance, rt runtime) *UpdateInfo {
	if inst.engine.Lifecycle() != Dedicated || inst.modified {
		return nil
	}
	info := &UpdateInfo{Versions: m.reg.UpdateVersions(inst.ref.Engine, inst.version.Version)}
	latest, digest, ok := m.reg.LatestImage(inst.ref.Engine, inst.version.Version)
	switch {
	case !ok:
	case latest != inst.version.Image:
		// Never an older patch tag (the catalog may lag behind).
		if !olderPatch(m.reg.releases[inst.ref.Engine], inst.version.Image, latest) {
			info.Image, info.LatestImage = true, latest
		}
	case digest == "":
	default:
		// A new release of the tag is an image that does not carry the
		// mirror digest (an image may carry several digests: the pinned
		// one need not be the mirror's). Without the running image, the
		// pinned digest stands for it.
		repo, _, _ := cutImageTag(inst.version.Image)
		carried := repoDigestsOf(repo, rt.repoDigests)
		if len(carried) == 0 && inst.version.Digest != "" {
			carried = []string{inst.version.Digest}
		}
		if len(carried) > 0 && !slices.Contains(carried, digest) {
			info.Image, info.LatestImage = true, latest
		}
	}
	return info
}

// CheckUpdates discovers the versions of every engine now (the "check for
// updates" action), unless a check started less than checkCooldown ago.
func (m *Manager) CheckUpdates(ctx context.Context) (CatalogStatus, error) {
	if m.cfg.Releases == nil {
		return m.reg.Status(), newError(CodeReleasesUnavailable, "", "version discovery is not configured")
	}
	m.checkMu.Lock()
	if !m.lastCheck.IsZero() && m.now().Sub(m.lastCheck) < checkCooldown {
		m.checkMu.Unlock()
		status := m.reg.Status()
		status.Throttled = true
		return status, nil
	}
	m.lastCheck = m.now()
	m.checkMu.Unlock()
	return m.refresh(ctx), nil
}

// refresh runs one discovery, detached from the request.
func (m *Manager) refresh(ctx context.Context) CatalogStatus {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), checkTimeout)
	defer cancel()
	inUse := map[string][]string{}
	if insts, err := m.loadAll(); err == nil {
		for _, inst := range insts {
			inUse[inst.ref.Engine] = append(inUse[inst.ref.Engine], inst.version.Version)
		}
	}
	status, errs := m.reg.Refresh(ctx, m.cfg.Releases, inUse)
	for _, err := range errs {
		m.logger.Warn("services: version discovery", "error", err)
	}
	m.logger.Info("services: versions discovered", "failed", strings.Join(status.Failed, ","))
	m.onChange()
	return status
}

// RunUpdateChecks discovers the versions every interval (none when interval
// is zero), until ctx ends.
func (m *Manager) RunUpdateChecks(ctx context.Context, interval time.Duration) {
	if m.cfg.Releases == nil || interval <= 0 {
		return
	}
	timer := time.NewTimer(firstCheckDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		m.checkMu.Lock()
		m.lastCheck = m.now()
		m.checkMu.Unlock()
		m.refresh(ctx)
		timer.Reset(interval)
	}
}

// RecordImages records, once, the image of the dedicated instances created
// before images were recorded (the built-in catalog image they were
// created with): the catalog can then move to newer patch tags without
// making them look edited by hand. Run at startup, before any discovery.
func (m *Manager) RecordImages(ctx context.Context) {
	insts, err := m.loadAll()
	if err != nil {
		return
	}
	for _, inst := range insts {
		if inst.engine.Lifecycle() != Dedicated || inst.modified {
			continue
		}
		if r, rerr := m.readReference(inst.ref); rerr != nil || r.Image != "" {
			continue
		}
		unlock, lerr := m.lock(ctx, inst.ref.Key())
		if lerr != nil {
			continue
		}
		if fresh, ferr := m.load(inst.ref.Key()); ferr == nil && !fresh.modified {
			if werr := m.writeReference(fresh.ref, fresh.reference()); werr != nil {
				m.logger.Error("services: recording the image of an instance", "instance", fresh.ref.Key(), "error", werr)
			}
		}
		unlock()
	}
}

// UpdateResult is the outcome of an update.
type UpdateResult struct {
	// Updated is false when the instance already ran the target image.
	Updated bool   `json:"updated"`
	Version string `json:"version"`
	Image   string `json:"image"`
}

// Update moves a dedicated instance to the latest image of its version
// (version "" or its own) or to a newer version offered by UpdateVersions.
//
// The digest of the target tag is read on the mirror now and the image is
// pulled by digest (content-addressed: what is run is what was checked).
// The instance is then pinned to it: its compose file runs
// "repo:tag@digest" and its reference records the digest. No local tag is
// moved: the tag of a version is shared by its instances, and each one
// keeps the digest it records until it is updated itself. On failure the
// files, and thus the previous pinned reference, are restored and the
// instance started on its previous image. Versions never go back: data
// written by a newer version may not be readable by an older one.
func (m *Manager) Update(ctx context.Context, key, version, actor string) (*UpdateResult, error) {
	if err := m.knownKey(key); err != nil {
		return nil, err
	}
	pre, lerr := m.load(key)
	if lerr != nil {
		return nil, lerr
	}
	if pre.engine.Lifecycle() != Dedicated {
		return nil, newError(CodeUpdateNotSupported, "", "shared instances are not updated from the panel: their version is fixed")
	}
	if pre.modified {
		return nil, errModifiedByHand()
	}
	if m.cfg.Releases == nil {
		return nil, newError(CodeReleasesUnavailable, "", "version discovery is not configured")
	}
	// The state is rendered again from the kept digest: checked before
	// anything is pulled.
	if _, derr := m.readDigest(pre.ref); errors.Is(derr, fs.ErrNotExist) {
		return nil, newError(CodePasswordResetRequired, "", "this instance predates configuration changes: reset its password once, then retry")
	}
	target, terr := m.updateTarget(pre, version)
	if terr != nil {
		return nil, terr
	}
	// Checked before the pull to fail fast, and again under the lock (the
	// options may have changed meanwhile).
	if verr := validateTargetOptions(pre.engine, target, pre.opts); verr != nil {
		return nil, verr
	}
	// Pull outside the lock, on its own deadline (it may take minutes); the
	// reference is the catalog's, never a client value.
	pctx, pcancel := context.WithTimeout(context.WithoutCancel(ctx), pullTimeout)
	pinned, pulledID, perr := m.pullByDigest(pctx, &target)
	pcancel()
	if perr != nil {
		m.logger.Error("services: pulling an update failed", "instance", key, "image", target.Image, "error", perr)
		return nil, newError(CodePullFailed, "", "the new image could not be pulled")
	}

	ctx, cancel := detach(ctx)
	defer cancel()
	unlock, err := m.lock(ctx, key)
	if err != nil {
		return nil, err
	}
	defer unlock()
	inst, lerr := m.load(key)
	if lerr != nil {
		return nil, lerr
	}
	if inst.modified {
		return nil, errModifiedByHand()
	}
	if inst.version.Version != pre.version.Version || inst.version.Reference() != pre.version.Reference() {
		return nil, newError(CodeInProgress, "", "the instance changed during the update, try again")
	}
	if verr := validateTargetOptions(inst.engine, target, inst.opts); verr != nil {
		return nil, verr
	}
	// Only a trusted container is recreated.
	rt, rerr := m.ready(ctx, inst)
	if rerr != nil {
		return nil, rerr
	}
	result := &UpdateResult{Version: target.Version, Image: target.Image}
	if target.Version == inst.version.Version && target.Image == inst.version.Image && pulledID == rt.imageID {
		// Already the latest image; one not pinned yet is pinned to it
		// (nothing to recreate: it runs that image). One pinned to another
		// digest of the same image is left as is: both name that image.
		if inst.version.Digest == "" {
			if perr := m.pinLocked(inst, target.Digest); perr != nil {
				return nil, m.storageError(perr)
			}
		}
		return result, nil
	}
	before := inst.version
	aerr := m.applyConfig(ctx, inst, target, inst.opts, applyHooks{
		rolledBack: func(reason string) {
			m.audit.Warn("service instance update rolled back", "user", actor, "instance", key,
				"version_before", before.Version, "version_after", target.Version,
				"image_before", before.Reference(), "image_after", target.Reference(), "reason", reason)
		},
		verify: func(vctx context.Context, fresh *instance) error {
			rt, rerr := m.ready(vctx, fresh)
			if rerr != nil {
				return rerr
			}
			if rt.imageID != pulledID {
				return errors.New("the container does not run the pulled image")
			}
			return nil
		},
		what: "image",
	})
	if aerr != nil {
		return nil, aerr
	}
	m.audit.Info("service instance updated", "user", actor, "instance", key,
		"version_before", before.Version, "version_after", target.Version,
		"image_before", before.Reference(), "image_after", target.Reference(),
		"image_id_before", rt.imageID, "image_id_after", pulledID, "digest", pinned)
	m.onChange()
	result.Updated = true
	return result, nil
}

// validateTargetOptions checks the options against the target version
// (engines with version-dependent options).
func validateTargetOptions(e Engine, target Version, opts Options) error {
	if ov, ok := e.(OptionValidator); ok {
		if verr := ov.ValidateOptions(target, opts); verr != nil {
			return verr
		}
	}
	return nil
}

// pullTimeout bounds the pull of an update (outside opTimeout).
const pullTimeout = 4 * time.Minute

// pullByDigest reads the digest of an image on the mirror and pulls it by
// digest; v.Digest is set to it. It returns the pulled reference
// ("repo@sha256:...") and the ID of the pulled image.
func (m *Manager) pullByDigest(ctx context.Context, v *Version) (string, string, error) {
	digest, err := m.cfg.Releases.Digest(ctx, v.Image)
	if err != nil {
		return "", "", err
	}
	if !releases.ValidDigest(digest) {
		return "", "", errors.New("invalid digest")
	}
	repo, _, ok := cutImageTag(v.Image)
	if !ok {
		return "", "", errors.New("image without tag")
	}
	pinned := repo + "@" + digest
	if err := m.docker.PullImage(ctx, pinned, v.Platform); err != nil {
		return "", "", err
	}
	img, err := m.docker.InspectImage(ctx, pinned)
	if err != nil {
		return "", "", err
	}
	if !slices.Contains(repoDigestsOf(repo, img.RepoDigests), digest) {
		return "", "", errors.New("the pulled image does not carry the expected digest")
	}
	v.Digest = digest
	return pinned, img.ID, nil
}

// repoDigestsOf returns the digests Docker recorded for an image in a
// repository (RepoDigests entries "repo@sha256:..." of that repository
// only, compared in their normalized form), sorted.
func repoDigestsOf(repo string, repoDigests []string) []string {
	want, err := distref.ParseNormalizedNamed(repo)
	if err != nil {
		return nil
	}
	var out []string
	for _, rd := range repoDigests {
		named, perr := distref.ParseNormalizedNamed(rd)
		if perr != nil {
			continue
		}
		c, ok := named.(distref.Canonical)
		if !ok || named.Name() != want.Name() {
			continue
		}
		if _, tagged := named.(distref.Tagged); tagged {
			continue
		}
		if d := c.Digest().String(); releases.ValidDigest(d) {
			out = append(out, d)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// localDigest returns the digest of the local image of a tag, as Docker
// recorded it when the image was pulled ("" when it has none for the
// repository of the tag, e.g. an image built or loaded locally). The image
// is pulled when absent, as compose would.
func (m *Manager) localDigest(ctx context.Context, engine string, v Version) (string, error) {
	repo, _, ok := cutImageTag(v.Image)
	if !ok {
		return "", errors.New("image without tag")
	}
	img, err := m.docker.InspectImage(ctx, v.Image)
	if err != nil {
		if perr := m.docker.PullImage(ctx, v.Image, v.Platform); perr != nil {
			return "", perr
		}
		if img, err = m.docker.InspectImage(ctx, v.Image); err != nil {
			return "", err
		}
	}
	if ds := repoDigestsOf(repo, img.RepoDigests); len(ds) > 0 {
		return m.pickDigest(engine, v, ds), nil
	}
	return "", nil
}

// PinImages pins, once, the image of the dedicated instances created before
// images were pinned by digest: the digest is the one of the image their
// verified container runs, for the repository of their tag (never a digest
// read from a file of the stacks directory). The reference is written
// first, then compose.yaml; the container is not recreated (it already runs
// that image). An instance whose digest cannot be derived (container
// missing or untrusted, image without a digest for its repository) keeps
// its tag-only rendering, and is pinned by its next update. It also
// finishes an interrupted pinning (reference pinned, compose.yaml not
// rewritten yet). Run at startup, after RecordImages.
func (m *Manager) PinImages(ctx context.Context) {
	insts, err := m.loadAll()
	if err != nil {
		return
	}
	for _, inst := range insts {
		if !needsPinning(inst) {
			continue
		}
		unlock, lerr := m.lock(ctx, inst.ref.Key())
		if lerr != nil {
			continue
		}
		m.pinInstance(ctx, inst.ref.Key())
		unlock()
	}
}

// needsPinning: a dedicated instance without a digest, or one whose only
// difference with the panel's files is a compose file without its digest.
func needsPinning(inst *instance) bool {
	if inst.engine.Lifecycle() != Dedicated {
		return false
	}
	if inst.modified {
		return inst.unpinnedCompose
	}
	return inst.version.Digest == ""
}

// pinInstance pins one instance (instance lock held). The digest is always
// one the verified container's image carries: the recorded one when it
// does, otherwise (no digest yet, or a recorded one the container does not
// run: an update interrupted between its reference and compose writes) the
// running image's, the container being verified against the tag-only
// rendering it was created from.
func (m *Manager) pinInstance(ctx context.Context, key string) {
	fresh, ferr := m.load(key)
	if ferr != nil || !needsPinning(fresh) {
		return
	}
	all, lerr := m.docker.ListContainers(ctx)
	if lerr != nil {
		m.logger.Warn("services: image not pinned by digest: containers cannot be listed", "instance", key, "error", lerr)
		return
	}
	recorded := fresh.version.Digest
	digest := ""
	if recorded != "" {
		if rt := m.inspect(ctx, fresh, all); rt.containerID != "" && rt.problem == "" {
			digest = recorded // verifyImage: the image carries it
		}
	}
	if digest == "" {
		bare := *fresh
		bare.version.Digest = ""
		rt := m.inspect(ctx, &bare, all)
		repo, _, _ := cutImageTag(fresh.version.Image)
		ds := repoDigestsOf(repo, rt.repoDigests)
		switch {
		case rt.containerID == "" || rt.problem != "":
			m.logger.Warn("services: image not pinned by digest: no trusted container, or its published port not applied yet", "instance", key, "problem", rt.problem)
			return
		case len(ds) == 0:
			m.logger.Warn("services: image not pinned by digest: the running image has no digest for its repository (kept on its tag)", "instance", key, "image", fresh.version.Image)
			return
		}
		digest = m.pickDigest(fresh.ref.Engine, fresh.version, ds)
		if recorded != "" {
			m.logger.Warn("services: the recorded digest is not the running image's (interrupted update): pinned to the running image",
				"instance", key, "recorded", recorded, "digest", digest)
		}
	}
	if err := m.pinLocked(fresh, digest); err != nil {
		m.logger.Error("services: pinning the image of an instance", "instance", key, "error", err)
		return
	}
	m.logger.Info("services: image pinned by digest", "instance", key, "image", fresh.version.Image, "digest", digest)
}

// pickDigest chooses among the digests an image carries for its
// repository: the mirror's for the tag when the catalog knows it and the
// image carries it, otherwise the smallest (each one names that image).
func (m *Manager) pickDigest(engine string, v Version, ds []string) string {
	if latest, d, ok := m.reg.LatestImage(engine, v.Version); ok && latest == v.Image && slices.Contains(ds, d) {
		return d
	}
	return ds[0]
}

// writePinnedCompose writes the compose file of an instance being pinned;
// tests replace it to simulate a write failure (file permissions do not
// stop root, which runs the CI jobs).
var writePinnedCompose = composekit.WriteFileAtomic

// pinLocked records the digest of a trusted instance and renders its
// compose file with it (instance lock held). The reference is the commit
// point; when the compose file cannot be written, the previous reference
// is written back and the error returned. Interrupted in between (crash),
// the instance is modified until PinImages finishes it
// (instance.unpinnedCompose).
func (m *Manager) pinLocked(inst *instance, digest string) error {
	if !releases.ValidDigest(digest) {
		return errors.New("invalid digest")
	}
	v := inst.version
	v.Digest = digest
	compose, err := Render(inst.engine, inst.ref, v, inst.opts, inst.networks, m.cfg.Render)
	if err != nil {
		return err
	}
	wrote := inst.version.Digest != digest
	if wrote {
		r := inst.reference()
		r.Digest = digest
		if err := m.writeReference(inst.ref, r); err != nil {
			return err
		}
	}
	if err := writePinnedCompose(inst.dir, ComposeFile, compose, filePerm); err != nil {
		// Give the previous reference back: a pinned reference with the
		// compose file without its digest would make the instance modified.
		if wrote {
			if rerr := m.writeReference(inst.ref, inst.reference()); rerr != nil {
				return errors.Join(err, rerr)
			}
		}
		return err
	}
	if fresh, lerr := m.load(inst.ref.Key()); lerr != nil || fresh.modified || fresh.unpinnedCompose {
		return errors.New("the instance files changed while its image was pinned")
	}
	return nil
}

// cutImageTag splits "host/repo:tag".
func cutImageTag(image string) (string, string, bool) {
	i := strings.LastIndex(image, ":")
	if i < 0 || strings.Contains(image[i:], "/") {
		return "", "", false
	}
	return image[:i], image[i+1:], image[i+1:] != ""
}

// olderPatch reports whether candidate is an older patch tag of the same
// version than current (exact-tag engines: the catalog may lag behind the
// image an instance runs).
func olderPatch(rel Releases, current, candidate string) bool {
	_, ct, ok1 := cutImageTag(current)
	_, nt, ok2 := cutImageTag(candidate)
	if !ok1 || !ok2 {
		return false
	}
	cv, cp, ok1 := parseTag(rel, ct)
	nv, np, ok2 := parseTag(rel, nt)
	return ok1 && ok2 && cv == nv && cp >= 0 && np >= 0 && np < cp
}

// updateTarget resolves the version an instance is updated to.
func (m *Manager) updateTarget(inst *instance, version string) (Version, *Error) {
	id, current := inst.ref.Engine, inst.version.Version
	if version == "" || version == current {
		if v, ok := m.reg.Version(id, current); ok && !olderPatch(m.reg.releases[id], inst.version.Image, v.Image) {
			return v, nil
		}
		return inst.version, nil // no newer tag known: the same one (new digest)
	}
	if !versionRe.MatchString(version) {
		return Version{}, newError(CodeInvalidVersion, "version", "invalid version")
	}
	if CompareVersions(version, current) < 0 {
		return Version{}, newError(CodeVersionDowngrade, "version", "an instance cannot go back to an older version")
	}
	for _, v := range m.reg.UpdateVersions(id, current) {
		if v.Version == version {
			return v, nil
		}
	}
	return Version{}, newError(CodeInvalidVersion, "version", "this version is not offered as an update")
}
