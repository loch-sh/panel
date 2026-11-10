package networks

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"host-panel-api/internal/docker"
)

// EnsureShared registers the network shared with the services module
// (created internal when missing). An existing Docker network is adopted
// only when the panel created it (label) and it is an internal bridge.
func (m *Manager) EnsureShared(ctx context.Context, dockerName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n, ok := m.nets[SharedName]; ok && n.DockerName == dockerName {
		return nil
	}
	n := Network{Name: SharedName, DockerName: dockerName, System: true, CreatedAt: m.now().UTC(), CreatedBy: "system"}
	ins, err := m.docker.InspectNetwork(ctx, dockerName)
	switch {
	case errors.Is(err, docker.ErrNetworkNotFound):
		id, cerr := m.docker.CreateInternalNetwork(ctx, dockerName, netip.Prefix{}, map[string]string{LabelManaged: "true"})
		if cerr != nil {
			return fmt.Errorf("creating the shared network %s: %w", dockerName, cerr)
		}
		if ins, err = m.docker.InspectNetwork(ctx, dockerName); err != nil || ins.ID != id {
			return fmt.Errorf("inspecting the shared network %s: %v", dockerName, err)
		}
		m.audit.Info("network created", "user", "system", "network", SharedName, "docker", dockerName)
	case err != nil:
		return fmt.Errorf("inspecting the shared network %s: %w", dockerName, err)
	}
	n.ID = ins.ID
	if p := check(n, ins); p != "" {
		return fmt.Errorf("the shared network %s exists but was not created by the panel as an internal network (%s): remove it, the panel creates it again", dockerName, p)
	}
	n.Subnet, n.Gateway = subnetOf(ins)
	m.nets[SharedName] = n
	return m.persist()
}

// Reconcile brings back the registered networks missing from Docker (same
// name, subnet and labels: the members' compose files still apply) and
// reports the ones that were replaced. Called at startup.
func (m *Manager) Reconcile(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	changed := false
	for name, n := range m.nets {
		st, problem, _ := m.state(ctx, n)
		switch st {
		case StateMissing:
			if problem != "" {
				continue // Docker unreachable: nothing to conclude
			}
			var subnet netip.Prefix
			if p, err := netip.ParsePrefix(n.Subnet); err == nil {
				subnet = p
			}
			labels := map[string]string{LabelManaged: "true"}
			if !n.System {
				labels[LabelNetwork] = n.Name
			}
			id, err := m.docker.CreateInternalNetwork(ctx, n.DockerName, subnet, labels)
			if err != nil {
				m.logger.Error("networks: recreating a missing network failed", "network", n.DockerName, "error", err)
				continue
			}
			n.ID = id
			m.nets[name] = n
			changed = true
			m.audit.Info("network recreated", "user", "system", "network", name, "subnet", n.Subnet)
		case StateCompromised:
			m.logger.Error("networks: the Docker network is not the one the panel created: its members are not trusted", "network", n.DockerName, "problem", problem)
			m.audit.Warn("network compromised", "user", "system", "network", name, "problem", problem)
		}
	}
	if changed {
		if err := m.persist(); err != nil {
			m.logger.Error("networks: saving the registry", "error", err)
		}
	}
}
