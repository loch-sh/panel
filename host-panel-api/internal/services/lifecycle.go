package services

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"host-panel-api/internal/composekit"
)

// rollbackTimeout bounds the clean-up of a failed operation, which runs on
// its own deadline (the operation may have failed by timing out).
const rollbackTimeout = 2 * time.Minute

// newInstance is what createInstance writes. opts and password are set for
// dedicated instances (the password goes, as digests, to the state files).
type newInstance struct {
	e        Engine
	v        Version
	ref      InstanceRef
	opts     Options
	password string
	// networks the instance joins (registry names, at least one).
	networks []string
	// digest of the password and state rendered from it, once (bcrypt is
	// salted): written, then compared by filesIntact.
	digest string
	state  []GeneratedFile
}

// reference is what the panel records about the new instance.
func (n newInstance) reference() reference {
	r := reference{Engine: n.ref.Engine, Version: n.ref.Version, Name: n.ref.Name, Options: n.opts,
		Networks: slices.Sorted(slices.Values(n.networks))}
	if n.e.Lifecycle() == Dedicated {
		r.Image, r.Digest = n.v.Image, n.v.Digest
	}
	return r
}

// createInstance writes and starts a new instance. On failure it removes what
// it created, unless the project may still be running (compose down failed):
// the files are then kept for an operator. It must be called with the
// instance lock held.
func (m *Manager) createInstance(ctx context.Context, n newInstance, actor string) (*instance, error) {
	e, v, ref := n.e, n.v, n.ref
	if e.Service(ref, v, n.opts).State {
		d := e.(DedicatedEngine)
		digest, err := d.PasswordDigest(n.password)
		if err != nil {
			return nil, m.storageError(err)
		}
		files, err := d.StateFiles(ref, v, n.opts, digest)
		if err != nil {
			return nil, m.storageError(err)
		}
		n.digest, n.state = digest, files
	}
	if cerr := m.checkNameFree(ctx, ref); cerr != nil {
		return nil, cerr
	}
	if e.Lifecycle() == Dedicated {
		// Pinned by digest: the image of the tag as Docker pulled it (the
		// one compose would run), so that no later move of the tag, shared
		// by the instances of the version, changes what this one runs.
		digest, derr := m.localDigest(ctx, e.ID(), v)
		if derr != nil {
			m.logger.Error("services: pulling the image of a new instance failed", "instance", ref.Key(), "image", v.Image, "error", derr)
			return nil, newError(CodePullFailed, "", "the image could not be pulled")
		}
		if digest == "" {
			m.logger.Warn("services: the image of a new instance has no digest for its repository: run by its tag", "instance", ref.Key(), "image", v.Image)
		}
		v.Digest = digest
		n.v = v
	}
	dir := m.instanceDir(ref)
	// os.Mkdir fails if the directory appeared meanwhile (e.g. created by
	// CI): it is the actual reservation of the name.
	if err := os.Mkdir(dir, dirPerm); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, newError(CodeDirectoryConflict, "", "a directory with the instance name already exists in the stacks directory")
		}
		return nil, m.storageError(err)
	}
	committed, started, dataCreated := false, false, false
	defer func() {
		if !committed {
			m.rollbackInstance(ref, dir, started, dataCreated)
		}
	}()

	if err := composekit.WriteFileAtomic(dir, composekit.ManagedMarker, []byte(managedMarkerContent), filePerm); err != nil {
		return nil, m.storageError(err)
	}
	var derr error
	if dataCreated, derr = m.prepareDataDir(e, ref); derr != nil {
		return nil, derr
	}
	compose, err := m.writeInstanceFiles(n, dir)
	if err != nil {
		return nil, err
	}
	exposure, err := m.exposureFiles(e, ref, v, n.opts, dir, nil)
	if err != nil {
		return nil, m.storageError(err)
	}
	if err := m.writeExposureFiles(e, dir, exposure); err != nil {
		return nil, m.storageError(err)
	}
	// The stacks directory is writable by CI: check again, right before
	// running compose, that the files are still the ones just written.
	if !m.filesIntact(n, dir, compose) || !exposureUpToDate(dir, exposure) {
		m.logger.Error("services: instance files changed before start", "instance", ref.Key())
		return nil, newError(CodeDirectoryConflict, "", "the instance files changed while it was being created")
	}
	started = true
	if out, err := m.compose(ctx, dir, upArgs...); err != nil {
		// The compose output holds no secret (the admin password is a file,
		// never an environment variable).
		m.logger.Error("services: instance failed to start", "instance", ref.Key(), "error", err, "output", out)
		return nil, newError(CodeStartFailed, "", "the instance failed to start")
	}
	inst, lerr := m.load(ref.Key())
	if lerr != nil {
		return nil, lerr
	}
	committed = true
	m.audit.Info("service instance created", "user", actor, "instance", ref.Key(), "image", v.Reference())
	return inst, nil
}

// filesIntact re-reads the compose and generated files of a new instance.
func (m *Manager) filesIntact(n newInstance, dir string, compose []byte) bool {
	if composekit.RealDir(dir) != nil {
		return false
	}
	got, err := composekit.ReadFileNoFollow(filepath.Join(dir, ComposeFile))
	if err != nil || !bytes.Equal(got, compose) {
		return false
	}
	spec := n.e.Service(n.ref, n.v, n.opts)
	for _, f := range spec.Files {
		got, err := composekit.ReadFileNoFollow(filepath.Join(dir, f.Name))
		if err != nil || !bytes.Equal(got, f.Content) {
			return false
		}
	}
	if r, err := m.readReference(n.ref); err != nil || !r.equal(n.reference()) {
		return false
	}
	if spec.State {
		if d, err := m.readDigest(n.ref); err != nil || d != n.digest {
			return false
		}
		stateDir := m.cfg.Render.StatePath(n.ref)
		if composekit.RealDir(stateDir) != nil {
			return false
		}
		for _, f := range n.state {
			got, err := composekit.ReadFileNoFollow(filepath.Join(stateDir, f.Name))
			if err != nil || !bytes.Equal(got, f.Content) {
				return false
			}
		}
	}
	return spec.NoAdminSecret || composekit.IsRegularFile(filepath.Join(dir, SecretsDir, AdminSecretFile))
}

// prepareDataDir creates the (empty) data directory of an instance. An
// existing empty directory is accepted (and reset to 0700); a non-empty one
// is refused: reusing it would bring back databases and users nobody knows
// the passwords of.
func (m *Manager) prepareDataDir(e Engine, ref InstanceRef) (created bool, err error) {
	if err := composekit.RealDir(m.cfg.Render.DataDir); err != nil {
		return false, m.storageError(err)
	}
	p := m.cfg.Render.DataPath(ref)
	switch err := os.Mkdir(p, dataPerm); {
	case err == nil:
		created = true
	case errors.Is(err, os.ErrExist):
		if composekit.RealDir(p) != nil {
			return false, newError(CodeDataDirNotEmpty, "", "the data path of the instance exists and is not a directory")
		}
		entries, rerr := os.ReadDir(p)
		if rerr != nil {
			return false, m.storageError(rerr)
		}
		if len(entries) > 0 {
			return false, newError(CodeDataDirNotEmpty, "", "the data directory of the instance is not empty")
		}
		if err := os.Chmod(p, dataPerm); err != nil {
			return false, m.storageError(err)
		}
	default:
		return false, m.storageError(err)
	}
	uid, gid := e.DataOwner()
	if err := m.chown(p, uid, gid); err != nil {
		if !errors.Is(err, os.ErrPermission) {
			return created, m.storageError(err)
		}
		// Only root can chown; outside production (dev on a laptop) the
		// engine may still fix the ownership itself.
		m.logger.Warn("services: cannot chown the data directory (API not running as root)", "path", p)
	}
	return created, nil
}

// writeInstanceFiles writes the admin secret, the generated config files
// and, last, the compose file (which marks the instance as complete). It
// returns the compose file written.
func (m *Manager) writeInstanceFiles(n newInstance, dir string) ([]byte, error) {
	e, v, ref := n.e, n.v, n.ref
	compose, err := Render(e, ref, v, n.opts, n.networks, m.cfg.Render)
	if err != nil {
		return nil, m.storageError(err)
	}
	spec := e.Service(ref, v, n.opts)
	if !spec.NoAdminSecret {
		secretsDir := filepath.Join(dir, SecretsDir)
		if _, err := composekit.EnsureDir(secretsDir, secretDirPerm); err != nil {
			return nil, m.storageError(err)
		}
		password, err := GeneratePassword()
		if err != nil {
			return nil, m.storageError(err)
		}
		if err := composekit.WriteFileAtomic(secretsDir, AdminSecretFile, []byte(password), secretPerm); err != nil {
			return nil, m.storageError(err)
		}
	}
	if err := m.ensurePrivate(ref, spec.State); err != nil {
		return nil, m.storageError(err)
	}
	if err := m.writeReference(ref, n.reference()); err != nil {
		return nil, m.storageError(err)
	}
	if spec.State {
		if err := m.writeDigest(ref, n.digest); err != nil {
			return nil, m.storageError(err)
		}
		if err := m.writeState(e, spec.SecretState, ref, n.state); err != nil {
			return nil, err
		}
		// Keys generated once: an instance created again gets new ones
		// (its data directory is new too).
		for _, name := range spec.GeneratedKeys {
			key := make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return nil, m.storageError(err)
			}
			if err := m.writeStateFile(e, true, m.cfg.Render.StatePath(ref), name, []byte(base64.StdEncoding.EncodeToString(key))); err != nil {
				return nil, m.storageError(err)
			}
		}
	}
	for _, f := range spec.Files { // names checked by NewRegistry
		if err := composekit.WriteFileAtomic(dir, f.Name, f.Content, configPerm); err != nil {
			return nil, m.storageError(err)
		}
	}
	if err := composekit.WriteFileAtomic(dir, ComposeFile, compose, filePerm); err != nil {
		return nil, m.storageError(err)
	}
	return compose, nil
}

// rollbackInstance removes what createInstance created: the project, the
// data directory only if this call created it (never archived: it holds no
// resource), then the instance directory.
func (m *Manager) rollbackInstance(ref InstanceRef, dir string, started, dataCreated bool) {
	ctx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
	defer cancel()
	defer m.onChange()
	if started {
		if out, err := m.compose(ctx, dir, downArgs...); err != nil {
			m.logger.Error("services: rollback: compose down failed, files kept", "instance", ref.Key(), "error", err, "output", out)
			return
		}
	}
	if dataCreated {
		if err := m.removeDataDir(ref, false); err != nil {
			m.logger.Error("services: rollback: removing the data directory", "instance", ref.Key(), "error", err)
			return
		}
	}
	if err := m.removePrivate(ref); err != nil {
		m.logger.Error("services: rollback: removing the private directory", "instance", ref.Key(), "error", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		m.logger.Error("services: rollback: removing the instance directory", "instance", ref.Key(), "error", err)
	}
}

// removeDataDir deletes (or archives) the data directory of an instance. The
// path is computed from the instance reference, never read from a compose
// file, and neither it nor its parent may be a symlink. os.RemoveAll does not
// follow symlinks found inside the tree.
func (m *Manager) removeDataDir(ref InstanceRef, archive bool) error {
	if err := composekit.RealDir(m.cfg.Render.DataDir); err != nil {
		return err
	}
	p := m.cfg.Render.DataPath(ref)
	if err := composekit.RealDir(p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !archive {
		return os.RemoveAll(p)
	}
	// rename(2) silently replaces an empty directory: refuse any existing
	// target instead.
	target := fmt.Sprintf("%s-retired-%s", p, m.now().UTC().Format("20060102T150405.000000000Z"))
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("archive target %s already exists", target)
	}
	return os.Rename(p, target)
}

// retire stops and removes an instance that holds no resource, archiving its
// data when configured.
func (m *Manager) retire(ctx context.Context, inst *instance, actor string) error {
	return m.retireWith(ctx, inst, actor, m.cfg.ArchiveOnRetire)
}

// retireWith removes the containers, then the data, then the instance
// directory (last: if anything fails, the instance is still visible and the
// retirement can be retried with Retire).
func (m *Manager) retireWith(ctx context.Context, inst *instance, actor string, archive bool) error {
	key := inst.ref.Key()
	// Stop first, right after the last resource was removed: no client can
	// create a database behind the panel's back while the data is purged.
	if out, err := m.compose(ctx, inst.dir, stopArgs...); err != nil {
		m.logger.Error("services: retire: compose stop failed", "instance", key, "error", err, "output", out)
		return err
	}
	if out, err := m.compose(ctx, inst.dir, downArgs...); err != nil {
		m.logger.Error("services: retire: compose down failed", "instance", key, "error", err, "output", out)
		return err
	}
	if err := m.removeDataDir(inst.ref, archive); err != nil {
		m.logger.Error("services: retire: removing the data directory", "instance", key, "error", err)
		return err
	}
	if err := m.removePrivate(inst.ref); err != nil {
		m.logger.Error("services: retire: removing the private directory", "instance", key, "error", err)
		return err
	}
	if err := os.RemoveAll(inst.dir); err != nil {
		m.logger.Error("services: retire: removing the instance directory", "instance", key, "error", err)
		return err
	}
	m.audit.Info("service instance retired", "user", actor, "instance", key, "archived", archive)
	return nil
}

// writeState writes the state files of a dedicated instance (password
// digests, read by the engine user: world-readable, the directory is not
// reachable from the sites).
func (m *Manager) writeState(e Engine, secret bool, ref InstanceRef, files []GeneratedFile) error {
	stateDir := m.cfg.Render.StatePath(ref)
	for _, f := range files {
		if !validGeneratedName(f.Name) {
			return m.storageError(fmt.Errorf("invalid state file name %q", f.Name))
		}
		if err := m.writeStateFile(e, secret, stateDir, f.Name, f.Content); err != nil {
			return m.storageError(err)
		}
	}
	return nil
}

// writeStateFile writes a state file: readable by the engine user (0644
// digests), or, when it holds a secret, 0400 owned by the engine user. The
// container only reads it at start, after the chown.
func (m *Manager) writeStateFile(e Engine, secret bool, dir, name string, data []byte) error {
	if !secret {
		return composekit.WriteFileAtomic(dir, name, data, configPerm)
	}
	if err := composekit.WriteFileAtomic(dir, name, data, secretPerm); err != nil {
		return err
	}
	p := filepath.Join(dir, name)
	uid, gid := e.DataOwner()
	if err := m.chown(p, uid, gid); err != nil {
		if !errors.Is(err, os.ErrPermission) {
			return err
		}
		// Outside production (API not running as root) the file cannot be
		// given to the engine user: it must stay readable by it, which is
		// only acceptable behind private directories closed to other host
		// users. Otherwise the secret is not left readable.
		if perr := closedParents(dir, 2); perr != nil {
			_ = os.Remove(p)
			return fmt.Errorf("cannot give a secret state file to the engine user, and its parent directories are not private: %w", perr)
		}
		m.logger.Warn("services: cannot chown a secret state file (API not running as root): left readable behind private directories", "path", p)
		return os.Chmod(p, 0o444)
	}
	return nil
}

// closedParents checks that the n parent directories of dir are real
// directories closed to group and others (mode 0700 or stricter).
func closedParents(dir string, n int) error {
	p := dir
	for range n {
		p = filepath.Dir(p)
		st, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if !st.IsDir() || st.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("%s is not a private directory (mode %v)", p, st.Mode())
		}
	}
	return nil
}
