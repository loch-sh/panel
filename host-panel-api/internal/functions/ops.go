package functions

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"

	"host-panel-api/internal/networks"
)

// UpdateRequest changes the configuration of a function; nil fields are
// kept. Env replaces every variable.
type UpdateRequest struct {
	MemoryMB       *int      `json:"memoryMB,omitempty"`
	CPUs           *float64  `json:"cpus,omitempty"`
	TimeoutSeconds *int      `json:"timeoutSeconds,omitempty"`
	TmpSizeMB      *int      `json:"tmpSizeMB,omitempty"`
	Handler        *string   `json:"handler,omitempty"`
	Env            *[]EnvVar `json:"env,omitempty"`
	// MCPInvocable opts the function in or out of invocation by the loch
	// MCP tool (no container change).
	MCPInvocable *bool `json:"mcpInvocable,omitempty"`
}

// operate runs fn on the reference of a function under its lock, with a
// context detached from the request.
func (m *Manager) operate(ctx context.Context, name string, fn func(ctx context.Context, n Name, prev *Reference) error) error {
	n, nerr := parse(name)
	if nerr != nil {
		return nerr
	}
	ctx, cancel := detach(ctx)
	defer cancel()
	unlock, err := m.lock(ctx, n)
	if err != nil {
		return err
	}
	defer unlock()
	prev, err := m.load(n)
	if err != nil {
		return err
	}
	return fn(ctx, n, prev)
}

// Update changes the resources, the handler, the variables or the MCP
// opt-in of a function; a deployed, running one is recreated with them
// (not for the MCP opt-in alone, which is in the reference only).
func (m *Manager) Update(ctx context.Context, name string, req UpdateRequest, actor string) (*Detail, error) {
	var out *Detail
	err := m.operate(ctx, name, func(ctx context.Context, n Name, prev *Reference) error {
		next := prev.clone()
		var fields []string
		if req.MemoryMB != nil {
			next.MemoryMB, fields = *req.MemoryMB, append(fields, "memoryMB")
		}
		if req.CPUs != nil {
			next.CPUs, fields = *req.CPUs, append(fields, "cpus")
		}
		if req.TimeoutSeconds != nil {
			next.TimeoutSeconds, fields = *req.TimeoutSeconds, append(fields, "timeoutSeconds")
		}
		if req.TmpSizeMB != nil {
			next.TmpSizeMB, fields = *req.TmpSizeMB, append(fields, "tmpSizeMB")
		}
		if req.Handler != nil {
			rt := prev.runtime()
			h := rt.HandlerOrDefault(*req.Handler)
			if err := rt.ValidateHandler(h); err != nil {
				return err
			}
			next.Handler, fields = h, append(fields, "handler")
		}
		if req.Env != nil {
			next.Env, fields = slices.Clone(*req.Env), append(fields, "env")
		}
		if req.MCPInvocable != nil {
			next.MCPInvocable, fields = *req.MCPInvocable, append(fields, "mcpInvocable")
		}
		if len(fields) == 0 {
			return newError(CodeInvalidLimits, "", "nothing to change")
		}
		audit := func() {
			// Variable values are not secrets, but stay out of the trail.
			m.audit.Info("function updated", "user", actor, "function", n.String(), "fields", strings.Join(fields, ","),
				"memory_mb", next.MemoryMB, "cpus", next.CPUs, "timeout_s", next.TimeoutSeconds, "mcp_invocable", next.MCPInvocable)
		}
		// The flag alone is not part of the container: the reference is
		// written, nothing is recreated.
		if len(fields) == 1 && req.MCPInvocable != nil {
			if m.modified(prev) {
				return m.refused(n, actor, "update", "compose project modified outside the panel", CodeModified,
					"the function was modified outside the panel: it is never run by the panel (purge it)")
			}
			if err := m.paths.WriteReference(*next, m.cfg.Config); err != nil {
				return m.storageError("writing the reference", err)
			}
			audit()
			out = m.detail(ctx, next)
			return nil
		}
		if err := ValidateLimits(next.Limits, m.cfg.Config); err != nil {
			return err
		}
		if err := ValidateEnv(next.Env, next.Secrets); err != nil {
			return err
		}
		release, rerr := m.reserve(n, next.Limits, false)
		if rerr != nil {
			return rerr
		}
		defer release()
		if err := m.apply(ctx, prev, next, change{}, "update", actor); err != nil {
			return err
		}
		audit()
		out = m.detail(ctx, next)
		return nil
	})
	return out, err
}

// SetSecret sets the value of a secret (created when missing). The value
// is written to the private env_file only: never logged, audited nor
// returned.
func (m *Manager) SetSecret(ctx context.Context, name, key, value, actor string) error {
	if err := ValidateEnvName(key, "key"); err != nil {
		return err
	}
	if err := ValidateEnvValue(value, "value"); err != nil {
		return &Error{Code: CodeInvalidSecret, Field: "value", Message: err.Message}
	}
	return m.operate(ctx, name, func(ctx context.Context, n Name, prev *Reference) error {
		values, err := m.paths.ReadSecretsEnv(n)
		if err != nil {
			return m.storageError("reading the secrets", err)
		}
		next := prev.clone()
		created := !slices.Contains(next.Secrets, key)
		if created {
			next.Secrets = append(next.Secrets, key)
		}
		next.Sort()
		if err := ValidateEnv(next.Env, next.Secrets); err != nil {
			return err
		}
		values[key] = value
		data, err := RenderSecretsEnv(next.Secrets, values)
		if err != nil {
			return err
		}
		if err := m.apply(ctx, prev, next, change{secrets: data, recreate: true}, "secret", actor); err != nil {
			return err
		}
		m.audit.Info("function secret set", "user", actor, "function", n.String(), "secret", key, "created", created)
		return nil
	})
}

// DeleteSecret removes a secret.
func (m *Manager) DeleteSecret(ctx context.Context, name, key, actor string) error {
	return m.operate(ctx, name, func(ctx context.Context, n Name, prev *Reference) error {
		if !slices.Contains(prev.Secrets, key) {
			return newError(CodeSecretNotFound, "key", "secret not found")
		}
		values, err := m.paths.ReadSecretsEnv(n)
		if err != nil {
			return m.storageError("reading the secrets", err)
		}
		next := prev.clone()
		next.Secrets = slices.DeleteFunc(next.Secrets, func(s string) bool { return s == key })
		delete(values, key)
		data, err := RenderSecretsEnv(next.Secrets, values)
		if err != nil {
			return err
		}
		if err := m.apply(ctx, prev, next, change{secrets: data, recreate: true}, "secret", actor); err != nil {
			return err
		}
		m.audit.Info("function secret removed", "user", actor, "function", n.String(), "secret", key)
		return nil
	})
}

// RotateCredentials replaces the invoke credentials of a function (the
// container is recreated: the runtime reads them at startup). sigv4, when
// set, enables or disables the SigV4 key pair. The new credentials are
// returned once; the panel keeps only what the runtime needs (the token's
// SHA-256, the key pair when enabled).
func (m *Manager) RotateCredentials(ctx context.Context, name string, sigv4 *bool, actor string) (*Credentials, error) {
	var out *Credentials
	err := m.operate(ctx, name, func(ctx context.Context, n Name, prev *Reference) error {
		next := prev.clone()
		if sigv4 != nil {
			next.SigV4 = *sigv4
		}
		creds, err := newCredentials(n, next.SigV4)
		if err != nil {
			return m.storageError("generating credentials", err)
		}
		if err := m.apply(ctx, prev, next, change{credentials: renderCredentialsEnv(creds), recreate: true}, "credentials", actor); err != nil {
			return err
		}
		m.audit.Info("function credentials rotated", "user", actor, "function", n.String(), "sigv4", next.SigV4)
		out = creds
		return nil
	})
	return out, err
}

// SetNetworks replaces the networks of a function. Removed networks are
// cut before the container is recreated; if the recreation fails, the
// function is stopped rather than reconnected.
func (m *Manager) SetNetworks(ctx context.Context, name string, names []string, actor string) ([]string, error) {
	var out []string
	err := m.operate(ctx, name, func(ctx context.Context, n Name, prev *Reference) error {
		nets, cerr := m.checkNetworks(ctx, names, "")
		if cerr != nil {
			return cerr
		}
		if slices.Equal(nets, prev.Networks) {
			out = nets
			return nil
		}
		next := prev.clone()
		next.Networks = nets
		cut := slices.DeleteFunc(slices.Clone(prev.Networks), func(x string) bool { return slices.Contains(nets, x) })
		added := slices.DeleteFunc(slices.Clone(nets), func(x string) bool { return slices.Contains(prev.Networks, x) })
		if err := m.apply(ctx, prev, next, change{cut: cut}, "networks", actor); err != nil {
			if len(cut) > 0 && CodeOf(err) == CodeStartFailed {
				// Stopped, never reconnected: record it.
				stopped := prev.clone()
				stopped.Stopped = true
				if werr := m.paths.WriteReference(*stopped, m.cfg.Config); werr != nil {
					m.logger.Error("functions: recording a stopped function", "function", n.String(), "error", werr)
				}
			}
			return err
		}
		for _, x := range added {
			m.audit.Info("function network attached", "user", actor, "function", n.String(), "network", x)
		}
		for _, x := range cut {
			m.audit.Info("function network detached", "user", actor, "function", n.String(), "network", x)
		}
		out = nets
		return nil
	})
	return out, err
}

// SetEgress gives or removes outbound access (a compose-owned network
// that is not internal: it reaches the host gateway, the LAN and metadata
// endpoints unless DOCKER-USER rules say otherwise).
func (m *Manager) SetEgress(ctx context.Context, name string, enabled bool, actor string) error {
	return m.operate(ctx, name, func(ctx context.Context, n Name, prev *Reference) error {
		if prev.Egress == enabled {
			return nil
		}
		next := prev.clone()
		next.Egress = enabled
		if err := m.apply(ctx, prev, next, change{}, "egress", actor); err != nil {
			return err
		}
		log := m.audit.Info
		if enabled {
			log = m.audit.Warn
		}
		log("function egress toggled", "user", actor, "function", n.String(), "enabled", enabled)
		return nil
	})
}

// Actions on a function.
const (
	ActionStart   = "start"
	ActionStop    = "stop"
	ActionRestart = "restart"
)

// Action starts, stops or restarts a function. Stopping goes through the
// Docker API (never compose: it works on a modified function too).
func (m *Manager) Action(ctx context.Context, name, action, actor string) error {
	switch action {
	case ActionStart, ActionStop, ActionRestart:
	default:
		return newError(CodeInvalidAction, "action", "unknown action")
	}
	return m.operate(ctx, name, func(ctx context.Context, n Name, prev *Reference) error {
		if action == ActionStop {
			next := prev.clone()
			next.Stopped = true
			if err := m.paths.WriteReference(*next, m.cfg.Config); err != nil {
				return m.storageError("writing the reference", err)
			}
			defer m.onChange()
			if err := m.stopContainers(ctx, n); err != nil {
				return newError(CodeDockerError, "", "the function could not be stopped")
			}
			m.audit.Info("function action", "user", actor, "function", n.String(), "action", action)
			return nil
		}
		if prev.Deployment == nil {
			return newError(CodeNotDeployed, "", "the function has no code yet: deploy it first")
		}
		next := prev.clone()
		next.Stopped = false
		// The previous reference must be "running" for apply to restart it
		// on failure only when it was running before.
		if err := m.apply(ctx, prev, next, change{recreate: action == ActionRestart}, action, actor); err != nil {
			return err
		}
		m.audit.Info("function action", "user", actor, "function", n.String(), "action", action)
		return nil
	})
}

// NetworkSource exposes the functions as members of the panel networks
// (member kind "function").
type NetworkSource struct{ M *Manager }

// Members returns every function with its networks.
func (s NetworkSource) Members(context.Context) ([]networks.DeclaredMember, error) {
	refs, err := s.M.loadAll()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]networks.DeclaredMember, 0, len(refs))
	for _, r := range refs {
		n, _ := r.FunctionName()
		out = append(out, networks.DeclaredMember{Name: r.Name, Project: n.Project(), Container: n.Project(), Networks: slices.Clone(r.Networks)})
	}
	return out, nil
}

// SetNetworks replaces the networks of a function (synchronous).
func (s NetworkSource) SetNetworks(ctx context.Context, name string, nets []string, actor string) (string, error) {
	_, err := s.M.SetNetworks(ctx, name, nets, actor)
	return "", err
}
