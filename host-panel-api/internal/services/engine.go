package services

import "context"

// Engine describes a kind of service instance (PostgreSQL, MariaDB...).
// Engines only provide what is specific to them: the Manager renders the
// common parts of the compose file (name, container, secret, data mount,
// network, logging, labels, hardening) and enforces the invariants (no
// published port, internal network only, secret as a file).
type Engine interface {
	ID() string // "postgres", "s3"; 2 to 16 lowercase letters and digits, starting with a letter
	Kind() Kind
	DefaultPort() int
	// Lifecycle is OnDemand (shared instance, Provisioner required) or
	// Dedicated (one instance per resource, DedicatedEngine required).
	Lifecycle() Lifecycle
	// Catalog is the built-in catalog, newest first: the versions offered
	// until a discovery on the registries succeeded (see Releases).
	Catalog() []Version
	// Releases tells where the versions of the engine are published.
	Releases() Releases
	// VersionOf returns the catalog entry of any version the engine
	// supports, running the image of tag (a tag of that version, from
	// Releases.Tag): image reference, data mount, platform, name limit.
	VersionOf(version, tag string) Version
	// DataOwner owns the data directory, created by the panel before the
	// first start (PostgreSQL 18 does not fix the ownership itself).
	DataOwner() (uid, gid int)
	// Service returns the engine-specific part of the compose service. opts
	// are the validated options of a dedicated instance (nil otherwise).
	Service(ref InstanceRef, v Version, opts Options) ServiceSpec
	// Provisioner returns the resource capability of OnDemand engines (nil
	// for Dedicated ones).
	Provisioner() Provisioner
	// ConnectionInfo returns what a client needs to connect to a resource.
	ConnectionInfo(p ConnParams) []Credential
}

// ConnParams describe how a client reaches a resource.
type ConnParams struct {
	Host     string
	Port     int
	Name     string
	Password string
	TLS      bool // the instance requires TLS (rediss://, amqps://...)
}

// DedicatedEngine is implemented by Dedicated engines. The resource is the
// instance: its credentials are rendered into state files, written by the
// panel in the instance directory (StateDir) and mounted read-only at
// StateMount. They hold password digests only, except for engines whose
// protocol needs the secret itself (SigV4): those set ServiceSpec.SecretState.
type DedicatedEngine interface {
	// Options lists the settings offered at creation.
	Options() []Option
	// ValidateName adds the engine rules to the shared ones.
	ValidateName(name string) *Error
	// PasswordDigest computes the digest of a generated password (refusing
	// any other). The panel keeps it in the private directory, so that the
	// state can be rendered again when the options change.
	PasswordDigest(password string) (string, error)
	// StateFiles renders the credential files of the instance from the
	// digest. It must check the digest's form (it is read back from disk).
	// Healthchecks must not need a secret (e.g. an unauthenticated PING
	// answered by NOAUTH).
	StateFiles(ref InstanceRef, v Version, opts Options, digest string) ([]GeneratedFile, error)
}

// ServiceSpec is the engine-specific part of the "db" compose service.
type ServiceSpec struct {
	Command     []string
	Environment map[string]string // never a secret: secrets go through files
	Healthcheck []string          // exec form, e.g. ["CMD","pg_isready",...]
	ShmSize     string
	PidsLimit   int // 0 = defaultPidsLimit
	// StopGracePeriod lets the engine flush before being killed on stop
	// ("" = defaultStopGracePeriod).
	StopGracePeriod string
	// Tmpfs are paths mounted as tmpfs ("path:options"): volumes declared
	// by the image that the engine does not need (they would otherwise
	// become anonymous volumes, refused by the container verification).
	Tmpfs []string
	// Files are written in the instance directory and mounted read-only.
	Files []GeneratedFile

	// User runs the container as "uid:gid" ("" = image default).
	User string
	// MemLimit (bytes) and CPUs bound the container (0 = unlimited).
	MemLimit int64
	CPUs     float64
	// NoAdminSecret: the engine has no admin account (nothing runs inside,
	// or admin commands use a local channel): no secret is generated.
	NoAdminSecret bool
	// State mounts the panel-written state directory (DedicatedEngine).
	State bool
	// SecretState: the state files hold a secret in clear (S3 SigV4 needs
	// it). They are written 0400 and owned by DataOwner instead of 0644.
	SecretState bool
	// GeneratedKeys are state files holding a random key (32 bytes, base64)
	// created once with the instance and kept for its whole life (e.g. an
	// encryption key: losing it loses the data). Written like SecretState.
	GeneratedKeys []string
	// Hardened drops every capability and makes the root filesystem
	// read-only (writable paths go through Tmpfs and the data mount). Only
	// for images that need no privilege to start (User set, data directory
	// owned by DataOwner beforehand).
	Hardened bool
}

// GeneratedFile is a config file rendered by the engine.
type GeneratedFile struct {
	Name    string // file name in the instance directory (one segment)
	Target  string // absolute path in the container
	Content []byte
}

// Target is the verified instance a provisioner works on: its version (the
// dialect may depend on it) and an executor bound to its container. It is
// the extension point shared by every engine; capabilities needed by future
// engines (e.g. managed config files for NATS) are added here.
type Target struct {
	Version Version
	Executor
}

// Unauthenticated is implemented by dedicated engines without any
// authentication (ElasticMQ): every site on the services network reaches
// them. No password is generated or rotated; clients are warned.
type Unauthenticated interface {
	Unauthenticated() bool
}

// IsUnauthenticated reports whether an engine has no authentication.
func IsUnauthenticated(e Engine) bool {
	u, ok := e.(Unauthenticated)
	return ok && u.Unauthenticated()
}

// OptionValidator is implemented by dedicated engines whose options depend
// on the version (e.g. a setting the image only honors from a release on):
// an option the version would silently ignore is refused.
type OptionValidator interface {
	ValidateOptions(v Version, opts Options) *Error
}

// LegacyAuthProvisioner is implemented by provisioners offering, per
// resource, an older password mechanism for clients that lack the default
// one (MySQL 8.4 mysql_native_password instead of caching_sha2_password,
// PostgreSQL md5 instead of SCRAM-SHA-256). The passwords stay generated
// (190 bits): what is lost is the protection of the exchange against an
// observer of the network. SetPassword keeps the mechanism of the user.
type LegacyAuthProvisioner interface {
	// LegacyAuth reports whether the version offers the legacy mechanism
	// (false when its default already is the legacy one, e.g. MariaDB).
	LegacyAuth(v Version) bool
	// ProvisionLegacy is Provision with the legacy mechanism.
	ProvisionLegacy(ctx context.Context, t Target, name, password string) error
}

// SupportsLegacyAuth reports whether resources of a version can use the
// legacy password mechanism.
func SupportsLegacyAuth(e Engine, v Version) bool {
	if e.Lifecycle() != OnDemand {
		return false
	}
	lp, ok := e.Provisioner().(LegacyAuthProvisioner)
	return ok && lp.LegacyAuth(v)
}

// SupportsTLS reports whether a version can require TLS (the name of a
// dedicated instance does not change it).
func SupportsTLS(e Engine, v Version) bool {
	return supportsTLS(e, InstanceRef{Engine: e.ID(), Version: v.Version}, v)
}

// SupportsOptionalTLS reports whether a version can offer TLS without
// requiring it.
func SupportsOptionalTLS(e Engine, v Version) bool {
	return supportsOptionalTLS(e, InstanceRef{Engine: e.ID(), Version: v.Version}, v)
}

// Provisioner creates and deletes resources in a running instance. Every
// method receives the Target of the verified container of the instance. Names passed in were accepted by ValidateName (shared rules) and
// by the provisioner's own ValidateName; passwords are generated by the
// Manager (IsGeneratedPassword).
//
// List must return every user-level object the panel could have created
// (databases and users without a database alike): an instance is retired,
// and its data deleted, when List is empty.
//
// Provisioners return ErrResourceExists, ErrResourceNotFound,
// ErrOutcomeUnknown or an *EngineError; their errors never contain engine
// output.
type Provisioner interface {
	// ValidateName adds the engine rules (e.g. a shorter limit on some
	// versions) to the shared ones.
	ValidateName(v Version, name string) *Error
	// Init hardens the instance. It must be idempotent: it runs before every
	// provisioning, so an instance whose first creation was interrupted is
	// hardened too.
	Init(ctx context.Context, t Target) error
	List(ctx context.Context, t Target) ([]Resource, error)
	Provision(ctx context.Context, t Target, name, password string) error
	Deprovision(ctx context.Context, t Target, name string) error
	SetPassword(ctx context.Context, t Target, name, password string) error
}
