package functions

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"host-panel-api/internal/registry"
	"host-panel-api/internal/releases"
)

// ProjectPrefix prefixes the compose project, the container and the
// directories of a function.
const ProjectPrefix = "fn-"

// DedicatedNetworkPrefix prefixes the registry name of the network a
// function gets by default (decision log §20, plan reviews: never
// "services"; callers are attached explicitly).
const DedicatedNetworkPrefix = "fn-"

// nameRe: at most 21 characters, so that the default network fn-<name>
// fits the 24 characters of a network name.
var nameRe = regexp.MustCompile(`\A[a-z](?:[a-z0-9-]{0,19}[a-z0-9])?\z`)

// MaxNameLen is the length of the longest function name.
const MaxNameLen = 21

// reservedNames are path segments of the API and the interface
// (/functions/config, /functions/new) or names with a meaning.
var reservedNames = map[string]bool{
	"config": true, "new": true, "default": true, "all": true, "latest": true,
	"function": true, "functions": true, "deployments": true, "invoke": true,
	"panel": true, "loch": true, "system": true, "api": true,
}

// ValidateName checks a function name (also its hostname on its networks).
func ValidateName(name string) *Error {
	if !nameRe.MatchString(name) {
		return newError(CodeInvalidName, "name", "a function name is 1 to %d lowercase letters, digits or dashes, starting with a letter and not ending with a dash", MaxNameLen)
	}
	if reservedNames[name] {
		return newError(CodeInvalidName, "name", "this function name is reserved")
	}
	return nil
}

// Name is a validated function name: every path, project and network name
// of a function is computed from one, so that no unvalidated string ever
// reaches the file system. The zero Name is invalid (methods taking it fail
// or compute paths that never exist).
type Name struct{ s string }

// ParseName validates a function name.
func ParseName(s string) (Name, *Error) {
	if err := ValidateName(s); err != nil {
		return Name{}, err
	}
	return Name{s}, nil
}

// String returns the name.
func (n Name) String() string { return n.s }

// valid reports whether n came from ParseName.
func (n Name) valid() bool { return n.s != "" }

// Project is the compose project and container name of a function.
func (n Name) Project() string { return ProjectPrefix + n.s }

// DedicatedNetwork is the registry name of the default network of a function.
func (n Name) DedicatedNetwork() string { return DedicatedNetworkPrefix + n.s }

// Runtime is a supported language runtime (an official AWS Lambda base image).
type Runtime struct {
	ID         string
	Repository string // Docker Hub repository, pulled through registry.Hub
	// Entrypoint is the image's ENTRYPOINT, passed after "--" to the loch
	// runtime. v1 accepts the AWS entrypoint only.
	Entrypoint []string
	// Custom: an OS-only image (provided.al2023) running the "bootstrap"
	// executable of the code (Go, Rust, any compiled language). The code is
	// checked for it at deploy time (checkBootstrap).
	Custom bool
	// DefaultHandler is used when none is given (custom runtimes only).
	DefaultHandler string
	versionRe      *regexp.Regexp
	tagRe          *regexp.Regexp // the image tags of any of its versions
	handlerRe      *regexp.Regexp
	handlerHint    string
}

// handlerRe: "file.export" (Node) or "module.function" / "pkg.module.function"
// (Python), the file part being a relative path without "." or ".."
// components. "$" is excluded (compose interpolation) as are spaces.
var handlerRe = regexp.MustCompile(`\A[A-Za-z0-9_-]+(?:/[A-Za-z0-9_-]+)*(?:\.[A-Za-z_][A-Za-z0-9_]*)+\z`)

// customHandlerRe: a custom runtime receives the handler as _HANDLER only
// (the bootstrap may read it, as on AWS): any name of letters, digits,
// "_", "-" and ".", optionally slash-separated, no component starting with
// "." or "-" (no "..", no option-like value).
var customHandlerRe = regexp.MustCompile(`\A[A-Za-z0-9_][A-Za-z0-9_.-]*(?:/[A-Za-z0-9_][A-Za-z0-9_.-]*)*\z`)

const handlerHint = "the handler must be <file>.<function> (e.g. index.handler or app/main.handler)"

// BootstrapFile is the executable a custom runtime starts, at the root of
// the code (/var/task/bootstrap).
const BootstrapFile = "bootstrap"

var runtimes = map[string]Runtime{
	"nodejs": {ID: "nodejs", Repository: "amazon/aws-lambda-nodejs", Entrypoint: []string{"/lambda-entrypoint.sh"},
		versionRe: regexp.MustCompile(`\A[1-9][0-9]\z`), tagRe: numericTagRe, handlerRe: handlerRe, handlerHint: handlerHint},
	"python": {ID: "python", Repository: "amazon/aws-lambda-python", Entrypoint: []string{"/lambda-entrypoint.sh"},
		versionRe: regexp.MustCompile(`\A3\.[1-9][0-9]?\z`), tagRe: numericTagRe, handlerRe: handlerRe, handlerHint: handlerHint},
	// The provided images run /var/task/bootstrap through the same
	// entrypoint, which exports its argument as _HANDLER (lambda-runtime
	// spec, integration test TestProvidedAL2023). The version form admits
	// a later Amazon Linux for existing references; creation is bounded
	// by RuntimeVersions.
	"provided": {ID: "provided", Repository: "amazon/aws-lambda-provided", Entrypoint: []string{"/lambda-entrypoint.sh"},
		Custom: true, DefaultHandler: BootstrapFile,
		versionRe: regexp.MustCompile(`\Aal20[2-9][0-9]\z`), tagRe: alTagRe, handlerRe: customHandlerRe,
		handlerHint: "the handler of a custom runtime is a name such as bootstrap (letters, digits, _ - . and /)"},
}

// LookupRuntime returns a supported runtime.
func LookupRuntime(id string) (Runtime, bool) {
	r, ok := runtimes[id]
	return r, ok
}

// RuntimeIDs returns the supported runtimes, sorted.
func RuntimeIDs() []string {
	ids := make([]string, 0, len(runtimes))
	for id := range runtimes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// ValidVersion reports whether a runtime version has the expected form
// ("22", "3.13"). Availability is decided by the version discovery.
func (r Runtime) ValidVersion(v string) bool { return r.versionRe.MatchString(v) }

// Architectures: Go names (runtime binary, Docker image architecture) and
// the suffix of the AWS image tags (decision log §20, phase 0: live builds
// are arch-suffixed, without a manifest list).
var archTagSuffix = map[string]string{"amd64": "x86_64", "arm64": "arm64"}

// ValidArch reports whether an architecture is supported.
func ValidArch(arch string) bool { _, ok := archTagSuffix[arch]; return ok }

// AWS image tags: the line tag ("22-arm64", "al2023-arm64") or a dated
// build ("22.2026.11.05.18-arm64", "al2023.2026.11.05.18-arm64"); the
// "al" form belongs to the provided runtime only.
var (
	numericTagRe = regexp.MustCompile(`\A[0-9]+(?:\.[0-9]+)*-(?:arm64|x86_64)\z`)
	alTagRe      = regexp.MustCompile(`\Aal[0-9]+(?:\.[0-9]+)*-(?:arm64|x86_64)\z`)
)

// LineTag is the floating tag of a runtime version for an architecture.
func LineTag(version, arch string) string { return version + "-" + archTagSuffix[arch] }

// validTag reports whether tag is a build of version (of this runtime) for
// arch.
func (r Runtime) validTag(version, arch, tag string) bool {
	if !r.ValidVersion(version) || !r.tagRe.MatchString(tag) || !strings.HasSuffix(tag, "-"+archTagSuffix[arch]) {
		return false
	}
	base := strings.TrimSuffix(tag, "-"+archTagSuffix[arch])
	return base == version || strings.HasPrefix(base, version+".")
}

// ImageRepository is the reference of the runtime image without tag nor
// digest (the RepoDigests entry the daemon records).
func (r Runtime) ImageRepository() string {
	repo, _, _ := strings.Cut(registry.Hub(r.Repository+":x"), ":")
	return repo
}

// ImageRef is the pinned reference of a runtime image: tag for readers,
// digest for the daemon (mirror.gcr.io/amazon/aws-lambda-nodejs:22-arm64@sha256:…).
func (r Runtime) ImageRef(tag, digest string) string {
	return registry.Hub(r.Repository+":"+tag) + "@" + digest
}

const maxHandler = 128

// ValidateHandler checks the handler of a runtime.
func (r Runtime) ValidateHandler(h string) *Error {
	if len(h) > maxHandler || !r.handlerRe.MatchString(h) {
		return newError(CodeInvalidHandler, "handler", "%s", r.handlerHint)
	}
	return nil
}

// HandlerOrDefault returns the handler to record: the given one, or the
// default of a custom runtime when empty.
func (r Runtime) HandlerOrDefault(h string) string {
	if h == "" {
		return r.DefaultHandler
	}
	return h
}

// EnvVar is a non-secret environment variable of a function.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

var envNameRe = regexp.MustCompile(`\A[A-Za-z_][A-Za-z0-9_]{0,127}\z`)

// Bounds of the variables and secrets (Lambda allows 4 KB in total; the
// panel is more generous but bounded: they end up in the container config).
const (
	maxEnvValue = 4096
	maxEnvVars  = 100
	maxEnvTotal = 64 << 10
)

// reservedEnvPrefixes and reservedEnvNames are set by the panel, the loch
// runtime or the AWS bootstrap, or would change how the runtime loads code.
// AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY / AWS_SESSION_TOKEN and
// AWS_ENDPOINT_URL* stay allowed: they are how a function reaches Loch S3.
var reservedEnvPrefixes = []string{"AWS_LAMBDA_", "LAMBDA_", "LD_", "LOCH_RUNTIME_", "AWS_CONTAINER_", "AWS_XRAY_", "_"}

var reservedEnvNames = map[string]bool{
	"AWS_REGION": true, "AWS_DEFAULT_REGION": true, "AWS_EXECUTION_ENV": true,
	"AWS_EC2_METADATA_DISABLED": true, "PATH": true, "HOME": true, "HOSTNAME": true,
	"PYTHONDONTWRITEBYTECODE": true,
}

// ValidateEnvName checks the name of a variable or a secret.
func ValidateEnvName(name, field string) *Error {
	if !envNameRe.MatchString(name) {
		return newError(CodeInvalidEnv, field, "invalid variable name %s: letters, digits and underscores, not starting with a digit", quoteName(name))
	}
	upper := strings.ToUpper(name)
	if reservedEnvNames[upper] {
		return newError(CodeReservedEnv, field, "the variable %s is set by the panel", name)
	}
	for _, p := range reservedEnvPrefixes {
		if strings.HasPrefix(upper, p) {
			return newError(CodeReservedEnv, field, "variables starting with %s are reserved", p)
		}
	}
	return nil
}

// ValidateEnvValue checks the value of a variable or a secret: one line of
// UTF-8 (the env_file is line based), no NUL.
func ValidateEnvValue(v, field string) *Error {
	if len(v) > maxEnvValue {
		return newError(CodeInvalidEnv, field, "a value is at most %d bytes", maxEnvValue)
	}
	if !utf8.ValidString(v) || strings.ContainsAny(v, "\x00\r\n") {
		return newError(CodeInvalidEnv, field, "a value is one line of UTF-8 text")
	}
	return nil
}

// ValidateEnv checks the variables and secret names of a function: valid,
// not reserved, unique across both.
func ValidateEnv(env []EnvVar, secrets []string) *Error {
	if len(env)+len(secrets) > maxEnvVars {
		return newError(CodeInvalidEnv, "env", "at most %d variables and secrets", maxEnvVars)
	}
	seen := map[string]bool{}
	total := 0
	for _, v := range env {
		if err := ValidateEnvName(v.Name, "env"); err != nil {
			return err
		}
		if err := ValidateEnvValue(v.Value, "env"); err != nil {
			return err
		}
		if seen[v.Name] {
			return newError(CodeInvalidEnv, "env", "variable %s listed twice", v.Name)
		}
		seen[v.Name] = true
		total += len(v.Name) + len(v.Value)
	}
	for _, s := range secrets {
		if err := ValidateEnvName(s, "secrets"); err != nil {
			return err
		}
		if seen[s] {
			return newError(CodeInvalidEnv, "secrets", "%s is both a variable and a secret, or listed twice", s)
		}
		seen[s] = true
	}
	if total > maxEnvTotal {
		return newError(CodeInvalidEnv, "env", "the variables exceed %d bytes", maxEnvTotal)
	}
	return nil
}

// Limits are the resources of a function.
type Limits struct {
	MemoryMB       int     `json:"memoryMB"`
	CPUs           float64 `json:"cpus"`
	TimeoutSeconds int     `json:"timeoutSeconds"`
	TmpSizeMB      int     `json:"tmpSizeMB"`
}

// ValidateLimits checks the resources against the configuration. The /tmp
// tmpfs counts against the memory limit (documented, not refused: Lambda
// defaults to 128 MB of memory and 512 MB of /tmp).
func ValidateLimits(l Limits, cfg Config) *Error {
	maxMem := cfg.MaxMemoryMB
	if maxMem == 0 {
		maxMem = DefaultMaxMemoryMB
	}
	switch {
	case l.MemoryMB < MinMemoryMB || l.MemoryMB > maxMem:
		return newError(CodeInvalidLimits, "memoryMB", "the memory is between %d and %d MB", MinMemoryMB, maxMem)
	case l.CPUs < MinCPUs || l.CPUs > MaxCPUs || math.Abs(l.CPUs*100-math.Round(l.CPUs*100)) > 1e-6:
		return newError(CodeInvalidLimits, "cpus", "the CPU limit is between %g and %g, in steps of 0.01", MinCPUs, float64(MaxCPUs))
	case l.TimeoutSeconds < 1 || l.TimeoutSeconds > MaxTimeoutSeconds:
		return newError(CodeInvalidLimits, "timeoutSeconds", "the timeout is between 1 and %d seconds", MaxTimeoutSeconds)
	case l.TmpSizeMB < MinTmpMB || l.TmpSizeMB > MaxTmpMB:
		return newError(CodeInvalidLimits, "tmpSizeMB", "the /tmp size is between %d and %d MB", MinTmpMB, MaxTmpMB)
	}
	return nil
}

// Deployment is the code a function runs: an extracted zip.
type Deployment struct {
	ID        string `json:"id"`
	TreeHash  string `json:"treeHash"`  // TreeHash of the extracted, read-only directory
	ZipSHA256 string `json:"zipSha256"` // the uploaded archive, for the audit trail
	ZipSize   int64  `json:"zipSize"`
	Files     int    `json:"files"`
	Bytes     int64  `json:"bytes"` // extracted
	// Warnings are codes of deploy-time checks that did not refuse the
	// code (WarnBootstrapDynamic).
	Warnings []string `json:"warnings,omitempty"`
}

var deploymentIDRe = regexp.MustCompile(`\A[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}\z`)

// ValidDeploymentID reports whether id is a deployment id (NewDeploymentID).
func ValidDeploymentID(id string) bool { return deploymentIDRe.MatchString(id) }

var hexSHA256Re = regexp.MustCompile(`\A[0-9a-f]{64}\z`)

// RuntimeBinary pins the loch function runtime a function runs.
type RuntimeBinary struct {
	Version string `json:"version"`
	Arch    string `json:"arch"`
	SHA256  string `json:"sha256"`
}

// maxKeptDeployments bounds the kept deployments of a reference
// (FUNCTIONS_DEPLOYMENTS_KEPT is at most 20, the active one included).
const maxKeptDeployments = 20

// ReferenceVersion is the format of function.json.
const ReferenceVersion = 1

// Reference is what the panel records about a function, in its private
// directory (function.json). The compose file must be exactly the one
// rendered from it; the container must match it (Verify).
type Reference struct {
	Version        int    `json:"version"`
	Name           string `json:"name"`
	Runtime        string `json:"runtime"`
	RuntimeVersion string `json:"runtimeVersion"`
	Arch           string `json:"arch"`
	ImageTag       string `json:"imageTag"`
	ImageDigest    string `json:"imageDigest"` // manifest digest, pulled by digest
	// ImageID is the local image ID observed after the pull (the config
	// digest; with the containerd image store, the manifest digest).
	ImageID    string   `json:"imageId,omitempty"`
	Entrypoint []string `json:"entrypoint"` // the image's, after "--"
	Handler    string   `json:"handler"`
	Limits
	Env      []EnvVar `json:"env"`
	Secrets  []string `json:"secrets"` // names only; values in the private env_file
	Networks []string `json:"networks"`
	// Egress gives outbound access (a compose-owned, non-internal network).
	Egress bool `json:"egress,omitempty"`
	// SigV4 accepts SigV4 callers besides the bearer token: the key pair is
	// then in the container (readable by the function code).
	SigV4 bool `json:"sigv4,omitempty"`
	// MCPInvocable lets the loch MCP tool invoke the function (off by
	// default). A guard-rail enforced by the MCP client, not an access
	// control: the API cannot tell an MCP client from another one.
	MCPInvocable bool `json:"mcpInvocable,omitempty"`
	// Stopped: stopped by an admin; configuration changes are written
	// without starting it.
	Stopped    bool        `json:"stopped,omitempty"`
	Deployment *Deployment `json:"deployment,omitempty"`
	// Kept are the previous deployments kept for a rollback (oldest first,
	// the active one excluded); their directories are verified against
	// these hashes before an activation.
	Kept          []Deployment  `json:"kept,omitempty"`
	RuntimeBinary RuntimeBinary `json:"runtimeBinary"`
	CreatedAt     time.Time     `json:"createdAt"`

	// droppedWarnings: unknown warning codes removed when the reference
	// was read (ReadReference), logged once by the manager.
	droppedWarnings []string
}

// FunctionName returns the validated name of a reference.
func (r *Reference) FunctionName() (Name, *Error) { return ParseName(r.Name) }

// runtime returns the runtime of a validated reference.
func (r *Reference) runtime() Runtime { return runtimes[r.Runtime] }

// Image is the pinned image reference.
func (r *Reference) Image() string { return r.runtime().ImageRef(r.ImageTag, r.ImageDigest) }

// Validate checks a reference entirely: what is rendered and verified comes
// from it, so even the panel's own file is not trusted blindly.
func (r *Reference) Validate(cfg Config) *Error {
	bad := func(format string, args ...any) *Error {
		return newError(CodeInvalidReference, "", "function reference: "+format, args...)
	}
	if r.Version != ReferenceVersion {
		return bad("unsupported version %d", r.Version)
	}
	if err := ValidateName(r.Name); err != nil {
		return err
	}
	rt, ok := LookupRuntime(r.Runtime)
	if !ok || !rt.ValidVersion(r.RuntimeVersion) {
		return newError(CodeInvalidRuntime, "runtime", "unsupported runtime %s %s", quoteName(r.Runtime), quoteName(r.RuntimeVersion))
	}
	if !ValidArch(r.Arch) || !rt.validTag(r.RuntimeVersion, r.Arch, r.ImageTag) || !releases.ValidDigest(r.ImageDigest) {
		return bad("invalid image tag or digest")
	}
	if r.ImageID != "" && !releases.ValidDigest(r.ImageID) {
		return bad("invalid image id")
	}
	if !slices.Equal(r.Entrypoint, rt.Entrypoint) {
		return bad("unsupported image entrypoint")
	}
	if err := rt.ValidateHandler(r.Handler); err != nil {
		return err
	}
	if err := ValidateLimits(r.Limits, cfg); err != nil {
		return err
	}
	if err := ValidateEnv(r.Env, r.Secrets); err != nil {
		return err
	}
	if !slices.IsSortedFunc(r.Env, func(a, b EnvVar) int { return strings.Compare(a.Name, b.Name) }) || !slices.IsSorted(r.Secrets) {
		return bad("variables and secrets must be sorted")
	}
	if len(r.Networks) == 0 {
		return newError(CodeInvalidNetwork, "networks", "a function needs at least one network")
	}
	if !slices.IsSorted(r.Networks) || len(slices.Compact(slices.Clone(r.Networks))) != len(r.Networks) {
		return newError(CodeInvalidNetwork, "networks", "networks must be sorted and unique")
	}
	validDeployment := func(d *Deployment) bool {
		return ValidDeploymentID(d.ID) && hexSHA256Re.MatchString(d.TreeHash) && hexSHA256Re.MatchString(d.ZipSHA256) &&
			d.ZipSize >= 0 && d.Files >= 0 && d.Bytes >= 0 && validWarnings(d.Warnings)
	}
	if d := r.Deployment; d != nil && !validDeployment(d) {
		return bad("invalid deployment")
	}
	if len(r.Kept) > maxKeptDeployments {
		return bad("too many kept deployments")
	}
	seen := map[string]bool{}
	if r.Deployment != nil {
		seen[r.Deployment.ID] = true
	}
	for i := range r.Kept {
		d := &r.Kept[i]
		if !validDeployment(d) || seen[d.ID] || (i > 0 && r.Kept[i-1].ID >= d.ID) {
			return bad("invalid kept deployments (unique, valid, oldest first)")
		}
		seen[d.ID] = true
	}
	b := r.RuntimeBinary
	if !runtimeVersionRe.MatchString(b.Version) || b.Arch != r.Arch || !hexSHA256Re.MatchString(b.SHA256) {
		return bad("invalid runtime binary (architecture must be the image's)")
	}
	return nil
}

// Sort puts the lists of a reference in their canonical order.
func (r *Reference) Sort() {
	slices.SortFunc(r.Env, func(a, b EnvVar) int { return strings.Compare(a.Name, b.Name) })
	slices.Sort(r.Secrets)
	slices.Sort(r.Networks)
}

func (r *Reference) String() string {
	return fmt.Sprintf("function %s (%s %s)", r.Name, r.Runtime, r.RuntimeVersion)
}
