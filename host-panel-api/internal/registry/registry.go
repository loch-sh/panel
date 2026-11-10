// Package registry names the Docker images the panel runs. Images are pulled
// from mirror.gcr.io, Google's public mirror of Docker Hub, so that the
// panel does not depend on Docker Hub (rate limits, availability).
package registry

import "strings"

// Mirror is the Docker Hub mirror the images are pulled from.
const Mirror = "mirror.gcr.io"

// Hub returns the mirror reference of a Docker Hub image: "postgres:17"
// becomes "mirror.gcr.io/library/postgres:17" (official images live in the
// "library" namespace), "valkey/valkey:9.0" becomes
// "mirror.gcr.io/valkey/valkey:9.0".
func Hub(ref string) string {
	repo, _, _ := strings.Cut(ref, ":")
	if !strings.Contains(repo, "/") {
		return Mirror + "/library/" + ref
	}
	return Mirror + "/" + ref
}

// GHCR returns a GitHub Container Registry reference. Only for our own
// images (e.g. Loch S3), which are not on Docker Hub and therefore not on
// its mirror: an explicit exception to the mirror rule.
func GHCR(ref string) string {
	return "ghcr.io/" + ref
}
