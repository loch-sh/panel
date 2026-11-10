package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"host-panel-api/internal/composekit"
)

// exposureFailedMarker records the override that failed to apply, so the
// API does not retry (and cut the instance) on every restart until the
// exposure changes.
const exposureFailedMarker = ".loch-exposure-failed"

// tlsRequired reports whether an exposure requires TLS from every client:
// published outside the loopback, with SERVICES_TLS=auto, on an engine
// that can require it.
func (m *Manager) tlsRequired(e Engine, ref InstanceRef, v Version, exp *Exposure) bool {
	return exp != nil && exp.Public() && m.cfg.TLS == TLSAuto && supportsTLS(e, ref, v)
}

// TLSMode returns SERVICES_TLS.
func (m *Manager) TLSMode() TLSMode { return m.cfg.TLS }

// exposureFiles are the files derived from an exposure: name -> content.
func (m *Manager) exposureFiles(e Engine, ref InstanceRef, v Version, opts Options, dir string, exp *Exposure) (map[string][]byte, error) {
	tls := m.tlsRequired(e, ref, v, exp)
	over, err := RenderOverride(e, ref, v, opts, exp, tls)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{OverrideFile: over}
	for _, f := range tlsFiles(e, ref, v, tls, exp != nil && exp.TLSOptional) {
		files[f.Name] = f.Content
	}
	if tls {
		cert, err := m.certificate(ref, dir)
		if err != nil {
			return nil, err
		}
		files[filepath.Join(CertsDir, CertFile)] = cert.CertPEM
		files[filepath.Join(CertsDir, KeyFile)] = cert.KeyPEM
	}
	return files, nil
}

// certificate returns the configured certificate, the current one when it
// is still usable, or a new self-signed one.
func (m *Manager) certificate(ref InstanceRef, dir string) (Certificate, error) {
	if m.cfg.Certificate != nil {
		return *m.cfg.Certificate, nil
	}
	names := certificateNames(m.cfg.PublicHost, ref)
	certPEM, cerr := composekit.ReadFileNoFollow(filepath.Join(dir, CertsDir, CertFile))
	keyPEM, kerr := composekit.ReadFileNoFollow(filepath.Join(dir, CertsDir, KeyFile))
	if cerr == nil && kerr == nil && certificateUsable(certPEM, names, m.now()) &&
		ValidateCertificate(Certificate{CertPEM: certPEM, KeyPEM: keyPEM}, m.now()) == nil {
		return Certificate{CertPEM: certPEM, KeyPEM: keyPEM}, nil
	}
	return selfSignedCertificate(names, m.now())
}

// writeExposureFiles writes the override and the TLS files. The certificate
// key goes to the engine user (it refuses keys others can read).
func (m *Manager) writeExposureFiles(e Engine, dir string, files map[string][]byte) error {
	for name, content := range files {
		target := filepath.Join(dir, filepath.Dir(name))
		perm := os.FileMode(configPerm)
		if filepath.Dir(name) == CertsDir {
			if _, err := composekit.EnsureDir(target, secretDirPerm); err != nil {
				return err
			}
			uid, gid := e.DataOwner()
			m.chownTolerant(target, uid, gid)
			perm = 0o444
			if filepath.Base(name) == KeyFile {
				perm = secretPerm
			}
		}
		if err := composekit.WriteFileAtomic(target, filepath.Base(name), content, perm); err != nil {
			return err
		}
		if filepath.Dir(name) == CertsDir {
			uid, gid := e.DataOwner()
			m.chownTolerant(filepath.Join(target, filepath.Base(name)), uid, gid)
		}
	}
	return nil
}

func (m *Manager) chownTolerant(p string, uid, gid int) {
	if err := m.chown(p, uid, gid); err != nil && !errors.Is(err, os.ErrPermission) {
		m.logger.Warn("services: chown failed", "path", p, "error", err)
	}
}

// exposureUpToDate reports whether the files on disk are the expected ones.
// Only the override and the TLS files are compared: a usable certificate is
// kept (certificate returns it).
func exposureUpToDate(dir string, files map[string][]byte) bool {
	for name, content := range files {
		got, err := composekit.ReadFileNoFollow(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(got, content) {
			return false
		}
	}
	return true
}

func hashOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// ApplyExposures brings the published ports and TLS of every instance in
// line with their reference, at startup then once a day (certificate
// renewal). Instances are processed one at a time.
func (m *Manager) ApplyExposures(ctx context.Context) {
	ticker := time.NewTicker(exposureCheckInterval)
	defer ticker.Stop()
	for {
		m.applyAll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

const exposureCheckInterval = 24 * time.Hour

func (m *Manager) applyAll(ctx context.Context) {
	insts, err := m.loadAll()
	if err != nil {
		m.logger.Error("services: cannot list the instances to apply their exposure", "error", err)
		return
	}
	for _, inst := range insts {
		if ctx.Err() != nil {
			return // shutting down; an operation in progress always completes
		}
		if err := m.applyExposure(ctx, inst); err != nil {
			m.logger.Error("services: exposure not applied", "instance", inst.ref.Key(), "error", err)
		}
	}
}

// narrows reports whether the target removes or restricts an exposure in
// effect: a failure must then never bring the previous one back.
func narrows(applied AppliedExposure, exp *Exposure, tls bool) bool {
	cur, _ := applied.exposure()
	if cur == nil {
		return false
	}
	return exp == nil || *exp != *cur || (tls && !applied.TLS) || (!exp.Public() && cur.Public())
}

func (m *Manager) applyExposure(ctx context.Context, inst *instance) error {
	key := inst.ref.Key()
	unlock, err := m.lock(ctx, key)
	if err != nil {
		return err
	}
	defer unlock()
	ctx, cancel := detach(ctx)
	defer cancel()
	return m.applyExposureLocked(ctx, key)
}

// applyExposureLocked applies the recorded exposure of an instance. It must
// be called with the instance lock held.
func (m *Manager) applyExposureLocked(ctx context.Context, key string) error {
	// Re-read under the lock: the instance may have changed meanwhile.
	inst, lerr := m.load(key)
	if lerr != nil {
		return lerr
	}
	// The policy (API configuration) may have been tightened since the
	// exposure was chosen: it is checked again at every application, and an
	// exposure it no longer allows is closed (never applied as recorded).
	if inst.desired != nil {
		if verr := m.validateExposure(inst, *inst.desired); verr != nil {
			m.logger.Error("services: the recorded exposure is no longer allowed by the configuration: closing it",
				"instance", key, "exposure", inst.desired.String(), "reason", verr.Message)
			m.audit.Warn("service exposure revoked by policy", "user", "system", "instance", key,
				"before", inst.desired.String(), "reason", verr.Message)
			next := inst.reference()
			next.Exposure = nil
			if err := m.writeReference(inst.ref, next); err != nil {
				return err
			}
			if inst, lerr = m.load(key); lerr != nil {
				return lerr
			}
		}
	}
	files, err := m.exposureFiles(inst.engine, inst.ref, inst.version, inst.opts, inst.dir, inst.desired)
	if err != nil {
		return err
	}
	if exposureUpToDate(inst.dir, files) {
		_ = os.Remove(filepath.Join(inst.dir, exposureFailedMarker))
		return nil
	}
	exp := inst.desired
	tls := m.tlsRequired(inst.engine, inst.ref, inst.version, exp)
	closing := narrows(inst.applied, exp, tls)
	target := hashOf(files[OverrideFile])

	// compose.yaml edited by hand (e.g. by CI): it is never run by the panel
	// (the API is root). A port to close is closed by stopping the instance.
	if inst.modified {
		if !closing {
			return errors.New("instance files edited by hand: the new exposure is not applied (restore them, or remove the instance manually)")
		}
		if err := m.writeExposureFiles(inst.engine, inst.dir, files); err != nil {
			return err
		}
		out, err := m.compose(ctx, inst.dir, stopArgs...)
		m.markExposureFailed(inst, target)
		m.onChange()
		m.logger.Error("services: instance stopped: its published port had to be closed but its files were edited by hand",
			"instance", key, "error", err, "output", out)
		return errors.New("instance stopped instead of being recreated (files edited by hand)")
	}

	if failed, err := composekit.ReadFileNoFollow(filepath.Join(inst.dir, exposureFailedMarker)); err == nil && string(failed) == target {
		return errors.New("a previous attempt with this configuration failed: fix the cause, then change the configuration or remove " + exposureFailedMarker)
	}
	if exp != nil {
		if owner, err := m.portOwner(ctx, inst, exp.Port); err != nil {
			return err
		} else if owner != "" && !closing {
			m.markExposureFailed(inst, target)
			m.recordApplied(inst)
			return fmt.Errorf("port %d is already published by container %s", exp.Port, owner)
		}
	}

	backup := map[string][]byte{}
	for name := range files {
		if data, err := composekit.ReadFileNoFollow(filepath.Join(inst.dir, name)); err == nil {
			backup[name] = data
		}
	}
	certChanged := false
	for _, name := range []string{filepath.Join(CertsDir, CertFile), filepath.Join(CertsDir, KeyFile)} {
		if content, ok := files[name]; ok && !bytes.Equal(content, backup[name]) {
			certChanged = true
		}
	}
	if err := m.writeExposureFiles(inst.engine, inst.dir, files); err != nil {
		return err
	}
	// Check right before running compose that nothing else changed.
	if fresh, lerr := m.load(key); lerr != nil || fresh.modified {
		return errors.New("instance files changed while the exposure was applied")
	}
	args := upArgs
	if certChanged {
		// A renewed certificate is only read at start: recreate.
		args = append(append([]string{}, upArgs...), "--force-recreate")
	}
	out, err := m.compose(ctx, inst.dir, args...)
	if err == nil {
		_ = os.Remove(filepath.Join(inst.dir, exposureFailedMarker))
		applied := "none"
		if exp != nil {
			applied = exp.String()
		}
		m.audit.Info("service exposure applied", "user", "system", "instance", key,
			"before", inst.applied.Address+":"+strconv.Itoa(inst.applied.Port), "after", applied, "tls", tls)
		m.onChange()
		return nil
	}
	m.markExposureFailed(inst, target)
	defer m.onChange()
	rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer rcancel()
	if closing {
		// Never reopen what had to be closed: stop the instance instead.
		sout, serr := m.compose(rctx, inst.dir, stopArgs...)
		m.logger.Error("services: closing or restricting the published port failed: instance stopped",
			"instance", key, "error", err, "output", out, "stop_error", serr, "stop_output", sout)
		return fmt.Errorf("compose up failed, instance stopped: %w", err)
	}
	m.logger.Error("services: applying the exposure failed, restoring", "instance", key, "error", err, "output", out)
	for name := range files {
		if data, ok := backup[name]; ok {
			_ = composekit.WriteFileAtomic(filepath.Join(inst.dir, filepath.Dir(name)), filepath.Base(name), data, configPerm)
		} else {
			_ = os.Remove(filepath.Join(inst.dir, name))
		}
	}
	if rout, rerr := m.compose(rctx, inst.dir, upArgs...); rerr != nil {
		m.logger.Error("services: restoring the previous exposure failed", "instance", key, "error", rerr, "output", rout)
	}
	m.recordApplied(inst)
	return fmt.Errorf("compose up failed: %w", err)
}

// recordApplied records again the exposure still in effect after a failed
// opening: the record and the override agree, the instance stays usable.
func (m *Manager) recordApplied(inst *instance) {
	cur, _ := inst.applied.exposure()
	if cur != nil {
		cur.TLSOptional = inst.applied.TLSOptional
	}
	next := inst.reference()
	next.Exposure = recordOf(cur)
	if err := m.writeReference(inst.ref, next); err != nil {
		m.logger.Error("services: recording the exposure in effect failed", "instance", inst.ref.Key(), "error", err)
	}
}

func (m *Manager) markExposureFailed(inst *instance, hash string) {
	if err := composekit.WriteFileAtomic(inst.dir, exposureFailedMarker, []byte(hash), filePerm); err != nil {
		m.logger.Warn("services: cannot record the failed exposure", "instance", inst.ref.Key(), "error", err)
	}
}

// exposureFailed reports whether the last exposure change failed.
func exposureFailed(dir string) bool {
	return composekit.IsRegularFile(filepath.Join(dir, exposureFailedMarker))
}

// portOwner returns the container (other than the instance) publishing port.
func (m *Manager) portOwner(ctx context.Context, inst *instance, port int) (string, error) {
	all, err := m.docker.ListContainers(ctx)
	if err != nil {
		return "", err
	}
	for _, c := range all {
		if c.Labels[composeProjectLabel] == inst.ref.Project() {
			continue
		}
		for _, p := range c.Ports {
			if int(p.PublicPort) == port {
				return containerNameOf(c), nil
			}
		}
	}
	return "", nil
}
