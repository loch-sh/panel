package networks

import (
	"context"
	"slices"
	"sort"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
)

// Summary is a network of the list.
type Summary struct {
	Network
	State   string `json:"state"`
	Problem string `json:"problem,omitempty"`
	Members int    `json:"members"`
	// ReservedOverlap is the reserved range (NETWORKS_RESERVED_RANGES) the
	// subnet overlaps: set after the ranges changed.
	ReservedOverlap string `json:"reservedOverlap,omitempty"`
}

// List returns every registered network with its state.
func (m *Manager) List(ctx context.Context) ([]Summary, error) {
	m.mu.RLock()
	list := make([]Network, 0, len(m.nets))
	for _, n := range m.nets {
		list = append(list, n)
	}
	m.mu.RUnlock()
	sort.Slice(list, func(i, j int) bool {
		if list[i].System != list[j].System {
			return list[i].System
		}
		return list[i].Name < list[j].Name
	})
	out := make([]Summary, 0, len(list))
	for _, n := range list {
		st, problem, ins := m.state(ctx, n)
		s := Summary{Network: n, State: st, Problem: problem, ReservedOverlap: reservedOverlap(m.cfg.Reserved, n.Subnet)}
		if ins != nil {
			s.Members = len(ins.Containers)
		}
		out = append(out, s)
	}
	return out, nil
}

// Member is a container on a network, or a member declared but absent.
type Member struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Container   string `json:"container,omitempty"`
	ContainerID string `json:"containerId,omitempty"`
	State       string `json:"state,omitempty"`
	IPv4        string `json:"ipv4,omitempty"`
	MAC         string `json:"mac,omitempty"`
	// Expected: declared by its compose file or reference; Present: on the
	// Docker network now.
	Expected bool `json:"expected"`
	Present  bool `json:"present"`
}

// Detail is a network with its members.
type Detail struct {
	Summary
	Internal bool     `json:"internal"`
	Members  []Member `json:"members"`
}

type declared struct {
	kind string
	m    DeclaredMember
}

// declaredMembers returns every managed member, by container name.
func (m *Manager) declaredMembers(ctx context.Context) (map[string]declared, error) {
	m.mu.RLock()
	sources := make(map[string]MemberSource, len(m.sources))
	for k, s := range m.sources {
		sources[k] = s
	}
	m.mu.RUnlock()
	out := map[string]declared{}
	for kind, s := range sources {
		list, err := s.Members(ctx)
		if err != nil {
			return nil, err
		}
		for _, d := range list {
			out[d.Container] = declared{kind: kind, m: d}
		}
	}
	return out, nil
}

// Get returns a network with its members.
func (m *Manager) Get(ctx context.Context, name string) (*Detail, error) {
	m.mu.RLock()
	n, ok := m.nets[name]
	m.mu.RUnlock()
	if !ok {
		return nil, newError(CodeNotFound, "", "network not found")
	}
	st, problem, ins := m.state(ctx, n)
	d := &Detail{Summary: Summary{Network: n, State: st, Problem: problem, ReservedOverlap: reservedOverlap(m.cfg.Reserved, n.Subnet)}, Members: []Member{}}
	all, err := m.declaredMembers(ctx)
	if err != nil {
		return nil, m.storageError(err)
	}
	present := map[string]bool{}
	if ins != nil {
		d.Internal = ins.Internal
		d.Summary.Members = len(ins.Containers)
		members, err := m.endpoints(ctx, *ins, all, name)
		if err != nil {
			return nil, err
		}
		for _, mem := range members {
			if mem.Expected {
				present[all[mem.Container].m.Container] = true
			}
		}
		d.Members = members
	}
	for container, dm := range all {
		if slices.Contains(dm.m.Networks, name) && !present[container] {
			d.Members = append(d.Members, Member{Kind: dm.kind, Name: dm.m.Name, Expected: true})
		}
	}
	sortMembers(d.Members)
	return d, nil
}

// endpoints returns the containers of a Docker network, recognised by their
// container name (a member is expected when it declares the registry
// network named name; "" for a network the panel did not create).
func (m *Manager) endpoints(ctx context.Context, ins network.Inspect, all map[string]declared, name string) ([]Member, error) {
	containers, err := m.docker.ListContainers(ctx)
	if err != nil {
		return nil, m.dockerError("list containers", err)
	}
	byID := map[string]container.Summary{}
	for _, c := range containers {
		byID[c.ID] = c
	}
	out := make([]Member, 0, len(ins.Containers))
	for id, ep := range ins.Containers {
		mem := Member{Container: ep.Name, ContainerID: shortID(id), Present: true, Kind: KindUnmanaged, Name: ep.Name}
		if ep.IPv4Address.IsValid() {
			mem.IPv4 = ep.IPv4Address.Addr().String()
		}
		if len(ep.MacAddress) > 0 {
			mem.MAC = ep.MacAddress.String()
		}
		if c, known := byID[id]; known {
			mem.State = string(c.State)
		}
		if dm, ok := all[ep.Name]; ok {
			mem.Kind, mem.Name = dm.kind, dm.m.Name
			mem.Expected = name != "" && slices.Contains(dm.m.Networks, name)
		} else if m.docker.IsSelf(ctx, id) {
			mem.Kind, mem.Name = KindPanel, ep.Name
		}
		out = append(out, mem)
	}
	return out, nil
}

func sortMembers(list []Member) {
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})
}

// Limits returns the creation rules.
func (m *Manager) Limits() Limits {
	m.mu.RLock()
	count := 0
	for _, n := range m.nets {
		if !n.System {
			count++
		}
	}
	m.mu.RUnlock()
	l := Limits{Max: m.cfg.Max, Count: count, MinPrefix: minPrefix, MaxPrefix: maxPrefix, ReservedRanges: []string{}, PrivateRanges: []string{}}
	for _, r := range m.cfg.Reserved {
		l.ReservedRanges = append(l.ReservedRanges, r.String())
	}
	for _, r := range privateRanges {
		l.PrivateRanges = append(l.PrivateRanges, r.String())
	}
	return l
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
