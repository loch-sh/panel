package handler

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"host-panel-api/internal/docker"
	"host-panel-api/internal/middleware"
	"host-panel-api/internal/model"

	"github.com/moby/moby/api/types/container"
)

const (
	composeProjectLabel   = "com.docker.compose.project"
	composeServiceLabel   = "com.docker.compose.service"
	composeDependsOnLabel = "com.docker.compose.depends_on"

	// stackActionTimeout bounds a whole stack operation. Each container stop
	// may take up to its own stop timeout (10s by default), so this leaves room
	// for large stacks while preventing a stuck daemon from hanging forever.
	stackActionTimeout = 5 * time.Minute

	maxStackNameLength = 255
)

// StackHandler handles actions on a whole Docker Compose stack.
type StackHandler struct {
	Cache  *docker.ContainerCache
	Client *docker.Client
	Logger *slog.Logger
	Audit  *slog.Logger
}

// StackActionResult is the outcome of the action for one container.
type StackActionResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Service string `json:"service"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`

	err error // raw Docker error, logged but never returned to the client
}

// StackActionResponse is returned by POST /stacks/{name}/{action}.
type StackActionResponse struct {
	Stack   string              `json:"stack"`
	Action  string              `json:"action"`
	Results []StackActionResult `json:"results"`
	Failed  int                 `json:"failed"`
}

// Action executes start, stop or restart on every container of a Compose stack
// the user can access. Containers are processed in dependency order (from the
// `com.docker.compose.depends_on` label): dependencies first for start/restart,
// dependents first for stop. Containers of the same level run concurrently.
func (h *StackHandler) Action(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	if !middleware.RequireScope(r.Context(), "containers:actions") {
		writeError(w, http.StatusForbidden, ErrInsufficientScope, "Insufficient API key scope")
		return
	}

	stack := r.PathValue("name")
	action := r.PathValue("action")

	if !validStackName(stack) {
		writeError(w, http.StatusBadRequest, ErrInvalidStackName, "Invalid stack name")
		return
	}
	// A function is started and stopped by the functions module, which
	// verifies it first.
	if model.IsFunctionContainerName(stack) {
		writeError(w, http.StatusForbidden, ErrFunctionProtected, "Function containers are managed through the functions API (/functions/{name}/{action})")
		return
	}

	var run func(ctx context.Context, id string) error
	switch action {
	case "start":
		run = h.Client.StartContainer
	case "stop":
		run = h.Client.StopContainer
	case "restart":
		run = h.Client.RestartContainer
	default:
		writeError(w, http.StatusNotAcceptable, ErrUnknownAction, "Unknown action")
		return
	}

	containers, err := h.Cache.List(r.Context())
	if err != nil {
		h.Logger.Error("failed to list containers", "error", err)
		writeError(w, http.StatusInternalServerError, ErrContainerListFailed, "Failed to list containers")
		return
	}

	targets := selectStackContainers(containers, stack, user, h.Client.SelfID())
	if len(targets) == 0 {
		writeError(w, http.StatusNotFound, ErrStackNotFound, "Stack not found")
		return
	}

	levels := orderByDependencies(targets)
	if action == "stop" {
		reverseLevels(levels)
	}

	// The operation must not be interrupted halfway if the client disconnects,
	// otherwise the stack is left in an inconsistent state.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), stackActionTimeout)
	defer cancel()

	results := runLevels(ctx, levels, action, run)
	h.Cache.Invalidate()

	resp := StackActionResponse{Stack: stack, Action: action, Results: results}
	for _, res := range results {
		if !res.OK {
			resp.Failed++
			h.Logger.Error("stack container action failed",
				"stack", stack, "action", action, "container", res.Name, "error", res.err)
		}
	}

	if resp.Failed > 0 {
		h.Logger.Error("stack action partially failed",
			"stack", stack, "action", action, "failed", resp.Failed, "total", len(results))
	}
	h.Audit.Info("stack action",
		"user", user.Email, "stack", stack, "action", action,
		"containers", len(results), "failed", resp.Failed)

	writeJSON(w, http.StatusOK, resp)
}

// validStackName accepts Compose project names; it only guards against
// obviously invalid input since the name is matched against labels, never
// used in a path or a command.
func validStackName(name string) bool {
	if name == "" || len(name) > maxStackNameLength {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// selectStackContainers returns the non-system containers of the given Compose
// project that the user can access, excluding the API's own container so a
// stack action can never stop the panel mid-request.
func selectStackContainers(containers []container.Summary, stack string, user *model.User, selfID string) []container.Summary {
	candidates := filterNonSystem(containers)
	if user.Profile == model.ProfileRestricted {
		candidates = filterByPermissions(candidates, user.Permissions)
	}

	result := make([]container.Summary, 0)
	for _, c := range candidates {
		if c.Labels[composeProjectLabel] != stack || docker.IsFunctionSummary(c) {
			continue
		}
		if selfID != "" && strings.HasPrefix(c.ID, selfID) {
			continue
		}
		result = append(result, c)
	}
	return result
}

// parseDependsOn extracts service names from the Compose depends_on label
// (format: "db:service_started:false,redis:service_healthy:true").
func parseDependsOn(label string) []string {
	if label == "" {
		return nil
	}
	var deps []string
	for part := range strings.SplitSeq(label, ",") {
		name, _, _ := strings.Cut(strings.TrimSpace(part), ":")
		if name != "" {
			deps = append(deps, name)
		}
	}
	return deps
}

// orderByDependencies groups containers into levels: every container of a
// level only depends on services of previous levels. Dependencies outside the
// selection are ignored. Cyclic dependencies end up together in a final level.
func orderByDependencies(containers []container.Summary) [][]container.Summary {
	byService := make(map[string][]container.Summary)
	for _, c := range containers {
		svc := serviceKey(c)
		byService[svc] = append(byService[svc], c)
	}

	deps := make(map[string]map[string]bool, len(byService))
	for svc, items := range byService {
		set := make(map[string]bool)
		for _, c := range items {
			for _, d := range parseDependsOn(c.Labels[composeDependsOnLabel]) {
				if _, known := byService[d]; known && d != svc {
					set[d] = true
				}
			}
		}
		deps[svc] = set
	}

	var levels [][]container.Summary
	done := make(map[string]bool, len(byService))
	for len(done) < len(byService) {
		var ready []string
		for svc, set := range deps {
			if done[svc] {
				continue
			}
			satisfied := true
			for d := range set {
				if !done[d] {
					satisfied = false
					break
				}
			}
			if satisfied {
				ready = append(ready, svc)
			}
		}

		if len(ready) == 0 {
			// Cycle: process the remaining services together.
			for svc := range deps {
				if !done[svc] {
					ready = append(ready, svc)
				}
			}
		}

		sort.Strings(ready)
		var level []container.Summary
		for _, svc := range ready {
			done[svc] = true
			level = append(level, byService[svc]...)
		}
		levels = append(levels, level)
	}
	return levels
}

// serviceKey identifies the Compose service of a container, falling back to
// its name for containers without the service label.
func serviceKey(c container.Summary) string {
	if svc := c.Labels[composeServiceLabel]; svc != "" {
		return svc
	}
	return containerName(c)
}

func containerName(c container.Summary) string {
	if len(c.Names) > 0 {
		return strings.TrimPrefix(c.Names[0], "/")
	}
	return c.ID
}

func reverseLevels(levels [][]container.Summary) {
	for i, j := 0, len(levels)-1; i < j; i, j = i+1, j-1 {
		levels[i], levels[j] = levels[j], levels[i]
	}
}

// runLevels applies run to every container, level by level. Containers inside
// a level run concurrently; a failure does not stop the remaining containers.
func runLevels(ctx context.Context, levels [][]container.Summary, action string, run func(context.Context, string) error) []StackActionResult {
	var results []StackActionResult
	for _, level := range levels {
		levelResults := make([]StackActionResult, len(level))
		var wg sync.WaitGroup
		for i, c := range level {
			levelResults[i] = StackActionResult{
				ID:      c.ID,
				Name:    containerName(c),
				Service: c.Labels[composeServiceLabel],
			}
			if skipAction(action, c.State) {
				levelResults[i].OK = true
				continue
			}
			wg.Go(func() {
				if err := run(ctx, c.ID); err != nil {
					levelResults[i].Error = "action failed"
					levelResults[i].err = err
					return
				}
				levelResults[i].OK = true
			})
		}
		wg.Wait()
		results = append(results, levelResults...)
	}
	return results
}

// skipAction avoids useless Docker calls: starting a running container or
// stopping one that is not running.
func skipAction(action string, state container.ContainerState) bool {
	switch action {
	case "start":
		return state == container.StateRunning
	case "stop":
		return state != container.StateRunning && state != container.StatePaused && state != container.StateRestarting
	}
	return false
}
