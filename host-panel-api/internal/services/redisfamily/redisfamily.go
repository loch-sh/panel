// Package redisfamily is the Redis and Valkey engine of the services module:
// dedicated instances (one container per resource), administered by
// nothing but their configuration. The tenant user is the only usable
// account; the panel never runs a command inside the container.
package redisfamily

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"host-panel-api/internal/registry"
	"host-panel-api/internal/services"
)

// Engine is Redis or Valkey (same configuration language, same ACL).
type Engine struct {
	id      string
	repo    string // repository on Docker Hub
	image   string // image name, pulled from the mirror
	server  string // redis-server / valkey-server
	cli     string // redis-cli / valkey-cli
	catalog []services.Version
	// modules: Redis 8 bundles modules (JSON, Search...), with their own ACL
	// categories; they are disabled unless the "modules" option is on.
	modules bool
}

// Image user (alpine images): uid 999, gid 1000.
const (
	uid = 999
	gid = 1000
)

// NewRedis returns the Redis engine (official alpine images).
func NewRedis() *Engine {
	e := &Engine{id: "redis", repo: "library/redis", image: "redis", server: "redis-server", cli: "redis-cli", modules: true}
	e.catalog = []services.Version{e.VersionOf("8.4", "8.4-alpine"), e.VersionOf("8.2", "8.2-alpine"), e.VersionOf("7.4", "7.4-alpine")}
	return e
}

// NewValkey returns the Valkey engine (official alpine images).
func NewValkey() *Engine {
	e := &Engine{id: "valkey", repo: "valkey/valkey", image: "valkey/valkey", server: "valkey-server", cli: "valkey-cli"}
	e.catalog = []services.Version{e.VersionOf("9.0", "9.0-alpine"), e.VersionOf("8.1", "8.1-alpine"), e.VersionOf("7.2", "7.2-alpine")}
	return e
}

// Releases: the configuration needs 7.2 (maxmemory-clients, ACL selectors).
func (e *Engine) Releases() services.Releases {
	return services.Releases{Source: "docker.io/" + e.repo, Tag: regexp.MustCompile(`\A([0-9]+\.[0-9]+)-alpine\z`),
		LineTag: "%s-alpine", EOL: e.id, Min: "7.2"}
}

func (e *Engine) VersionOf(version, tag string) services.Version {
	return services.Version{Version: version, Image: registry.Hub(e.image + ":" + tag), DataMount: "/data"}
}

func (e *Engine) ID() string                      { return e.id }
func (*Engine) Kind() services.Kind               { return services.KindCache }
func (*Engine) DefaultPort() int                  { return 6379 }
func (*Engine) Lifecycle() services.Lifecycle     { return services.Dedicated }
func (*Engine) DataOwner() (int, int)             { return uid, gid }
func (*Engine) Provisioner() services.Provisioner { return nil }
func (e *Engine) Catalog() []services.Version     { return e.catalog }

// Option keys and values.
const (
	optMemory      = "memory"      // container memory (MB); maxmemory is half of it
	optPolicy      = "policy"      // maxmemory-policy
	optPersistence = "persistence" // aof, rdb, none
	optScripting   = "scripting"   // Lua scripts and functions
	optModules     = "modules"     // Redis 8 bundled modules
)

// Options: the container gets twice maxmemory (a rewrite of the AOF or a
// snapshot forks the process: copy-on-write may double the memory).
func (e *Engine) Options() []services.Option {
	opts := []services.Option{
		{Key: optMemory, Values: []string{"128", "256", "512", "1024", "2048", "4096"}, Default: "256", Mutable: true,
			Custom: &services.CustomRange{Min: 128, Max: 65536, Step: 128}},
		{Key: optPolicy, Values: []string{"noeviction", "allkeys-lru", "allkeys-lfu", "volatile-lru", "volatile-lfu", "volatile-ttl"}, Default: "noeviction", Mutable: true},
		// Not mutable: at start the server only reads the files of the
		// persistence in use, the data would be lost.
		{Key: optPersistence, Values: []string{"aof", "rdb", "none"}, Default: "aof"},
		{Key: optScripting, Values: []string{"on", "off"}, Default: "on", Mutable: true},
	}
	if e.modules {
		opts = append(opts, services.Option{Key: optModules, Values: []string{"off", "on"}, Default: "off", Mutable: true})
	}
	return opts
}

func (*Engine) ValidateName(string) *services.Error { return nil }

const (
	confFile   = "server.conf"
	confTarget = "/etc/loch/server.conf"
	aclFile    = "users.acl"
	socketPath = "/tmp/server.sock"
)

func (e *Engine) Service(_ services.InstanceRef, _ services.Version, opts services.Options) services.ServiceSpec {
	mem, _ := strconv.Atoi(opts[optMemory])
	return services.ServiceSpec{
		Command: []string{e.server, confTarget},
		// No account answers on the socket without a password: NOAUTH
		// proves the server is up without giving the probe any secret.
		Healthcheck:     []string{"CMD-SHELL", e.cli + " -s " + socketPath + " ping 2>&1 | grep -qE 'PONG|NOAUTH'"},
		StopGracePeriod: "30s",
		PidsLimit:       128,
		Tmpfs:           []string{"/tmp:size=1m"},
		Files:           []services.GeneratedFile{{Name: confFile, Target: confTarget, Content: e.config(mem, opts)}},
		User:            strconv.Itoa(uid) + ":" + strconv.Itoa(gid),
		MemLimit:        int64(mem) << 20,
		CPUs:            1,
		NoAdminSecret:   true,
		State:           true,
		Hardened:        true,
	}
}

// config renders the server configuration. Every value comes from a fixed
// list (options) or a constant.
func (e *Engine) config(mem int, opts services.Options) []byte {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("# Generated by Loch.sh Panel (services). Rewritten by the panel: do not edit.")
	w("bind * -::*")
	w("port 6379")
	w("protected-mode yes")
	w("unixsocket %s", socketPath)
	w("unixsocketperm 700")
	w("aclfile %s/%s", services.StateMount, aclFile)
	w("dir /data")
	w("logfile \"\"")
	// Memory: data, then client buffers, bounded.
	w("maxmemory %dmb", mem/2)
	w("maxmemory-policy %s", opts[optPolicy])
	w("maxmemory-clients 5%%")
	w("maxclients 512")
	w("client-query-buffer-limit 16mb")
	w("proto-max-bulk-len 16mb")
	w("client-output-buffer-limit normal 32mb 16mb 60")
	w("client-output-buffer-limit pubsub 16mb 4mb 60")
	w("timeout 0")
	w("tcp-keepalive 300")
	// Nothing that changes the server from a connection.
	w("enable-debug-command no")
	w("enable-module-command no")
	w("enable-protected-configs no")
	w("sanitize-dump-payload yes")
	switch opts[optPersistence] {
	case "aof":
		w("appendonly yes")
		w("appendfsync everysec")
		w("save \"\"")
	case "rdb":
		w("appendonly no")
		w("save 3600 1 300 100 60 10000")
	default:
		w("appendonly no")
		w("save \"\"")
	}
	return []byte(b.String())
}

// StateFiles: the ACL file. "default" is disabled; the tenant may use every
// data command, but no administration (CONFIG, REPLICAOF, MIGRATE: file
// writes and server-side requests), no DEBUG, RESTORE or other @dangerous
// command except the harmless ones applications use.
func (e *Engine) StateFiles(ref services.InstanceRef, v services.Version, opts services.Options, digest string) ([]services.GeneratedFile, error) {
	if !sha256Re.MatchString(digest) {
		return nil, fmt.Errorf("redisfamily: invalid password digest")
	}
	rules := []string{"on", "#" + digest, "~*", "&*", "+@all",
		"-@admin", "-@dangerous", "-debug", "-migrate",
		"+info", "+flushdb", "+flushall", "+keys", "+sort", "+sort_ro",
		// SCRIPT DEBUG SYNC would block the server.
		"-script|debug"}
	if opts[optScripting] == "off" {
		rules = append(rules, "-@scripting")
	}
	if e.modules && opts[optModules] != "on" && hasModules(v) {
		rules = append(rules, "-@bloom", "-@cuckoo", "-@cms", "-@topk", "-@tdigest", "-@search", "-@timeseries", "-@json")
	}
	acl := "user default off resetkeys resetchannels -@all\n" +
		"user " + ref.Name + " " + strings.Join(rules, " ") + "\n"
	return []services.GeneratedFile{{Name: aclFile, Content: []byte(acl)}}, nil
}

// PasswordDigest is the SHA-256 the ACL file expects (no salt: the
// passwords are 190-bit random strings).
func (*Engine) PasswordDigest(password string) (string, error) {
	if !services.IsGeneratedPassword(password) {
		return "", fmt.Errorf("redisfamily: refusing a password not generated by the panel")
	}
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:]), nil
}

var sha256Re = regexp.MustCompile(`\A[0-9a-f]{64}\z`)

// hasModules: Redis 8 and later bundle modules (their ACL categories do not
// exist before: an unknown category would stop the server).
func hasModules(v services.Version) bool {
	major, _, _ := strings.Cut(v.Version, ".")
	n, err := strconv.Atoi(major)
	return err == nil && n >= 8
}

// TLS: TLS only, on the same port (plain connections are refused); the
// socket used by the healthcheck is not affected.
func (*Engine) TLS(services.InstanceRef, services.Version) (services.TLSSpec, bool) {
	return services.TLSSpec{Args: []string{
		"--port", "0",
		"--tls-port", "6379",
		"--tls-cert-file", services.TLSMount + "/" + services.CertFile,
		"--tls-key-file", services.TLSMount + "/" + services.KeyFile,
		"--tls-auth-clients", "no",
		"--tls-protocols", "TLSv1.2 TLSv1.3",
	}}, true
}

func (*Engine) ConnectionInfo(p services.ConnParams) []services.Credential {
	scheme := "redis"
	if p.TLS {
		scheme = "rediss"
	}
	uri := url.URL{Scheme: scheme, User: url.UserPassword(p.Name, p.Password), Host: p.Host + ":" + strconv.Itoa(p.Port), Path: "/0"}
	return []services.Credential{
		{Key: "host", Value: p.Host},
		{Key: "port", Value: strconv.Itoa(p.Port)},
		{Key: "user", Value: p.Name},
		{Key: "password", Value: p.Password, Secret: true},
		{Key: "uri", Value: uri.String(), Secret: true},
	}
}
