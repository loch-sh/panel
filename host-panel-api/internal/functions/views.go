package functions

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
)

// Function states.
const (
	StateNotDeployed = "not_deployed" // created, no code yet
	StateRunning     = "running"
	StateStopped     = "stopped"
	StateMissing     = "missing"    // deployed and not stopped, but no container
	StateModified    = "modified"   // compose project edited outside the panel (never run)
	StateUnverified  = "unverified" // a container runs, but not the reference's
	StateInvalid     = "invalid"    // unreadable reference (purge only)
)

// Summary is a function in the list.
type Summary struct {
	Name           string   `json:"name"`
	Runtime        string   `json:"runtime"`
	RuntimeVersion string   `json:"runtimeVersion"`
	Handler        string   `json:"handler"`
	Limits                  // memoryMB, cpus, timeoutSeconds, tmpSizeMB
	State          string   `json:"state"`
	Health         string   `json:"health,omitempty"`
	Deployment     string   `json:"deployment,omitempty"`
	Networks       []string `json:"networks"`
	Egress         bool     `json:"egress"`
	// MCPInvocable: the loch MCP tool may invoke it (client-side guard-rail).
	MCPInvocable bool      `json:"mcpInvocable"`
	CreatedAt    time.Time `json:"createdAt"`
}

// Detail is one function. Secret values and invoke credentials are never
// part of it: secret names only.
type Detail struct {
	Summary
	Arch        string           `json:"arch"`
	Image       string           `json:"image"`
	Env         []EnvVar         `json:"env"`
	Secrets     []string         `json:"secrets"`
	Deployments []DeploymentInfo `json:"deployments"`
	// Endpoint is the Invoke URL for the members of its networks.
	Endpoint string `json:"endpoint"`
	// SigV4: SigV4 callers are accepted (the key pair is in the container).
	SigV4 bool `json:"sigv4"`
	// Problem explains a StateUnverified function.
	Problem string `json:"problem,omitempty"`
}

func (m *Manager) summary(ref *Reference, state, health string) Summary {
	s := Summary{
		Name: ref.Name, Runtime: ref.Runtime, RuntimeVersion: ref.RuntimeVersion, Handler: ref.Handler,
		Limits: ref.Limits, State: state, Health: health, Networks: slices.Clone(ref.Networks),
		Egress: ref.Egress, MCPInvocable: ref.MCPInvocable, CreatedAt: ref.CreatedAt,
	}
	if ref.Deployment != nil {
		s.Deployment = ref.Deployment.ID
	}
	return s
}

// baseState is the state known without inspecting the container.
func (m *Manager) baseState(ref *Reference) string {
	switch {
	case m.modified(ref):
		return StateModified
	case ref.Deployment == nil:
		return StateNotDeployed
	case ref.Stopped:
		return StateStopped
	}
	return ""
}

// List returns every function (state from the container list, without the
// full verification done by Get).
func (m *Manager) List(ctx context.Context) ([]Summary, error) {
	names, err := m.names()
	if err != nil {
		return nil, m.storageError("listing the functions", err)
	}
	all, err := m.docker.ListContainers(ctx)
	if err != nil {
		return nil, m.dockerError("listing containers", err)
	}
	byName := map[string]container.Summary{}
	for _, c := range all {
		for _, name := range c.Names {
			byName[strings.TrimPrefix(name, "/")] = c
		}
	}
	out := make([]Summary, 0, len(names))
	for _, n := range names {
		ref, err := m.paths.ReadReference(n, m.cfg.Config)
		if err != nil {
			out = append(out, Summary{Name: n.String(), State: StateInvalid, Networks: []string{}})
			continue
		}
		state, health := m.baseState(ref), ""
		if state == "" {
			c, ok := byName[n.Project()]
			switch {
			case !ok:
				state = StateMissing
			case c.State == container.StateRunning:
				state = StateRunning
				health = healthOf(c.Status)
			default:
				state = StateStopped
			}
		}
		out = append(out, m.summary(ref, state, health))
	}
	return out, nil
}

// healthOf extracts the health from a Docker status ("Up 3 minutes (healthy)").
func healthOf(status string) string {
	for _, h := range []string{"healthy", "unhealthy", "health: starting"} {
		if strings.Contains(status, "("+h+")") {
			return strings.TrimPrefix(h, "health: ")
		}
	}
	return ""
}

// Get returns one function, its container fully verified.
func (m *Manager) Get(ctx context.Context, name string) (*Detail, error) {
	n, nerr := parse(name)
	if nerr != nil {
		return nil, nerr
	}
	ref, err := m.load(n)
	if err != nil {
		return nil, err
	}
	return m.detail(ctx, ref), nil
}

func (m *Manager) detail(ctx context.Context, ref *Reference) *Detail {
	n, _ := ref.FunctionName()
	state, health, problem := m.baseState(ref), "", ""
	if state == "" {
		info, err := m.docker.InspectContainerRaw(ctx, n.Project())
		switch {
		case err != nil:
			state = StateMissing
		case info.State == nil || !info.State.Running:
			state = StateStopped
		default:
			if _, problem = m.verifyRunning(ctx, ref, m.renderConfig(ref)); problem != "" {
				state = StateUnverified
			} else {
				state = StateRunning
			}
			if info.State.Health != nil {
				health = string(info.State.Health.Status)
			}
		}
	}
	env := slices.Clone(ref.Env)
	if env == nil {
		env = []EnvVar{}
	}
	return &Detail{
		Summary: m.summary(ref, state, health),
		Arch:    ref.Arch, Image: ref.Image(), Env: env, Secrets: append([]string{}, ref.Secrets...),
		Deployments: deploymentInfos(ref),
		Endpoint:    "http://" + n.Project() + ":" + runtimeInvokePort + "/2015-03-31/functions/" + n.String() + "/invocations",
		SigV4:       ref.SigV4,
		Problem:     truncate(problem, 200),
	}
}

// RuntimeInfo is a runtime offered at creation.
type RuntimeInfo struct {
	ID       string   `json:"id"`
	Versions []string `json:"versions"`
	// Custom: the code is a "bootstrap" executable (provided.al2023); the
	// handler is then optional, DefaultHandler when omitted.
	Custom         bool   `json:"custom,omitempty"`
	DefaultHandler string `json:"defaultHandler,omitempty"`
}

// Info is the configuration of the module shown to clients.
type Info struct {
	Arch         string        `json:"arch"`
	Runtimes     []RuntimeInfo `json:"runtimes"`
	RuntimeReady bool          `json:"runtimeReady"` // a trusted runtime binary is installed
	// Runtime: installed version, or installation running / unavailable.
	Runtime         RuntimeState `json:"runtime"`
	Max             int          `json:"max"`
	Count           int          `json:"count"`
	MaxMemoryMB     int          `json:"maxMemoryMB"`
	MinMemoryMB     int          `json:"minMemoryMB"`
	MaxTotalMemory  int          `json:"maxTotalMemoryMB"`
	MaxTotalCPUs    float64      `json:"maxTotalCpus"`
	UsedMemoryMB    int          `json:"usedMemoryMB"`
	UsedCPUs        float64      `json:"usedCpus"`
	MaxCodeBytes    int64        `json:"maxCodeBytes"`
	DeploymentsKept int          `json:"deploymentsKept"`
	Defaults        Limits       `json:"defaults"`
	MaxTimeout      int          `json:"maxTimeoutSeconds"`
	MaxEventBytes   int          `json:"maxEventBytes"`
}

// Info returns the configuration of the module.
func (m *Manager) Info() Info {
	info := Info{
		Arch: m.cfg.Arch, Max: m.cfg.Max, MaxMemoryMB: m.cfg.MaxMemoryMB, MinMemoryMB: MinMemoryMB,
		MaxTotalMemory: m.cfg.MaxTotalMemoryMB, MaxTotalCPUs: m.cfg.MaxTotalCPUs,
		MaxCodeBytes: m.cfg.MaxCodeBytes, DeploymentsKept: m.cfg.DeploymentsKept,
		Defaults:   Limits{MemoryMB: DefaultMemoryMB, CPUs: DefaultCPUs, TimeoutSeconds: DefaultTimeoutSeconds, TmpSizeMB: m.cfg.TmpSizeMB},
		MaxTimeout: MaxTimeoutSeconds, MaxEventBytes: MaxEventBytes,
	}
	for _, id := range RuntimeIDs() {
		rt := runtimes[id]
		info.Runtimes = append(info.Runtimes, RuntimeInfo{ID: id, Versions: slices.Clone(RuntimeVersions[id]),
			Custom: rt.Custom, DefaultHandler: rt.DefaultHandler})
	}
	if sp, ok := m.runtimes.(interface{ Status() RuntimeState }); ok {
		info.Runtime = sp.Status()
	} else if b, err := m.runtimes.Latest(m.cfg.Arch); err == nil {
		info.Runtime = RuntimeState{State: "installed", Version: b.Version}
	} else {
		info.Runtime = RuntimeState{State: "unavailable"}
	}
	info.RuntimeReady = info.Runtime.State == "installed"
	if refs, err := m.loadAll(); err == nil {
		info.Count = len(refs)
		for _, r := range refs {
			info.UsedMemoryMB += r.MemoryMB
			info.UsedCPUs += r.CPUs
		}
	}
	return info
}
