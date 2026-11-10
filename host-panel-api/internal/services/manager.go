package services

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"

	"host-panel-api/internal/composekit"
	"host-panel-api/internal/docker"
)

const (
	defaultMaxResources = 50
	// defaultLockTimeout covers the start of an instance (compose up waits
	// up to 3 minutes) so that a concurrent request waits instead of failing.
	defaultLockTimeout = 4 * time.Minute
	// opTimeout bounds a whole operation, detached from the request: an
	// instance must not be left half created because a client went away.
	opTimeout = 5 * time.Minute
	// listTimeout bounds the catalog reads done while listing instances,
	// listConcurrency how many run at the same time.
	listTimeout     = 10 * time.Second
	listConcurrency = 4

	dirPerm       = 0o750
	filePerm      = 0o640
	configPerm    = 0o644 // mounted config files are read by the engine user
	secretDirPerm = 0o700
	secretPerm    = 0o400 // read by the entrypoint as root, before it drops privileges
	dataPerm      = 0o700
)

const managedMarkerContent = "Created by Loch.sh Panel (services). Do not remove: the panel only manages directories carrying this file.\n"

// DockerAPI is the part of the Docker client used by the Manager.
type DockerAPI interface {
	ListContainers(ctx context.Context) ([]container.Summary, error)
	InspectContainer(ctx context.Context, id string) (container.InspectResponse, error)
	InspectImage(ctx context.Context, ref string) (image.InspectResponse, error)
	ExecRun(ctx context.Context, id string, req docker.ExecRequest) (docker.ExecResult, error)
	// RemoveContainer and RemoveNetwork remove an instance edited by hand
	// without running its compose file (Purge).
	RemoveContainer(ctx context.Context, id string) error
	RemoveNetwork(ctx context.Context, name string) error
	// DisconnectNetwork cuts a container from a network removed from its
	// instance, before the container is recreated.
	DisconnectNetwork(ctx context.Context, networkID, containerID string) error
	// PullImage pulls the image of a dedicated instance before it is
	// created (by tag, when absent) or updated (by digest). No tag is ever
	// moved: instances run their image pinned by digest.
	PullImage(ctx context.Context, ref, platform string) error
}

// Config configures a Manager.
type Config struct {
	StacksDir               string // shared with hosting and CI
	Render                  RenderConfig
	MaxResourcesPerInstance int  // 0 = 50
	MaxDedicatedInstances   int  // per dedicated engine; 0 = 20
	ArchiveOnRetire         bool // rename the data directory instead of deleting it
	LockTimeout             time.Duration
	// ComposeSlots bounds the compose processes running at the same time. It
	// should be shared with hosting; nil gives the Manager its own (2).
	ComposeSlots chan struct{}

	// Exposure bounds the published ports chosen in the panel.
	Exposure ExposurePolicy
	TLS      TLSMode // TLSAuto when empty
	// PublicHost is shown to clients and covered by the certificates.
	PublicHost string
	// Certificate is used instead of self-signed ones when set.
	Certificate *Certificate

	// Releases reads the registries and endoflife.date (nil: the versions
	// are never discovered, the built-in catalogs are used).
	Releases ReleaseSource
}

// Manager creates, lists and deletes instances and their resources.
type Manager struct {
	cfg      Config
	reg      *Registry
	runner   composekit.ComposeRunner
	docker   DockerAPI
	logger   *slog.Logger
	audit    *slog.Logger
	onChange func()
	now      func() time.Time
	chown    func(path string, uid, gid int) error

	mu    sync.Mutex
	locks map[string]*instanceLock

	checkMu   sync.Mutex
	lastCheck time.Time // last discovery started (manual checks are throttled)
}

// NewManager builds a Manager. onChange is called after every change (e.g.
// to invalidate the container cache); it may be nil.
func NewManager(cfg Config, reg *Registry, runner composekit.ComposeRunner, dock DockerAPI, logger, audit *slog.Logger, onChange func()) *Manager {
	if cfg.MaxResourcesPerInstance <= 0 {
		cfg.MaxResourcesPerInstance = defaultMaxResources
	}
	if cfg.MaxDedicatedInstances <= 0 {
		cfg.MaxDedicatedInstances = defaultMaxDedicated
	}
	if cfg.LockTimeout <= 0 {
		cfg.LockTimeout = defaultLockTimeout
	}
	if cfg.ComposeSlots == nil {
		cfg.ComposeSlots = make(chan struct{}, 2)
	}
	if cfg.TLS == "" {
		cfg.TLS = TLSAuto
	}
	if onChange == nil {
		onChange = func() {}
	}
	return &Manager{
		cfg: cfg, reg: reg, runner: runner, docker: dock,
		logger: logger, audit: audit, onChange: onChange,
		now: time.Now, chown: os.Lchown,
		locks: map[string]*instanceLock{},
	}
}

// Registry returns the enabled engines and versions.
func (m *Manager) Registry() *Registry { return m.reg }

// instanceLock serializes the operations on one instance. It is a channel so
// that waiting can be bounded and cancelled.
type instanceLock struct {
	ch   chan struct{}
	refs int
}

// lock takes the lock of an instance, waiting at most LockTimeout.
func (m *Manager) lock(ctx context.Context, key string) (func(), error) {
	m.mu.Lock()
	l := m.locks[key]
	if l == nil {
		l = &instanceLock{ch: make(chan struct{}, 1)}
		m.locks[key] = l
	}
	l.refs++
	m.mu.Unlock()

	release := func() {
		m.mu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(m.locks, key)
		}
		m.mu.Unlock()
	}
	timer := time.NewTimer(m.cfg.LockTimeout)
	defer timer.Stop()
	select {
	case l.ch <- struct{}{}:
		return func() { <-l.ch; release() }, nil
	case <-timer.C:
	case <-ctx.Done():
	}
	release()
	return nil, newError(CodeInProgress, "", "another operation is running on this instance, try again later")
}

// detach returns a context that survives the client going away.
func detach(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), opTimeout)
}

// compose runs a compose command, holding a compose slot.
func (m *Manager) compose(ctx context.Context, dir string, args ...string) (string, error) {
	select {
	case m.cfg.ComposeSlots <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-m.cfg.ComposeSlots }()
	return m.runner.Run(ctx, dir, args...)
}

var (
	upArgs   = []string{"up", "-d", "--remove-orphans", "--pull", "missing", "--wait", "--wait-timeout", "180"}
	stopArgs = []string{"stop"}
	downArgs = []string{"down", "--remove-orphans"}
)
