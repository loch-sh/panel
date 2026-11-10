package networks

import (
	"context"
	"slices"
	"strings"
)

// Disconnect removes a container the panel does not manage from a network
// (a managed member is detached instead, through its compose file).
func (m *Manager) Disconnect(ctx context.Context, name, containerID, actor string) error {
	d, err := m.Get(ctx, name)
	if err != nil {
		return err
	}
	var target *Member
	for i, mem := range d.Members {
		if mem.Present && mem.ContainerID != "" && (mem.ContainerID == containerID || strings.HasPrefix(containerID, mem.ContainerID) && len(containerID) == 64) {
			target = &d.Members[i]
		}
	}
	if target == nil {
		return newError(CodeMemberNotFound, "", "this container is not connected to the network")
	}
	if target.Expected || target.Kind == KindPanel {
		return newError(CodeMemberManaged, "", "this member is managed by the panel: detach it instead")
	}
	if d.State != StateOK {
		return newError(CodeCompromised, "", "the network is not the one the panel created")
	}
	if err := m.docker.DisconnectNetwork(ctx, d.ID, target.ContainerID); err != nil {
		return m.dockerError("disconnect", err)
	}
	m.audit.Info("network container disconnected", "user", actor, "network", name, "container", target.Container)
	return nil
}

func (m *Manager) source(kind string) (MemberSource, *Error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sources[kind]
	if !ok {
		return nil, newError(CodeInvalidKind, "kind", "unknown member kind")
	}
	return s, nil
}

func (m *Manager) memberNetworks(ctx context.Context, s MemberSource, member string) ([]string, error) {
	list, err := s.Members(ctx)
	if err != nil {
		return nil, m.storageError(err)
	}
	for _, d := range list {
		if d.Name == member {
			return slices.Clone(d.Networks), nil
		}
	}
	return nil, newError(CodeMemberNotFound, "name", "member not found")
}

// Attach adds a member to a network. It returns the ID of an asynchronous
// operation for a site.
func (m *Manager) Attach(ctx context.Context, name, kind, member, actor string) (string, error) {
	unlockOp, lerr := m.lockOp(ctx)
	if lerr != nil {
		return "", lerr
	}
	defer unlockOp()
	m.mu.RLock()
	n, ok := m.nets[name]
	m.mu.RUnlock()
	if !ok {
		return "", newError(CodeNotFound, "", "network not found")
	}
	if st, _, _ := m.state(ctx, n); st != StateOK {
		return "", newError(CodeCompromised, "", "the network is missing or is not the one the panel created")
	}
	s, serr := m.source(kind)
	if serr != nil {
		return "", serr
	}
	current, err := m.memberNetworks(ctx, s, member)
	if err != nil {
		return "", err
	}
	if slices.Contains(current, name) {
		return "", newError(CodeAlreadyAttached, "", "the member is already on this network")
	}
	op, err := s.SetNetworks(ctx, member, append(current, name), actor)
	if err != nil {
		return "", err
	}
	m.audit.Info("network member attached", "user", actor, "network", name, "kind", kind, "member", member)
	return op, nil
}

// Detach removes a member from a network.
func (m *Manager) Detach(ctx context.Context, name, kind, member, actor string) (string, error) {
	unlockOp, lerr := m.lockOp(ctx)
	if lerr != nil {
		return "", lerr
	}
	defer unlockOp()
	if _, ok := m.Lookup(name); !ok {
		return "", newError(CodeNotFound, "", "network not found")
	}
	s, serr := m.source(kind)
	if serr != nil {
		return "", serr
	}
	current, err := m.memberNetworks(ctx, s, member)
	if err != nil {
		return "", err
	}
	if !slices.Contains(current, name) {
		return "", newError(CodeNotAttached, "", "the member is not on this network")
	}
	op, err := s.SetNetworks(ctx, member, slices.DeleteFunc(current, func(x string) bool { return x == name }), actor)
	if err != nil {
		return "", err
	}
	m.audit.Info("network member detached", "user", actor, "network", name, "kind", kind, "member", member)
	return op, nil
}
