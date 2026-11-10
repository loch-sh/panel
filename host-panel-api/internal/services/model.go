package services

import (
	"context"
	"slices"
	"strings"
	"time"

	"host-panel-api/internal/docker"
)

// InstancePrefix prefixes the compose project, directory, container and data
// directory of every instance. It keeps instances apart from hosted stacks
// ("hosted-") and from the stacks deployed by CI in the same directory.
const InstancePrefix = "svc-"

// ManifestVersion is the version of the x-loch section written in compose
// files. Bump it whenever the rendered output changes, so instances written
// by a previous version are not reported as edited by hand.
//   - 2: networks chosen per instance (private networks), recorded in the
//     panel's reference with the exposure.
const ManifestVersion = 2

// Kind groups engines by the kind of service they provide.
type Kind string

const (
	KindDatabase  Kind = "database"
	KindCache     Kind = "cache"
	KindMessaging Kind = "messaging"
	KindStorage   Kind = "storage"
	KindQueue     Kind = "queue"
)

// Lifecycle tells when an instance exists.
type Lifecycle int

const (
	// OnDemand: one instance per engine version, shared by its resources,
	// created with the first one and retired with the last one (databases,
	// RabbitMQ). Suited to engines that are heavy and isolate their tenants.
	OnDemand Lifecycle = iota
	// Dedicated: the instance is the resource (one container each, named by
	// the user, sized at creation). Suited to light engines whose internal
	// isolation is weak (Redis, Valkey) or costly to administer (NATS). The
	// panel runs nothing inside: passwords live in panel-written state files.
	Dedicated
)

// String is the lifecycle as shown to clients.
func (l Lifecycle) String() string {
	if l == Dedicated {
		return "dedicated"
	}
	return "shared"
}

// Version is an entry of an engine catalog.
type Version struct {
	Version  string `json:"version"`  // "16", "11.4"
	Image    string `json:"image"`    // "postgres:16"
	Platform string `json:"platform"` // "" (native) or e.g. "linux/amd64"
	// Digest pins the image of a dedicated instance ("sha256:..."): the
	// compose file runs Image@Digest, so the tag, shared by every instance of
	// the version, can move without changing what an instance runs. Set
	// from the private reference only; empty in the catalogs.
	Digest string `json:"-"`
	// SupportUntil is the end of the security fixes (end of life),
	// ActiveSupportUntil the end of the active support (bug fixes):
	// YYYY-MM-DD, "" when unknown (endoflife.date, see catalog.go).
	SupportUntil       string `json:"supportUntil,omitempty"`
	ActiveSupportUntil string `json:"activeSupportUntil,omitempty"`
	Deprecated         bool   `json:"deprecated"` // computed: security support has ended
	// Pinned: offered because the configuration pins it.
	Pinned bool `json:"pinned,omitempty"`
	// MaxNameLength bounds resource names when the engine is stricter than
	// MaxNameLength (0 = MaxNameLength), e.g. MySQL 5.6 user names.
	MaxNameLength int    `json:"maxNameLength,omitempty"`
	DataMount     string `json:"-"` // data directory in the container
	// LegacyAuth: resources may use the legacy password mechanism (set in
	// the API response from SupportsLegacyAuth, not in the catalogs).
	LegacyAuth bool `json:"legacyAuth,omitempty"`
	// TLS: the version can require TLS from its clients; TLSOptional: it
	// can offer TLS without requiring it (set in the API response).
	TLS         bool `json:"tls,omitempty"`
	TLSOptional bool `json:"tlsOptional,omitempty"`
}

// Reference is the image reference an instance runs: "repo:tag@sha256:..."
// when pinned, the tag alone otherwise.
func (v Version) Reference() string {
	if v.Digest == "" {
		return v.Image
	}
	return v.Image + "@" + v.Digest
}

// NameLimit is the maximum resource name length of a version.
func (v Version) NameLimit() int {
	if v.MaxNameLength > 0 && v.MaxNameLength < MaxNameLength {
		return v.MaxNameLength
	}
	return MaxNameLength
}

// deprecatedAt reports whether upstream support has ended at now. ISO dates
// compare lexicographically; support lasts through the end date.
func (v Version) deprecatedAt(now time.Time) bool {
	return v.SupportUntil != "" && v.SupportUntil < now.UTC().Format(time.DateOnly)
}

// Slug turns a version into a name segment ("11.4" -> "11-4").
func Slug(version string) string { return strings.ReplaceAll(version, ".", "-") }

// InstanceRef identifies an instance. Every name and path is derived from it
// on the server side. Name is set for dedicated instances only.
type InstanceRef struct {
	Engine  string
	Version string
	Name    string
}

// Key is the public identifier of the instance: "postgres-16" (shared, by
// version) or "redis-cache" (dedicated, by name). Versions start with a
// digit and names with a letter: the two forms never collide.
func (r InstanceRef) Key() string {
	if r.Name != "" {
		return r.Engine + "-" + r.Name
	}
	return r.Engine + "-" + Slug(r.Version)
}

// Project is the compose project, directory and container name.
func (r InstanceRef) Project() string { return InstancePrefix + r.Key() }

// Executor runs a command in the (verified) container of an instance.
type Executor interface {
	Exec(ctx context.Context, req docker.ExecRequest) (docker.ExecResult, error)
}

// Resource is an item provisioned in an instance (a database and its user).
type Resource struct {
	Name string
	// Managed is false for resources created outside the panel (no dedicated
	// user, or a name the panel would not accept): they are listed and block
	// the retirement of the instance, but cannot be deleted nor rotated.
	Managed bool
}

// Credential is one line of connection information. Keys are stable
// identifiers translated by clients (host, port, database, user, password,
// uri). Secret values are shown only once.
type Credential struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret,omitempty"`
}

// Options are the settings of a dedicated instance chosen at creation
// (option key -> value), validated against the engine's Options().
type Options map[string]string

// Option is a setting a dedicated engine offers at creation: one value out
// of a fixed list (never free text: every value is rendered in a config).
type Option struct {
	Key     string   `json:"key"`
	Values  []string `json:"values"`
	Default string   `json:"default"`
	// Mutable options can be changed after creation (the instance is
	// restarted); the others would lose or misread the data.
	Mutable bool `json:"mutable"`
	// Custom, for sizes: any whole number of MB in the range is accepted
	// besides Values (e.g. 3072 for 3 GB).
	Custom *CustomRange `json:"custom,omitempty"`
}

// CustomRange bounds the custom values of a size option, in MB.
type CustomRange struct {
	Min  int `json:"min"`
	Max  int `json:"max"`
	Step int `json:"step"` // values are multiples of Step
}

// Accepts reports whether a value is valid for the option: one of Values,
// or a custom size in canonical form (digits, no leading zero) within the
// range and a multiple of the step.
func (o Option) Accepts(v string) bool {
	if slices.Contains(o.Values, v) {
		return true
	}
	c := o.Custom
	if c == nil || v == "" || len(v) > 9 || v[0] == '0' {
		return false
	}
	n := 0
	for _, r := range v {
		if r < '0' || r > '9' {
			return false
		}
		n = n*10 + int(r-'0')
	}
	return n >= c.Min && n <= c.Max && n%c.Step == 0
}
