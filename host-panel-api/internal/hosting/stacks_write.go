package hosting

// Writing stacks: create, update, deploy and delete, and the file helpers
// they share.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Create validates and writes a new stack, then deploys it asynchronously.
func (m *Manager) Create(ctx context.Context, spec *Spec, snippets Snippets, actor string) (*Operation, error) {
	if spec.Routing == RoutingSymfony && spec.AssetDirs == nil {
		spec.AssetDirs = append([]string(nil), DefaultAssetDirs...)
	}
	compose, err := m.validateAndRender(ctx, spec, snippets)
	if err != nil {
		return nil, err
	}
	if err := m.checkNetworks(ctx, spec); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkCreateConflicts(ctx, spec); err != nil {
		return nil, err
	}
	op, err := m.startOp(spec.Name, OpCreate)
	if err != nil {
		return nil, err
	}
	dir, err := m.reserveStackDir(spec.Name)
	if err != nil {
		m.dropOp(spec.Name, op)
		return nil, err
	}
	compose = stampCompose(compose, actor, OpCreate, time.Now())
	if err := m.writeSiteAndStackFiles(dir, spec, compose, snippets); err != nil {
		m.dropOp(spec.Name, op)
		_ = os.RemoveAll(dir)
		return nil, err
	}

	m.audit.Info("hosting stack created", "user", actor, "stack", spec.Name, "template", spec.Template, "image", Image(spec))
	snap := op.snapshot()
	m.run(spec.Name, op, actor, m.composeUp(dir, true))
	return snap, nil
}

// Update replaces the settings of a stack and redeploys it. On failure the
// previous files are restored and redeployed.
func (m *Manager) Update(ctx context.Context, name string, spec *Spec, snippets Snippets, force bool, actor string) (*Operation, error) {
	spec.Name = name
	compose, err := m.validateAndRender(ctx, spec, snippets)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	current, err := m.load(name)
	if err != nil {
		return nil, err
	}
	if err := m.checkUpdate(ctx, current, spec, snippets, force); err != nil {
		return nil, err
	}
	if err := m.checkNetworks(ctx, spec); err != nil {
		return nil, err
	}
	op, err := m.startOp(name, OpUpdate)
	if err != nil {
		return nil, err
	}
	dir := current.dir
	compose = stampCompose(compose, actor, OpUpdate, time.Now())
	if err := backupStack(dir); err != nil {
		m.dropOp(name, op)
		return nil, storageError(err)
	}
	if err := m.writeSiteAndStackFiles(dir, spec, compose, snippets); err != nil {
		m.dropOp(name, op)
		if rerr := restoreStack(dir); rerr != nil {
			m.logger.Error("hosting: cannot restore stack files after a failed write", "stack", name, "error", rerr)
		}
		return nil, err
	}

	m.audit.Info("hosting stack updated", "user", actor, "stack", name, "template", spec.Template, "image", Image(spec), "forced", force)
	snap := op.snapshot()
	m.run(name, op, actor, m.composeUpWithRollback(name, dir))
	return snap, nil
}

// validateAndRender normalizes and validates a spec, checks its PHP modules
// against the image and renders its compose file (without the stamp).
func (m *Manager) validateAndRender(ctx context.Context, spec *Spec, snippets Snippets) ([]byte, error) {
	Normalize(spec)
	if err := Validate(spec, snippets, m.cfg.Limits); err != nil {
		return nil, err
	}
	compose, err := Render(spec, m.cfg.Render)
	if err != nil {
		return nil, err
	}
	if err := m.checkModules(ctx, spec); err != nil {
		return nil, err
	}
	return compose, nil
}

// checkCreateConflicts verifies that the name, site and domains of a new
// stack are free. Called with m.mu held.
func (m *Manager) checkCreateConflicts(ctx context.Context, spec *Spec) error {
	containers, err := m.docker.ListContainers(ctx)
	if err != nil {
		return err
	}
	if err := m.checkNameFree(spec.Name, containers); err != nil {
		return err
	}
	stacks, err := m.loadAll()
	if err != nil {
		return storageError(err)
	}
	if err := checkSiteFree(spec, stacks); err != nil {
		return err
	}
	return checkDomainsFree(spec, containers, stacks)
}

// checkUpdate verifies that an update may replace the current stack.
// Called with m.mu held.
func (m *Manager) checkUpdate(ctx context.Context, current *loadedStack, spec *Spec, snippets Snippets, force bool) error {
	if current.modified && !force {
		return &Error{Code: CodeManuallyModified, Message: "the compose file (or the generated routing file) was modified outside the panel"}
	}
	if spec.Site != current.spec.Site {
		return &Error{Code: CodeInvalidSite, Field: "site", Message: "the site of a stack cannot be changed"}
	}
	if err := checkRulesFollowServer(current, spec, snippets); err != nil {
		return err
	}
	containers, err := m.docker.ListContainers(ctx)
	if err != nil {
		return err
	}
	stacks, err := m.loadAll()
	if err != nil {
		return storageError(err)
	}
	return checkDomainsFree(spec, containers, stacks)
}

// reserveStackDir creates the directory of a new stack and its panel marker.
// os.Mkdir fails if the directory appeared meanwhile (e.g. created by CI):
// it is the actual reservation of the name.
func (m *Manager) reserveStackDir(name string) (string, error) {
	dir := m.stackDir(name)
	if err := os.Mkdir(dir, dirPerm); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", taken(ReasonDirectory, "a directory with this name already exists in the stacks directory")
		}
		return "", storageError(err)
	}
	if err := writeFileAtomic(dir, ManagedMarker, []byte(managedMarkerContent)); err != nil {
		_ = os.RemoveAll(dir)
		return "", storageError(err)
	}
	return dir, nil
}

// composeUp returns an operation running `docker compose up` in dir.
func (m *Manager) composeUp(dir string, pull bool) func(context.Context, *Operation) error {
	return func(ctx context.Context, op *Operation) error {
		out, err := m.runner.Run(ctx, dir, upArgs(pull)...)
		op.Output = out
		return err
	}
}

// composeUpWithRollback runs `docker compose up` and, on failure, restores
// the previous files (.previous/) and redeploys them.
func (m *Manager) composeUpWithRollback(name, dir string) func(context.Context, *Operation) error {
	return func(ctx context.Context, op *Operation) error {
		out, err := m.runner.Run(ctx, dir, upArgs(false)...)
		op.Output = out
		if err == nil {
			return nil
		}
		if rerr := restoreStack(dir); rerr != nil {
			m.logger.Error("hosting rollback failed", "stack", name, "error", rerr)
			return err
		}
		// Own deadline: the update may have failed because the operation
		// itself timed out.
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer rcancel()
		rout, rerr := m.runner.Run(rctx, dir, upArgs(false)...)
		op.Output += "\n--- rollback ---\n" + rout
		op.RolledBack = rerr == nil
		return err
	}
}

// Deploy re-runs `docker compose up` on the files as they are on disk. A
// compose file edited outside the panel is only run with force: it may hold
// anything (privileged containers, host mounts) and the API has the Docker
// socket.
func (m *Manager) Deploy(_ context.Context, name string, pull, force bool, actor string) (*Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, err := m.load(name)
	if err != nil {
		return nil, err
	}
	if s.modified && !force {
		return nil, &Error{Code: CodeManuallyModified, Message: "the compose file (or the generated routing file) was modified outside the panel"}
	}
	if err := m.prepareDeploy(s); err != nil {
		return nil, err
	}
	op, err := m.startOp(name, OpDeploy)
	if err != nil {
		return nil, err
	}
	// Stamped once the operation is registered: a refused deploy (another
	// operation running) must not rewrite the file.
	if err := stampStack(s.dir, actor, OpDeploy); err != nil {
		m.dropOp(name, op)
		return nil, err
	}
	m.audit.Info("hosting stack deploy", "user", actor, "stack", name, "pull", pull, "forced", force)
	snap := op.snapshot()
	m.run(name, op, actor, m.composeUp(s.dir, pull))
	return snap, nil
}

// Delete stops and removes the containers of a stack, then its directory.
// The site data is kept unless purgeData is set and no other stack uses it.
func (m *Manager) Delete(_ context.Context, name string, purgeData bool, actor string) (*Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, err := m.load(name)
	if err != nil {
		return nil, err
	}
	if purgeData {
		stacks, err := m.loadAll()
		if err != nil {
			return nil, storageError(err)
		}
		if err := checkSiteFree(s.spec, stacks); err != nil {
			return nil, err
		}
	}
	op, err := m.startOp(name, OpDelete)
	if err != nil {
		return nil, err
	}
	m.audit.Info("hosting stack delete", "user", actor, "stack", name, "purge_data", purgeData)
	snap := op.snapshot()
	m.run(name, op, actor, m.composeDownAndRemove(name, s.dir, s.spec.Site, purgeData))
	return snap, nil
}

// prepareDeploy brings the files of a stack up to date before a redeploy:
// a stack written by a previous panel version (and not edited by hand) is
// regenerated, a deleted Symfony routing file is rewritten, and the mounted
// config files are checked.
func (m *Manager) prepareDeploy(s *loadedStack) error {
	cfgDir := filepath.Join(s.dir, ConfigDir)
	missingRouting := !isRegularFile(filepath.Join(cfgDir, RoutingFile))
	missingPool := !isRegularFile(filepath.Join(cfgDir, FPMPoolFile))
	switch {
	case !s.modified && s.spec.Version < SpecVersion:
		if err := m.refreshCompose(s); err != nil {
			return err
		}
	case missingRouting && s.spec.Routing == RoutingCustom:
		// Compose would fail on the missing file; custom rules cannot be
		// regenerated.
		return newError(CodeInvalidRouting, "routingRules", "the custom routing file is missing: update the hosting with its rules")
	case missingRouting || missingPool:
		// Only the missing files: a present one may hold a forced hand edit.
		if err := writeMissingGeneratedFiles(s, missingRouting, missingPool); err != nil {
			return err
		}
	}
	if err := checkConfigFiles(s.dir); err != nil {
		return storageError(err)
	}
	return nil
}

// composeDownAndRemove returns the operation deleting a stack: containers,
// then the site data (when purging), then the stack directory.
func (m *Manager) composeDownAndRemove(name, dir, site string, purgeData bool) func(context.Context, *Operation) error {
	return func(ctx context.Context, op *Operation) error {
		out, err := m.runner.Run(ctx, dir, downArgs...)
		op.Output = out
		if err != nil {
			return err
		}
		// Re-check under the lock, then release it before removing files: a
		// large purge can take minutes. The stack directory still exists until
		// the end, so its name and site cannot be claimed meanwhile.
		m.mu.Lock()
		siteDir, err := m.checkDeletable(dir, site, purgeData)
		m.mu.Unlock()
		if err != nil {
			return err
		}
		// The stack directory goes last: if purging the site fails, the stack
		// still exists and the failed operation stays visible to clients.
		if siteDir != "" {
			if err := os.RemoveAll(siteDir); err != nil {
				return err
			}
		}
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		m.dropOpLater(name)
		return nil
	}
}
