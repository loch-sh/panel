package services

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"host-panel-api/internal/composekit"
)

// Dedicated instances: the instance is the resource. The panel runs nothing
// inside them; their password lives, as digests, in state files written by
// the panel and read by the engine at start.

const (
	defaultMaxDedicated = 20
	// readyTimeout bounds the wait for a restarted instance to be healthy.
	readyTimeout = 2 * time.Minute
	readyPoll    = 2 * time.Second
)

var restartArgs = []string{"restart"}

// recreateArgs starts the instance on a new container: the configuration is
// bind-mounted file by file and read at start only, so an option that changes
// no compose field (eviction policy, scripting...) needs one.
var recreateArgs = append(append([]string{}, upArgs...), "--force-recreate")

// engineLockKey serializes the creations of one engine (instance limit).
// Instance keys never contain ':'.
func engineLockKey(engineID string) string { return "engine:" + engineID }

// createDedicated creates a dedicated instance. Lock order: engine, then
// instance (fixed, so that no two operations wait for each other).
func (m *Manager) createDedicated(ctx context.Context, e Engine, v Version, req CreateRequest, actor string) (*CreatedResource, error) {
	d := e.(DedicatedEngine)
	if err := validateDedicatedName(e, req.Name); err != nil {
		return nil, err
	}
	opts, oerr := ResolveOptions(d.Options(), req.Options)
	if oerr != nil {
		return nil, oerr
	}
	if ov, ok := e.(OptionValidator); ok {
		if verr := ov.ValidateOptions(v, opts); verr != nil {
			return nil, verr
		}
	}
	ref := InstanceRef{Engine: e.ID(), Version: v.Version, Name: req.Name}
	ctx, cancel := detach(ctx)
	defer cancel()
	unlockEngine, err := m.lock(ctx, engineLockKey(e.ID()))
	if err != nil {
		return nil, err
	}
	defer unlockEngine()
	unlock, err := m.lock(ctx, ref.Key())
	if err != nil {
		return nil, err
	}
	defer unlock()

	if _, lerr := m.load(ref.Key()); lerr == nil {
		return nil, newError(CodeResourceExists, "name", "an instance with this name already exists")
	}
	count, err := m.countDedicated(e.ID())
	if err != nil {
		return nil, m.storageError(err)
	}
	if count >= m.cfg.MaxDedicatedInstances {
		return nil, newError(CodeInstanceLimit, "", "the maximum number of instances of this engine is reached")
	}
	password, err := GeneratePassword()
	if err != nil {
		return nil, m.storageError(err)
	}
	nets, nerr := m.checkNetworks(ctx, e, req.Networks)
	if nerr != nil {
		return nil, nerr
	}
	inst, err := m.createInstance(ctx, newInstance{e: e, v: v, ref: ref, opts: opts, password: password, networks: nets}, actor)
	if err != nil {
		return nil, err
	}
	m.audit.Info("service resource created", "user", actor, "instance", ref.Key(), "resource", ref.Name, "options", formatOptions(opts))
	m.onChange()
	return &CreatedResource{
		Resource:        m.resourceSummary(inst, Resource{Name: ref.Name, Managed: true}),
		Credentials:     m.credentials(inst, ref.Name, password),
		InstanceCreated: true,
	}, nil
}

// countDedicated counts the instance directories of an engine, valid or
// not (an instance hidden by a configuration change still runs).
func (m *Manager) countDedicated(engineID string) (int, error) {
	entries, err := os.ReadDir(m.cfg.StacksDir)
	if err != nil {
		return 0, err
	}
	prefix := InstancePrefix + engineID + "-"
	n := 0
	for _, ent := range entries {
		// Only instances with a private directory (written by the panel, out
		// of reach of CI, which could otherwise exhaust the limit with
		// planted directories).
		rest, ok := strings.CutPrefix(ent.Name(), prefix)
		if ok && ent.IsDir() && dedicatedNameRe.MatchString(rest) &&
			composekit.RealDir(m.cfg.Render.PrivatePath(InstanceRef{Engine: engineID, Name: rest})) == nil {
			n++
		}
	}
	return n, nil
}

// deleteDedicated retires the instance (containers, then data). Called
// with the instance lock held.
func (m *Manager) deleteDedicated(ctx context.Context, inst *instance, name, actor string) (*DeleteResult, error) {
	if name != inst.ref.Name {
		return nil, newError(CodeResourceNotFound, "name", "resource not found")
	}
	if inst.modified {
		return nil, errModifiedByHand()
	}
	if fresh, lerr := m.load(inst.ref.Key()); lerr != nil || fresh.modified {
		return nil, newError(CodeInstanceModified, "", "the instance files changed during the operation")
	}
	if err := m.retire(ctx, inst, actor); err != nil {
		return nil, m.storageError(err)
	}
	m.audit.Info("service resource deleted", "user", actor, "instance", inst.ref.Key(), "resource", name)
	m.onChange()
	return &DeleteResult{InstanceRetired: true}, nil
}

// rotateDedicated writes new password digests and restarts the instance:
// every connection is closed, the old password stops working. On failure
// the previous state is restored and the instance restarted with it. The
// password is only returned once the instance is healthy with it.
func (m *Manager) rotateDedicated(ctx context.Context, inst *instance, name, actor string) ([]Credential, error) {
	if name != inst.ref.Name {
		return nil, newError(CodeResourceNotFound, "name", "resource not found")
	}
	if IsUnauthenticated(inst.engine) {
		return nil, newError(CodeNoCredentials, "", "this engine has no authentication: there is no password to reset")
	}
	if inst.modified {
		return nil, errModifiedByHand()
	}
	// Only a trusted container is restarted.
	if _, rerr := m.ready(ctx, inst); rerr != nil {
		return nil, rerr
	}
	password, err := GeneratePassword()
	if err != nil {
		return nil, m.storageError(err)
	}
	d := inst.engine.(DedicatedEngine)
	digest, err := d.PasswordDigest(password)
	if err != nil {
		return nil, m.storageError(err)
	}
	files, err := d.StateFiles(inst.ref, inst.version, inst.opts, digest)
	if err != nil {
		return nil, m.storageError(err)
	}
	oldDigest, _ := m.readDigest(inst.ref)
	secret := inst.engine.Service(inst.ref, inst.version, inst.opts).SecretState
	stateDir := m.cfg.Render.StatePath(inst.ref)
	if err := composekit.RealDir(stateDir); err != nil {
		return nil, m.storageError(err)
	}
	backup, err := readState(stateDir)
	if err != nil {
		return nil, m.storageError(err)
	}
	restore := func() {
		for fname, data := range backup {
			if werr := m.writeStateFile(inst.engine, secret, stateDir, fname, data); werr != nil {
				m.logger.Error("services: restoring the state failed", "instance", inst.ref.Key(), "error", werr)
			}
		}
		for _, f := range files {
			if _, ok := backup[f.Name]; !ok {
				_ = os.Remove(filepath.Join(stateDir, f.Name))
			}
		}
		if oldDigest != "" {
			_ = m.writeDigest(inst.ref, oldDigest)
		} else {
			// Instance created before the digest file: none must remain for
			// a password nobody received.
			_ = os.Remove(filepath.Join(m.cfg.Render.PrivatePath(inst.ref), digestFile))
		}
	}
	if err := m.writeState(inst.engine, secret, inst.ref, files); err != nil {
		restore()
		return nil, err
	}
	if err := m.writeDigest(inst.ref, digest); err != nil {
		restore()
		return nil, m.storageError(err)
	}
	// Right before running compose: the files are still the panel's.
	if fresh, lerr := m.load(inst.ref.Key()); lerr != nil || fresh.modified {
		restore()
		return nil, newError(CodeInstanceModified, "", "the instance files changed during the operation")
	}
	if err := m.restartReady(ctx, inst); err != nil {
		m.logger.Error("services: restart with the new password failed, restoring the previous one", "instance", inst.ref.Key(), "error", err)
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer rcancel()
		restore()
		m.onChange()
		if rerr := m.restartReady(rctx, inst); rerr != nil {
			m.logger.Error("services: restarting with the previous password failed", "instance", inst.ref.Key(), "error", rerr)
			return nil, newError(CodeOutcomeUnknown, "", "the instance did not restart; check its state")
		}
		return nil, newError(CodeStartFailed, "", "the instance did not restart with the new password; the previous one is kept")
	}
	m.audit.Info("service resource password reset", "user", actor, "instance", inst.ref.Key(), "resource", name)
	m.onChange()
	return m.credentials(inst, name, password), nil
}

// restartReady restarts an instance and waits until it is healthy.
func (m *Manager) restartReady(ctx context.Context, inst *instance) error {
	return m.runReady(ctx, inst, restartArgs)
}

// runReady runs a compose command, then waits until the instance is healthy.
func (m *Manager) runReady(ctx context.Context, inst *instance, args []string) error {
	if out, err := m.compose(ctx, inst.dir, args...); err != nil {
		m.logger.Error("services: restart failed", "instance", inst.ref.Key(), "error", err, "output", out)
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()
	ticker := time.NewTicker(readyPoll)
	defer ticker.Stop()
	for {
		if _, rerr := m.ready(wctx, inst); rerr == nil {
			return nil
		}
		select {
		case <-wctx.Done():
			return wctx.Err()
		case <-ticker.C:
		}
	}
}

// readState reads the state files of an instance (name -> content).
func readState(dir string) (map[string][]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, ent := range entries {
		if !ent.Type().IsRegular() {
			continue
		}
		data, err := composekit.ReadFileNoFollow(filepath.Join(dir, ent.Name()))
		if err != nil {
			return nil, err
		}
		out[ent.Name()] = data
	}
	return out, nil
}

// formatOptions renders options for logs ("memory=128,policy=noeviction").
func formatOptions(opts Options) string {
	keys := make([]string, 0, len(opts))
	for k := range opts {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+opts[k])
	}
	return strings.Join(parts, ",")
}

// UpdateOptions changes the mutable options of a dedicated instance (size,
// eviction policy...) and recreates its container with them. Immutable
// options (e.g. Redis persistence: changing it would lose the data) are
// refused. On failure the previous files are restored and the instance
// started with them again.
func (m *Manager) UpdateOptions(ctx context.Context, key string, changes map[string]string, actor string) (Options, error) {
	if err := m.knownKey(key); err != nil {
		return nil, err
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
	d, ok := inst.engine.(DedicatedEngine)
	if !ok || inst.engine.Lifecycle() != Dedicated {
		return nil, newError(CodeInvalidOption, "options", "this instance has no options")
	}
	if inst.modified {
		return nil, errModifiedByHand()
	}
	merged := maps.Clone(inst.opts)
	maps.Copy(merged, changes)
	opts, oerr := ResolveOptions(d.Options(), merged)
	if oerr != nil {
		return nil, oerr
	}
	if ov, ok := inst.engine.(OptionValidator); ok {
		if verr := ov.ValidateOptions(inst.version, opts); verr != nil {
			return nil, verr
		}
	}
	for _, o := range d.Options() {
		if opts[o.Key] != inst.opts[o.Key] && !o.Mutable {
			return nil, newError(CodeOptionImmutable, "options", "option %q cannot be changed after creation", o.Key)
		}
	}
	if maps.Equal(opts, inst.opts) {
		return opts, nil
	}
	// Only a trusted container is recreated.
	if _, rerr := m.ready(ctx, inst); rerr != nil {
		return nil, rerr
	}
	aerr := m.applyConfig(ctx, inst, inst.version, opts, applyHooks{
		rolledBack: func(reason string) {
			m.audit.Warn("service resource update rolled back", "user", actor, "instance", key, "resource", inst.ref.Name,
				"before", formatOptions(inst.opts), "after", formatOptions(opts), "reason", reason)
		},
		what: "options",
	})
	if aerr != nil {
		return nil, aerr
	}
	m.audit.Info("service resource updated", "user", actor, "instance", key, "resource", inst.ref.Name,
		"before", formatOptions(inst.opts), "after", formatOptions(opts))
	m.onChange()
	return opts, nil
}

// applyHooks adapt applyConfig to the operation.
type applyHooks struct {
	// rolledBack audits a change given up (reason: write failed, files
	// changed, start failed).
	rolledBack func(reason string)
	// verify checks the started instance (nil: healthy is enough); an
	// error rolls the change back like a failed start.
	verify func(ctx context.Context, fresh *instance) error
	// what names the change in the messages ("options", "version").
	what string
}

// applyConfig renders a dedicated instance with a version and options,
// writes every file (reference, state, compose, generated and exposure
// files) and recreates its container. On failure the previous files are
// restored and the instance started with them again. Called with the
// instance lock held, on a trusted, ready instance.
func (m *Manager) applyConfig(ctx context.Context, inst *instance, v Version, opts Options, hooks applyHooks) (res *Error) {
	if hooks.rolledBack == nil {
		hooks.rolledBack = func(string) {}
	}
	key := inst.ref.Key()
	d := inst.engine.(DedicatedEngine)
	ref := inst.ref
	ref.Version = v.Version
	digest, err := m.readDigest(inst.ref)
	if errors.Is(err, fs.ErrNotExist) {
		return newError(CodePasswordResetRequired, "", "this instance predates configuration changes: reset its password once, then retry")
	}
	if err != nil {
		return m.storageError(err)
	}
	secretState := inst.engine.Service(ref, v, opts).SecretState
	state, err := d.StateFiles(ref, v, opts, digest)
	if err != nil {
		return m.storageError(err)
	}
	compose, err := Render(inst.engine, ref, v, opts, inst.networks, m.cfg.Render)
	if err != nil {
		return m.storageError(err)
	}
	exposure, err := m.exposureFiles(inst.engine, ref, v, opts, inst.dir, inst.desired)
	if err != nil {
		return m.storageError(err)
	}

	// Back up every file the change rewrites.
	stateDir := m.cfg.Render.StatePath(inst.ref)
	backupState, err := readState(stateDir)
	if err != nil {
		return m.storageError(err)
	}
	instanceFiles := map[string][]byte{ComposeFile: compose}
	for _, f := range inst.engine.Service(ref, v, opts).Files {
		instanceFiles[f.Name] = f.Content
	}
	// Files of the previous configuration that the new one no longer has
	// are restored too.
	for _, f := range inst.engine.Service(inst.ref, inst.version, inst.opts).Files {
		if _, ok := instanceFiles[f.Name]; !ok {
			instanceFiles[f.Name] = nil
		}
	}
	for name, content := range exposure {
		// The certificate is kept as is (and its key must keep its mode):
		// it is neither backed up nor restored.
		if filepath.Dir(name) != CertsDir {
			instanceFiles[name] = content
		}
	}
	backup := map[string][]byte{}
	for name := range instanceFiles {
		if data, rerr := composekit.ReadFileNoFollow(filepath.Join(inst.dir, name)); rerr == nil {
			backup[name] = data
		}
	}
	// restore puts every rewritten file back (and removes the new ones); it
	// reports whether the previous configuration is fully back.
	restore := func() bool {
		ok := true
		check := func(err error) {
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				ok = false
				m.logger.Error("services: restoring the previous configuration failed", "instance", key, "error", err)
			}
		}
		for name := range instanceFiles {
			if data, had := backup[name]; had {
				check(composekit.WriteFileAtomic(filepath.Join(inst.dir, filepath.Dir(name)), filepath.Base(name), data, filePermOf(name)))
			} else {
				check(os.Remove(filepath.Join(inst.dir, name)))
			}
		}
		for name, data := range backupState {
			check(m.writeStateFile(inst.engine, secretState, stateDir, name, data))
		}
		for _, f := range state {
			if _, had := backupState[f.Name]; !had {
				check(os.Remove(filepath.Join(stateDir, f.Name)))
			}
		}
		check(m.writeReference(inst.ref, inst.reference()))
		return ok
	}

	write := func() error {
		next := inst.reference()
		next.Version, next.Image, next.Digest, next.Options = v.Version, v.Image, v.Digest, opts
		if err := m.writeReference(inst.ref, next); err != nil {
			return err
		}
		if err := m.writeState(inst.engine, secretState, inst.ref, state); err != nil {
			return err
		}
		if err := m.writeExposureFiles(inst.engine, inst.dir, exposure); err != nil {
			return err
		}
		for name, content := range instanceFiles {
			if _, isExposure := exposure[name]; isExposure {
				continue
			}
			if content == nil {
				if err := os.Remove(filepath.Join(inst.dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
				continue
			}
			if err := composekit.WriteFileAtomic(inst.dir, name, content, filePermOf(name)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := write(); err != nil {
		hooks.rolledBack("write failed")
		if !restore() {
			return newError(CodeOutcomeUnknown, "", "the previous files could not be restored; check the instance")
		}
		return m.storageError(err)
	}
	// Right before running compose: the files are exactly the panel's.
	fresh, lerr := m.load(key)
	if lerr != nil || fresh.modified || fresh.version.Reference() != v.Reference() {
		hooks.rolledBack("files changed")
		restore()
		return newError(CodeInstanceModified, "", "the instance files changed during the operation")
	}
	err = m.runReady(ctx, fresh, recreateArgs)
	if err == nil && hooks.verify != nil {
		// The running container must be the expected one (e.g. the image
		// pulled by digest, not whatever its tag points to now).
		err = hooks.verify(ctx, fresh)
	}
	if err != nil {
		m.logger.Error("services: the instance did not start with its new "+hooks.what+", restoring", "instance", key, "error", err)
		hooks.rolledBack("start failed")
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer rcancel()
		restored := restore()
		m.onChange()
		if !restored {
			return newError(CodeOutcomeUnknown, "", "the previous files could not be restored; check the instance")
		}
		if rerr := m.runReady(rctx, inst, recreateArgs); rerr != nil {
			m.logger.Error("services: restarting with the previous "+hooks.what+" failed", "instance", key, "error", rerr)
			return newError(CodeOutcomeUnknown, "", "the instance did not restart; check its state")
		}
		return newError(CodeStartFailed, "", "the instance did not start with the new %s; the previous ones are kept", hooks.what)
	}
	return nil
}

// filePermOf is the mode of a file of the instance directory.
func filePermOf(name string) os.FileMode {
	if name == ComposeFile {
		return filePerm
	}
	return configPerm
}
