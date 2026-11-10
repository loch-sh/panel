// Package s3 is the object storage engine of the services module, backed by
// Loch S3 (https://github.com/loch-sh/s3). Standalone mode: one dedicated
// instance per resource, the tenant being the only (root) user of its
// instance. The panel renders the users file in the private state directory,
// mounted read-only; the Loch S3 admin API stays off (no admin key), and the
// panel runs nothing inside the container.
//
// SigV4 needs the server to know the secret: unlike the other dedicated
// engines, the state holds the secret itself (see services-security.md).
package s3

import (
	"bytes"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"host-panel-api/internal/registry"
	"host-panel-api/internal/services"
)

// Engine runs Loch S3 as an unprivileged user.
type Engine struct{}

// New returns the Loch S3 engine (standalone mode).
func New() *Engine { return &Engine{} }

const (
	// uid/gid the panel runs the container as (compose `user:`), whatever
	// the image user (100:101): the data directory and the users file are
	// given to it, so the panel never depends on the image's choice.
	uid = 10001
	gid = 10001

	port      = 8080
	usersFile = "users.json"
	// Region shown to clients: any region is accepted by Loch S3; this one
	// is the SDK default (no LocationConstraint body on CreateBucket).
	Region = "us-east-1"
)

func (*Engine) ID() string                        { return "s3" }
func (*Engine) Kind() services.Kind               { return services.KindStorage }
func (*Engine) DefaultPort() int                  { return port }
func (*Engine) Lifecycle() services.Lifecycle     { return services.Dedicated }
func (*Engine) DataOwner() (int, int)             { return uid, gid }
func (*Engine) Provisioner() services.Provisioner { return nil }

// Catalog: Loch S3 is our own image, published on ghcr.io (not on Docker
// Hub, so not on its mirror): an explicit exception to the mirror rule.
func (e *Engine) Catalog() []services.Version {
	return []services.Version{e.VersionOf("1.4", "1.4.0")}
}

// Releases: exact tags on ghcr.io (our product: not on endoflife.date).
// Loch S3 1.4 is the first release with the features the panel renders
// (default bucket, quota, SSE-S3).
func (*Engine) Releases() services.Releases {
	return services.Releases{Source: "ghcr.io/loch-sh/s3", Tag: regexp.MustCompile(`\A([0-9]+\.[0-9]+)\.([0-9]+)\z`), Min: "1.4"}
}

func (*Engine) VersionOf(version, tag string) services.Version {
	return services.Version{Version: version, Image: registry.GHCR("loch-sh/s3:" + tag), DataMount: "/data"}
}

const (
	optMemory     = "memory"     // container memory (MB)
	optQuota      = "quota"      // storage quota (MB): objects, versions, pending uploads
	optEncryption = "encryption" // SSE-S3 for every object written
	encryptionKey = "sse.key"    // master key, generated once, in the state
)

// encryptionSince is the first Loch S3 version reading its key from a file
// and encrypting by default: older ones would ignore the setting.
const encryptionSince = "1.4"

func (*Engine) Options() []services.Option {
	return []services.Option{
		{Key: optMemory, Values: []string{"128", "256", "512", "1024"}, Default: "256", Mutable: true,
			Custom: &services.CustomRange{Min: 128, Max: 8192, Step: 128}},
		// Lowering the quota below the space used refuses new writes; the
		// data is kept.
		{Key: optQuota, Values: []string{"1024", "5120", "10240", "51200", "102400", "512000"}, Default: "10240", Mutable: true,
			Custom: &services.CustomRange{Min: 1024, Max: 10485760, Step: 1024}},
		// Fixed at creation: without its key, the data is lost; objects
		// written before an activation would stay in clear.
		{Key: optEncryption, Values: []string{"off", "on"}, Default: "off"},
	}
}

// ValidateOptions refuses the encryption on a version that cannot apply it
// (it would silently store objects in clear).
func (*Engine) ValidateOptions(v services.Version, opts services.Options) *services.Error {
	if opts[optEncryption] == "on" && !versionAtLeast(v.Version, encryptionSince) {
		return &services.Error{Code: services.CodeInvalidOption, Field: "options",
			Message: "server-side encryption needs Loch S3 " + encryptionSince + " or later"}
	}
	return nil
}

// versionAtLeast compares "major.minor" versions.
func versionAtLeast(v, min string) bool {
	parse := func(s string) (int, int) {
		maj, minr, _ := strings.Cut(s, ".")
		a, _ := strconv.Atoi(maj)
		b, _ := strconv.Atoi(minr)
		return a, b
	}
	a, b := parse(v)
	c, d := parse(min)
	return a > c || (a == c && b >= d)
}

// ValidateName: bucket names are 3 to 63 characters; dedicated names are
// already lowercase letters and digits.
func (*Engine) ValidateName(name string) *services.Error {
	if len(name) < 3 {
		return &services.Error{Code: services.CodeInvalidName, Field: "name", Message: "the name must have at least 3 characters (it is also the bucket name)"}
	}
	return nil
}

func (*Engine) Service(ref services.InstanceRef, _ services.Version, opts services.Options) services.ServiceSpec {
	mem, _ := strconv.Atoi(opts[optMemory])
	quota, _ := strconv.ParseInt(opts[optQuota], 10, 64)
	spec := services.ServiceSpec{
		Command: []string{"s3"},
		Environment: map[string]string{
			"S3_USERS_FILE":        services.StateMount + "/" + usersFile,
			"S3_MAX_STORAGE_BYTES": strconv.FormatInt(quota<<20, 10),
			// The bucket of the resource, owned by the tenant (root),
			// created at start if missing (existing ones are untouched).
			"S3_INIT_BUCKETS": ref.Name,
			// tokio runs file operations on this pool: bounded below
			// PidsLimit.
			"S3_MAX_BLOCKING_THREADS": "32",
		},
		// Unauthenticated, never logged, without figures or names.
		Healthcheck: []string{"CMD", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/_loch/health"},
		// Loch S3 lets requests in progress finish for 8 s after SIGTERM.
		StopGracePeriod: "30s",
		Tmpfs:           []string{"/tmp:size=16m"},
		PidsLimit:       128,
		User:            strconv.Itoa(uid) + ":" + strconv.Itoa(gid),
		MemLimit:        int64(mem) << 20,
		CPUs:            1,
		NoAdminSecret:   true,
		State:           true,
		SecretState:     true,
		Hardened:        true,
	}
	if opts[optEncryption] == "on" {
		// SSE-S3 for every object: master key generated by the panel, read
		// from the read-only state (never in the environment).
		spec.GeneratedKeys = []string{encryptionKey}
		spec.Environment["S3_ENCRYPTION_KEY_FILE"] = services.StateMount + "/" + encryptionKey
		spec.Environment["S3_ENCRYPT_BY_DEFAULT"] = "true"
	}
	return spec
}

// PasswordDigest: SigV4 signs with the secret itself, so the "digest" kept
// by the panel to render the state again is the secret (stored like the
// users file, in the private directory).
func (*Engine) PasswordDigest(password string) (string, error) {
	if !services.IsGeneratedPassword(password) {
		return "", fmt.Errorf("s3: refusing a secret not generated by the panel")
	}
	return password, nil
}

type userRecord struct {
	UserID          string `json:"user_id"`
	DisplayName     string `json:"display_name"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	IsRoot          bool   `json:"is_root"`
}

// StateFiles: the users file, with the tenant as the single root user.
func (*Engine) StateFiles(ref services.InstanceRef, _ services.Version, _ services.Options, secret string) ([]services.GeneratedFile, error) {
	// Read back from disk: only a secret of the panel's form is rendered.
	if !services.IsGeneratedPassword(secret) {
		return nil, fmt.Errorf("s3: invalid secret")
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string][]userRecord{"users": {{
		UserID: "root", DisplayName: ref.Name, AccessKeyID: AccessKeyID(secret), SecretAccessKey: secret, IsRoot: true,
	}}}); err != nil {
		return nil, err
	}
	return []services.GeneratedFile{{Name: usersFile, Content: b.Bytes()}}, nil
}

// AccessKeyID derives the access key from the secret: it changes with each
// rotation and needs no storage of its own. It is not secret (it travels in
// every request).
func AccessKeyID(secret string) string {
	sum := sha256.Sum256([]byte("loch-s3-akid\x00" + secret))
	return "LOCH" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])[:16]
}

func (*Engine) ConnectionInfo(p services.ConnParams) []services.Credential {
	scheme := "http"
	if p.TLS {
		scheme = "https"
	}
	return []services.Credential{
		{Key: "endpoint", Value: scheme + "://" + p.Host + ":" + strconv.Itoa(p.Port)},
		{Key: "host", Value: p.Host},
		{Key: "port", Value: strconv.Itoa(p.Port)},
		{Key: "region", Value: Region},
		{Key: "accessKeyId", Value: AccessKeyID(p.Password)},
		{Key: "secretAccessKey", Value: p.Password, Secret: true},
		{Key: "bucket", Value: p.Name},
		{Key: "pathStyle", Value: "true"},
	}
}
