package networks

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"

	"host-panel-api/internal/docker"
)

// CreateRequest describes a new network.
type CreateRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Subnet is optional: empty lets Docker allocate one.
	Subnet string `json:"subnet"`
}

func validDescription(s string) *Error {
	if len(s) > maxDescription {
		return newError(CodeInvalidName, "description", "the description is limited to %d characters", maxDescription)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return newError(CodeInvalidName, "description", "the description must not contain control characters")
		}
	}
	return nil
}

// Create creates a private network.
func (m *Manager) Create(ctx context.Context, req CreateRequest, actor string) (*Network, error) {
	if err := ValidateName(req.Name); err != nil {
		return nil, err
	}
	req.Description = strings.TrimSpace(req.Description)
	if err := validDescription(req.Description); err != nil {
		return nil, err
	}
	var subnet netip.Prefix
	if req.Subnet != "" {
		p, err := ParseSubnet(req.Subnet)
		if err != nil {
			return nil, err
		}
		if r := reservedOverlap(m.cfg.Reserved, p.String()); r != "" {
			return nil, newError(CodeInvalidSubnet, "subnet", "the subnet overlaps the reserved range %s (NETWORKS_RESERVED_RANGES)", r)
		}
		subnet = p
	}
	unlockOp, lerr := m.lockOp(ctx)
	if lerr != nil {
		return nil, lerr
	}
	defer unlockOp()
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.nets[req.Name]; exists {
		return nil, newError(CodeNameTaken, "name", "a network with this name already exists")
	}
	private := 0
	for _, n := range m.nets {
		if !n.System {
			private++
		}
	}
	if private >= m.cfg.Max {
		return nil, newError(CodeLimit, "", "the maximum number of networks is reached")
	}
	dockerName := DockerPrefix + req.Name
	// Never adopt a network by name: one created outside the panel could
	// have any configuration.
	if _, err := m.docker.InspectNetwork(ctx, dockerName); err == nil {
		return nil, newError(CodeNameTaken, "name", "a Docker network named %s already exists", dockerName)
	} else if !errors.Is(err, docker.ErrNetworkNotFound) {
		return nil, m.dockerError("inspect network", err)
	}
	id, err := m.docker.CreateInternalNetwork(ctx, dockerName, subnet, map[string]string{LabelManaged: "true", LabelNetwork: req.Name})
	if err != nil {
		if subnet.IsValid() && strings.Contains(strings.ToLower(err.Error()), "overlap") {
			return nil, newError(CodeInvalidSubnet, "subnet", "the subnet overlaps another Docker network")
		}
		if strings.Contains(strings.ToLower(err.Error()), "address pools") {
			return nil, newError(CodeInvalidSubnet, "subnet", "Docker has no free address pool: choose a subnet, or extend default-address-pools in the daemon settings")
		}
		return nil, m.dockerError("create network", err)
	}
	n := Network{Name: req.Name, DockerName: dockerName, ID: id, Description: req.Description, CreatedAt: m.now().UTC(), CreatedBy: actor}
	if ins, err := m.docker.InspectNetwork(ctx, dockerName); err == nil && ins.ID == id {
		n.Subnet, n.Gateway = subnetOf(ins)
	}
	// Docker allocates from its own pools, unaware of the ranges used
	// outside it: a subnet it chose in a reserved range is given back.
	if r := reservedOverlap(m.cfg.Reserved, n.Subnet); r != "" {
		if rerr := m.docker.RemoveNetworkByID(context.WithoutCancel(ctx), id); rerr != nil {
			m.logger.Error("networks: removing a network in a reserved range", "network", dockerName, "error", rerr)
		}
		return nil, newError(CodeInvalidSubnet, "subnet", "Docker allocated %s, which overlaps the reserved range %s: choose a subnet, or change default-address-pools in the daemon settings", n.Subnet, r)
	}
	m.nets[n.Name] = n
	if err := m.persist(); err != nil {
		delete(m.nets, n.Name)
		if rerr := m.docker.RemoveNetworkByID(context.WithoutCancel(ctx), id); rerr != nil {
			m.logger.Error("networks: removing a network not recorded", "network", dockerName, "error", rerr)
		}
		return nil, m.storageError(err)
	}
	m.audit.Info("network created", "user", actor, "network", n.Name, "subnet", n.Subnet)
	return &n, nil
}

// Update changes the description of a network (Docker cannot change the
// addressing of an existing network).
func (m *Manager) Update(name, description, actor string) (*Network, error) {
	description = strings.TrimSpace(description)
	if err := validDescription(description); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nets[name]
	if !ok {
		return nil, newError(CodeNotFound, "", "network not found")
	}
	old := n
	n.Description = description
	m.nets[name] = n
	if err := m.persist(); err != nil {
		m.nets[name] = old
		return nil, m.storageError(err)
	}
	m.audit.Info("network updated", "user", actor, "network", name)
	return &n, nil
}

// Delete removes a network without members.
func (m *Manager) Delete(ctx context.Context, name, actor string) error {
	unlockOp, lerr := m.lockOp(ctx)
	if lerr != nil {
		return lerr
	}
	defer unlockOp()
	all, err := m.declaredMembers(ctx)
	if err != nil {
		return m.storageError(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nets[name]
	if !ok {
		return newError(CodeNotFound, "", "network not found")
	}
	if n.System {
		return newError(CodeSystem, "", "the shared network cannot be deleted")
	}
	for _, d := range all {
		if slices.Contains(d.m.Networks, name) {
			return newError(CodeInUse, "", "the network still has members: detach them first")
		}
	}
	st, _, ins := m.state(ctx, n)
	if ins != nil && len(ins.Containers) > 0 {
		return newError(CodeInUse, "", "containers are still connected to the network")
	}
	if st == StateOK {
		// Docker refuses to remove a network with endpoints: a container
		// connected meanwhile keeps it.
		if err := m.docker.RemoveNetworkByID(ctx, n.ID); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "active endpoints") {
				return newError(CodeInUse, "", "containers are still connected to the network")
			}
			return m.dockerError("remove network", err)
		}
	} else if ins != nil {
		// A network replaced outside the panel is left alone: it is not ours.
		m.logger.Warn("networks: the Docker network is not the registered one, it is kept", "network", n.DockerName)
	}
	delete(m.nets, name)
	if err := m.persist(); err != nil {
		m.nets[name] = n
		return m.storageError(err)
	}
	m.audit.Info("network deleted", "user", actor, "network", name)
	return nil
}
