package networks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
)

// Docker is the subset of the Docker client used by the manager.
type Docker interface {
	InspectNetwork(ctx context.Context, name string) (network.Inspect, error)
	CreateInternalNetwork(ctx context.Context, name string, subnet netip.Prefix, labels map[string]string) (string, error)
	RemoveNetworkByID(ctx context.Context, id string) error
	DisconnectNetwork(ctx context.Context, networkID, containerID string) error
	ListContainers(ctx context.Context) ([]container.Summary, error)
	IsSelf(ctx context.Context, ref string) bool
	// ListNetworks and InspectNetworkByID read the networks the panel did
	// not create (shown read-only).
	ListNetworks(ctx context.Context) ([]network.Summary, error)
	InspectNetworkByID(ctx context.Context, id string) (network.Inspect, error)
}

// DeclaredMember is a site or an instance with the networks it declares.
type DeclaredMember struct {
	Name    string
	Project string // compose project of its containers
	// Container is the name of its container: Docker keeps container names
	// unique, unlike labels (any container can carry a compose label).
	Container string
	Networks  []string
}

// MemberSource is a kind of member managed by the panel (hosted sites,
// service instances).
type MemberSource interface {
	// Members returns every managed member with its declared networks.
	Members(ctx context.Context) ([]DeclaredMember, error)
	// SetNetworks replaces the networks of a member: its compose file is
	// rendered again and the container recreated. It may return the ID of
	// an asynchronous operation.
	SetNetworks(ctx context.Context, name string, networks []string, actor string) (string, error)
}

// Config holds the settings of the module.
type Config struct {
	// File is the registry path (DATA_DIR/networks.json).
	File string
	// Max is the maximum number of private networks (NETWORKS_MAX).
	Max int
	// Reserved are ranges used outside Docker (LAN, VPN, static routes:
	// NETWORKS_RESERVED_RANGES) that no panel network may overlap: Docker
	// only knows its own networks.
	Reserved []netip.Prefix
}

// Manager owns the registry and the Docker networks it describes.
type Manager struct {
	// opMu serializes the operations that change memberships or networks
	// (create, delete, attach, detach): a network cannot be removed while a
	// member is being attached to it. It is a channel so that waiting is
	// bounded (lockOp). Lock order: opMu, then a member's own lock (a
	// member source never calls back into an operation taking opMu while
	// holding its lock).
	opMu    chan struct{}
	mu      sync.RWMutex
	cfg     Config
	nets    map[string]Network
	docker  Docker
	sources map[string]MemberSource
	logger  *slog.Logger
	audit   *slog.Logger
	now     func() time.Time
}

// NewManager loads the registry.
func NewManager(cfg Config, dock Docker, logger, audit *slog.Logger) (*Manager, error) {
	m := &Manager{opMu: make(chan struct{}, 1), cfg: cfg, nets: map[string]Network{}, docker: dock, sources: map[string]MemberSource{},
		logger: logger, audit: audit, now: time.Now}
	data, err := os.ReadFile(cfg.File)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("reading %s: %w", cfg.File, err)
	case len(data) > 0:
		var list []Network
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, fmt.Errorf("decoding %s: %w", cfg.File, err)
		}
		for _, n := range list {
			if n.System {
				if n.Name != SharedName {
					return nil, fmt.Errorf("%s: unknown system network %q", cfg.File, n.Name)
				}
			} else if ValidateName(n.Name) != nil || n.DockerName != DockerPrefix+n.Name {
				return nil, fmt.Errorf("%s: invalid network %q", cfg.File, n.Name)
			}
			m.nets[n.Name] = n
		}
	}
	return m, nil
}

// AddSource registers a kind of member.
func (m *Manager) AddSource(kind string, s MemberSource) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sources[kind] = s
}

// persist writes the registry atomically. Called with m.mu held.
func (m *Manager) persist() error {
	list := make([]Network, 0, len(m.nets))
	for _, n := range m.nets {
		list = append(list, n)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(m.cfg.File)
	tmp, err := os.CreateTemp(dir, ".networks-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), m.cfg.File)
}

func (m *Manager) storageError(err error) *Error {
	m.logger.Error("networks: storage error", "error", err)
	return &Error{Code: CodeStorage, Message: "storage error"}
}

func (m *Manager) dockerError(op string, err error) *Error {
	m.logger.Error("networks: docker error", "op", op, "error", err)
	return &Error{Code: CodeDockerError, Message: "the Docker operation failed: " + op}
}

// Lookup returns the rendering reference of a registered network.
func (m *Manager) Lookup(name string) (Ref, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n, ok := m.nets[name]
	return n.Ref(), ok
}

// Shared returns the registry name of the shared network, if registered.
func (m *Manager) Shared() (string, bool) {
	_, ok := m.Lookup(SharedName)
	return SharedName, ok
}

// opLockTimeout bounds the wait for another network operation (an attach
// recreates its member: up to a few minutes).
var opLockTimeout = 5 * time.Minute

// lockOp takes opMu, waiting at most opLockTimeout.
func (m *Manager) lockOp(ctx context.Context) (func(), error) {
	t := time.NewTimer(opLockTimeout)
	defer t.Stop()
	select {
	case m.opMu <- struct{}{}:
		return func() { <-m.opMu }, nil
	case <-t.C:
	case <-ctx.Done():
	}
	return nil, newError(CodeBusy, "", "another network operation is running, try again later")
}
