package config

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/joho/godotenv"

	"host-panel-api/internal/env"
	"host-panel-api/internal/functions"
	"host-panel-api/internal/networks"
	"host-panel-api/internal/services"
)

// Config holds the entire application configuration.
type Config struct {
	// OIDC (common)
	OIDCProvider string
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Scope        string
	FrontURL     string

	// OIDC — Microsoft Entra ID
	TenantID      string
	ApplicationID string

	// OIDC — Keycloak
	KeycloakURL   string
	KeycloakRealm string

	// OIDC — Generic (any standard OIDC provider)
	OIDCDiscoveryURL string

	// Security
	CookieSecret   string
	PasswordSecret string
	CookieDomain   string

	// Runtime
	Env     string
	Port    int
	DataDir string
	LogDir  string

	// Cron
	CronHTTPTimeout int // seconds, default 900 (15 min)
	CronMaxJobs     int // max number of jobs, default 50

	// Networks (private networks of the sites and service instances)
	NetworksMax int // private networks, default 50
	// NetworksReservedRanges are used outside Docker (LAN, VPN, static
	// routes): no panel network may overlap them.
	NetworksReservedRanges []netip.Prefix
	// TrustedProxies may set X-Forwarded-For (loopback always). Empty: the
	// loopback only.
	TrustedProxies []netip.Prefix

	// SFTP (SFTPGo integration — "web agency" mode)
	SFTPEnabled          bool     // toggles the SFTP management plugin
	SFTPDirectory        string   // base directory hosting the sites, e.g. /data/websites/hosted
	SFTPAPIURL           string   // base URL of the SFTPGo admin REST API, e.g. http://sftpgo:8080
	SFTPAPIKey           string   // SFTPGo admin API key
	SFTPDomain           string   // SFTP connection domain shown to clients
	SFTPPort             string   // SFTP connection port shown to clients
	SFTPWebUserInterface string   // URL of the SFTPGo web client UI shown to clients
	SFTPInitDirs         []string // directories created in a new user's home when requested, default [web logs]

	// Hosting (self-service web stacks, generated as plain compose projects)
	HostingEnabled         bool    // toggles the hosting plugin
	HostingStacksDir       string  // compose projects directory, shared with CI, default /data/compose
	HostingSitesDir        string  // site data directory, default /data/websites/hosted
	HostingTraefikNetwork  string  // external Docker network joined by the web services (required)
	HostingHTTPEntrypoint  string  // Traefik HTTP entrypoint, default web
	HostingHTTPSEntrypoint string  // Traefik HTTPS entrypoint, default websecure
	HostingCertResolver    string  // Traefik certificate resolver, default letsencrypt (empty = default certificate)
	HostingRunUID          int     // owner of the site directories created by the panel, default 1000
	HostingRunGID          int     // group of the site directories created by the panel, default 1000
	HostingDefaultMemoryMB int     // default memory limit of a stack in MB, default 0 (unlimited)
	HostingMaxMemoryMB     int     // max memory limit of a stack in MB, default 0 (host memory)
	HostingDefaultCPUs     float64 // default CPU limit of a stack, default 0 (unlimited)
	HostingMaxCPUs         float64 // max CPU limit of a stack, default 0 (host CPUs)

	// Services (managed database instances; compose files in HOSTING_STACKS_DIRECTORY)
	ServicesEnabled          bool                // toggles the services module
	ServicesDataDir          string              // instance data, default /data/services
	ServicesNetwork          string              // network shared by the instances and the sites, default loch-services
	ServicesEngines          []string            // enabled engines (empty = all)
	ServicesPinnedVersions   map[string][]string // versions always offered, per engine
	ServicesUpdateCheckHours int                 // hours between two discoveries (0 = on demand only)
	ServicesMaxResources     int                 // resources per instance, default 50
	ServicesMaxDedicated     int                 // dedicated instances per engine (Redis, Valkey, NATS), default 20
	ServicesArchiveOnRetire  bool                // rename the data of a retired instance instead of deleting it
	// Published ports, chosen in the panel within these bounds
	ServicesExposeCIDRs      string // host addresses allowed (SERVICES_EXPOSE_ALLOWED_CIDRS), default 127.0.0.1/32
	ServicesAllowPublicBind  bool   // required for non-loopback addresses
	ServicesAllowTLSOptional bool   // a public exposure may offer TLS without requiring it
	ServicesTLS              string // auto (default) or off
	// ServicesS3AdminAPIKey selects the shared Loch S3 mode (not available
	// yet: the s3 engine is then disabled). Empty: standalone mode, one
	// instance per resource.
	ServicesS3AdminAPIKey string
	ServicesPublicHost    string // shown to clients, covered by the certificates
	ServicesTLSCertFile   string // optional certificate instead of self-signed ones
	ServicesTLSKeyFile    string

	// Functions (serverless: AWS Lambda handlers; compose files in HOSTING_STACKS_DIRECTORY)
	FunctionsEnabled         bool   // toggles the functions module
	FunctionsDataDir         string // code, runtime binaries and private references, default /data/functions
	FunctionsMax             int    // number of functions, default 50
	FunctionsMaxMemoryMB     int    // memory limit of one function, default 3008 MB
	FunctionsMaxCodeBytes    int64  // size of an uploaded zip, default 50 MiB
	FunctionsDeploymentsKept int    // deployments kept for a rollback, default 3
	FunctionsTmpSizeMB       int    // default /tmp size of a function, default 512 MB
	// loch function runtime binary: downloaded from the GitHub releases
	// (default), or read from a directory (offline installations).
	FunctionsRuntimeDownload  bool
	FunctionsRuntimeSourceDir string
	// Global budget of the functions (sum of their limits); 0 = derived
	// from the Docker host at startup (its memory, 4 × its CPUs).
	FunctionsMaxTotalMemoryMB int
	FunctionsMaxTotalCPUs     float64
}

// Functions returns the configuration of the functions module.
func (c *Config) Functions() functions.Config {
	return functions.Config{
		Enabled: c.FunctionsEnabled, DataDir: c.FunctionsDataDir, StacksDir: c.HostingStacksDir,
		Max: c.FunctionsMax, MaxMemoryMB: c.FunctionsMaxMemoryMB, MaxCodeBytes: c.FunctionsMaxCodeBytes,
		DeploymentsKept: c.FunctionsDeploymentsKept, TmpSizeMB: c.FunctionsTmpSizeMB,
	}
}

// lintedPrefixes are the prefixes of the variables of the API: an unknown
// variable with one of them is a typo or a removed setting.
var lintedPrefixes = []string{"OIDC_", "SFTP_", "HOSTING_", "SERVICES_", "FUNCTIONS_", "NETWORKS_", "CRON_", "COOKIE_", "KEYCLOAK_", "TRUSTED_"}

// toolingVars are variables of the development tooling (Makefile) that
// share a linted prefix.
var toolingVars = []string{"SFTP_KEY_NAME"}

// Load loads the configuration from .env (if present) then from
// environment variables. The host environment always takes precedence
// because godotenv.Load does not overwrite already defined variables. Every
// problem is reported at once (env.Problems): the API must not start with
// a configuration it would partly ignore.
func Load() (*Config, error) {
	// Load .env without error if the file does not exist
	_ = godotenv.Load()
	return load(env.New(os.LookupEnv), os.Environ())
}

func load(r *env.Reader, environ []string) (*Config, error) {
	cfg := &Config{
		OIDCProvider: r.String("OIDC_PROVIDER", "microsoft"),
		ClientID:     r.Required("CLIENT_ID"),
		ClientSecret: r.Required("CLIENT_SECRET"),
		RedirectURI:  r.Required("REDIRECT_URI"),
		Scope:        r.Required("SCOPE"),
		FrontURL:     r.Required("FRONT_URL"),

		CookieSecret:   r.Secret("COOKIE_SECRET", 32),
		PasswordSecret: r.Required("PASSWORD_SECRET"),
		CookieDomain:   r.String("COOKIE_DOMAIN", ""),

		Env:     r.String("APP_ENV", "production"),
		Port:    r.Int("PORT", 2538, 1, 65535),
		DataDir: r.String("DATA_DIR", "./data"),
		LogDir:  r.String("API_LOG_DIR", "./log"),

		CronHTTPTimeout: r.Int("CRON_HTTP_TIMEOUT", 900, 1, 86400),
		CronMaxJobs:     r.Int("CRON_MAX_JOBS", 50, 1, 10000),

		NetworksMax:            r.Int("NETWORKS_MAX", 50, 1, 500),
		NetworksReservedRanges: r.Prefixes("NETWORKS_RESERVED_RANGES", ""),
		TrustedProxies:         r.Prefixes("TRUSTED_PROXIES", ""),
	}
	for _, p := range cfg.NetworksReservedRanges {
		if !p.Addr().Is4() || p.Bits() < 8 {
			r.Fail("NETWORKS_RESERVED_RANGES", "%s: list IPv4 ranges of at least /8 (the panel networks are IPv4)", p)
		}
	}
	for _, p := range cfg.TrustedProxies {
		if (p.Addr().Is4() && p.Bits() < 8) || (p.Addr().Is6() && p.Bits() < 32) {
			r.Fail("TRUSTED_PROXIES", "%s is too wide: list the subnet of the reverse proxy only (any client could forge its address)", p)
		}
	}
	// Provider-specific variables are only required for the chosen provider.
	switch cfg.OIDCProvider {
	case "microsoft":
		cfg.TenantID = r.Required("TENANT_ID")
		cfg.ApplicationID = r.Required("APPLICATION_ID")
		r.Declare("KEYCLOAK_URL", "KEYCLOAK_REALM", "OIDC_DISCOVERY_URL")
	case "keycloak":
		cfg.KeycloakURL = r.Required("KEYCLOAK_URL")
		cfg.KeycloakRealm = r.Required("KEYCLOAK_REALM")
		r.Declare("TENANT_ID", "APPLICATION_ID", "OIDC_DISCOVERY_URL")
	case "generic":
		cfg.OIDCDiscoveryURL = r.Required("OIDC_DISCOVERY_URL")
		r.Declare("TENANT_ID", "APPLICATION_ID", "KEYCLOAK_URL", "KEYCLOAK_REALM")
	default:
		r.Fail("OIDC_PROVIDER", "must be one of microsoft, keycloak, generic")
		r.Declare("TENANT_ID", "APPLICATION_ID", "KEYCLOAK_URL", "KEYCLOAK_REALM", "OIDC_DISCOVERY_URL")
	}

	loadSFTP(cfg, r)
	loadHosting(cfg, r)
	loadServices(cfg, r)
	loadFunctions(cfg, r)

	// Read by the Makefile (sftp-key target), exported to `make dev`: known,
	// not used by the API.
	r.Declare(toolingVars...)
	r.CheckUnknown(environ, lintedPrefixes...)
	if err := r.Err(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// loadSFTP reads the SFTP_* variables. SFTP_DOMAIN, SFTP_PORT and
// SFTP_WEB_USER_INTERFACE are display-only.
func loadSFTP(cfg *Config, r *env.Reader) {
	cfg.SFTPEnabled = r.Bool("SFTP_ENABLED", false)
	read := r.String
	if cfg.SFTPEnabled {
		read = func(name, _ string) string { return r.Required(name) }
	}
	cfg.SFTPDirectory = read("SFTP_DIRECTORY", "")
	cfg.SFTPAPIURL = read("SFTP_API_URL", "")
	cfg.SFTPAPIKey = read("SFTP_API_KEY", "")
	cfg.SFTPDomain = r.String("SFTP_DOMAIN", "")
	cfg.SFTPPort = r.String("SFTP_PORT", "")
	cfg.SFTPWebUserInterface = r.String("SFTP_WEB_USER_INTERFACE", "")
	cfg.SFTPInitDirs = r.List("SFTP_INIT_DIRS", "web,logs")
}

// loadHosting reads the HOSTING_* variables.
func loadHosting(cfg *Config, r *env.Reader) {
	cfg.HostingEnabled = r.Bool("HOSTING_ENABLED", false)
	cfg.HostingStacksDir = strings.TrimRight(r.String("HOSTING_STACKS_DIRECTORY", "/data/compose"), "/")
	cfg.HostingSitesDir = strings.TrimRight(r.String("HOSTING_SITES_DIRECTORY", "/data/websites/hosted"), "/")
	if cfg.HostingEnabled {
		cfg.HostingTraefikNetwork = r.Required("HOSTING_TRAEFIK_NETWORK")
	} else {
		cfg.HostingTraefikNetwork = r.String("HOSTING_TRAEFIK_NETWORK", "")
	}
	cfg.HostingHTTPEntrypoint = r.String("HOSTING_TRAEFIK_HTTP_ENTRYPOINT", "web")
	cfg.HostingHTTPSEntrypoint = r.String("HOSTING_TRAEFIK_HTTPS_ENTRYPOINT", "websecure")
	cfg.HostingCertResolver = r.String("HOSTING_TRAEFIK_CERTRESOLVER", "letsencrypt")
	cfg.HostingRunUID = r.Int("HOSTING_RUN_UID", 1000, 0, 1<<31-1)
	cfg.HostingRunGID = r.Int("HOSTING_RUN_GID", 1000, 0, 1<<31-1)
	// Maximums are 0 (host capacity, resolved at startup) and defaults 0
	// (unlimited), or a valid limit; defaults are checked against the
	// resolved maximums at startup (see ResolveHostingMax).
	limitMB := func(name, zero string) int {
		n := r.Int(name, 0, 0, 1<<31-1)
		if n != 0 && n < 64 {
			r.Fail(name, "must be 0 (%s) or at least 64", zero)
		}
		return n
	}
	limitCPUs := func(name, zero string) float64 {
		f := r.Float(name, 0, 0, 4096)
		if f != 0 && f < 0.1 {
			r.Fail(name, "must be 0 (%s) or at least 0.1", zero)
		}
		return f
	}
	cfg.HostingDefaultMemoryMB = limitMB("HOSTING_DEFAULT_MEMORY_MB", "unlimited")
	cfg.HostingMaxMemoryMB = limitMB("HOSTING_MAX_MEMORY_MB", "host memory")
	cfg.HostingDefaultCPUs = limitCPUs("HOSTING_DEFAULT_CPUS", "unlimited")
	cfg.HostingMaxCPUs = limitCPUs("HOSTING_MAX_CPUS", "host CPUs")
}

// ResolveHostingMax sets the hosting maximums from the host capacity: 0 means
// "the whole host", and a configured value above the host capacity is capped
// (Docker would reject it anyway). It returns the names of the variables that
// were capped, then checks the defaults against the resolved maximums.
func (c *Config) ResolveHostingMax(hostCPUs int, hostMemBytes int64) ([]string, error) {
	hostMemMB := int(hostMemBytes / (1 << 20))
	var capped []string
	if c.HostingMaxMemoryMB == 0 || c.HostingMaxMemoryMB > hostMemMB {
		if c.HostingMaxMemoryMB != 0 {
			capped = append(capped, "HOSTING_MAX_MEMORY_MB")
		}
		c.HostingMaxMemoryMB = hostMemMB
	}
	if c.HostingMaxCPUs == 0 || c.HostingMaxCPUs > float64(hostCPUs) {
		if c.HostingMaxCPUs != 0 {
			capped = append(capped, "HOSTING_MAX_CPUS")
		}
		c.HostingMaxCPUs = float64(hostCPUs)
	}
	if c.HostingDefaultMemoryMB > c.HostingMaxMemoryMB {
		return capped, fmt.Errorf("HOSTING_DEFAULT_MEMORY_MB (%d) exceeds the maximum (%d MB)", c.HostingDefaultMemoryMB, c.HostingMaxMemoryMB)
	}
	if c.HostingDefaultCPUs > c.HostingMaxCPUs {
		return capped, fmt.Errorf("HOSTING_DEFAULT_CPUS (%g) exceeds the maximum (%g)", c.HostingDefaultCPUs, c.HostingMaxCPUs)
	}
	return capped, nil
}

// serviceEngineIDs are the engines of the services module (variables named
// after them).
var serviceEngineIDs = []string{"postgres", "mariadb", "mysql", "mongodb", "redis", "valkey", "rabbitmq", "nats", "s3", "elasticmq", "mosquitto"}

// loadServices reads the SERVICES_* variables. Engine and version names are
// checked against the engine catalogs when the module starts.
func loadServices(cfg *Config, r *env.Reader) {
	cfg.ServicesEnabled = r.Bool("SERVICES_ENABLED", false)
	cfg.ServicesDataDir = strings.TrimRight(r.String("SERVICES_DATA_DIRECTORY", "/data/services"), "/")
	cfg.ServicesNetwork = r.String("SERVICES_NETWORK", "loch-services")
	cfg.ServicesEngines = r.List("SERVICES_ENGINES", "")
	// Versions are discovered on the registries; pinned ones are always
	// offered (e.g. an out-of-support version still needed).
	cfg.ServicesPinnedVersions = map[string][]string{}
	var removedVersions []string
	for _, engine := range serviceEngineIDs {
		name := "SERVICES_" + strings.ToUpper(engine) + "_PINNED_VERSIONS"
		if v := r.List(name, ""); len(v) > 0 {
			cfg.ServicesPinnedVersions[engine] = v
		}
		removedVersions = append(removedVersions, "SERVICES_"+strings.ToUpper(engine)+"_VERSIONS")
	}
	r.Removed("versions are discovered on the registries; use SERVICES_<ENGINE>_PINNED_VERSIONS to always offer a version",
		removedVersions...)
	// Hours between two discoveries of the versions and updates (0: on
	// demand only).
	cfg.ServicesUpdateCheckHours = r.Int("SERVICES_UPDATE_CHECK_INTERVAL", 12, 0, 168)
	cfg.ServicesMaxDedicated = r.Int("SERVICES_MAX_DEDICATED_INSTANCES", 20, 1, 200)
	cfg.ServicesMaxResources = r.Int("SERVICES_MAX_RESOURCES_PER_INSTANCE", 50, 1, 500)
	cfg.ServicesArchiveOnRetire = r.Bool("SERVICES_ARCHIVE_ON_RETIRE", false)
	cfg.ServicesAllowPublicBind = r.Bool("SERVICES_ALLOW_PUBLIC_BIND", false)
	cfg.ServicesExposeCIDRs = r.String("SERVICES_EXPOSE_ALLOWED_CIDRS", "")
	if _, err := services.ParseAllowedCIDRs(cfg.ServicesExposeCIDRs, cfg.ServicesAllowPublicBind); err != nil {
		r.Fail("SERVICES_EXPOSE_ALLOWED_CIDRS", "%s", strings.TrimPrefix(err.Error(), "SERVICES_EXPOSE_ALLOWED_CIDRS: "))
	}
	cfg.ServicesAllowTLSOptional = r.Bool("SERVICES_ALLOW_TLS_OPTIONAL", false)
	cfg.ServicesTLS = r.Enum("SERVICES_TLS", "auto", "auto", "off")
	cfg.ServicesS3AdminAPIKey = r.String("SERVICES_S3_ADMIN_API_KEY", "")
	cfg.ServicesPublicHost = r.String("SERVICES_PUBLIC_HOST", "")
	if h := cfg.ServicesPublicHost; h != "" && !publicHostRe.MatchString(h) {
		r.Fail("SERVICES_PUBLIC_HOST", "must be a host name or an IP address (no scheme, no path)")
	}
	cfg.ServicesTLSCertFile = r.String("SERVICES_TLS_CERT_FILE", "")
	cfg.ServicesTLSKeyFile = r.String("SERVICES_TLS_KEY_FILE", "")
	if (cfg.ServicesTLSCertFile == "") != (cfg.ServicesTLSKeyFile == "") {
		r.Fail("SERVICES_TLS_KEY_FILE", "SERVICES_TLS_CERT_FILE and SERVICES_TLS_KEY_FILE go together")
	}
	r.Removed("the published ports are chosen in the panel, within SERVICES_EXPOSE_ALLOWED_CIDRS",
		"SERVICES_EXPOSED_PORTS", "SERVICES_PUBLISH_ADDRESS")
	r.Removed("optional TLS is chosen per exposure in the panel, allowed by SERVICES_ALLOW_TLS_OPTIONAL",
		"SERVICES_TLS_OPTIONAL")
	if !cfg.ServicesEnabled {
		return
	}
	if !filepath.IsAbs(cfg.ServicesDataDir) || filepath.Clean(cfg.ServicesDataDir) != cfg.ServicesDataDir {
		r.Fail("SERVICES_DATA_DIRECTORY", "must be an absolute, clean path")
	}
	if !filepath.IsAbs(cfg.HostingStacksDir) || filepath.Clean(cfg.HostingStacksDir) != cfg.HostingStacksDir {
		r.Fail("HOSTING_STACKS_DIRECTORY", "must be an absolute, clean path")
	}
	// The engines' data must not be reachable by the sites, the SFTP
	// users or the stacks deployed by CI.
	others := map[string]string{
		"HOSTING_STACKS_DIRECTORY": cfg.HostingStacksDir,
		"HOSTING_SITES_DIRECTORY":  cfg.HostingSitesDir,
		"SFTP_DIRECTORY":           strings.TrimRight(cfg.SFTPDirectory, "/"),
	}
	for name, dir := range others {
		if dir != "" && pathsOverlap(cfg.ServicesDataDir, dir) {
			r.Fail("SERVICES_DATA_DIRECTORY", "must not be inside %s (or contain it)", name)
		}
	}
	if !networkNameRe.MatchString(cfg.ServicesNetwork) || reservedNetworks[cfg.ServicesNetwork] ||
		cfg.ServicesNetwork == cfg.HostingTraefikNetwork || strings.HasPrefix(cfg.ServicesNetwork, networks.DockerPrefix) {
		r.Fail("SERVICES_NETWORK", "must be a dedicated network name (not bridge, host, none, the Traefik network nor a %s* name)", networks.DockerPrefix)
	}
}

// loadFunctions reads the FUNCTIONS_* variables. Sizes are in MB by
// default and accept a unit (512M, 1G; binary units).
func loadFunctions(cfg *Config, r *env.Reader) {
	cfg.FunctionsEnabled = r.Bool("FUNCTIONS_ENABLED", false)
	cfg.FunctionsDataDir = strings.TrimRight(r.String("FUNCTIONS_DATA_DIRECTORY", "/data/functions"), "/")
	cfg.FunctionsMax = r.Int("FUNCTIONS_MAX", functions.DefaultMax, 1, 1000)
	cfg.FunctionsMaxMemoryMB = sizeMB(r, "FUNCTIONS_MAX_MEMORY", functions.DefaultMaxMemoryMB, functions.MinMemoryMB, functions.MaxMemoryMB)
	cfg.FunctionsMaxCodeBytes = int64(sizeMB(r, "FUNCTIONS_MAX_CODE_SIZE", functions.DefaultMaxCodeBytes>>20, functions.MinCodeMB, functions.MaxCodeMB)) << 20
	cfg.FunctionsDeploymentsKept = r.Int("FUNCTIONS_DEPLOYMENTS_KEPT", functions.DefaultDeploymentsKept, 1, 20)
	cfg.FunctionsTmpSizeMB = sizeMB(r, "FUNCTIONS_TMP_SIZE", functions.DefaultTmpSizeMB, functions.MinTmpMB, functions.MaxTmpMB)
	cfg.FunctionsMaxTotalMemoryMB = 0
	if r.String("FUNCTIONS_MAX_TOTAL_MEMORY", "0") != "0" {
		cfg.FunctionsMaxTotalMemoryMB = sizeMB(r, "FUNCTIONS_MAX_TOTAL_MEMORY", 0, functions.MinMemoryMB, 16<<20)
	}
	cfg.FunctionsMaxTotalCPUs = r.Float("FUNCTIONS_MAX_TOTAL_CPUS", 0, 0, 4096)
	cfg.FunctionsRuntimeDownload = r.Bool("FUNCTIONS_RUNTIME_DOWNLOAD", true)
	cfg.FunctionsRuntimeSourceDir = r.AbsPath("FUNCTIONS_RUNTIME_SOURCE_DIR", "")
	if !cfg.FunctionsEnabled {
		return
	}
	// Both paths are written in the compose files (binds, env_file,
	// labels): no character compose could interpolate or parse.
	if dir := cfg.FunctionsRuntimeSourceDir; dir != "" {
		if err := functions.CheckRuntimeSourceDir(dir); err != nil {
			r.Fail("FUNCTIONS_RUNTIME_SOURCE_DIR", "%v (it must be a directory, and its runtime files regular files, owned by the panel's user or root and writable by nobody else)", err)
		}
	}
	if !functions.ValidDataPath(cfg.FunctionsDataDir) {
		r.Fail("FUNCTIONS_DATA_DIRECTORY", "must be an absolute, clean path of letters, digits and _ . / -")
	}
	if !functions.ValidDataPath(cfg.HostingStacksDir) {
		r.Fail("HOSTING_STACKS_DIRECTORY", "must be an absolute, clean path of letters, digits and _ . / - (required by FUNCTIONS_ENABLED)")
	}
	// The code, the runtime binaries and the private references (secrets,
	// credentials) must not be reachable by CI, the sites, the SFTP users or
	// the service instances, nor be mixed with the logs.
	others := map[string]string{
		"HOSTING_STACKS_DIRECTORY": cfg.HostingStacksDir,
		"HOSTING_SITES_DIRECTORY":  cfg.HostingSitesDir,
		"SFTP_DIRECTORY":           strings.TrimRight(cfg.SFTPDirectory, "/"),
		"SERVICES_DATA_DIRECTORY":  cfg.ServicesDataDir,
		"API_LOG_DIR":              cfg.LogDir,
	}
	for name, dir := range others {
		if dir != "" && realPathsOverlap(cfg.FunctionsDataDir, dir) {
			r.Fail("FUNCTIONS_DATA_DIRECTORY", "must not be inside %s (or contain it)", name)
		}
	}
	// A subdirectory of DATA_DIR is fine (the panel's private data, backed
	// up with it, as SERVICES_DATA_DIRECTORY may be); DATA_DIR itself or a
	// parent of it would mix the panel's stores with the functions' files.
	if cfg.DataDir != "" && realPathsContain(cfg.FunctionsDataDir, cfg.DataDir) {
		r.Fail("FUNCTIONS_DATA_DIRECTORY", "must not be DATA_DIR or contain it (a subdirectory of DATA_DIR is fine)")
	}
}

// realPathsContain reports whether dir is a or inside a, compared as
// realPathsOverlap does (as given, absolute, symlinks resolved).
func realPathsContain(a, dir string) bool {
	forms := func(p string) []string {
		out := []string{p}
		if abs, err := filepath.Abs(p); err == nil {
			out = append(out, abs, resolveExisting(abs))
		}
		return out
	}
	for _, x := range forms(a) {
		for _, y := range forms(dir) {
			if x == y || strings.HasPrefix(y, strings.TrimRight(x, "/")+"/") {
				return true
			}
		}
	}
	return false
}

// realPathsOverlap compares two directories as given and with their
// symlinks resolved, relative ones made absolute: a link must not hide that
// one is inside the other, even for a directory not created yet (the
// longest existing prefix is resolved, the rest appended).
func realPathsOverlap(a, b string) bool {
	forms := func(p string) []string {
		out := []string{p}
		if abs, err := filepath.Abs(p); err == nil {
			out = append(out, abs, resolveExisting(abs))
		}
		return out
	}
	for _, x := range forms(a) {
		for _, y := range forms(b) {
			if pathsOverlap(x, y) {
				return true
			}
		}
	}
	return false
}

// resolveExisting resolves the symlinks of the longest existing prefix of
// an absolute, clean path and appends the missing components.
func resolveExisting(p string) string {
	var rest []string
	for cur := p; ; cur = filepath.Dir(cur) {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(append([]string{real}, rest...)...)
		}
		if parent := filepath.Dir(cur); parent == cur {
			return p
		} else {
			rest = append([]string{filepath.Base(cur)}, rest...)
		}
	}
}

var sizeRe = regexp.MustCompile(`\A([0-9]{1,6})\s*([MmGg](?:i?[Bb])?)?\z`)

// sizeMB reads a size in MB within [min, max]: "512", "512M", "512MiB",
// "1G" (units are binary).
func sizeMB(r *env.Reader, name string, def, min, max int) int {
	v := r.String(name, "")
	if v == "" {
		return def
	}
	m := sizeRe.FindStringSubmatch(v)
	if m == nil {
		r.Fail(name, "must be a size in MB (e.g. 512, 512M or 1G)")
		return def
	}
	n, _ := strconv.Atoi(m[1])
	if strings.HasPrefix(strings.ToUpper(m[2]), "G") {
		n *= 1024
	}
	if n < min || n > max {
		r.Fail(name, "must be between %d and %d MB", min, max)
		return def
	}
	return n
}

var networkNameRe = regexp.MustCompile(`\A[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}\z`)

// publicHostRe accepts a DNS name or an IP address (IPv6 without brackets).
var publicHostRe = regexp.MustCompile(`\A(([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?|[0-9a-fA-F:.]+)\z`)

var reservedNetworks = map[string]bool{"bridge": true, "host": true, "none": true, "default": true}

// pathsOverlap reports whether a and b are equal or one contains the other.
func pathsOverlap(a, b string) bool {
	within := func(p, dir string) bool { return strings.HasPrefix(p, strings.TrimRight(dir, "/")+"/") }
	return a == b || within(a, b) || within(b, a)
}
