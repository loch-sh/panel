package hosting

import (
	"time"

	"host-panel-api/internal/registry"
)

// PHPVersion is an entry of the PHP image catalog (webdevops images).
type PHPVersion struct {
	Tag string `json:"tag"`
	// End of active support (bug fixes) and of security support of the PHP
	// branch, from https://www.php.net/supported-versions.php (YYYY-MM-DD).
	ActiveSupportUntil   string `json:"activeSupportUntil"`
	SecuritySupportUntil string `json:"securitySupportUntil"`
	// Deprecated is true once security support has ended (computed).
	Deprecated bool `json:"deprecated"`
}

// phpBranch holds the support dates of a PHP branch.
type phpBranch struct {
	active   string
	security string
}

// phpBranches lists the support calendar of each PHP branch.
var phpBranches = map[string]phpBranch{
	"8.5": {"2027-12-31", "2029-12-31"},
	"8.4": {"2026-12-31", "2028-12-31"},
	"8.3": {"2025-12-31", "2027-12-31"},
	"8.2": {"2024-12-31", "2026-12-31"},
	"8.1": {"2023-11-25", "2025-12-31"},
	"8.0": {"2022-11-26", "2023-11-26"},
	"7.4": {"2021-11-28", "2022-11-28"},
	"5.6": {"2017-01-19", "2018-12-31"},
}

// phpTags is the allowlist of webdevops PHP image tags, in display order.
// Free tags are not accepted: a typo or an incompatible image would only fail
// at deploy time. 5.6 has no alpine variant upstream.
var phpTags = []string{
	"8.5", "8.5-alpine",
	"8.4", "8.4-alpine",
	"8.3", "8.3-alpine",
	"8.2", "8.2-alpine",
	"8.1", "8.1-alpine",
	"8.0", "8.0-alpine",
	"7.4", "7.4-alpine",
	"5.6",
}

// DefaultPHPVersion is preselected in the creation form.
const DefaultPHPVersion = "8.4"

// staticImageTag is the only maintained tag of webdevops/nginx and
// webdevops/apache (`latest` is years old and must never be used).
const staticImageTag = "alpine"

// branchOf returns the PHP branch of an image tag ("8.4-alpine" → "8.4").
func branchOf(tag string) string {
	if i := len(tag) - len("-alpine"); i > 0 && tag[i:] == "-alpine" {
		return tag[:i]
	}
	return tag
}

// PHPVersions returns the catalog with support dates. A version is
// deprecated once the security support of its branch has ended at now.
func PHPVersions(now time.Time) []PHPVersion {
	today := now.UTC().Format(time.DateOnly)
	out := make([]PHPVersion, 0, len(phpTags))
	for _, tag := range phpTags {
		b := phpBranches[branchOf(tag)]
		out = append(out, PHPVersion{
			Tag:                  tag,
			ActiveSupportUntil:   b.active,
			SecuritySupportUntil: b.security,
			// ISO dates compare lexicographically; support lasts through the end date.
			Deprecated: b.security < today,
		})
	}
	return out
}

func validPHPVersion(tag string) bool {
	for _, t := range phpTags {
		if t == tag {
			return true
		}
	}
	return false
}

// notMirrored are the images missing from the Docker Hub mirror (checked on
// 2026-11-05): they are still pulled from Docker Hub.
var notMirrored = map[string]bool{
	"webdevops/php-apache:8.4":        true,
	"webdevops/php-apache:8.4-alpine": true,
	"webdevops/php-apache:8.1-alpine": true,
	"webdevops/php-apache:8.0-alpine": true,
	"webdevops/nginx:alpine":          true,
}

// Image returns the image reference of the web service of a spec, from the
// Docker Hub mirror when it has it.
func Image(spec *Spec) string {
	var ref string
	switch spec.Template {
	case TemplatePHPNginx:
		ref = "webdevops/php-nginx:" + spec.PHPVersion
	case TemplatePHPApache:
		ref = "webdevops/php-apache:" + spec.PHPVersion
	case TemplateNginx:
		ref = "webdevops/nginx:" + staticImageTag
	case TemplateApache:
		ref = "webdevops/apache:" + staticImageTag
	default:
		return ""
	}
	if notMirrored[ref] {
		return ref
	}
	return registry.Hub(ref)
}
