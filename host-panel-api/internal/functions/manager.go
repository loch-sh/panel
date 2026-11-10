package functions

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/network"

	"host-panel-api/internal/networks"
)

// The Manager runs the functions: creation (name reservation, quotas),
// deployments and rollbacks, configuration, secrets, invoke credentials,
// networks, start / stop and deletion. Every compose run goes through one
// choke point (apply) that re-verifies the artifacts and refuses a function
// whose compose file was edited outside the panel.

const (
	// defaultLockTimeout covers a compose up (it waits for the healthcheck)
	// so that a concurrent request waits instead of failing.
	defaultLockTimeout = 4 * time.Minute
	// opTimeout bounds an operation detached from the request (a function
	// must not be left half deployed because a client went away); it
	// includes an image pull at creation.
	opTimeout = 10 * time.Minute
	// rollbackTimeout bounds the restoration after a failure.
	rollbackTimeout = 3 * time.Minute
	// defaultInvokeConcurrency bounds the invocations in flight per
	// function from the panel (the runtime queues the others itself).
	defaultInvokeConcurrency = 2
	// invokeQueueWait is how long an invocation waits for a panel slot.
	invokeQueueWait = 5 * time.Second
	// runtimeQueueWait is the runtime's LOCH_RUNTIME_QUEUE_TIMEOUT default
	// (30 s) and invokeMargin the margin the invoke subcommand adds (5 s);
	// the panel's exec deadline adds its own margin on top.
	runtimeQueueWait = 30 * time.Second
	invokeMargin     = 5 * time.Second
	panelMargin      = 10 * time.Second

	stackDirPerm  = 0o750
	stackFilePerm = 0o640
)

const managedMarkerContent = "Created by Loch.sh Panel (functions). Do not remove: the panel only manages directories carrying this file.\n"

// upArgs: the image is pulled by the panel (by digest, with retries)
// before compose runs; compose never pulls.
var upArgs = []string{"up", "-d", "--remove-orphans", "--pull", "never", "--wait", "--wait-timeout", "120"}

// DockerAPI is the part of the Docker client used by the Manager.
type DockerAPI interface {
	ListContainers(ctx context.Context) ([]container.Summary, error)
	// InspectContainerRaw: the environment is compared with what the panel
	// rendered (the client-facing inspect masks it).
	InspectContainerRaw(ctx context.Context, id string) (container.InspectResponse, error)
	InspectImage(ctx context.Context, ref string) (image.InspectResponse, error)
	InspectNetwork(ctx context.Context, name string) (network.Inspect, error)
	PullImage(ctx context.Context, ref, platform string) error
	StopContainer(ctx context.Context, id string) error
	RemoveContainer(ctx context.Context, id string) error
	RemoveNetwork(ctx context.Context, name string) error
	DisconnectNetwork(ctx context.Context, networkID, containerID string) error
}

// NetworkRegistry is the part of the networks manager used by the Manager:
// the resolver of the compose rendering, and the creation and deletion of
// the dedicated network of a function.
type NetworkRegistry interface {
	networks.Resolver
	Create(ctx context.Context, req networks.CreateRequest, actor string) (*networks.Network, error)
	Delete(ctx context.Context, name, actor string) error
}

// RuntimeBinaries are the stored runtime binaries (RuntimeStore).
type RuntimeBinaries interface {
	RuntimeVerifier
	Latest(arch string) (RuntimeBinary, error)
	Path(version, arch string) string
}

// DigestSource reads the digest of an image on its registry
// (releases.Client).
type DigestSource interface {
	Digest(ctx context.Context, image string) (string, error)
}

// ManagerConfig configures a Manager.
type ManagerConfig struct {
	Config
	// Arch is the architecture of the Docker host ("amd64", "arm64").
	Arch string
	// MaxTotalMemoryMB and MaxTotalCPUs bound the sum of the limits of
	// every function (0: no bound).
	MaxTotalMemoryMB int
	MaxTotalCPUs     float64
	LockTimeout      time.Duration
	// ComposeSlots bounds the compose processes running at the same time
	// (shared with the other modules when set).
	ComposeSlots chan struct{}
	// InvokeConcurrency bounds the invocations in flight per function.
	InvokeConcurrency int
	// UploadConcurrency bounds the zips validated at the same time.
	UploadConcurrency int
}

// Deps are the collaborators of a Manager.
type Deps struct {
	Runner   ComposeRunner
	Docker   DockerAPI
	Networks NetworkRegistry
	Runtimes RuntimeBinaries
	Digests  DigestSource
	Invoker  Invoker
	Logger   *slog.Logger
	Audit    *slog.Logger
	// OnChange is called after every change (container cache); may be nil.
	OnChange func()
	// OnDelete is called after a function was deleted or purged (the
	// cron jobs invoking it are disabled); may be nil.
	OnDelete func(name, actor string)
}

// Manager manages the functions.
type Manager struct {
	cfg       ManagerConfig
	paths     Paths
	runner    ComposeRunner
	docker    DockerAPI
	nets      NetworkRegistry
	runtimes  RuntimeBinaries
	digests   DigestSource
	invoker   Invoker
	extractor *Extractor
	logger    *slog.Logger
	audit     *slog.Logger
	onChange  func()
	onDelete  func(name, actor string)
	now       func() time.Time
	sleep     func(ctx context.Context, d time.Duration) error
	// invokeWait is how long an invocation waits for a panel slot.
	invokeWait time.Duration

	mu    sync.Mutex
	locks map[string]*fnLock

	// budgetMu protects pending: the limits of the functions being created
	// or updated, counted by the quota checks until written.
	budgetMu sync.Mutex
	pending  map[string]Limits

	invokeMu    sync.Mutex
	invokeSlots map[string]chan struct{}

	invocations *invocationAudit

	// droppedWarnings: unknown deployment warning codes already logged.
	droppedWarnings sync.Map
}

// NewManager builds a Manager.
func NewManager(cfg ManagerConfig, d Deps) *Manager {
	if cfg.LockTimeout <= 0 {
		cfg.LockTimeout = defaultLockTimeout
	}
	if cfg.ComposeSlots == nil {
		cfg.ComposeSlots = make(chan struct{}, 2)
	}
	if cfg.InvokeConcurrency <= 0 || cfg.InvokeConcurrency > 2 {
		cfg.InvokeConcurrency = defaultInvokeConcurrency
	}
	if cfg.UploadConcurrency <= 0 {
		cfg.UploadConcurrency = 2
	}
	if cfg.DeploymentsKept <= 0 {
		cfg.DeploymentsKept = DefaultDeploymentsKept
	}
	if cfg.MaxCodeBytes <= 0 {
		cfg.MaxCodeBytes = DefaultMaxCodeBytes
	}
	if d.OnChange == nil {
		d.OnChange = func() {}
	}
	if d.OnDelete == nil {
		d.OnDelete = func(string, string) {}
	}
	m := &Manager{
		cfg: cfg, paths: cfg.Paths(), runner: d.Runner, docker: d.Docker, nets: d.Networks,
		runtimes: d.Runtimes, digests: d.Digests, invoker: d.Invoker,
		extractor: NewExtractor(DefaultZipLimits(cfg.MaxCodeBytes), cfg.UploadConcurrency),
		logger:    d.Logger, audit: d.Audit, onChange: d.OnChange, onDelete: d.OnDelete,
		now: time.Now, sleep: sleepCtx, invokeWait: invokeQueueWait,
		locks: map[string]*fnLock{}, pending: map[string]Limits{}, invokeSlots: map[string]chan struct{}{},
	}
	m.invocations = newInvocationAudit(d.Audit, m.now)
	return m
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// fnLock serializes the operations on one function. It is a channel so
// that waiting can be bounded and cancelled.
type fnLock struct {
	ch   chan struct{}
	refs int
}

// lock takes the lock of a function, waiting at most LockTimeout.
func (m *Manager) lock(ctx context.Context, n Name) (func(), error) {
	key := n.String()
	m.mu.Lock()
	l := m.locks[key]
	if l == nil {
		l = &fnLock{ch: make(chan struct{}, 1)}
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
	return nil, newError(CodeInProgress, "", "another operation is running on this function, try again later")
}

// detach returns a context that survives the client going away.
func detach(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), opTimeout)
}

// ComposeRunner runs compose with the file on stdin
// (composekit.ExecRunner.RunStdin).
type ComposeRunner interface {
	RunStdin(ctx context.Context, dir string, compose []byte, args ...string) (string, error)
}

// compose runs a compose command, holding a compose slot.
func (m *Manager) compose(ctx context.Context, dir string, compose []byte, args ...string) (string, error) {
	select {
	case m.cfg.ComposeSlots <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-m.cfg.ComposeSlots }()
	return m.runner.RunStdin(ctx, dir, compose, args...)
}

// storageError logs an internal failure and returns a generic error.
func (m *Manager) storageError(op string, err error) *Error {
	var fe *Error
	if errors.As(err, &fe) && fe.Code != CodeStorage {
		return fe
	}
	m.logger.Error("functions: "+op, "error", err)
	return newError(CodeStorage, "", "storage error")
}

// dockerError logs a Docker failure and returns a generic error.
func (m *Manager) dockerError(op string, err error) *Error {
	m.logger.Error("functions: "+op, "error", err)
	return newError(CodeDockerError, "", "Docker error")
}

// parse validates a name coming from a client.
func parse(name string) (Name, *Error) { return ParseName(name) }

// load returns the reference of an existing function.
func (m *Manager) load(n Name) (*Reference, error) {
	ref, err := m.paths.ReadReference(n, m.cfg.Config)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, newError(CodeNotFound, "", "function not found")
		}
		var fe *Error
		if errors.As(err, &fe) {
			m.logger.Error("functions: invalid reference", "function", n.String(), "error", err)
			return nil, fe
		}
		return nil, m.storageError("reading a reference", err)
	}
	for _, c := range ref.droppedWarnings {
		if _, seen := m.droppedWarnings.LoadOrStore(c, true); !seen {
			m.logger.Warn("functions: unknown deployment warning dropped (written by a newer panel?)", "function", n.String(), "warning", truncate(c, 64))
		}
	}
	return ref, nil
}

// Exists returns nil if the function exists (its reference is readable),
// a client-facing error otherwise (INVALID_FUNCTION_NAME,
// FUNCTION_NOT_FOUND, …). It reads the reference only: no Docker call.
func (m *Manager) Exists(name string) error {
	n, nerr := parse(name)
	if nerr != nil {
		return nerr
	}
	if _, err := m.load(n); err != nil {
		return err
	}
	return nil
}

// names lists the functions (their private directories with a reference).
func (m *Manager) names() ([]Name, error) {
	entries, err := os.ReadDir(m.paths.PrivateRoot())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Name
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), ProjectPrefix) {
			continue
		}
		n, perr := ParseName(strings.TrimPrefix(e.Name(), ProjectPrefix))
		if perr != nil || n.Project() != e.Name() {
			continue
		}
		out = append(out, n)
	}
	slices.SortFunc(out, func(a, b Name) int { return strings.Compare(a.s, b.s) })
	return out, nil
}

// loadAll returns the readable references (invalid ones are logged).
func (m *Manager) loadAll() ([]*Reference, error) {
	names, err := m.names()
	if err != nil {
		return nil, err
	}
	out := make([]*Reference, 0, len(names))
	for _, n := range names {
		ref, err := m.paths.ReadReference(n, m.cfg.Config)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				m.logger.Warn("functions: unreadable reference", "function", n.String(), "error", err)
			}
			continue
		}
		out = append(out, ref)
	}
	return out, nil
}

// renderConfig is the rendering configuration of a reference.
func (m *Manager) renderConfig(ref *Reference) RenderConfig {
	return RenderConfig{Paths: m.paths, Networks: m.nets,
		RuntimePath: m.runtimes.Path(ref.RuntimeBinary.Version, ref.RuntimeBinary.Arch)}
}

// modified reports whether a deployed function's compose project is not
// what the panel renders (a function never deployed has no project: one
// existing anyway was made by someone else).
func (m *Manager) modified(ref *Reference) bool {
	n, _ := ref.FunctionName()
	if ref.Deployment == nil {
		_, err := os.Lstat(m.paths.ProjectDir(n))
		return !errors.Is(err, fs.ErrNotExist)
	}
	return !ComposeIntact(ref, m.renderConfig(ref))
}

// refused records a refused verification (audit) and returns its error.
func (m *Manager) refused(n Name, actor, op, reason, code, message string) *Error {
	m.audit.Warn("function verification refused", "user", actor, "function", n.String(), "operation", op, "reason", truncate(reason, 200))
	return newError(code, "", "%s", message)
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// Startup removes what interrupted uploads left and reports the functions
// edited outside the panel. It runs before the API serves requests.
func (m *Manager) Startup(ctx context.Context) {
	if removed, err := m.paths.SweepLeftovers(); err != nil {
		m.logger.Warn("functions: removing upload leftovers", "error", err)
	} else if len(removed) > 0 {
		m.logger.Info("functions: upload leftovers removed", "count", len(removed))
	}
	refs, err := m.loadAll()
	if err != nil {
		m.logger.Error("functions: listing the functions", "error", err)
		return
	}
	for _, ref := range refs {
		if m.modified(ref) {
			m.logger.Warn("functions: compose project modified outside the panel: it is never run (purge it or restore it)", "function", ref.Name)
		}
	}
}

// RunAudit flushes the aggregated invocation entries every minute until
// ctx is done (then once more).
func (m *Manager) RunAudit(ctx context.Context) { m.invocations.run(ctx) }

// FlushAudit writes the pending aggregated invocation entries.
func (m *Manager) FlushAudit() { m.invocations.flush() }

// CloseAudit is the final flush (shutdown): invocations ending afterwards
// are audited one by one.
func (m *Manager) CloseAudit() { m.invocations.close() }
