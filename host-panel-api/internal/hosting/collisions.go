package hosting

// Collision checks: a new stack must not reuse a name, domain or site already
// taken by another stack, compose project, container or Traefik router.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/moby/moby/api/types/container"
)

// CheckName reports whether a stack name is free (format, reserved names,
// directories and Docker resources).
func (m *Manager) CheckName(ctx context.Context, name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	containers, err := m.docker.ListContainers(ctx)
	if err != nil {
		return err
	}
	return m.checkNameFree(name, containers)
}

func taken(reason, msg string) *Error {
	return &Error{Code: CodeNameTaken, Field: "name", Reason: reason, Message: msg}
}

func (m *Manager) checkNameFree(name string, containers []container.Summary) error {
	project := ProjectName(name)
	if _, err := os.Lstat(m.stackDir(name)); err == nil {
		if _, loadErr := m.load(name); loadErr == nil {
			return taken(ReasonStack, "a hosted stack with this name already exists")
		}
		return taken(ReasonDirectory, "a directory with this name already exists in the stacks directory")
	} else if !errors.Is(err, os.ErrNotExist) {
		return storageError(err)
	}

	// Exact names only: "blog" must not collide with "blog-staging", whose
	// container is hosted-blog-staging-web-1 and routers hosted-blog-staging-*.
	containerRe := regexp.MustCompile("^" + regexp.QuoteMeta(project) + "(-" + webService + "-[0-9]+)?$")
	ownLabels := []string{
		"traefik.http.routers." + project + ".",
		"traefik.http.routers." + project + "-http.",
		"traefik.http.routers." + project + "-https.",
		"traefik.http.services." + project + ".",
		"traefik.http.middlewares." + project + "-redirect.",
	}
	for _, c := range containers {
		if c.Labels[composeProjectLabel] == project {
			return taken(ReasonProject, "a compose project with this name already exists")
		}
		if containerRe.MatchString(containerName(c)) {
			return taken(ReasonContainer, "a container with this name already exists")
		}
		for key := range c.Labels {
			for _, p := range ownLabels {
				if strings.HasPrefix(key, p) {
					return taken(ReasonTraefikRouter, "a Traefik router with this name already exists")
				}
			}
		}
	}
	return nil
}

var hostRuleRe = regexp.MustCompile(`Host\(([^)]*)\)`)

// routedDomains extracts the Host() domains of the Traefik routers of a container.
func routedDomains(labels map[string]string) []string {
	var domains []string
	for key, value := range labels {
		if !strings.HasPrefix(key, "traefik.http.routers.") || !strings.HasSuffix(key, ".rule") {
			continue
		}
		for _, m := range hostRuleRe.FindAllStringSubmatch(value, -1) {
			for _, d := range strings.Split(m[1], ",") {
				if d = strings.ToLower(strings.Trim(strings.TrimSpace(d), "`\"'")); d != "" {
					domains = append(domains, d)
				}
			}
		}
	}
	return domains
}

// checkDomainsFree verifies that no other container or hosted stack routes
// one of the domains of spec.
func checkDomainsFree(spec *Spec, containers []container.Summary, stacks map[string]*loadedStack) error {
	if len(spec.Domains) == 0 {
		return nil
	}
	own := ProjectName(spec.Name)
	inUse := func(d string) *Error {
		return &Error{Code: CodeDomainInUse, Field: "domains", Message: fmt.Sprintf("domain %q is already routed", d)}
	}
	for _, c := range containers {
		if c.Labels[composeProjectLabel] == own {
			continue
		}
		for _, d := range routedDomains(c.Labels) {
			if slices.Contains(spec.Domains, d) {
				return inUse(d)
			}
		}
	}
	for name, s := range stacks {
		if name == spec.Name {
			continue
		}
		for _, d := range s.spec.Domains {
			if slices.Contains(spec.Domains, d) {
				return inUse(d)
			}
		}
	}
	return nil
}

func checkSiteFree(spec *Spec, stacks map[string]*loadedStack) error {
	for name, s := range stacks {
		if name != spec.Name && s.spec.Site == spec.Site {
			return &Error{Code: CodeSiteInUse, Field: "site", Message: fmt.Sprintf("site is already used by stack %q", name)}
		}
	}
	return nil
}
