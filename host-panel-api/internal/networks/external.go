package networks

// Docker networks the panel did not create (Docker's own bridge, host and
// none, the Traefik network, the networks of compose stacks deployed by CI,
// networks created by hand): shown read-only, so that an admin sees the
// whole addressing of the host and which containers sit where.

import (
	"context"
	"errors"
	"net/netip"
	"sort"
	"time"

	"github.com/moby/moby/api/types/network"

	"host-panel-api/internal/docker"
)

// Origins of a Docker network the panel did not create.
const (
	OriginDocker  = "docker"  // created by Docker itself (bridge, host, none)
	OriginCompose = "compose" // created by a compose project
	OriginOther   = "other"   // created by hand or by another tool
)

// dockerDefaultNetworks are created by Docker itself.
var dockerDefaultNetworks = map[string]bool{"bridge": true, "host": true, "none": true}

// External is a Docker network the panel did not create.
type External struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Driver   string   `json:"driver"`
	Scope    string   `json:"scope"`
	Internal bool     `json:"internal"`
	Subnets  []string `json:"subnets"`
	Gateways []string `json:"gateways"`
	Origin   string   `json:"origin"`
	// ComposeProject is the compose project that created the network.
	ComposeProject string    `json:"composeProject,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	Containers     int       `json:"containers"`
	// ReservedOverlap: a reserved range (NETWORKS_RESERVED_RANGES) it
	// overlaps.
	ReservedOverlap string `json:"reservedOverlap,omitempty"`
}

// ExternalDetail is a Docker network the panel did not create, with its
// containers (recognised like the members of a panel network).
type ExternalDetail struct {
	External
	Members []Member `json:"members"`
}

func externalOf(n network.Network, reserved []netip.Prefix) External {
	e := External{ID: n.ID, Name: n.Name, Driver: n.Driver, Scope: n.Scope, Internal: n.Internal,
		Subnets: []string{}, Gateways: []string{}, CreatedAt: n.Created, Origin: OriginOther}
	switch {
	case dockerDefaultNetworks[n.Name]:
		e.Origin = OriginDocker
	case n.Labels["com.docker.compose.project"] != "":
		e.Origin, e.ComposeProject = OriginCompose, n.Labels["com.docker.compose.project"]
	}
	for _, c := range n.IPAM.Config {
		if c.Subnet.IsValid() {
			e.Subnets = append(e.Subnets, c.Subnet.String())
			if e.ReservedOverlap == "" {
				e.ReservedOverlap = reservedOverlap(reserved, c.Subnet.String())
			}
		}
		if c.Gateway.IsValid() {
			e.Gateways = append(e.Gateways, c.Gateway.String())
		}
	}
	return e
}

// isRegistered reports whether a Docker network is one of the panel's.
func (m *Manager) isRegistered(id, name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, n := range m.nets {
		if n.ID == id || n.DockerName == name {
			return true
		}
	}
	return false
}

// ListExternal returns the Docker networks the panel did not create, with
// their number of containers.
func (m *Manager) ListExternal(ctx context.Context) ([]External, error) {
	list, err := m.docker.ListNetworks(ctx)
	if err != nil {
		return nil, m.dockerError("list networks", err)
	}
	containers, err := m.docker.ListContainers(ctx)
	if err != nil {
		return nil, m.dockerError("list containers", err)
	}
	count := map[string]int{}
	for _, c := range containers {
		if c.NetworkSettings == nil {
			continue
		}
		for _, ep := range c.NetworkSettings.Networks {
			if ep != nil {
				count[ep.NetworkID]++
			}
		}
	}
	out := []External{}
	for _, s := range list {
		if m.isRegistered(s.ID, s.Name) {
			continue
		}
		e := externalOf(s.Network, m.cfg.Reserved)
		e.Containers = count[s.ID]
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Origin == OriginDocker) != (out[j].Origin == OriginDocker) {
			return out[i].Origin != OriginDocker // Docker's own networks last
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// GetExternal returns a Docker network the panel did not create, by ID.
func (m *Manager) GetExternal(ctx context.Context, id string) (*ExternalDetail, error) {
	if !validNetworkID(id) {
		return nil, newError(CodeNotFound, "", "network not found")
	}
	ins, err := m.docker.InspectNetworkByID(ctx, id)
	if errors.Is(err, docker.ErrNetworkNotFound) {
		return nil, newError(CodeNotFound, "", "network not found")
	}
	if err != nil {
		return nil, m.dockerError("inspect network", err)
	}
	if m.isRegistered(ins.ID, ins.Name) {
		return nil, newError(CodeNotFound, "", "network not found: it is a panel network")
	}
	all, err := m.declaredMembers(ctx)
	if err != nil {
		return nil, m.storageError(err)
	}
	members, err := m.endpoints(ctx, ins, all, "")
	if err != nil {
		return nil, err
	}
	sortMembers(members)
	d := &ExternalDetail{External: externalOf(ins.Network, m.cfg.Reserved), Members: members}
	d.Containers = len(members)
	return d, nil
}

// validNetworkID accepts a full Docker network ID (64 hexadecimal digits).
func validNetworkID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
