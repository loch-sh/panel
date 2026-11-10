package s3

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"host-panel-api/internal/services"
	"host-panel-api/internal/services/servicestest"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const testSecret = "Abcdefghijklmnopqrstuvwxyz012345"

func opts(e *Engine, over map[string]string) services.Options {
	o, err := services.ResolveOptions(e.Options(), over)
	if err != nil {
		panic(err)
	}
	return o
}

func TestUsersFile(t *testing.T) {
	if !services.IsGeneratedPassword(testSecret) {
		t.Fatal("test secret must look generated")
	}
	e := New()
	ref := services.InstanceRef{Engine: "s3", Name: "media"}
	digest, err := e.PasswordDigest(testSecret)
	if err != nil || digest != testSecret {
		t.Fatalf("SigV4 keeps the secret itself: %q %v", digest, err)
	}
	files, err := e.StateFiles(ref, e.Catalog()[0], opts(e, nil), digest)
	if err != nil || len(files) != 1 || files[0].Name != "users.json" {
		t.Fatalf("%v %v", files, err)
	}
	var doc struct {
		Users []userRecord `json:"users"`
	}
	if err := json.Unmarshal(files[0].Content, &doc); err != nil || len(doc.Users) != 1 {
		t.Fatalf("%s %v", files[0].Content, err)
	}
	u := doc.Users[0]
	if !u.IsRoot || u.UserID != "root" || u.SecretAccessKey != testSecret || u.AccessKeyID != AccessKeyID(testSecret) || u.DisplayName != "media" {
		t.Fatalf("exactly one root user, the tenant: %+v", u)
	}

	if _, err := e.PasswordDigest("chosen-by-user"); err == nil {
		t.Fatal("only generated secrets")
	}
	// Read back from disk: anything but a generated secret is refused (it
	// is rendered in JSON).
	for _, bad := range []string{"", "short", `Abcdefghijklmnopqrstuvwxyz01234"`, testSecret + "\n"} {
		if _, err := e.StateFiles(ref, e.Catalog()[0], opts(e, nil), bad); err == nil {
			t.Fatalf("secret %q accepted", bad)
		}
	}
}

func TestAccessKeyID(t *testing.T) {
	a, b := AccessKeyID(testSecret), AccessKeyID("Bbcdefghijklmnopqrstuvwxyz012345")
	if !regexp.MustCompile(`\ALOCH[A-Z2-7]{16}\z`).MatchString(a) || a == b || a != AccessKeyID(testSecret) {
		t.Fatalf("deterministic, 20 characters, changes with the secret: %q %q", a, b)
	}
	if strings.Contains(a, testSecret[:8]) {
		t.Fatal("the access key must not reveal the secret")
	}
}

func TestConnectionInfo(t *testing.T) {
	creds := New().ConnectionInfo(services.ConnParams{Host: "svc-s3-media", Port: 8080, Name: "media", Password: testSecret})
	got := map[string]services.Credential{}
	for _, c := range creds {
		got[c.Key] = c
	}
	if got["bucket"].Value != "media" || got["endpoint"].Value != "http://svc-s3-media:8080" || got["region"].Value != Region || got["pathStyle"].Value != "true" ||
		got["accessKeyId"].Value != AccessKeyID(testSecret) || got["accessKeyId"].Secret {
		t.Fatalf("%+v", creds)
	}
	for _, c := range creds {
		if strings.Contains(c.Value, testSecret) != c.Secret {
			t.Fatalf("%s: secret flag must match the presence of the secret", c.Key)
		}
	}
	tls := New().ConnectionInfo(services.ConnParams{Host: "s3.example.com", Port: 9000, Password: testSecret, TLS: true})
	if tls[0].Value != "https://s3.example.com:9000" {
		t.Fatalf("%+v", tls[0])
	}
}

func TestServiceSpec(t *testing.T) {
	e := New()
	spec := e.Service(services.InstanceRef{Engine: "s3", Name: "media"}, e.Catalog()[0], opts(e, map[string]string{"memory": "512", "quota": "5120"}))
	if spec.MemLimit != 512<<20 || spec.Environment["S3_MAX_STORAGE_BYTES"] != "5368709120" || spec.User != "10001:10001" ||
		!spec.Hardened || !spec.State || !spec.SecretState || !spec.NoAdminSecret {
		t.Fatalf("%+v", spec)
	}
	for k := range spec.Environment {
		// Any of them would switch the authentication mode or turn on
		// the admin API.
		if k == "S3_ADMIN_API_KEY" || k == "S3_ACCESS_KEY_ID" || k == "S3_SECRET_ACCESS_KEY" || k == "S3_ENCRYPTION_KEY" {
			t.Fatalf("%s must never be set", k)
		}
	}
	if spec.Environment["S3_INIT_BUCKETS"] != "media" {
		t.Fatal("the resource bucket is created at start")
	}
	if spec.Environment["S3_USERS_FILE"] != services.StateMount+"/users.json" {
		t.Fatal("users file read from the read-only state mount")
	}
	if e.ValidateName("ab") == nil || e.ValidateName("abc") != nil {
		t.Fatal("bucket names have at least 3 characters")
	}
}

func TestRenderGolden(t *testing.T) {
	e := New()
	cfg := services.RenderConfig{DataDir: "/data/services", Networks: servicestest.Networks()}
	out, err := services.Render(e, services.InstanceRef{Engine: "s3", Name: "media"}, e.Catalog()[0], opts(e, nil), []string{"services"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "compose-s3-media.golden.yaml")
	if *update {
		os.MkdirAll("testdata", 0o755)
		os.WriteFile(golden, out, 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if string(out) != string(want) {
		t.Fatalf("compose differs from %s:\n%s", golden, out)
	}
	for _, forbidden := range []string{"ports:", "privileged", "network_mode", "S3_SECRET", "S3_ADMIN", testSecret} {
		if strings.Contains(string(out), forbidden) {
			t.Fatalf("compose must not contain %q", forbidden)
		}
	}
}

func TestEncryptionOption(t *testing.T) {
	e := New()
	off := e.Service(services.InstanceRef{Engine: "s3", Name: "media"}, e.Catalog()[0], opts(e, nil))
	if len(off.GeneratedKeys) != 0 || off.Environment["S3_ENCRYPT_BY_DEFAULT"] != "" {
		t.Fatal("encryption is off by default")
	}
	on := e.Service(services.InstanceRef{Engine: "s3", Name: "media"}, e.Catalog()[0], opts(e, map[string]string{"encryption": "on"}))
	if len(on.GeneratedKeys) != 1 || on.Environment["S3_ENCRYPTION_KEY_FILE"] != services.StateMount+"/sse.key" || on.Environment["S3_ENCRYPT_BY_DEFAULT"] != "true" {
		t.Fatalf("%+v", on)
	}
	if _, ok := on.Environment["S3_ENCRYPTION_KEY"]; ok {
		t.Fatal("the key never goes through the environment")
	}
	for _, o := range e.Options() {
		if o.Key == "encryption" && o.Mutable {
			t.Fatal("encryption is fixed at creation")
		}
	}
	if e.ValidateOptions(services.Version{Version: "1.3"}, opts(e, map[string]string{"encryption": "on"})) == nil {
		t.Fatal("refused on a version that would ignore it")
	}
	if e.ValidateOptions(services.Version{Version: "1.4"}, opts(e, map[string]string{"encryption": "on"})) != nil ||
		e.ValidateOptions(services.Version{Version: "1.3"}, opts(e, nil)) != nil {
		t.Fatal("accepted where supported, or when off")
	}
}
