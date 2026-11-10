package services

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/moby/moby/api/types/container"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

var kvRef = InstanceRef{Engine: "kv", Version: "8", Name: "cache"}

func createKV(t *testing.T, env *testEnv, name string, opts map[string]string) *CreatedResource {
	t.Helper()
	res, err := env.m.Create(context.Background(), CreateRequest{Engine: "kv", Version: "8", Name: name, Options: opts}, "admin@example.com")
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return res
}

func secretOf(creds []Credential, key string) string {
	for _, c := range creds {
		if c.Key == key {
			return c.Value
		}
	}
	return ""
}

func TestDedicatedCreate(t *testing.T) {
	env := newEnv(t)
	res := createKV(t, env, "cache", map[string]string{"memory": "128"})
	if !res.InstanceCreated || res.Resource.Instance != "kv-cache" || res.Resource.Host != "svc-kv-cache" {
		t.Fatalf("res = %+v", res)
	}
	password := secretOf(res.Credentials, "password")
	if !IsGeneratedPassword(password) {
		t.Fatal("a generated password is returned once")
	}
	dir := filepath.Join(env.cfg.StacksDir, kvRef.Project())
	if exists(filepath.Join(dir, StateDir)) {
		t.Fatal("the state must not be in the stacks directory (writable by CI)")
	}
	acl, err := os.ReadFile(filepath.Join(env.cfg.Render.StatePath(kvRef), "users.acl"))
	if err != nil || !strings.Contains(string(acl), hashOf([]byte(password))) || strings.Contains(string(acl), password) {
		t.Fatalf("state must hold the digest only: %q %v", acl, err)
	}
	if exists(filepath.Join(dir, SecretsDir)) {
		t.Fatal("a dedicated instance has no admin secret")
	}
	compose, _ := os.ReadFile(filepath.Join(dir, ComposeFile))
	for _, want := range []string{"mem_limit: 134217728", "memswap_limit: 134217728", "cpus: 1", "user: 999:1000", "memory: \"128\"", "policy: noeviction", "name: cache", StateMount, "read_only: true", "- ALL"} {
		if !strings.Contains(string(compose), want) {
			t.Errorf("compose lacks %q:\n%s", want, compose)
		}
	}
	if strings.Contains(string(compose), "secrets:") {
		t.Fatal("no secret section without an admin secret")
	}
	list, err := env.m.Instances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got *InstanceSummary
	for i := range list {
		if list[i].Key == "kv-cache" {
			got = &list[i]
		}
	}
	if got == nil || !got.Ready || got.Modified || got.Name != "cache" || got.Version != "8" || got.Options["memory"] != "128" || *got.Resources != 1 {
		t.Fatalf("summary = %+v", got)
	}
	if rs, err := env.m.Resources(context.Background(), "kv-cache"); err != nil || len(rs) != 1 || rs[0].Name != "cache" || !rs[0].Managed {
		t.Fatalf("resources = %+v %v", rs, err)
	}
	if !strings.Contains(env.audit.String(), "service resource created") || strings.Contains(env.audit.String(), password) {
		t.Fatal("creation audited, without the password")
	}
}

func TestDedicatedValidation(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	create := func(name string, opts map[string]string) error {
		_, err := env.m.Create(ctx, CreateRequest{Engine: "kv", Version: "8", Name: name, Options: opts}, "admin")
		return err
	}
	wantCode(t, create("my_cache", nil), CodeInvalidName) // becomes a host name
	wantCode(t, create("abcdefghijklmnopqrstuvwxy", nil), CodeInvalidName)
	wantCode(t, create("reserved", nil), CodeNameReserved)
	wantCode(t, create("root", nil), CodeNameReserved)
	wantCode(t, create("cache", map[string]string{"memory": "4096"}), CodeInvalidOption)
	wantCode(t, create("cache", map[string]string{"color": "red"}), CodeInvalidOption)
	_, err := env.m.Create(ctx, CreateRequest{Engine: "fake", Version: "2", Name: "shop", Options: map[string]string{"memory": "64"}}, "admin")
	wantCode(t, err, CodeInvalidOption) // shared engines have no options
	createKV(t, env, "cache", nil)
	wantCode(t, create("cache", nil), CodeResourceExists)
	if env.runner.count("svc-kv-") != 1 {
		t.Fatalf("refused requests run nothing: %v", env.runner.commands())
	}
}

func TestDedicatedLimit(t *testing.T) {
	env := newEnv(t)
	env.m.cfg.MaxDedicatedInstances = 2
	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = env.m.Create(context.Background(), CreateRequest{Engine: "kv", Version: "8", Name: "c" + string(rune('a'+i))}, "admin")
		}()
	}
	wg.Wait()
	ok, limited := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case CodeOf(err) == CodeInstanceLimit:
			limited++
		default:
			t.Fatalf("unexpected error %v", err)
		}
	}
	if ok != 2 || limited != 3 {
		t.Fatalf("concurrent creations must respect the limit: %d created, %d refused", ok, limited)
	}
}

func TestDedicatedRotate(t *testing.T) {
	env := newEnv(t)
	res := createKV(t, env, "cache", nil)
	old := secretOf(res.Credentials, "password")
	acl := filepath.Join(env.cfg.Render.StatePath(kvRef), "users.acl")
	creds, err := env.m.RotatePassword(context.Background(), "kv-cache", "cache", "admin")
	if err != nil {
		t.Fatal(err)
	}
	pw := secretOf(creds, "password")
	data, _ := os.ReadFile(acl)
	if pw == old || !strings.Contains(string(data), hashOf([]byte(pw))) || env.runner.restarts != 1 {
		t.Fatalf("rotation writes the new digest and restarts: %q restarts=%d", data, env.runner.restarts)
	}
	wantCode(t, func() error {
		_, err := env.m.RotatePassword(context.Background(), "kv-cache", "other", "admin")
		return err
	}(), CodeResourceNotFound)

	// Every restart fails: the previous digest is restored, no password
	// returned, and the outcome is unknown (the instance may be down).
	env.runner.fail["restart"] = errors.New("boom")
	_, err = env.m.RotatePassword(context.Background(), "kv-cache", "cache", "admin")
	wantCode(t, err, CodeOutcomeUnknown)
	if after, _ := os.ReadFile(acl); string(after) != string(data) {
		t.Fatal("a failed rotation must restore the previous state")
	}
}

func TestDedicatedDelete(t *testing.T) {
	env := newEnv(t)
	createKV(t, env, "cache", nil)
	dir := filepath.Join(env.cfg.StacksDir, kvRef.Project())
	data := env.cfg.Render.DataPath(kvRef)
	wantCode(t, func() error { _, err := env.m.Delete(context.Background(), "kv-cache", "other", "admin"); return err }(), CodeResourceNotFound)
	res, err := env.m.Delete(context.Background(), "kv-cache", "cache", "admin")
	if err != nil || !res.InstanceRetired {
		t.Fatalf("%+v %v", res, err)
	}
	if exists(dir) || exists(data) || exists(env.cfg.Render.PrivatePath(kvRef)) || env.docker.byProject(kvRef.Project()) != nil {
		t.Fatal("deleting a dedicated instance removes its files, data and container")
	}
}

func TestDedicatedTampering(t *testing.T) {
	env := newEnv(t)
	createKV(t, env, "cache", nil)
	compose := filepath.Join(env.cfg.StacksDir, kvRef.Project(), ComposeFile)
	orig, _ := os.ReadFile(compose)

	// A larger memory limit written by hand: modified, rotation refused.
	os.WriteFile(compose, []byte(strings.Replace(string(orig), "mem_limit: 67108864", "mem_limit: 99999999999", 1)), 0o640)
	_, err := env.m.RotatePassword(context.Background(), "kv-cache", "cache", "admin")
	wantCode(t, err, CodeInstanceModified)
	_, err = env.m.Delete(context.Background(), "kv-cache", "cache", "admin")
	wantCode(t, err, CodeInstanceModified)

	// Valid options that differ from the panel's record (e.g. a larger
	// memory size rewritten consistently): modified, never run.
	os.WriteFile(compose, []byte(strings.NewReplacer(`memory: "64"`, `memory: "128"`, "67108864", "134217728").Replace(string(orig))), 0o640)
	if inst, lerr := env.m.load("kv-cache"); lerr != nil || !inst.modified {
		t.Fatalf("a manifest differing from the record must be reported as modified: %v", lerr)
	}

	// Options outside the catalog: the instance is not managed at all.
	os.WriteFile(compose, []byte(strings.Replace(string(orig), `memory: "64"`, `memory: "999999"`, 1)), 0o640)
	if _, lerr := env.m.load("kv-cache"); lerr == nil {
		t.Fatal("invalid options in the manifest must not be trusted")
	}
	os.WriteFile(compose, orig, 0o640)

	// A container whose limits differ is not trusted.
	c := env.docker.byProject(kvRef.Project())
	env.docker.mu.Lock()
	c.inspect.HostConfig.Memory = 0
	env.docker.mu.Unlock()
	inst, _ := env.m.load("kv-cache")
	if _, rerr := env.m.ready(context.Background(), inst); rerr == nil {
		t.Fatal("a container without the memory limit must be refused")
	}
	env.docker.mu.Lock()
	c.inspect.HostConfig.Memory = 64 << 20
	c.inspect.HostConfig.CapDrop = nil
	env.docker.mu.Unlock()
	if _, rerr := env.m.ready(context.Background(), inst); rerr == nil {
		t.Fatal("a container keeping its capabilities must be refused")
	}
}

func TestDedicatedKeys(t *testing.T) {
	env := newEnv(t)
	reg := env.m.Registry()
	for key, want := range map[string]bool{"kv-cache": true, "kv-my_cache": false, "kv-": false, "kv-8": false, "fake-2": true, "fake-cache": false, "nope-x": false} {
		if got := reg.KnownKey(key); got != want {
			t.Errorf("KnownKey(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestDedicatedPublishedWithTLS(t *testing.T) {
	env := newEnv(t)
	createKV(t, env, "cache", nil)
	env.expose(t, "kv-cache", Exposure{Address: netip.MustParseAddr("203.0.113.10"), Port: 16379})
	creds, err := env.m.RotatePassword(context.Background(), "kv-cache", "cache", "admin")
	if err != nil {
		t.Fatal(err)
	}
	res := &CreatedResource{Credentials: creds}
	if !strings.HasPrefix(secretOf(res.Credentials, "uri"), "kvs://") || secretOf(res.Credentials, "external") == "" {
		t.Fatalf("a published instance requires TLS from every client: %+v", res.Credentials)
	}
	inst, _ := env.m.load("kv-cache")
	if _, rerr := env.m.ready(context.Background(), inst); rerr != nil {
		t.Fatalf("published container verified: %v", rerr)
	}
}

func TestDedicatedRotateRestoresOnFailure(t *testing.T) {
	env := newEnv(t)
	createKV(t, env, "cache", nil)
	acl := filepath.Join(env.cfg.Render.StatePath(kvRef), "users.acl")
	before, _ := os.ReadFile(acl)
	// The restart with the new password fails, the one with the old works.
	env.runner.failTimes["restart"] = 1
	_, err := env.m.RotatePassword(context.Background(), "kv-cache", "cache", "admin")
	wantCode(t, err, CodeStartFailed)
	if after, _ := os.ReadFile(acl); string(after) != string(before) {
		t.Fatal("the previous digest must be restored")
	}
}

func TestDedicatedRetireAndRollback(t *testing.T) {
	env := newEnv(t)
	createKV(t, env, "cache", nil)
	if err := env.m.Retire(context.Background(), "kv-cache", "admin"); err != nil {
		t.Fatal(err)
	}
	if exists(env.cfg.Render.PrivatePath(kvRef)) || exists(filepath.Join(env.cfg.StacksDir, kvRef.Project())) {
		t.Fatal("retiring a dedicated instance deletes it")
	}
	// A failed start removes everything that was created.
	env.runner.fail["up"] = errors.New("boom")
	_, err := env.m.Create(context.Background(), CreateRequest{Engine: "kv", Version: "8", Name: "cache"}, "admin")
	wantCode(t, err, CodeStartFailed)
	if exists(env.cfg.Render.PrivatePath(kvRef)) || exists(filepath.Join(env.cfg.StacksDir, kvRef.Project())) || exists(env.cfg.Render.DataPath(kvRef)) {
		t.Fatal("a failed creation leaves nothing behind")
	}
}

func TestDedicatedReference(t *testing.T) {
	env := newEnv(t)
	createKV(t, env, "cache", nil)
	ref := filepath.Join(env.cfg.Render.PrivatePath(kvRef), referenceFile)
	os.Remove(ref)
	if inst, lerr := env.m.load("kv-cache"); lerr != nil || !inst.modified {
		t.Fatal("an instance without its private reference is reported as modified")
	}
	os.Symlink("/etc/passwd", ref)
	if inst, lerr := env.m.load("kv-cache"); lerr != nil || !inst.modified {
		t.Fatal("a symlinked reference is not trusted")
	}
}

func TestDedicatedCountIgnoresForeignDirectories(t *testing.T) {
	env := newEnv(t)
	env.m.cfg.MaxDedicatedInstances = 1
	// Directories planted in the stacks directory (CI), even with the marker.
	os.Mkdir(filepath.Join(env.cfg.StacksDir, "svc-kv-planted"), 0o755)
	os.WriteFile(filepath.Join(env.cfg.StacksDir, "svc-kv-planted", ".loch-managed"), nil, 0o644)
	createKV(t, env, "cache", nil)
}

func TestDedicatedUpdateOptions(t *testing.T) {
	env := newEnv(t)
	createKV(t, env, "cache", nil)
	ctx := context.Background()
	acl := filepath.Join(env.cfg.Render.StatePath(kvRef), "users.acl")
	before, _ := os.ReadFile(acl)

	opts, err := env.m.UpdateOptions(ctx, "kv-cache", map[string]string{"memory": "128", "policy": "allkeys-lru"}, "admin")
	if err != nil || opts["memory"] != "128" || opts["policy"] != "allkeys-lru" || opts["persistence"] != "aof" {
		t.Fatalf("%v %v", opts, err)
	}
	inst, lerr := env.m.load("kv-cache")
	if lerr != nil || inst.modified || inst.opts["memory"] != "128" {
		t.Fatalf("the new options are the panel's record: %+v %v", inst, lerr)
	}
	if _, rerr := env.m.ready(ctx, inst); rerr != nil {
		t.Fatalf("container recreated with the new limits: %v", rerr)
	}
	after, _ := os.ReadFile(acl)
	if string(after) == string(before) || !strings.Contains(string(after), "policy=allkeys-lru") {
		t.Fatalf("state rendered again from the kept digest: %q", after)
	}
	if !strings.Contains(env.audit.String(), "service resource updated") {
		t.Fatal("update audited")
	}

	// An option that changes no compose field still recreates the container
	// (its configuration files are read at start only).
	recreates := env.runner.count("svc-kv-cache up -d --remove-orphans --pull missing --wait --wait-timeout 180 --force-recreate")
	if _, err := env.m.UpdateOptions(ctx, "kv-cache", map[string]string{"policy": "noeviction"}, "admin"); err != nil ||
		env.runner.count("svc-kv-cache up -d --remove-orphans --pull missing --wait --wait-timeout 180 --force-recreate") != recreates+1 {
		t.Fatalf("policy change recreates the container: %v", err)
	}

	// Immutable option, unknown value, nothing to change.
	_, err = env.m.UpdateOptions(ctx, "kv-cache", map[string]string{"persistence": "none"}, "admin")
	wantCode(t, err, CodeOptionImmutable)
	_, err = env.m.UpdateOptions(ctx, "kv-cache", map[string]string{"memory": "9999"}, "admin")
	wantCode(t, err, CodeInvalidOption)
	ups := env.runner.count("svc-kv-cache up")
	if _, err := env.m.UpdateOptions(ctx, "kv-cache", map[string]string{"memory": "128"}, "admin"); err != nil || env.runner.count("svc-kv-cache up") != ups {
		t.Fatalf("no change, no restart: %v", err)
	}
	// Shared instances have no options.
	mustCreate(t, env, "shop")
	_, err = env.m.UpdateOptions(ctx, "fake-2", map[string]string{"memory": "128"}, "admin")
	wantCode(t, err, CodeInvalidOption)
}

func TestDedicatedUpdateRestoresOnFailure(t *testing.T) {
	env := newEnv(t)
	createKV(t, env, "cache", nil)
	compose := filepath.Join(env.cfg.StacksDir, kvRef.Project(), ComposeFile)
	before, _ := os.ReadFile(compose)
	env.runner.failTimes["up"] = 1 // the new configuration does not start
	_, err := env.m.UpdateOptions(context.Background(), "kv-cache", map[string]string{"memory": "128"}, "admin")
	wantCode(t, err, CodeStartFailed)
	after, _ := os.ReadFile(compose)
	inst, _ := env.m.load("kv-cache")
	if string(after) != string(before) || inst.modified || inst.opts["memory"] != "64" {
		t.Fatal("a failed update restores the previous configuration")
	}
}

func TestDedicatedUpdateNeedsTheDigest(t *testing.T) {
	env := newEnv(t)
	createKV(t, env, "cache", nil)
	// An instance created before the digest file existed.
	os.Remove(filepath.Join(env.cfg.Render.PrivatePath(kvRef), digestFile))
	_, err := env.m.UpdateOptions(context.Background(), "kv-cache", map[string]string{"memory": "128"}, "admin")
	wantCode(t, err, CodePasswordResetRequired)
	// A failed rotation leaves no digest of a password nobody received.
	env.runner.failTimes["restart"] = 5
	if _, err := env.m.RotatePassword(context.Background(), "kv-cache", "cache", "admin"); err == nil {
		t.Fatal("rotation should fail")
	}
	if _, err := os.Stat(filepath.Join(env.cfg.Render.PrivatePath(kvRef), digestFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("digest of an unknown password left: %v", err)
	}
}

func TestSecretStateFile(t *testing.T) {
	env := newEnv(t)
	root := t.TempDir()
	private := filepath.Join(root, ".loch", "svc-x")
	state := filepath.Join(private, "state")
	os.MkdirAll(state, 0o755)
	os.Chmod(filepath.Join(root, ".loch"), 0o700)
	os.Chmod(private, 0o700)
	var chowned []string
	env.m.chown = func(p string, uid, gid int) error {
		chowned = append(chowned, fmt.Sprintf("%s %d:%d", filepath.Base(p), uid, gid))
		return nil
	}
	e := fakeDedicatedEngine{}
	if err := env.m.writeStateFile(e, true, state, "users.json", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(filepath.Join(state, "users.json"))
	if st.Mode().Perm() != 0o400 || len(chowned) != 1 || chowned[0] != "users.json 999:1000" {
		t.Fatalf("secret state: 0400, owned by the engine user: %v %v", st.Mode(), chowned)
	}
	if err := env.m.writeStateFile(e, false, state, "users.acl", []byte("digest")); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(filepath.Join(state, "users.acl")); st.Mode().Perm() != 0o644 || len(chowned) != 1 {
		t.Fatal("digests keep 0644, no chown")
	}

	// API not running as root: readable fallback only behind private parents.
	env.m.chown = func(string, int, int) error { return os.ErrPermission }
	if err := env.m.writeStateFile(e, true, state, "users.json", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(filepath.Join(state, "users.json")); st.Mode().Perm() != 0o444 {
		t.Fatalf("fallback: %v", st.Mode())
	}
	os.Chmod(private, 0o755)
	if err := env.m.writeStateFile(e, true, state, "users.json", []byte("secret")); err == nil {
		t.Fatal("a readable secret behind a loose parent must be refused")
	}
	if _, err := os.Stat(filepath.Join(state, "users.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the refused secret file is removed")
	}
}

func TestPurgeModifiedInstance(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	createKV(t, env, "cache", nil)
	key := "kv-cache"
	// Not edited: the usual deletion paths apply.
	wantCode(t, env.m.Purge(ctx, key, "admin"), CodeInstanceNotModified)

	dir := filepath.Join(env.cfg.StacksDir, kvRef.Project())
	compose := filepath.Join(dir, ComposeFile)
	orig, _ := os.ReadFile(compose)
	os.WriteFile(compose, []byte(strings.Replace(string(orig), "mem_limit: 67108864", "mem_limit: 99999999999", 1)), 0o640)
	// A container started by hand from the instance directory under another
	// project name, and an unrelated one.
	env.docker.mu.Lock()
	env.docker.extra = append(env.docker.extra,
		container.Summary{ID: "byhand", Labels: map[string]string{composeProjectLabel: "other", composeWorkingDirLabel: dir}},
		container.Summary{ID: "unrelated", Labels: map[string]string{composeProjectLabel: "hosted-blog"}},
	)
	env.docker.mu.Unlock()
	ups := env.runner.count("svc-kv-cache")

	if err := env.m.Purge(ctx, key, "admin"); err != nil {
		t.Fatal(err)
	}
	if env.runner.count("svc-kv-cache") != ups {
		t.Fatal("the edited compose file must never be run")
	}
	if env.docker.byProject(kvRef.Project()) != nil {
		t.Fatal("the instance container is removed")
	}
	env.docker.mu.Lock()
	extra := slices.Clone(env.docker.extra)
	env.docker.mu.Unlock()
	if len(extra) != 1 || extra[0].ID != "unrelated" {
		t.Fatalf("only the instance's containers are removed: %+v", extra)
	}
	for _, p := range []string{dir, env.cfg.Render.PrivatePath(kvRef), env.cfg.Render.DataPath(kvRef)} {
		if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s still present: %v", p, err)
		}
	}
	if !strings.Contains(env.audit.String(), "service instance purged") {
		t.Fatal("purge audited")
	}
	wantCode(t, env.m.Purge(ctx, key, "admin"), CodeInstanceNotFound)
}

func TestGeneratedKeyKeptForTheInstanceLife(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	createKV(t, env, "cache", nil)
	keyPath := filepath.Join(env.cfg.Render.StatePath(kvRef), "master.key")
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(string(key))
	if err != nil || len(raw) != 32 {
		t.Fatalf("32 random bytes, base64: %q %v", key, err)
	}
	if st, _ := os.Stat(keyPath); st.Mode().Perm() != 0o400 {
		t.Fatalf("key mode %v", st.Mode())
	}
	if _, err := env.m.RotatePassword(ctx, "kv-cache", "cache", "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.m.UpdateOptions(ctx, "kv-cache", map[string]string{"memory": "128"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(keyPath); string(after) != string(key) {
		t.Fatal("the key must survive rotations and option changes (losing it loses the data)")
	}
	other := InstanceRef{Engine: "kv", Version: "8", Name: "other"}
	createKV(t, env, "other", nil)
	if k2, _ := os.ReadFile(filepath.Join(env.cfg.Render.StatePath(other), "master.key")); string(k2) == string(key) {
		t.Fatal("each instance has its own key")
	}
}
