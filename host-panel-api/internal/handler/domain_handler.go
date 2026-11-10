package handler

import (
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"host-panel-api/internal/docker"
	"host-panel-api/internal/middleware"
	"host-panel-api/internal/model"
)

// TraefikRouter represents a Traefik router parsed from a container's labels.
type TraefikRouter struct {
	Name         string   `json:"name"`
	Rule         string   `json:"rule"`
	Domains      []string `json:"domains"`
	Entrypoints  []string `json:"entrypoints"`
	TLS          bool     `json:"tls"`
	CertResolver string   `json:"certResolver,omitempty"`
	Middlewares  []string `json:"middlewares,omitempty"`
	ServiceName  string   `json:"serviceName"`
}

// TraefikService represents a Traefik service parsed from a container's labels.
type TraefikService struct {
	Name string `json:"name"`
	Port int    `json:"port,omitempty"`
}

// DomainEntry groups the Traefik routing information of a container.
type DomainEntry struct {
	ContainerID    string           `json:"containerId"`
	ContainerName  string           `json:"containerName"`
	ContainerState string           `json:"containerState"`
	ContainerImage string           `json:"containerImage"`
	TraefikEnabled bool             `json:"traefikEnabled"`
	Routers        []TraefikRouter  `json:"routers"`
	Services       []TraefikService `json:"services"`
}

// DomainHandler handles the Traefik domain mapping endpoint.
type DomainHandler struct {
	Cache  *docker.ContainerCache
	Logger *slog.Logger
}

var (
	routerLabelRe  = regexp.MustCompile(`^traefik\.http\.routers\.([^.]+)\.(.+)$`)
	serviceLabelRe = regexp.MustCompile(`^traefik\.http\.services\.([^.]+)\.loadbalancer\.server\.port$`)
	hostRe         = regexp.MustCompile("Host\\(`([^`]+)`\\)")
)

// List returns the Traefik domain mapping filtered by user profile.
func (h *DomainHandler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, ErrForbidden, "Forbidden")
		return
	}

	containers, err := h.Cache.List(r.Context())
	if err != nil {
		h.Logger.Error("failed to list containers for domains", "error", err)
		writeError(w, http.StatusInternalServerError, ErrContainerListFailed, "Failed to list containers")
		return
	}

	filtered := filterNonSystem(containers)

	if user.Profile == model.ProfileRestricted {
		filtered = filterByPermissions(filtered, user.Permissions)
	}

	entries := make([]DomainEntry, 0)
	for _, c := range filtered {
		routers, services := parseTraefikLabels(c.Labels)
		if len(routers) == 0 {
			continue
		}

		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}

		entries = append(entries, DomainEntry{
			ContainerID:    c.ID,
			ContainerName:  name,
			ContainerState: string(c.State),
			ContainerImage: c.Image,
			TraefikEnabled: c.Labels["traefik.enable"] == "true",
			Routers:        routers,
			Services:       services,
		})
	}

	writeJSON(w, http.StatusOK, entries)
}

// parseTraefikLabels extracts Traefik routers and services from Docker labels.
func parseTraefikLabels(labels map[string]string) ([]TraefikRouter, []TraefikService) {
	routerMap := make(map[string]*TraefikRouter)
	services := make([]TraefikService, 0)

	for key, value := range labels {
		if matches := routerLabelRe.FindStringSubmatch(key); matches != nil {
			name := matches[1]
			field := matches[2]

			router, exists := routerMap[name]
			if !exists {
				router = &TraefikRouter{Name: name, ServiceName: name}
				routerMap[name] = router
			}

			switch field {
			case "rule":
				router.Rule = value
				router.Domains = extractDomainsFromRule(value)
			case "entrypoints":
				router.Entrypoints = splitTrim(value)
			case "tls":
				router.TLS = value == "true"
			case "tls.certresolver":
				router.CertResolver = value
			case "middlewares":
				router.Middlewares = splitTrim(value)
			case "service":
				router.ServiceName = value
			}

			continue
		}

		if matches := serviceLabelRe.FindStringSubmatch(key); matches != nil {
			name := matches[1]
			port, err := strconv.Atoi(value)
			if err != nil {
				continue
			}
			services = append(services, TraefikService{Name: name, Port: port})
		}
	}

	routers := make([]TraefikRouter, 0, len(routerMap))
	for _, r := range routerMap {
		if r.CertResolver != "" {
			r.TLS = true
		}
		routers = append(routers, *r)
	}

	return routers, services
}

// extractDomainsFromRule extracts domain names from a Traefik rule.
// Handles formats: Host(`a.com`), Host(`a.com`,`b.com`), Host(`a.com`) || Host(`b.com`)
func extractDomainsFromRule(rule string) []string {
	matches := hostRe.FindAllStringSubmatch(rule, -1)
	var domains []string
	for _, m := range matches {
		// A Host() can contain multiple domains separated by commas
		for _, d := range strings.Split(m[1], ",") {
			d = strings.TrimSpace(d)
			// Remove extra backticks just in case
			d = strings.Trim(d, "`")
			if d != "" {
				domains = append(domains, d)
			}
		}
	}
	return domains
}

// splitTrim splits a string by comma and trims whitespace.
func splitTrim(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
