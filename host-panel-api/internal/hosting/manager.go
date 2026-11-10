package hosting

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
)

const (
	composeProjectLabel = "com.docker.compose.project"
	operationTimeout    = 15 * time.Minute
	rollbackTimeout     = 10 * time.Minute
	// maxConcurrentOps bounds the compose processes running at the same time
	// across all stacks (image pulls are heavy).
	maxConcurrentOps = 4
)

// ContainerLister lists all containers (running or not, system included).
type ContainerLister interface {
	ListContainers(ctx context.Context) ([]container.Summary, error)
}

// Config configures a Manager.
type Config struct {
	StacksDir string // shared with CI stacks, e.g. /data/compose
	Render    RenderConfig
	RunUID    int
	RunGID    int
	Limits    Limits
}

// Manager creates, updates, deploys and deletes hosted stacks.
type Manager struct {
	cfg      Config
	runner   ComposeRunner
	modules  ModuleInspector
	docker   ContainerLister
	logger   *slog.Logger
	audit    *slog.Logger
	onChange func()

	// mu serializes the check-then-write sequences (name, domain and site
	// reservations) so two concurrent requests cannot claim the same resource.
	mu sync.Mutex

	opMu sync.Mutex
	ops  map[string]*Operation // last operation per stack name

	sem chan struct{} // limits concurrent operations
}

// NewManager builds a Manager. onChange is called after every operation
// (e.g. to invalidate the container cache); it may be nil.
func NewManager(cfg Config, runner ComposeRunner, docker ContainerLister, modules ModuleInspector, logger, audit *slog.Logger, onChange func()) *Manager {
	if onChange == nil {
		onChange = func() {}
	}
	m := &Manager{
		cfg: cfg, runner: runner, docker: docker, modules: modules,
		logger: logger, audit: audit, onChange: onChange,
		ops: make(map[string]*Operation),
		sem: make(chan struct{}, maxConcurrentOps),
	}
	m.recoverInterrupted()
	return m
}

func groupByProject(containers []container.Summary) map[string][]container.Summary {
	out := make(map[string][]container.Summary)
	for _, c := range containers {
		if p := c.Labels[composeProjectLabel]; strings.HasPrefix(p, StackPrefix) {
			out[p] = append(out[p], c)
		}
	}
	return out
}

func containerName(c container.Summary) string {
	if len(c.Names) == 0 {
		return ""
	}
	return strings.TrimPrefix(c.Names[0], "/")
}
