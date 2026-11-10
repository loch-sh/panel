package hosting

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // timezone validation must not depend on the host tzdata
	"unicode"
	"unicode/utf8"

	"host-panel-api/internal/networks"
)

// Error codes returned by the hosting package. The handler maps them to HTTP
// statuses; the frontend maps them to localized messages.
const (
	CodeInvalidName       = "INVALID_HOSTING_NAME"
	CodeNameTaken         = "HOSTING_NAME_TAKEN"
	CodeStackNotFound     = "HOSTING_STACK_NOT_FOUND"
	CodeInvalidTemplate   = "INVALID_HOSTING_TEMPLATE"
	CodeInvalidPHPVersion = "INVALID_PHP_VERSION"
	CodeInvalidPHPSetting = "INVALID_PHP_SETTING"
	CodeInvalidPHPModule  = "INVALID_PHP_MODULE"
	CodeImageInspect      = "HOSTING_IMAGE_INSPECT_FAILED"
	CodeInvalidSite       = "INVALID_HOSTING_SITE"
	// CodeInvalidNetwork: unknown, duplicated or unusable private network.
	CodeInvalidNetwork   = "INVALID_HOSTING_NETWORK"
	CodeSiteInUse        = "HOSTING_SITE_IN_USE"
	CodeInvalidDocRoot   = "INVALID_DOCUMENT_ROOT"
	CodeInvalidDomain    = "INVALID_DOMAIN"
	CodeDomainInUse      = "DOMAIN_IN_USE"
	CodeInvalidResources = "INVALID_RESOURCES"
	CodeInvalidEnvVar    = "INVALID_ENV_VAR"
	CodeReservedEnvVar   = "RESERVED_ENV_VAR"
	CodeInvalidSnippet   = "INVALID_SNIPPET"
	CodeInvalidRouting   = "INVALID_ROUTING"
	CodeOperationRunning = "HOSTING_OPERATION_IN_PROGRESS"
	CodeManuallyModified = "HOSTING_MANUALLY_MODIFIED"
	CodeStorage          = "HOSTING_STORAGE_ERROR"
	// Operation error codes: HOSTING_<KIND>_FAILED (see OperationFailedCode)
	// and CodeOperationInterrupted when the API restarted meanwhile.
	CodeOperationInterrupted = "HOSTING_OPERATION_INTERRUPTED"
)

// Reasons attached to CodeNameTaken.
const (
	ReasonReserved      = "reserved"
	ReasonStack         = "stack"
	ReasonDirectory     = "directory"
	ReasonProject       = "compose_project"
	ReasonContainer     = "container"
	ReasonTraefikRouter = "traefik_router"
	// ReasonWebServerChanged: INVALID_ROUTING for custom rules kept across
	// an Nginx/Apache template change.
	ReasonWebServerChanged = "web_server_changed"
)

// Error is a typed, client-facing error. Message is an English fallback;
// Field and Reason give the frontend enough context to highlight the input.
type Error struct {
	Code    string
	Field   string
	Reason  string
	Message string
}

func (e *Error) Error() string { return e.Message }

func newError(code, field, format string, args ...any) *Error {
	return &Error{Code: code, Field: field, Message: fmt.Sprintf(format, args...)}
}

// Limits bounds what a stack may request.
type Limits struct {
	MaxMemoryMB     int     `json:"maxMemoryMB"`
	MaxCPUs         float64 `json:"maxCpus"`
	MaxDomains      int     `json:"maxDomains"`
	MaxEnvVars      int     `json:"maxEnvVars"`
	MaxSnippetBytes int     `json:"maxSnippetBytes"`
}

const (
	minMemoryMB        = 64
	minCPUs            = 0.1
	maxNameLength      = 32
	maxSegmentLength   = 255
	maxPathLength      = 1024
	maxEnvNameLength   = 128
	maxEnvValueBytes   = 4096
	maxExecutionTime   = 3600
	maxDisabledModules = 64
)

var (
	// A DNS label: valid as compose project, container, Traefik router and
	// host name. Lowercase only, so names cannot differ by case alone.
	nameRe    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	labelRe   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	sizeRe    = regexp.MustCompile(`^([1-9][0-9]{0,4})([KMG])$`)
	moduleRe  = regexp.MustCompile(`^[a-z0-9_]{2,64}$`)

	reservedNames = []string{"admin", "api", "check-name", "default", "loch", "mail", "root", "sftpgo", "system", "traefik", "www"}
)

// managedEnvVars are set by the panel from the form (or must never be
// overridden: run user, dynamic linker).
var managedEnvVars = []string{
	"PHP_MEMORY_LIMIT", "PHP_UPLOAD_MAX_FILESIZE", "PHP_POST_MAX_SIZE",
	"PHP_MAX_EXECUTION_TIME", "PHP_DISPLAY_ERRORS", "PHP_DATE_TIMEZONE", "PHP_DISMOD",
	"WEB_DOCUMENT_ROOT", "WEB_PHP_TIMEOUT",
}

var reservedEnvPrefixes = []string{"APPLICATION_", "CONTAINER_", "LD_"}

// ValidateName checks the format of a stack name (without prefix).
func ValidateName(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	return nil // never return a typed nil *Error as an error
}

func validateName(name string) *Error {
	if name == "" || len(name) > maxNameLength || !nameRe.MatchString(name) {
		return newError(CodeInvalidName, "name",
			"name must be 1-%d lowercase letters, digits or dashes, not starting or ending with a dash", maxNameLength)
	}
	if slices.Contains(reservedNames, name) {
		return &Error{Code: CodeNameTaken, Field: "name", Reason: ReasonReserved, Message: "this name is reserved"}
	}
	return nil
}

// ValidPathSegment reports whether s is a safe single directory name: no
// separators, no "." or "..", no control characters, no "$" (Compose would
// interpolate it) and no ":" (volume separator). A leading dot is allowed.
func ValidPathSegment(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > maxSegmentLength {
		return false
	}
	for _, r := range s {
		if r == '/' || r == '\\' || r == '$' || r == ':' || unicode.IsControl(r) {
			return false
		}
	}
	return utf8.ValidString(s)
}

// validRelativePath validates a "/"-separated relative path ("" allowed).
func validRelativePath(p string) bool {
	if p == "" {
		return true
	}
	if len(p) > maxPathLength {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if !ValidPathSegment(seg) {
			return false
		}
	}
	return true
}

// webPathSegmentRe allows the characters a path written into web server
// configuration can safely contain (document root, Symfony asset
// directories): no quotes, spaces, braces, ";" or glob characters.
var webPathSegmentRe = regexp.MustCompile(`^[A-Za-z0-9._@+~-]+$`)

// validWebPath validates a relative path used in web server configuration.
func validWebPath(p string) bool {
	if !validRelativePath(p) {
		return false
	}
	if p == "" {
		return true
	}
	for _, seg := range strings.Split(p, "/") {
		if !webPathSegmentRe.MatchString(seg) {
			return false
		}
	}
	return true
}

// ValidateDomain checks a host name (lowercase, at least one dot, no wildcard).
func ValidateDomain(d string) bool {
	if len(d) > 253 || !strings.Contains(d, ".") {
		return false
	}
	for _, label := range strings.Split(d, ".") {
		if !labelRe.MatchString(label) {
			return false
		}
	}
	return true
}

// validateStored checks the fields of a spec read from disk that the panel
// uses to build paths or commands. A compose file in the shared stacks
// directory may have been written by something else than the panel: its
// x-loch section is untrusted input.
func validateStored(spec *Spec) *Error {
	if spec.Version < 1 || spec.Version > SpecVersion {
		return newError(CodeStackNotFound, "", "unsupported x-loch version %d", spec.Version)
	}
	if err := validateName(spec.Name); err != nil {
		return err
	}
	if !spec.Template.Valid() {
		return newError(CodeInvalidTemplate, "template", "unknown template %q", spec.Template)
	}
	if spec.Template.HasPHP() && !validPHPVersion(spec.PHPVersion) {
		return newError(CodeInvalidPHPVersion, "phpVersion", "unsupported PHP version %q", spec.PHPVersion)
	}
	if !ValidPathSegment(spec.Site) {
		return newError(CodeInvalidSite, "site", "invalid site directory name")
	}
	if !validWebPath(spec.DocumentRoot) {
		return newError(CodeInvalidDocRoot, "documentRoot", "invalid document root")
	}
	if err := validateRouting(spec, Snippets{}); err != nil {
		return err
	}
	if err := validateNetworks(spec.Networks); err != nil {
		return err
	}
	return validateDomains(spec.Domains, 1<<10)
}

// maxNetworks bounds the networks of a stack.
const maxNetworks = 16

// validateNetworks checks the network names of a spec (sorted, unique).
// Whether they exist is checked when rendering.
func validateNetworks(names []string) *Error {
	if len(names) > maxNetworks {
		return newError(CodeInvalidNetwork, "networks", "a site joins at most %d networks", maxNetworks)
	}
	for i, n := range names {
		if n != networks.SharedName && networks.ValidateName(n) != nil {
			return newError(CodeInvalidNetwork, "networks", "invalid network name %q", n)
		}
		if i > 0 && names[i-1] >= n {
			return newError(CodeInvalidNetwork, "networks", "networks must be unique")
		}
	}
	return nil
}

// Normalize trims and lowercases user input in place before validation.
func Normalize(spec *Spec) {
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Site = strings.TrimSpace(spec.Site)
	spec.DocumentRoot = strings.Trim(strings.TrimSpace(spec.DocumentRoot), "/")
	for i, d := range spec.Domains {
		spec.Domains[i] = strings.ToLower(strings.TrimSpace(d))
	}
	if spec.Site == "" {
		spec.Site = spec.Name
	}
	for i, d := range spec.AssetDirs {
		spec.AssetDirs[i] = strings.Trim(strings.TrimSpace(d), "/")
	}
	if !spec.Template.HasPHP() {
		spec.PHPVersion = ""
		spec.PHP = nil
	}
	for i, n := range spec.Networks {
		spec.Networks[i] = strings.TrimSpace(n)
	}
	slices.Sort(spec.Networks)
	if len(spec.Networks) == 0 {
		spec.Networks = nil
	}
}

// Validate checks a normalized spec. It does not check collisions with
// existing resources (see Manager).
func Validate(spec *Spec, snippets Snippets, limits Limits) error {
	if err := validate(spec, snippets, limits); err != nil {
		return err
	}
	return nil // never return a typed nil *Error as an error
}

func validate(spec *Spec, snippets Snippets, limits Limits) *Error {
	if err := validateName(spec.Name); err != nil {
		return err
	}
	if !spec.Template.Valid() {
		return newError(CodeInvalidTemplate, "template", "unknown template %q", spec.Template)
	}
	if spec.Template.HasPHP() {
		if !validPHPVersion(spec.PHPVersion) {
			return newError(CodeInvalidPHPVersion, "phpVersion", "unsupported PHP version %q", spec.PHPVersion)
		}
		if spec.PHP == nil {
			return newError(CodeInvalidPHPSetting, "php", "PHP settings are required")
		}
		if err := validatePHP(spec.PHP); err != nil {
			return err
		}
	}
	if !ValidPathSegment(spec.Site) {
		return newError(CodeInvalidSite, "site", "invalid site directory name")
	}
	if !validWebPath(spec.DocumentRoot) {
		return newError(CodeInvalidDocRoot, "documentRoot", "invalid document root")
	}
	if err := validateDomains(spec.Domains, limits.MaxDomains); err != nil {
		return err
	}
	if err := validateNetworks(spec.Networks); err != nil {
		return err
	}
	if spec.RedirectHTTPS && !spec.HTTPS {
		return newError(CodeInvalidDomain, "redirectHttps", "HTTP to HTTPS redirect requires HTTPS")
	}
	// 0 = no limit: Docker kills a container that reaches its memory limit,
	// so limits are opt-in.
	if r := spec.Resources; r.MemoryMB != 0 && (r.MemoryMB < minMemoryMB || r.MemoryMB > limits.MaxMemoryMB) {
		return newError(CodeInvalidResources, "resources.memoryMB",
			"memory limit must be 0 (unlimited) or %d-%d MB", minMemoryMB, limits.MaxMemoryMB)
	}
	if r := spec.Resources; r.CPUs != 0 && (r.CPUs < minCPUs || r.CPUs > limits.MaxCPUs) {
		return newError(CodeInvalidResources, "resources.cpus",
			"CPU limit must be 0 (unlimited) or %.1f-%.1f", minCPUs, limits.MaxCPUs)
	}
	if err := validateEnv(spec.Env, limits.MaxEnvVars); err != nil {
		return err
	}
	if err := validateRouting(spec, snippets); err != nil {
		return err
	}
	if err := validateCustomRules(spec, snippets); err != nil {
		return err
	}
	return validateSnippets(spec.Template, snippets, limits.MaxSnippetBytes)
}

func validatePHP(p *PHPSettings) *Error {
	if !validMemoryLimit(p.MemoryLimit) {
		return newError(CodeInvalidPHPSetting, "php.memoryLimit", "memory_limit must look like 256M or -1")
	}
	if p.WebMemoryLimit != "" && !validMemoryLimit(p.WebMemoryLimit) {
		return newError(CodeInvalidPHPSetting, "php.webMemoryLimit", "web memory_limit must look like 256M, -1 or be empty")
	}
	upload, ok := parseSize(p.UploadMaxFilesize)
	if !ok {
		return newError(CodeInvalidPHPSetting, "php.uploadMaxFilesize", "upload_max_filesize must look like 64M")
	}
	post, ok := parseSize(p.PostMaxSize)
	if !ok {
		return newError(CodeInvalidPHPSetting, "php.postMaxSize", "post_max_size must look like 64M")
	}
	if post < upload {
		return newError(CodeInvalidPHPSetting, "php.postMaxSize", "post_max_size must be greater than or equal to upload_max_filesize")
	}
	if p.MaxExecutionTime < 0 || p.MaxExecutionTime > maxExecutionTime {
		return newError(CodeInvalidPHPSetting, "php.maxExecutionTime", "max_execution_time must be 0-%d seconds", maxExecutionTime)
	}
	if _, err := time.LoadLocation(p.Timezone); err != nil || p.Timezone == "" || p.Timezone == "Local" {
		return newError(CodeInvalidPHPSetting, "php.timezone", "unknown timezone")
	}
	// Names only here: availability in the image is checked by the Manager.
	if len(p.DisabledModules) > maxDisabledModules {
		return newError(CodeInvalidPHPModule, "php.disabledModules", "at most %d disabled modules", maxDisabledModules)
	}
	for i, m := range p.DisabledModules {
		if !moduleRe.MatchString(m) || slices.Contains(p.DisabledModules[:i], m) {
			return newError(CodeInvalidPHPModule, "php.disabledModules", "invalid or duplicate module name %q", m)
		}
	}
	return nil
}

// validMemoryLimit accepts a PHP size (e.g. 256M) or -1 (unlimited).
func validMemoryLimit(v string) bool {
	return v == "-1" || sizeRe.MatchString(v)
}

// parseSize converts a PHP shorthand size (e.g. 64M) to bytes.
func parseSize(s string) (int64, bool) {
	m := sizeRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	switch m[2] {
	case "K":
		n <<= 10
	case "M":
		n <<= 20
	case "G":
		n <<= 30
	}
	return n, true
}

func validateDomains(domains []string, max int) *Error {
	if len(domains) > max {
		return newError(CodeInvalidDomain, "domains", "at most %d domains", max)
	}
	seen := make(map[string]bool, len(domains))
	for _, d := range domains {
		if !ValidateDomain(d) {
			return newError(CodeInvalidDomain, "domains", "invalid domain %q", d)
		}
		if seen[d] {
			return newError(CodeInvalidDomain, "domains", "duplicate domain %q", d)
		}
		seen[d] = true
	}
	return nil
}

func validateEnv(env []EnvVar, max int) *Error {
	if len(env) > max {
		return newError(CodeInvalidEnvVar, "env", "at most %d environment variables", max)
	}
	seen := make(map[string]bool, len(env))
	for _, v := range env {
		if len(v.Name) > maxEnvNameLength || !envNameRe.MatchString(v.Name) {
			return newError(CodeInvalidEnvVar, "env", "invalid variable name %q", v.Name)
		}
		if seen[v.Name] {
			return newError(CodeInvalidEnvVar, "env", "duplicate variable %q", v.Name)
		}
		seen[v.Name] = true
		if isReservedEnv(v.Name) {
			return &Error{Code: CodeReservedEnvVar, Field: "env", Message: fmt.Sprintf("variable %q is managed by the panel", v.Name)}
		}
		if len(v.Value) > maxEnvValueBytes || !utf8.ValidString(v.Value) ||
			strings.ContainsAny(v.Value, "\x00\r\n") {
			return newError(CodeInvalidEnvVar, "env", "invalid value for %q (single line, max %d bytes)", v.Name, maxEnvValueBytes)
		}
	}
	return nil
}

func isReservedEnv(name string) bool {
	upper := strings.ToUpper(name)
	if slices.Contains(managedEnvVars, upper) {
		return true
	}
	for _, p := range reservedEnvPrefixes {
		if strings.HasPrefix(upper, p) {
			return true
		}
	}
	return false
}

func validateSnippets(t Template, s Snippets, maxBytes int) *Error {
	check := func(field, content string) *Error {
		if len(content) > maxBytes {
			return newError(CodeInvalidSnippet, field, "configuration is too large (max %d bytes)", maxBytes)
		}
		if !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
			return newError(CodeInvalidSnippet, field, "configuration must be valid UTF-8 text")
		}
		return nil
	}
	if s.PHPIni != "" && !t.HasPHP() {
		return newError(CodeInvalidSnippet, "phpIni", "php.ini is only available for PHP templates")
	}
	if err := check("phpIni", s.PHPIni); err != nil {
		return err
	}
	if err := check("routingRules", s.RoutingRules); err != nil {
		return err
	}
	return check("vhost", s.Vhost)
}
