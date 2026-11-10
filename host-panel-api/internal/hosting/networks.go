package hosting

// Private networks of the stacks (internal/networks): checks, update and
// the source that lists the stacks as members of the networks.

import (
	"context"
	"errors"
	"os"

	"host-panel-api/internal/networks"
)

// checkNetworks verifies that every network a stack joins is the one the
// panel created, at every write (a network replaced since it was chosen is
// refused too).
func (m *Manager) checkNetworks(ctx context.Context, spec *Spec) error {
	if m.cfg.Render.Networks == nil {
		return nil
	}
	for _, name := range spec.Networks {
		if _, ok := m.cfg.Render.Networks.Verify(ctx, name); !ok {
			return newError(CodeInvalidNetwork, "networks", "network %q is missing or is not the one the panel created", name)
		}
	}
	return nil
}

// SetNetworks replaces the networks of a stack and redeploys it (same path
// as Update, with the current settings and files).
func (m *Manager) SetNetworks(ctx context.Context, name string, nets []string, actor string) (*Operation, error) {
	m.mu.Lock()
	current, err := m.load(name)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	snippets, _, err := readSnippets(current)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	spec := *current.spec
	spec.Networks = append([]string(nil), nets...)
	return m.Update(ctx, name, &spec, snippets, false, actor)
}

// NetworkSource exposes the hosted stacks as members of the panel networks.
type NetworkSource struct{ M *Manager }

// Members returns every hosted stack with its networks.
func (s NetworkSource) Members(context.Context) ([]networks.DeclaredMember, error) {
	stacks, err := s.M.loadAll()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]networks.DeclaredMember, 0, len(stacks))
	for name, st := range stacks {
		// Compose names the container <project>-<service>-1; Docker keeps
		// container names unique.
		out = append(out, networks.DeclaredMember{Name: name, Project: ProjectName(name), Container: ProjectName(name) + "-" + webService + "-1",
			Networks: append([]string(nil), st.spec.Networks...)})
	}
	return out, nil
}

// SetNetworks replaces the networks of a stack (asynchronous: returns the
// operation ID).
func (s NetworkSource) SetNetworks(ctx context.Context, name string, nets []string, actor string) (string, error) {
	op, err := s.M.SetNetworks(ctx, name, nets, actor)
	if err != nil {
		return "", err
	}
	return op.ID, nil
}
