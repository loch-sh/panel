package functions

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/moby/moby/api/types/network"

	"host-panel-api/internal/composekit"
)

// change is what an operation writes besides the reference.
type change struct {
	secrets     []byte // new secrets env_file, nil: unchanged
	credentials []byte // new credentials env_file, nil: unchanged
	// cut are networks removed from the function: its container is
	// disconnected from them before it is recreated, and never reconnected
	// if the recreation fails (it is stopped instead).
	cut []string
	// recreate forces the recreation of the container (credentials,
	// secrets, restart).
	recreate bool
}

// fileBackup is the previous content of a file rewritten by apply.
type fileBackup struct {
	dir, name string
	data      []byte
	existed   bool
	perm      os.FileMode
}

func backupFile(dir, name string, perm os.FileMode) (fileBackup, error) {
	data, err := composekit.ReadFileNoFollow(filepath.Join(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return fileBackup{dir: dir, name: name, perm: perm}, nil
	}
	if err != nil {
		return fileBackup{}, err
	}
	return fileBackup{dir: dir, name: name, data: data, existed: true, perm: perm}, nil
}

func (b fileBackup) restore() error {
	if !b.existed {
		err := os.Remove(filepath.Join(b.dir, b.name))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return composekit.WriteFileAtomic(b.dir, b.name, b.data, b.perm)
}

// apply is the single choke point of every compose run. It writes the
// next reference (and the env_files of the change) and, for a deployed
// function that is not stopped, runs compose up with the rendered file —
// after re-verifying the runtime binary and the deployment tree
// (CheckArtifacts) and refusing a function whose compose project was
// edited outside the panel (ComposeIntact on the previous reference). The
// container is then verified against the reference. On any failure every
// file is restored and the previous configuration started again (or the
// function stopped when networks were cut). Called with the function lock
// held.
func (m *Manager) apply(ctx context.Context, prev, next *Reference, ch change, op, actor string) error {
	n, nerr := next.FunctionName()
	if nerr != nil {
		return nerr
	}
	if prev.Name != next.Name {
		return newError(CodeInvalidReference, "", "function reference: name mismatch")
	}
	if m.modified(prev) {
		return m.refused(n, actor, op, "compose project modified outside the panel", CodeModified,
			"the function was modified outside the panel: it is never run by the panel (purge it)")
	}
	next.Sort()
	if err := next.Validate(m.cfg.Config); err != nil {
		return err
	}
	deployed := next.Deployment != nil
	run := deployed && !next.Stopped
	rc := m.renderConfig(next)
	var compose []byte
	if deployed {
		if run {
			runtimePath, err := CheckArtifacts(next, m.paths, m.runtimes)
			if CodeOf(err) == CodeRuntimeUnavailable {
				// Missing, being installed again: not a tampering.
				m.logger.Warn("functions: runtime binary not available", "function", n.String(), "error", err)
				return err
			}
			if err != nil {
				m.logger.Error("functions: artifacts refused", "function", n.String(), "error", err)
				code := CodeOf(err)
				if code == "" {
					code = CodeArtifactModified
				}
				return m.refused(n, actor, op, "artifacts not verified", code,
					"the runtime binary or the deployment does not match the reference: the function is not started")
			}
			rc.RuntimePath = runtimePath
			if err := m.ensureImage(ctx, next); err != nil {
				return err
			}
		}
		var err error
		if compose, err = Render(next, rc); err != nil {
			return m.storageError("rendering a compose file", err)
		}
	}

	// Back up every file the operation rewrites.
	private := m.paths.Private(n)
	projectDir := m.paths.ProjectDir(n)
	var backups []fileBackup
	backup := func(dir, name string, perm os.FileMode) error {
		b, err := backupFile(dir, name, perm)
		if err == nil {
			backups = append(backups, b)
		}
		return err
	}
	if err := backup(private, ReferenceFile, referencePerm); err != nil {
		return m.storageError("backing up a reference", err)
	}
	if ch.secrets != nil {
		if err := backup(private, SecretsEnvFile, secretPerm); err != nil {
			return m.storageError("backing up secrets", err)
		}
	}
	if ch.credentials != nil {
		if err := backup(private, CredentialsEnvFile, secretPerm); err != nil {
			return m.storageError("backing up credentials", err)
		}
	}
	_, statErr := os.Lstat(projectDir)
	projectExisted := statErr == nil
	if deployed && projectExisted {
		if err := backup(projectDir, ComposeFile, stackFilePerm); err != nil {
			return m.storageError("backing up a compose file", err)
		}
	}
	restoreFiles := func() bool {
		ok := true
		for i := len(backups) - 1; i >= 0; i-- {
			if err := backups[i].restore(); err != nil {
				ok = false
				m.logger.Error("functions: restoring a file failed", "function", n.String(), "file", backups[i].name, "error", err)
			}
		}
		if deployed && !projectExisted {
			if err := os.RemoveAll(projectDir); err != nil {
				ok = false
				m.logger.Error("functions: removing a new compose project failed", "function", n.String(), "error", err)
			}
		}
		return ok
	}

	// Write.
	write := func() error {
		if ch.secrets != nil {
			if err := m.paths.writePrivateFile(n, SecretsEnvFile, ch.secrets, secretPerm); err != nil {
				return err
			}
		}
		if ch.credentials != nil {
			if err := m.paths.writePrivateFile(n, CredentialsEnvFile, ch.credentials, secretPerm); err != nil {
				return err
			}
		}
		if err := m.paths.WriteReference(*next, m.cfg.Config); err != nil {
			return err
		}
		if !deployed {
			return nil
		}
		if _, err := composekit.EnsureDir(projectDir, stackDirPerm); err != nil {
			return err
		}
		if !composekit.IsRegularFile(filepath.Join(projectDir, composekit.ManagedMarker)) {
			if err := composekit.WriteFileAtomic(projectDir, composekit.ManagedMarker, []byte(managedMarkerContent), stackFilePerm); err != nil {
				return err
			}
		}
		return composekit.WriteFileAtomic(projectDir, ComposeFile, compose, stackFilePerm)
	}
	if err := write(); err != nil {
		restoreFiles()
		return m.storageError("writing the function files", err)
	}
	if !run {
		if prev.Deployment != nil && !prev.Stopped && next.Stopped {
			m.stopContainers(ctx, n)
		}
		m.onChange()
		return nil
	}
	// The files must still be the rendered ones (nothing raced the write).
	if !ComposeIntact(next, rc) {
		restoreFiles()
		return newError(CodeModified, "", "the function files changed during the operation")
	}
	defer m.onChange()
	if len(ch.cut) > 0 {
		m.disconnect(ctx, n, ch.cut)
	}
	err := m.up(ctx, n, compose, ch.recreate)
	if err == nil {
		if _, problem := m.verifyRunning(ctx, next, rc); problem != "" {
			m.refused(n, actor, op, problem, CodeVerificationFailed, "")
			err = newError(CodeVerificationFailed, "", "the container does not match the function reference")
		}
	}
	if err == nil {
		if prev.Egress && !next.Egress {
			if rerr := m.docker.RemoveNetwork(ctx, n.Project()+"_"+egressNetworkKey); rerr != nil {
				m.logger.Warn("functions: removing the egress network", "function", n.String(), "error", rerr)
			}
		}
		return nil
	}
	return m.rollback(ctx, prev, n, ch, restoreFiles, err)
}

// rollback restores the previous files after a failed up, then starts the
// previous configuration again (or stops the function when networks were
// cut, or when it had never run).
func (m *Manager) rollback(ctx context.Context, prev *Reference, n Name, ch change, restoreFiles func() bool, cause error) error {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()
	m.logger.Error("functions: the function did not start with its new configuration", "function", n.String(), "error", cause)
	if !restoreFiles() {
		m.stopContainers(rctx, n)
		return newError(CodeOutcomeUnknown, "", "the previous files could not be restored: the function was stopped, check it")
	}
	reason := newError(CodeStartFailed, "", "the function did not start with its new configuration; the previous one is kept")
	if fe, ok := cause.(*Error); ok && fe.Code == CodeVerificationFailed {
		reason = fe
	}
	switch {
	case prev.Deployment == nil:
		// Never ran: nothing to start again.
		m.removeContainers(rctx, n)
		return reason
	case len(ch.cut) > 0:
		m.stopContainers(rctx, n)
		return newError(CodeStartFailed, "", "the function did not restart without the removed networks: it was stopped")
	case prev.Stopped:
		m.stopContainers(rctx, n)
		return reason
	}
	prc := m.renderConfig(prev)
	runtimePath, aerr := CheckArtifacts(prev, m.paths, m.runtimes)
	if aerr == nil {
		prc.RuntimePath = runtimePath
	}
	prevCompose, rerr := Render(prev, prc)
	if aerr != nil || rerr != nil || !ComposeIntact(prev, prc) {
		m.stopContainers(rctx, n)
		return newError(CodeOutcomeUnknown, "", "the previous configuration could not be verified: the function was stopped")
	}
	if err := m.up(rctx, n, prevCompose, true); err != nil {
		m.logger.Error("functions: restarting the previous configuration failed", "function", n.String(), "error", err)
		return newError(CodeOutcomeUnknown, "", "the function did not restart with its previous configuration; check its state")
	}
	return reason
}

// up runs compose up for the project of a function with the compose
// bytes the caller verified, given on stdin (the file on disk, writable by
// CI, could change between the check and the run).
func (m *Manager) up(ctx context.Context, n Name, compose []byte, recreate bool) error {
	args := slices.Clone(upArgs)
	if recreate {
		args = append(args, "--force-recreate")
	}
	out, err := m.compose(ctx, m.paths.ProjectDir(n), compose, args...)
	if err != nil {
		// The output may hold host paths: logs only.
		m.logger.Error("functions: compose up failed", "function", n.String(), "error", err, "output", truncate(out, 4096))
		return newError(CodeStartFailed, "", "the function did not start")
	}
	return nil
}

// Problems of verifyRunning that are states rather than mismatches.
const (
	problemNotFound   = "container not found"
	problemNotRunning = "container not running"
)

// verifyRunning inspects the container of a function and compares it with
// the reference. It returns the ID of the verified container (the only one
// to use afterwards: a name can be taken by another container meanwhile)
// and "" when it matches. The compose project must have exactly this one
// container: another one started by hand under the project would share
// its networks and secrets.
func (m *Manager) verifyRunning(ctx context.Context, ref *Reference, rc RenderConfig) (string, string) {
	n, _ := ref.FunctionName()
	info, err := m.docker.InspectContainerRaw(ctx, n.Project())
	if err != nil {
		return "", problemNotFound
	}
	if info.State == nil || !info.State.Running {
		return "", problemNotRunning
	}
	img, err := m.docker.InspectImage(ctx, info.Image)
	if err != nil {
		return "", "image not found"
	}
	var egress *network.Inspect
	if ref.Egress {
		e, err := m.docker.InspectNetwork(ctx, n.Project()+"_"+egressNetworkKey)
		if err != nil {
			return "", "egress network not found"
		}
		egress = &e
	}
	if problem := VerifyContainer(ref, info, img, rc, egress); problem != "" {
		return "", problem
	}
	ids, err := m.projectContainers(ctx, n)
	if err != nil {
		return "", "containers cannot be listed"
	}
	if len(ids) != 1 || ids[0] != info.ID {
		return "", "other containers run in the function's project"
	}
	return info.ID, ""
}

// projectContainers returns the IDs of the containers of a function: its
// compose project, its working directory or its name (labels can be
// forged, so every match is removed or stopped, never trusted).
func (m *Manager) projectContainers(ctx context.Context, n Name) ([]string, error) {
	all, err := m.docker.ListContainers(ctx)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, c := range all {
		match := c.Labels[composeProjectLabel] == n.Project() || c.Labels[composeWorkingDirLabel] == m.paths.ProjectDir(n)
		for _, name := range c.Names {
			if name == "/"+n.Project() {
				match = true
			}
		}
		if match {
			ids = append(ids, c.ID)
		}
	}
	return ids, nil
}

// stopContainers stops the containers of a function through the Docker API
// (never compose: the file may have been edited).
func (m *Manager) stopContainers(ctx context.Context, n Name) error {
	ids, err := m.projectContainers(ctx, n)
	if err != nil {
		m.logger.Error("functions: listing containers", "function", n.String(), "error", err)
		return err
	}
	var errs []error
	for _, id := range ids {
		if err := m.docker.StopContainer(ctx, id); err != nil {
			m.logger.Error("functions: stopping a container", "function", n.String(), "container", id, "error", err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// removeContainers removes the containers of a function (Docker API).
func (m *Manager) removeContainers(ctx context.Context, n Name) (int, error) {
	ids, err := m.projectContainers(ctx, n)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, id := range ids {
		if err := m.docker.RemoveContainer(ctx, id); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// disconnect cuts the function container from networks removed from it.
func (m *Manager) disconnect(ctx context.Context, n Name, names []string) {
	info, err := m.docker.InspectContainerRaw(ctx, n.Project())
	if err != nil {
		return
	}
	for _, name := range names {
		if ref, ok := m.nets.Lookup(name); ok {
			if err := m.docker.DisconnectNetwork(ctx, ref.ID, info.ID); err != nil {
				m.logger.Warn("functions: disconnecting a removed network", "function", n.String(), "network", name, "error", err)
			}
		}
	}
}
