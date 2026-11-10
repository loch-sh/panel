package services

// Networks of the instances: every instance joins one or more panel
// networks (internal/networks), recorded in its reference and rendered in
// its compose file as external networks.

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"host-panel-api/internal/composekit"
	"host-panel-api/internal/networks"
)

// checkNetworks validates the networks requested for an instance. None
// means the shared network, which an engine without authentication never
// joins: every site on it would reach the instance.
func (m *Manager) checkNetworks(ctx context.Context, e Engine, names []string) ([]string, *Error) {
	if len(names) == 0 {
		if IsUnauthenticated(e) {
			return nil, newError(CodeInvalidNetwork, "networks", "this engine has no authentication: choose the private networks of the sites allowed to reach it")
		}
		if _, ok := m.cfg.Render.Networks.Lookup(networks.SharedName); !ok {
			return nil, newError(CodeInvalidNetwork, "networks", "choose at least one network")
		}
		names = []string{networks.SharedName}
	}
	sorted := slices.Sorted(slices.Values(names))
	for i, name := range sorted {
		if i > 0 && sorted[i-1] == name {
			return nil, newError(CodeInvalidNetwork, "networks", "network %q listed twice", name)
		}
		if name == networks.SharedName && IsUnauthenticated(e) {
			return nil, newError(CodeInvalidNetwork, "networks", "this engine has no authentication: it cannot join the shared network")
		}
		if _, ok := m.cfg.Render.Networks.Lookup(name); !ok {
			return nil, newError(CodeInvalidNetwork, "networks", "unknown network %q", name)
		}
		if _, ok := m.cfg.Render.Networks.Verify(ctx, name); !ok {
			return nil, newError(CodeInvalidNetwork, "networks", "network %q is missing or is not the one the panel created", name)
		}
	}
	return sorted, nil
}

// UpdateNetworks replaces the networks of an instance. The compose file is
// rendered again and the container recreated (Compose recreates a container
// whose networks change). A removed network is cut right away, before the
// recreation: if the recreation then fails, the instance is stopped rather
// than reconnected.
func (m *Manager) UpdateNetworks(ctx context.Context, key string, names []string, actor string) ([]string, error) {
	if err := m.knownKey(key); err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, newError(CodeLastNetwork, "networks", "an instance keeps at least one network")
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
	nets, nerr := m.checkNetworks(ctx, inst.engine, names)
	if nerr != nil {
		return nil, nerr
	}
	if slices.Equal(nets, inst.networks) {
		return nets, nil
	}
	rt, rerr := m.ready(ctx, inst)
	if rerr != nil {
		return nil, rerr
	}
	compose, err := Render(inst.engine, inst.ref, inst.version, inst.opts, nets, m.cfg.Render)
	if err != nil {
		return nil, m.storageError(err)
	}
	backup, err := composekit.ReadFileNoFollow(filepath.Join(inst.dir, ComposeFile))
	if err != nil {
		return nil, m.storageError(err)
	}
	previous := inst.reference()
	next := inst.reference()
	next.Networks = nets
	restore := func() bool {
		werr := m.writeReference(inst.ref, previous)
		cerr := composekit.WriteFileAtomic(inst.dir, ComposeFile, backup, filePerm)
		if werr != nil || cerr != nil {
			m.logger.Error("services: restoring the previous networks failed", "instance", key, "error", errors.Join(werr, cerr))
			return false
		}
		return true
	}
	if err := m.writeReference(inst.ref, next); err != nil {
		return nil, m.storageError(err)
	}
	if err := composekit.WriteFileAtomic(inst.dir, ComposeFile, compose, filePerm); err != nil {
		restore()
		return nil, m.storageError(err)
	}
	fresh, lerr := m.load(key)
	if lerr != nil || fresh.modified {
		restore()
		return nil, newError(CodeInstanceModified, "", "the instance files changed during the operation")
	}
	removed := slices.DeleteFunc(slices.Clone(inst.networks), func(n string) bool { return slices.Contains(nets, n) })
	for _, name := range removed {
		if ref, ok := m.cfg.Render.Networks.Lookup(name); ok {
			if err := m.docker.DisconnectNetwork(ctx, ref.ID, rt.containerID); err != nil {
				m.logger.Warn("services: disconnecting a removed network failed", "instance", key, "network", name, "error", err)
			}
		}
	}
	defer m.onChange()
	if err := m.runReady(ctx, fresh, upArgs); err != nil {
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer rcancel()
		if len(removed) > 0 {
			// Never reconnect what had to be cut: stop the instance.
			out, serr := m.compose(rctx, inst.dir, stopArgs...)
			m.logger.Error("services: the instance did not restart without the removed networks: stopped",
				"instance", key, "error", err, "stop_error", serr, "output", out)
			if serr != nil {
				m.audit.Warn("service instance networks update failed", "user", actor, "instance", key, "outcome", "stop failed")
				return nil, newError(CodeOutcomeUnknown, "", "the instance did not restart with its new networks, and stopping it failed: check it")
			}
			m.audit.Warn("service instance networks update failed", "user", actor, "instance", key, "outcome", "stopped")
			return nil, newError(CodeStartFailed, "", "the instance did not restart with its new networks: it was stopped")
		}
		m.audit.Warn("service instance networks update failed", "user", actor, "instance", key, "outcome", "restored")
		if !restore() {
			return nil, newError(CodeOutcomeUnknown, "", "the previous files could not be restored; check the instance")
		}
		if rerr := m.runReady(rctx, inst, upArgs); rerr != nil {
			return nil, newError(CodeOutcomeUnknown, "", "the instance did not restart; check its state")
		}
		return nil, newError(CodeStartFailed, "", "the instance did not start with its new networks; the previous ones are kept")
	}
	m.audit.Info("service instance networks updated", "user", actor, "instance", key,
		"before", strings.Join(inst.networks, ","), "after", strings.Join(nets, ","))
	return nets, nil
}

// NetworkSource exposes the instances as members of the panel networks.
type NetworkSource struct{ M *Manager }

// Members returns every managed instance with its networks.
func (s NetworkSource) Members(context.Context) ([]networks.DeclaredMember, error) {
	insts, err := s.M.loadAll()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]networks.DeclaredMember, 0, len(insts))
	for _, inst := range insts {
		// container_name is the project: Docker keeps container names unique.
		out = append(out, networks.DeclaredMember{Name: inst.ref.Key(), Project: inst.ref.Project(), Container: inst.ref.Project(), Networks: slices.Clone(inst.networks)})
	}
	return out, nil
}

// SetNetworks replaces the networks of an instance (synchronous).
func (s NetworkSource) SetNetworks(ctx context.Context, key string, nets []string, actor string) (string, error) {
	_, err := s.M.UpdateNetworks(ctx, key, nets, actor)
	return "", err
}
