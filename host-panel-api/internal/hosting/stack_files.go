package hosting

// Files of a stack on disk: writing the compose and generated files,
// stamping deployments, regenerating outdated files and the checks done
// before writing or deleting them.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// writeGeneratedFiles (re)writes the files the panel derives from the spec:
// the Symfony routing rules and the PHP-FPM pool overrides.
func writeGeneratedFiles(s *loadedStack) error {
	return writeMissingGeneratedFiles(s, true, true)
}

// writeMissingGeneratedFiles writes the selected generated files of a stack.
// Custom routing rules are never regenerated.
func writeMissingGeneratedFiles(s *loadedStack, routing, pool bool) error {
	cfgDir := filepath.Join(s.dir, ConfigDir)
	gen := generatedFor(s.spec, Snippets{})
	if routing && isGeneratedRouting(s.spec.Routing) {
		if err := writeFileAtomic(cfgDir, RoutingFile, []byte(gen.Routing)); err != nil {
			return storageError(err)
		}
	}
	if pool {
		if err := writeFileAtomic(cfgDir, FPMPoolFile, []byte(gen.FPMPool)); err != nil {
			return storageError(err)
		}
	}
	return nil
}

// checkRulesFollowServer refuses to keep custom routing rules unchanged when
// the web server changes (Nginx and Apache syntaxes differ, the container
// would not start).
func checkRulesFollowServer(current *loadedStack, spec *Spec, snippets Snippets) error {
	// The current file holds the rules of the previous web server, whether it
	// was custom or generated (Symfony rules seeded into custom rules).
	if spec.Routing != RoutingCustom || current.spec.Routing == RoutingDefault ||
		current.spec.Template.UsesNginx() == spec.Template.UsesNginx() {
		return nil
	}
	previous, err := readOptional(filepath.Join(current.dir, ConfigDir, RoutingFile))
	if err != nil {
		return storageError(err)
	}
	if previous == snippets.RoutingRules {
		return &Error{Code: CodeInvalidRouting, Field: "routingRules", Reason: ReasonWebServerChanged,
			Message: "the custom routing rules are written for the previous web server: rewrite them for the new template"}
	}
	return nil
}

// stampStack records the deployment in the compose file on disk (only the
// stamp comment changes, even in a file edited by hand).
func stampStack(dir, actor, kind string) error {
	data, err := readFileNoFollow(filepath.Join(dir, ComposeFile))
	if err != nil {
		return storageError(err)
	}
	if err := writeFileAtomic(dir, ComposeFile, stampCompose(data, actor, kind, time.Now())); err != nil {
		return storageError(err)
	}
	return nil
}

// refreshCompose rewrites the compose file of a stack from its spec with the
// current panel version (the previous file is kept in .previous/).
func (m *Manager) refreshCompose(s *loadedStack) error {
	for _, v := range s.spec.Env {
		if isReservedEnv(v.Name) {
			m.logger.Warn("hosting: environment variable now managed by the panel, its value is overridden", "stack", s.spec.Name, "variable", v.Name)
		}
	}
	compose, err := Render(s.spec, m.cfg.Render)
	if err != nil {
		return err
	}
	if err := backupStack(s.dir); err != nil {
		return storageError(err)
	}
	if err := writeFileAtomic(s.dir, ComposeFile, compose); err != nil {
		return storageError(err)
	}
	if err := writeGeneratedFiles(s); err != nil {
		return err
	}
	m.logger.Info("hosting: compose file regenerated with the current panel version", "stack", s.spec.Name, "from_version", s.spec.Version)
	return nil
}

// checkDeletable verifies, right before deleting, that the stack directory
// is still a real directory and returns the site directory to purge ("" when
// not purging or already gone). The site directory must be a direct child of
// the sites directory, whatever the compose file says.
func (m *Manager) checkDeletable(stackDir, site string, purge bool) (string, error) {
	if err := realDir(stackDir); err != nil {
		return "", err
	}
	if !purge {
		return "", nil
	}
	sitesDir := filepath.Clean(m.cfg.Render.SitesDir)
	siteDir := filepath.Join(sitesDir, site)
	if !ValidPathSegment(site) || filepath.Dir(siteDir) != sitesDir {
		return "", fmt.Errorf("refusing to purge %q: not a site directory", siteDir)
	}
	switch err := realDir(siteDir); {
	case err == nil:
		return siteDir, nil
	case errors.Is(err, os.ErrNotExist):
		return "", nil
	default:
		return "", err
	}
}

// writeSiteAndStackFiles creates the site directories and writes the stack
// files (compose, snippets, generated files).
func (m *Manager) writeSiteAndStackFiles(dir string, spec *Spec, compose []byte, snippets Snippets) error {
	if err := ensureSiteDirs(m.cfg.Render.SitesDir, spec.Site, spec.DocumentRoot, m.cfg.RunUID, m.cfg.RunGID); err != nil {
		m.logger.Error("hosting: cannot create site directories", "stack", spec.Name, "error", err)
		return storageError(err)
	}
	if err := writeStackFiles(dir, compose, snippets, generatedFor(spec, snippets)); err != nil {
		m.logger.Error("hosting: cannot write stack files", "stack", spec.Name, "error", err)
		return storageError(err)
	}
	return nil
}
