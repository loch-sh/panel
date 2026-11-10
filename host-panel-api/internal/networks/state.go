package networks

import (
	"context"
	"errors"

	"github.com/moby/moby/api/types/network"

	"host-panel-api/internal/docker"
)

// check returns why a live network is not the registered one ("" when it is).
func check(n Network, ins network.Inspect) string {
	switch {
	case ins.ID != n.ID:
		return ProblemReplaced
	case ins.Driver != "bridge" || ins.Scope != "" && ins.Scope != "local":
		return ProblemNotBridge
	case !ins.Internal:
		return ProblemNotInternal
	case ins.EnableIPv6 || ins.ConfigOnly || ins.ConfigFrom.Network != "":
		return ProblemOptions
	case ins.Labels[LabelManaged] != "true":
		return ProblemMissingLabel
	case !n.System && ins.Labels[LabelNetwork] != n.Name:
		return ProblemUnexpectedLabel
	case ins.Labels["com.docker.compose.project"] != "" || ins.Labels["com.docker.compose.network"] != "":
		return ProblemComposeProject
	}
	if n.Subnet != "" {
		for _, c := range ins.IPAM.Config {
			if c.Subnet.String() == n.Subnet {
				return ""
			}
		}
		return ProblemUnexpectedSubnet
	}
	return ""
}

// state inspects a registered network.
func (m *Manager) state(ctx context.Context, n Network) (string, string, *network.Inspect) {
	ins, err := m.docker.InspectNetwork(ctx, n.DockerName)
	if errors.Is(err, docker.ErrNetworkNotFound) {
		return StateMissing, "", nil
	}
	if err != nil {
		return StateMissing, ProblemInspectFailed, nil
	}
	if p := check(n, ins); p != "" {
		return StateCompromised, p, &ins
	}
	return StateOK, "", &ins
}

// Verify reports whether the live network of a registry name is the
// registered one, with its Docker ID (used before trusting a container).
func (m *Manager) Verify(ctx context.Context, name string) (string, bool) {
	m.mu.RLock()
	n, ok := m.nets[name]
	m.mu.RUnlock()
	if !ok {
		return "", false
	}
	st, _, _ := m.state(ctx, n)
	return n.ID, st == StateOK
}

// subnetOf returns the first IPv4 subnet and gateway of a network.
func subnetOf(ins network.Inspect) (string, string) {
	for _, c := range ins.IPAM.Config {
		if c.Subnet.Addr().Is4() {
			gw := ""
			if c.Gateway.IsValid() {
				gw = c.Gateway.String()
			}
			return c.Subnet.String(), gw
		}
	}
	return "", ""
}
