package functions

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"host-panel-api/internal/composekit"
	"host-panel-api/internal/docker"
	"host-panel-api/internal/networks"
)

// CreateRequest describes a new function. Zero limits take the defaults.
type CreateRequest struct {
	Name           string   `json:"name"`
	Runtime        string   `json:"runtime"`
	RuntimeVersion string   `json:"runtimeVersion"`
	Handler        string   `json:"handler"`
	MemoryMB       int      `json:"memoryMB,omitempty"`
	CPUs           float64  `json:"cpus,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
	TmpSizeMB      int      `json:"tmpSizeMB,omitempty"`
	Env            []EnvVar `json:"env,omitempty"`
	// Networks are joined besides the function's own network fn-<name>.
	Networks []string `json:"networks,omitempty"`
	Egress   bool     `json:"egress,omitempty"`
	// SigV4 also accepts SigV4 callers (AWS SDKs); off by default: the key
	// pair is then readable by the function code.
	SigV4 bool `json:"sigv4,omitempty"`
	// MCPInvocable opts the function in to invocation by the loch MCP
	// tool (checked by the MCP client; off by default).
	MCPInvocable bool `json:"mcpInvocable,omitempty"`
}

// Created is the answer to a creation: the invoke credentials are shown
// this once.
type Created struct {
	Function    *Detail      `json:"function"`
	Credentials *Credentials `json:"credentials"`
}

// checkNetworks validates the networks of a function: at least one, never
// the services network, registered and verified.
func (m *Manager) checkNetworks(ctx context.Context, names []string, own string) ([]string, *Error) {
	sorted := slices.Sorted(slices.Values(names))
	if len(sorted) == 0 {
		return nil, newError(CodeInvalidNetwork, "networks", "a function keeps at least one network")
	}
	if len(sorted) > 16 {
		return nil, newError(CodeInvalidNetwork, "networks", "at most 16 networks")
	}
	for i, name := range sorted {
		if i > 0 && sorted[i-1] == name {
			return nil, newError(CodeInvalidNetwork, "networks", "network %s listed twice", quoteName(name))
		}
		if name == networks.SharedName {
			return nil, newError(CodeInvalidNetwork, "networks", "a function never joins the services network: attach it to a private network")
		}
		if name == own {
			continue // created (or verified) by the caller
		}
		if _, ok := m.nets.Lookup(name); !ok {
			return nil, newError(CodeInvalidNetwork, "networks", "unknown network %s", quoteName(name))
		}
		if _, ok := m.nets.Verify(ctx, name); !ok {
			return nil, newError(CodeInvalidNetwork, "networks", "network %s is missing or is not the one the panel created", quoteName(name))
		}
	}
	return sorted, nil
}

// reserve checks the number of functions and the global memory / CPU
// budget with the limits of a function being created or updated, and
// holds them until release (concurrent operations count each other).
func (m *Manager) reserve(n Name, l Limits, creating bool) (func(), *Error) {
	m.budgetMu.Lock()
	defer m.budgetMu.Unlock()
	refs, err := m.loadAll()
	if err != nil {
		return nil, m.storageError("listing the functions", err)
	}
	totals := map[string]Limits{}
	for _, r := range refs {
		totals[r.Name] = r.Limits
	}
	for name, p := range m.pending {
		totals[name] = p
	}
	if _, exists := totals[n.String()]; creating && exists {
		return nil, newError(CodeExists, "name", "a function with this name already exists")
	}
	if creating && len(totals) >= m.cfg.Max {
		return nil, newError(CodeLimit, "", "the maximum number of functions (%d) is reached", m.cfg.Max)
	}
	// An update that does not raise the limits of the function is always
	// allowed (the budget may have been lowered since).
	if cur, ok := totals[n.String()]; ok && !creating && l.MemoryMB <= cur.MemoryMB && l.CPUs <= cur.CPUs {
		if _, busy := m.pending[n.String()]; busy {
			return nil, newError(CodeInProgress, "", "another operation is running on this function, try again later")
		}
		m.pending[n.String()] = l
		return func() {
			m.budgetMu.Lock()
			delete(m.pending, n.String())
			m.budgetMu.Unlock()
		}, nil
	}
	totals[n.String()] = l
	mem, cpus := 0, 0.0
	for _, t := range totals {
		mem += t.MemoryMB
		cpus += t.CPUs
	}
	if m.cfg.MaxTotalMemoryMB > 0 && mem > m.cfg.MaxTotalMemoryMB {
		return nil, newError(CodeBudget, "memoryMB", "the functions would use %d MB of memory, over the budget of %d MB", mem, m.cfg.MaxTotalMemoryMB)
	}
	if m.cfg.MaxTotalCPUs > 0 && math.Round(cpus*100) > math.Round(m.cfg.MaxTotalCPUs*100) {
		return nil, newError(CodeBudget, "cpus", "the functions would use %.2f CPUs, over the budget of %.2f", cpus, m.cfg.MaxTotalCPUs)
	}
	if _, busy := m.pending[n.String()]; busy {
		return nil, newError(CodeInProgress, "", "another operation is running on this function, try again later")
	}
	m.pending[n.String()] = l
	return func() {
		m.budgetMu.Lock()
		delete(m.pending, n.String())
		m.budgetMu.Unlock()
	}, nil
}

// checkNameFree refuses a name already used by a directory, a container, a
// compose project or (network) a network (the panel never adopts anything
// by name).
func (m *Manager) checkNameFree(ctx context.Context, n Name, network bool) *Error {
	if _, err := os.Lstat(filepath.Join(m.paths.Private(n), ReferenceFile)); err == nil {
		return newError(CodeExists, "name", "a function with this name already exists")
	}
	for _, dir := range []string{m.paths.Private(n), m.paths.FunctionData(n), m.paths.ProjectDir(n)} {
		if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
			return newError(CodeNameConflict, "name", "a directory with the function name already exists")
		}
	}
	all, err := m.docker.ListContainers(ctx)
	if err != nil {
		return m.dockerError("listing containers", err)
	}
	for _, c := range all {
		if c.Labels[composeProjectLabel] == n.Project() || c.Labels[docker.FunctionLabel] == n.String() {
			return newError(CodeNameConflict, "name", "a container or compose project already uses the function name")
		}
		for _, name := range c.Names {
			if name == "/"+n.Project() {
				return newError(CodeNameConflict, "name", "a container already uses the function name")
			}
		}
	}
	if _, ok := m.nets.Lookup(n.DedicatedNetwork()); ok && network {
		return newError(CodeNameConflict, "name", "a network named %s already exists", n.DedicatedNetwork())
	}
	if _, err := m.docker.InspectNetwork(ctx, n.Project()+"_"+egressNetworkKey); err == nil {
		return newError(CodeNameConflict, "name", "a Docker network of the function name already exists")
	}
	return nil
}

// Create reserves a function: validated configuration, pinned image,
// dedicated network, private directory with its reference, empty secrets
// and generated invoke credentials. It runs nothing until code is
// deployed. The credentials are returned once.
func (m *Manager) Create(ctx context.Context, req CreateRequest, actor string) (*Created, error) {
	n, nerr := parse(req.Name)
	if nerr != nil {
		return nil, nerr
	}
	rt, ok := LookupRuntime(req.Runtime)
	if !ok {
		return nil, newError(CodeInvalidRuntime, "runtime", "unsupported runtime %s", quoteName(req.Runtime))
	}
	if !slices.Contains(RuntimeVersions[rt.ID], req.RuntimeVersion) {
		return nil, newError(CodeInvalidRuntime, "runtimeVersion", "unsupported %s version %s", rt.ID, quoteName(req.RuntimeVersion))
	}
	if !ValidArch(m.cfg.Arch) {
		return nil, newError(CodeInvalidRuntime, "", "the Docker host architecture is not supported")
	}
	handler := rt.HandlerOrDefault(req.Handler)
	if err := rt.ValidateHandler(handler); err != nil {
		return nil, err
	}
	limits := Limits{MemoryMB: req.MemoryMB, CPUs: req.CPUs, TimeoutSeconds: req.TimeoutSeconds, TmpSizeMB: req.TmpSizeMB}
	if limits.MemoryMB == 0 {
		limits.MemoryMB = DefaultMemoryMB
	}
	if limits.CPUs == 0 {
		limits.CPUs = DefaultCPUs
	}
	if limits.TimeoutSeconds == 0 {
		limits.TimeoutSeconds = DefaultTimeoutSeconds
	}
	if limits.TmpSizeMB == 0 {
		limits.TmpSizeMB = m.cfg.TmpSizeMB
		if limits.TmpSizeMB == 0 {
			limits.TmpSizeMB = DefaultTmpSizeMB
		}
	}
	if err := ValidateLimits(limits, m.cfg.Config); err != nil {
		return nil, err
	}
	env := slices.Clone(req.Env)
	if err := ValidateEnv(env, nil); err != nil {
		return nil, err
	}
	own := n.DedicatedNetwork()
	nets, cerr := m.checkNetworks(ctx, append(slices.Clone(req.Networks), own), own)
	if cerr != nil {
		return nil, cerr
	}
	binary, err := m.runtimes.Latest(m.cfg.Arch)
	if err != nil {
		return nil, err
	}

	ctx, cancel := detach(ctx)
	defer cancel()
	// Early refusal before anything is pulled or created.
	if err := m.checkNameFree(ctx, n, true); err != nil {
		return nil, err
	}
	// The image is pulled before anything is created (a pull failure
	// leaves nothing behind).
	tag, digest, err := m.resolveImage(ctx, rt, req.RuntimeVersion)
	if err != nil {
		return nil, err
	}
	imageID, err := m.pullImage(ctx, rt, tag, digest)
	if err != nil {
		return nil, err
	}
	creds, err := newCredentials(n, req.SigV4)
	if err != nil {
		return nil, m.storageError("generating credentials", err)
	}
	ref := &Reference{
		Version: ReferenceVersion, Name: n.String(), Runtime: rt.ID, RuntimeVersion: req.RuntimeVersion,
		Arch: m.cfg.Arch, ImageTag: tag, ImageDigest: digest, ImageID: imageID,
		Entrypoint: slices.Clone(rt.Entrypoint), Handler: handler, Limits: limits,
		Env: env, Secrets: []string{}, Networks: nets, Egress: req.Egress, SigV4: req.SigV4,
		MCPInvocable:  req.MCPInvocable,
		RuntimeBinary: binary, CreatedAt: m.now().UTC(),
	}
	ref.Sort()
	if err := ref.Validate(m.cfg.Config); err != nil {
		return nil, err
	}

	// The dedicated network first, without the function lock (lock order:
	// networks, then function): its creation is atomic, so it also
	// reserves the name against a concurrent creation.
	if _, err := m.nets.Create(ctx, networks.CreateRequest{Name: own, Description: "Network of the function " + n.String()}, actor); err != nil {
		if ne, ok := networks.AsError(err); ok && ne.Code == networks.CodeNameTaken {
			return nil, newError(CodeNameConflict, "name", "a network named %s already exists", own)
		}
		m.logger.Error("functions: creating the dedicated network", "function", n.String(), "error", err)
		return nil, newError(CodeDockerError, "", "the network of the function could not be created")
	}
	if werr := m.createLocked(ctx, n, ref, creds); werr != nil {
		// After the function lock is released.
		if err := m.nets.Delete(context.WithoutCancel(ctx), own, actor); err != nil {
			m.logger.Error("functions: removing the network of a function not created", "function", n.String(), "error", err)
		}
		return nil, werr
	}
	m.audit.Info("function created", "user", actor, "function", n.String(), "runtime", rt.ID+" "+req.RuntimeVersion,
		"image", rt.ImageRef(tag, digest), "memory_mb", limits.MemoryMB, "cpus", limits.CPUs,
		"networks", strings.Join(nets, ","), "egress", req.Egress)
	if req.Egress {
		m.audit.Warn("function egress toggled", "user", actor, "function", n.String(), "enabled", true)
	}
	m.onChange()
	return &Created{Function: m.detail(ctx, ref), Credentials: creds}, nil
}

// createLocked writes the files of a new function under its lock, within
// the quotas. Nothing is left behind on failure.
func (m *Manager) createLocked(ctx context.Context, n Name, ref *Reference, creds *Credentials) error {
	unlock, err := m.lock(ctx, n)
	if err != nil {
		return err
	}
	defer unlock()
	release, rerr := m.reserve(n, ref.Limits, true)
	if rerr != nil {
		return rerr
	}
	defer release()
	if err := m.checkNameFree(ctx, n, false); err != nil {
		return err
	}
	fail := func(e error) error {
		if err := m.paths.RemoveFunctionDirs(n); err != nil {
			m.logger.Error("functions: removing the directories of a function not created", "function", n.String(), "error", err)
		}
		return e
	}
	if err := m.paths.EnsureFunctionDirs(n); err != nil {
		return fail(m.storageError("creating the function directories", err))
	}
	if err := m.paths.WriteSecretsEnv(n, nil, map[string]string{}); err != nil {
		return fail(m.storageError("writing the secrets", err))
	}
	if err := m.paths.writePrivateFile(n, CredentialsEnvFile, renderCredentialsEnv(creds), secretPerm); err != nil {
		return fail(m.storageError("writing the credentials", err))
	}
	// The reference last: it is what makes the function exist.
	if err := m.paths.WriteReference(*ref, m.cfg.Config); err != nil {
		return fail(m.storageError("writing the reference", err))
	}
	return nil
}

// DeleteResult reports what a deletion could not remove.
type DeleteResult struct {
	Containers int `json:"containers"`
	// NetworkKept: the dedicated network still has members (attached by
	// hand or other members): it is kept.
	NetworkKept bool `json:"networkKept,omitempty"`
}

// Delete removes a function that was not edited outside the panel.
func (m *Manager) Delete(ctx context.Context, name, actor string) (*DeleteResult, error) {
	return m.remove(ctx, name, actor, false)
}

// Purge removes a function edited outside the panel (or whose reference
// is unreadable), without running any of its files.
func (m *Manager) Purge(ctx context.Context, name, actor string) (*DeleteResult, error) {
	return m.remove(ctx, name, actor, true)
}

func (m *Manager) remove(ctx context.Context, name, actor string, purge bool) (*DeleteResult, error) {
	n, nerr := parse(name)
	if nerr != nil {
		return nil, nerr
	}
	ctx, cancel := detach(ctx)
	defer cancel()
	res, err := m.removeLocked(ctx, n, actor, purge)
	if err != nil {
		return nil, err
	}
	m.forgetSlots(n)
	// The dedicated network, once the function no longer declares it and
	// its lock is released (lock order: networks, then function).
	if _, ok := m.nets.Lookup(n.DedicatedNetwork()); ok {
		if err := m.nets.Delete(ctx, n.DedicatedNetwork(), actor); err != nil {
			m.logger.Warn("functions: the dedicated network is kept", "function", n.String(), "error", err)
			res.NetworkKept = true
		}
	}
	event := "function deleted"
	if purge {
		event = "function purged"
	}
	m.audit.Warn(event, "user", actor, "function", n.String(), "containers", res.Containers, "network_kept", res.NetworkKept)
	m.onDelete(n.String(), actor)
	m.onChange()
	return res, nil
}

// removeLocked removes everything of a function but its network, under its
// lock.
func (m *Manager) removeLocked(ctx context.Context, n Name, actor string, purge bool) (*DeleteResult, error) {
	unlock, err := m.lock(ctx, n)
	if err != nil {
		return nil, err
	}
	defer unlock()
	ref, lerr := m.load(n)
	broken := false
	if lerr != nil {
		if CodeOf(lerr) == CodeNotFound {
			if _, err := os.Lstat(m.paths.Private(n)); errors.Is(err, fs.ErrNotExist) {
				return nil, lerr
			}
		}
		// An unreadable reference: only a purge removes it.
		broken = true
	}
	modified := broken || m.modified(ref)
	if purge && !modified {
		return nil, newError(CodeNotModified, "", "the function was not modified outside the panel: delete it instead")
	}
	if !purge && modified {
		return nil, newError(CodeModified, "", "the function was modified outside the panel: purge it")
	}
	res := &DeleteResult{}
	removed, err := m.removeContainers(ctx, n)
	res.Containers = removed
	if err != nil {
		m.logger.Error("functions: removing a container", "function", n.String(), "error", err)
		return nil, newError(CodeDockerError, "", "the containers of the function could not be removed")
	}
	partial := func(step string) {
		m.audit.Warn("function deletion incomplete", "user", actor, "function", n.String(), "containers", removed, "failed_step", step)
	}
	if err := m.docker.RemoveNetwork(ctx, n.Project()+"_"+egressNetworkKey); err != nil {
		m.logger.Warn("functions: removing the egress network", "function", n.String(), "error", err)
	}
	// The compose project, at the path the panel computes (never one read
	// from the files).
	projectDir := m.paths.ProjectDir(n)
	if err := composekit.RealDir(projectDir); err == nil {
		if err := os.RemoveAll(projectDir); err != nil {
			m.logger.Error("functions: removing the compose project", "function", n.String(), "error", err)
			partial("compose project")
			return nil, newError(CodeStorage, "", "storage error")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		// A link in place of the directory: remove the link itself.
		if err := os.Remove(projectDir); err != nil {
			partial("compose project")
			return nil, m.storageError("removing the compose project", err)
		}
	}
	// Deployments, upload leftovers, secrets, credentials and reference
	// (last: until then the function is listed and the deletion can be
	// retried).
	if err := m.paths.RemoveFunctionDirs(n); err != nil {
		m.logger.Error("functions: removing the function directories", "function", n.String(), "error", err)
		partial("directories")
		return nil, newError(CodeStorage, "", "storage error")
	}
	return res, nil
}
