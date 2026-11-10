package functions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"

	"host-panel-api/internal/docker"
)

const actor = "admin@example.com"

var bg = context.Background()

func TestCreateReservesTheFunction(t *testing.T) {
	e := newTestEnv(t)
	c := e.create(t, "resize")
	n := nm("resize")

	// Credentials: a random token (no SigV4 pair by default), shown once.
	cr := c.Credentials
	if len(cr.Token) < 32 || cr.AccessKeyID != "" || cr.SecretAccessKey != "" {
		t.Fatalf("credentials of an unexpected form: %d %q", len(cr.Token), cr.AccessKeyID)
	}
	if cr.Endpoint != "http://fn-resize:8080/2015-03-31/functions/resize/invocations" {
		t.Fatalf("endpoint %q", cr.Endpoint)
	}
	other := e.create(t, "report").Credentials
	if other.Token == cr.Token {
		t.Fatal("credentials must be random")
	}
	// Files: the env_files are 0400, the reference holds no credential.
	for _, f := range []string{e.m.paths.SecretsEnv(n), e.m.paths.CredentialsEnv(n)} {
		if fi, err := os.Stat(f); err != nil || fi.Mode().Perm() != 0o400 {
			t.Fatalf("%s: %v %v", f, fi.Mode(), err)
		}
	}
	// Only the SHA-256 of the token reaches the container.
	creds, _ := os.ReadFile(e.m.paths.CredentialsEnv(n))
	sum := sha256.Sum256([]byte(cr.Token))
	if string(creds) != "LOCH_RUNTIME_TOKEN_SHA256="+hex.EncodeToString(sum[:])+"\n" {
		t.Fatalf("credentials env_file: %q", creds)
	}
	assertNoLeak(t, e, string(creds), cr.Token)
	ref, err := e.m.load(n)
	if err != nil {
		t.Fatal(err)
	}
	refJSON, _ := json.Marshal(ref)
	assertNoLeak(t, e, string(refJSON), cr.Token, cr.AccessKeyID, cr.SecretAccessKey)
	if ref.ImageTag != "22-arm64" || ref.ImageDigest != testDigest || ref.ImageID != testImageID || ref.Deployment != nil {
		t.Fatalf("reference: %+v", ref)
	}
	// The dedicated network is created and joined; nothing runs yet.
	if !slices.Contains(e.nets.created, "fn-resize") || !slices.Equal(ref.Networks, []string{"fn-resize"}) {
		t.Fatalf("networks: %v %v", e.nets.created, ref.Networks)
	}
	if e.runner.ups() != 0 || c.Function.State != StateNotDeployed {
		t.Fatalf("ups %d, state %s", e.runner.ups(), c.Function.State)
	}
	if _, err := os.Lstat(e.m.paths.ProjectDir(n)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("no compose project before the first deployment")
	}
	assertNoLeak(t, e, e.audit.String()+e.logs.String(), cr.Token, cr.AccessKeyID, cr.SecretAccessKey)
	if !strings.Contains(e.audit.String(), `"msg":"function created"`) {
		t.Fatal("creation not audited")
	}
	// Created again: refused.
	_, err = e.m.Create(bg, CreateRequest{Name: "resize", Runtime: "nodejs", RuntimeVersion: "22", Handler: "index.handler"}, actor)
	wantCode(t, err, CodeExists)
}

// assertNoLeak fails if a secret appears in s.
func assertNoLeak(t *testing.T, _ *testEnv, s string, secrets ...string) {
	t.Helper()
	for _, v := range secrets {
		if v != "" && strings.Contains(s, v) {
			t.Fatalf("secret value leaked: %q in %s", v, s)
		}
	}
}

func TestCreateRefusals(t *testing.T) {
	base := func() CreateRequest {
		return CreateRequest{Name: "resize", Runtime: "nodejs", RuntimeVersion: "22", Handler: "index.handler"}
	}
	cases := map[string]struct {
		mutate func(*CreateRequest, *testEnv)
		code   string
	}{
		"bad name":          {func(r *CreateRequest, _ *testEnv) { r.Name = "Bad" }, CodeInvalidName},
		"reserved name":     {func(r *CreateRequest, _ *testEnv) { r.Name = "config" }, CodeInvalidName},
		"runtime":           {func(r *CreateRequest, _ *testEnv) { r.Runtime = "java" }, CodeInvalidRuntime},
		"version":           {func(r *CreateRequest, _ *testEnv) { r.RuntimeVersion = "14" }, CodeInvalidRuntime},
		"handler":           {func(r *CreateRequest, _ *testEnv) { r.Handler = "../x.handler" }, CodeInvalidHandler},
		"memory":            {func(r *CreateRequest, _ *testEnv) { r.MemoryMB = 64 }, CodeInvalidLimits},
		"reserved env":      {func(r *CreateRequest, _ *testEnv) { r.Env = []EnvVar{{Name: "LD_PRELOAD", Value: "x"}} }, CodeReservedEnv},
		"services network":  {func(r *CreateRequest, _ *testEnv) { r.Networks = []string{"services"} }, CodeInvalidNetwork},
		"unknown network":   {func(r *CreateRequest, _ *testEnv) { r.Networks = []string{"nope"} }, CodeInvalidNetwork},
		"no runtime binary": {func(_ *CreateRequest, e *testEnv) { e.runtimes.noBinary = true }, CodeRuntimeUntrusted},
		"budget":            {func(r *CreateRequest, _ *testEnv) { r.MemoryMB, r.CPUs = 3008, 1 }, ""}, // checked below
		"container name taken": {func(_ *CreateRequest, e *testEnv) {
			e.docker.containers["fn-resize"] = container.InspectResponse{ID: "other", Config: &container.Config{}}
		}, CodeNameConflict},
		"project taken": {func(_ *CreateRequest, e *testEnv) {
			e.docker.containers["web"] = container.InspectResponse{ID: "web", Config: &container.Config{Labels: map[string]string{"com.docker.compose.project": "fn-resize"}}}
		}, CodeNameConflict},
		"network taken": {func(_ *CreateRequest, e *testEnv) {
			e.nets.refs["fn-resize"] = e.nets.refs["images"]
		}, CodeNameConflict},
		"stacks directory taken": {func(_ *CreateRequest, e *testEnv) {
			mustMkdir(t, filepath.Join(e.m.cfg.StacksDir, "fn-resize"), 0o755)
		}, CodeNameConflict},
		"pull fails": {func(_ *CreateRequest, e *testEnv) { e.docker.pullFailures = imageAttempts }, CodeImagePullFailed},
	}
	for name, tc := range cases {
		if tc.code == "" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			e := newTestEnv(t)
			req := base()
			tc.mutate(&req, e)
			_, err := e.m.Create(bg, req, actor)
			wantCode(t, err, tc.code)
			// Nothing left behind.
			if _, err := os.Lstat(e.m.paths.Private(nm("resize"))); !errors.Is(err, os.ErrNotExist) && name != "bad name" && name != "reserved name" {
				t.Fatal("private directory left behind")
			}
			if tc.code != CodeNameConflict && slices.Contains(e.nets.created, "fn-resize") && !slices.Contains(e.nets.deleted, "fn-resize") {
				t.Fatal("network left behind")
			}
		})
	}
}

func TestCreateRetriesThePull(t *testing.T) {
	e := newTestEnv(t)
	e.digests.failures = 2
	e.docker.pullFailures = 2
	e.create(t, "resize")
	if e.docker.pulls != 3 {
		t.Fatalf("pulls = %d", e.docker.pulls)
	}
}

func TestQuotas(t *testing.T) {
	e := newTestEnv(t, func(c *ManagerConfig) { c.Max = 2; c.MaxTotalMemoryMB = 1500; c.MaxTotalCPUs = 2 })
	e.create(t, "one")
	// Budget: 512 + 1024 > 1500.
	_, err := e.m.Create(bg, CreateRequest{Name: "two", Runtime: "nodejs", RuntimeVersion: "22", Handler: "index.handler", MemoryMB: 1024}, actor)
	wantCode(t, err, CodeBudget)
	_, err = e.m.Create(bg, CreateRequest{Name: "two", Runtime: "nodejs", RuntimeVersion: "22", Handler: "index.handler", CPUs: 1.5}, actor)
	wantCode(t, err, CodeBudget)
	e.create(t, "two")
	// Count.
	_, err = e.m.Create(bg, CreateRequest{Name: "three", Runtime: "nodejs", RuntimeVersion: "22", Handler: "index.handler", MemoryMB: 128, CPUs: 0.1}, actor)
	wantCode(t, err, CodeLimit)
	// Update within the budget of the others.
	mem := 1024
	_, err = e.m.Update(bg, "one", UpdateRequest{MemoryMB: &mem}, actor)
	wantCode(t, err, CodeBudget)
}

func TestDeployActivateRollbackAndPrune(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	n := nm("resize")
	d1 := e.deploy(t, "resize", "exports.handler = async () => 1")
	if e.runner.ups() != 1 {
		t.Fatalf("ups = %d", e.runner.ups())
	}
	got, err := e.m.Get(bg, "resize")
	if err != nil || got.State != StateRunning || got.Deployment != d1.ID {
		t.Fatalf("get: %+v %v", got, err)
	}
	ref, _ := e.m.load(n)
	if !ComposeIntact(ref, e.m.renderConfig(ref)) {
		t.Fatal("compose must be the rendered one")
	}
	d2 := e.deploy(t, "resize", "exports.handler = async () => 2")
	d3 := e.deploy(t, "resize", "exports.handler = async () => 3")
	list, _ := e.m.Deployments(bg, "resize")
	if len(list) != 3 || list[0].ID != d3.ID || !list[0].Active || list[2].ID != d1.ID {
		t.Fatalf("deployments: %+v", list)
	}
	// Rollback to d1.
	if err := e.m.Activate(bg, "resize", d1.ID, actor); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.audit.String(), "function deployment rolled back") {
		t.Fatal("rollback not audited")
	}
	ref, _ = e.m.load(n)
	if ref.Deployment.ID != d1.ID || len(ref.Kept) != 2 {
		t.Fatalf("after rollback: %+v", ref)
	}
	wantCode(t, e.m.Activate(bg, "resize", d1.ID, actor), CodeDeploymentActive)
	wantCode(t, e.m.Activate(bg, "resize", "20990101T000000Z-00000000", actor), CodeDeploymentNotFound)
	// A fourth deployment prunes the oldest kept (d2), never the active.
	d4 := e.deploy(t, "resize", "exports.handler = async () => 4")
	ref, _ = e.m.load(n)
	ids := []string{ref.Deployment.ID}
	for _, d := range ref.Kept {
		ids = append(ids, d.ID)
	}
	if len(ids) != 3 || ids[0] != d4.ID || slices.Contains(ids, d2.ID) {
		t.Fatalf("kept: %v (d2 %s)", ids, d2.ID)
	}
	if _, err := os.Lstat(e.m.paths.Deployment(n, d2.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pruned deployment still on disk")
	}
	// A kept deployment modified on disk is never activated.
	dir := e.m.paths.Deployment(n, d3.ID)
	mustChmod(t, dir, 0o755)
	mustWriteFile(t, filepath.Join(dir, "evil.js"), []byte("x"), 0o444)
	mustChmod(t, dir, 0o555)
	wantCode(t, e.m.Activate(bg, "resize", d3.ID, actor), CodeArtifactModified)
	if !strings.Contains(e.audit.String(), "function verification refused") {
		t.Fatal("refusal not audited")
	}
}

func TestComposeModifiedIsNeverRun(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	e.deploy(t, "resize", "exports.handler = async () => 1")
	n := nm("resize")
	compose := filepath.Join(e.m.paths.ProjectDir(n), ComposeFile)
	mustWriteFile(t, compose, []byte("services: {evil: {image: alpine, privileged: true}}\n"), 0o640)
	ups := e.runner.ups()

	mem := 1024
	_, err := e.m.Update(bg, "resize", UpdateRequest{MemoryMB: &mem}, actor)
	wantCode(t, err, CodeModified)
	wantCode(t, e.m.Action(bg, "resize", ActionRestart, actor), CodeModified)
	_, err = e.m.Deploy(bg, "resize", bytes.NewReader(codeZip(t, "x")), actor)
	wantCode(t, err, CodeModified)
	_, err = e.m.Invoke(bg, "resize", bytes.NewReader([]byte(`{}`)), actor)
	wantCode(t, err, CodeModified)
	wantCode(t, e.m.SetSecret(bg, "resize", "API_KEY", "v", actor), CodeModified)
	_, err = e.m.RotateCredentials(bg, "resize", nil, actor)
	wantCode(t, err, CodeModified)
	if e.runner.ups() != ups {
		t.Fatal("compose ran on a modified function")
	}
	if got, _ := e.m.Get(bg, "resize"); got.State != StateModified {
		t.Fatalf("state %s", got.State)
	}
	// Stopping still works (Docker API, never compose).
	if err := e.m.Action(bg, "resize", ActionStop, actor); err != nil {
		t.Fatal(err)
	}
	// Delete refuses, purge removes everything without running the file.
	_, err = e.m.Delete(bg, "resize", actor)
	wantCode(t, err, CodeModified)
	res, err := e.m.Purge(bg, "resize", actor)
	if err != nil || res.Containers != 1 {
		t.Fatalf("purge: %+v %v", res, err)
	}
	for _, p := range []string{e.m.paths.ProjectDir(n), e.m.paths.Private(n), e.m.paths.FunctionData(n)} {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s left behind", p)
		}
	}
	if !slices.Contains(e.nets.deleted, "fn-resize") || !strings.Contains(e.audit.String(), "function purged") {
		t.Fatal("network not removed or purge not audited")
	}
	if e.runner.ups() != ups {
		t.Fatal("compose ran during the purge")
	}
}

func TestPurgeRefusesIntactAndDeleteRemoves(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	e.deploy(t, "resize", "exports.handler = async () => 1")
	_, err := e.m.Purge(bg, "resize", actor)
	wantCode(t, err, CodeNotModified)
	e.nets.inUse["fn-resize"] = true // a caller is still attached
	res, err := e.m.Delete(bg, "resize", actor)
	if err != nil || !res.NetworkKept || res.Containers != 1 {
		t.Fatalf("delete: %+v %v", res, err)
	}
	if _, err := e.m.Get(bg, "resize"); CodeOf(err) != CodeNotFound {
		t.Fatalf("still found: %v", err)
	}
	_, err = e.m.Delete(bg, "resize", actor)
	wantCode(t, err, CodeNotFound)
}

func TestArtifactsAreReverifiedBeforeEveryUp(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	e.deploy(t, "resize", "exports.handler = async () => 1")
	e.runtimes.verifyErr = untrusted("the runtime binary does not have its trusted sha256")
	ups := e.runner.ups()
	wantCode(t, e.m.Action(bg, "resize", ActionRestart, actor), CodeRuntimeUntrusted)
	_, err := e.m.Deploy(bg, "resize", bytes.NewReader(codeZip(t, "x")), actor)
	wantCode(t, err, CodeRuntimeUntrusted)
	if e.runner.ups() != ups {
		t.Fatal("compose ran with an unverified runtime")
	}
	// The refused deployment is removed.
	ref, _ := e.m.load(nm("resize"))
	entries, _ := os.ReadDir(e.m.paths.Deployments(nm("resize")))
	if len(entries) != 1 || entries[0].Name() != ref.Deployment.ID {
		t.Fatalf("deployments on disk: %v", entries)
	}
	// The active deployment modified on disk: refused too.
	e.runtimes.verifyErr = nil
	dir := e.m.paths.Deployment(nm("resize"), ref.Deployment.ID)
	mustChmod(t, filepath.Join(dir, "index.js"), 0o644)
	wantCode(t, e.m.Action(bg, "resize", ActionStart, actor), CodeArtifactModified)
}

func TestFailedUpRestoresThePreviousConfiguration(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	e.deploy(t, "resize", "exports.handler = async () => 1")
	n := nm("resize")
	before, _ := os.ReadFile(filepath.Join(e.m.paths.Private(n), ReferenceFile))
	composeBefore, _ := os.ReadFile(filepath.Join(e.m.paths.ProjectDir(n), ComposeFile))
	e.runner.failUps = 1
	mem := 1024
	_, err := e.m.Update(bg, "resize", UpdateRequest{MemoryMB: &mem}, actor)
	wantCode(t, err, CodeStartFailed)
	after, _ := os.ReadFile(filepath.Join(e.m.paths.Private(n), ReferenceFile))
	composeAfter, _ := os.ReadFile(filepath.Join(e.m.paths.ProjectDir(n), ComposeFile))
	if !bytes.Equal(before, after) || !bytes.Equal(composeBefore, composeAfter) {
		t.Fatal("files not restored")
	}
	if last := e.runner.last(); !slices.Contains(last, "--force-recreate") {
		t.Fatalf("previous configuration not started again: %v", last)
	}
	// A first deployment that does not start leaves nothing.
	e.create(t, "report")
	e.runner.failUps = 1
	_, err = e.m.Deploy(bg, "report", bytes.NewReader(codeZip(t, "x")), actor)
	wantCode(t, err, CodeStartFailed)
	if _, err := os.Lstat(e.m.paths.ProjectDir(nm("report"))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("compose project of a failed first deployment left behind")
	}
	if ref, _ := e.m.load(nm("report")); ref.Deployment != nil {
		t.Fatal("failed deployment recorded")
	}
}

func TestContainerNotMatchingIsRolledBack(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	e.runner.tamper = func(i *container.InspectResponse) { i.HostConfig.Privileged = true }
	_, err := e.m.Deploy(bg, "resize", bytes.NewReader(codeZip(t, "x")), actor)
	wantCode(t, err, CodeVerificationFailed)
	if !strings.Contains(e.audit.String(), "function verification refused") {
		t.Fatal("not audited")
	}
}

func TestSecretsNeverLeak(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	e.deploy(t, "resize", "exports.handler = async () => 1")
	const value = "s3cr3t-value-never-shown"
	if err := e.m.SetSecret(bg, "resize", "AWS_SECRET_ACCESS_KEY", value, actor); err != nil {
		t.Fatal(err)
	}
	if err := e.m.SetSecret(bg, "resize", "API_TOKEN", "other-"+value, actor); err != nil {
		t.Fatal(err)
	}
	n := nm("resize")
	data, _ := os.ReadFile(e.m.paths.SecretsEnv(n))
	if string(data) != "API_TOKEN=other-"+value+"\nAWS_SECRET_ACCESS_KEY="+value+"\n" {
		t.Fatalf("secrets env_file: %q", data)
	}
	if fi, _ := os.Stat(e.m.paths.SecretsEnv(n)); fi.Mode().Perm() != 0o400 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if !slices.Contains(e.runner.last(), "--force-recreate") {
		t.Fatal("secrets are applied by recreating the container")
	}
	d, _ := e.m.Get(bg, "resize")
	out, _ := json.Marshal(d)
	list, _ := e.m.List(bg)
	outList, _ := json.Marshal(list)
	compose, _ := os.ReadFile(filepath.Join(e.m.paths.ProjectDir(n), ComposeFile))
	ref, _ := os.ReadFile(filepath.Join(e.m.paths.Private(n), ReferenceFile))
	assertNoLeak(t, e, string(out)+string(outList)+string(compose)+string(ref)+e.audit.String()+e.logs.String(), value)
	if !slices.Equal(d.Secrets, []string{"API_TOKEN", "AWS_SECRET_ACCESS_KEY"}) {
		t.Fatalf("secret names: %v", d.Secrets)
	}
	// Invalid values and names: the value is never echoed in the error.
	err := e.m.SetSecret(bg, "resize", "API_TOKEN", "line\nbreak-"+value, actor)
	wantCode(t, err, CodeInvalidSecret)
	assertNoLeak(t, e, err.Error(), value)
	wantCode(t, e.m.SetSecret(bg, "resize", "AWS_LAMBDA_FUNCTION_NAME", "x", actor), CodeReservedEnv)
	wantCode(t, e.m.SetSecret(bg, "resize", "LOCH_RUNTIME_TOKEN", "x", actor), CodeReservedEnv)
	wantCode(t, e.m.SetSecret(bg, "resize", "OUTPUT_BUCKET", "x", actor), CodeInvalidEnv) // already a variable
	// Removal.
	if err := e.m.DeleteSecret(bg, "resize", "API_TOKEN", actor); err != nil {
		t.Fatal(err)
	}
	wantCode(t, e.m.DeleteSecret(bg, "resize", "API_TOKEN", actor), CodeSecretNotFound)
	data, _ = os.ReadFile(e.m.paths.SecretsEnv(n))
	if string(data) != "AWS_SECRET_ACCESS_KEY="+value+"\n" {
		t.Fatalf("after removal: %q", data)
	}
	if !strings.Contains(e.audit.String(), `"secret":"API_TOKEN"`) {
		t.Fatal("secret change not audited by name")
	}
}

func TestRotateCredentials(t *testing.T) {
	e := newTestEnv(t)
	old := e.create(t, "resize").Credentials
	e.deploy(t, "resize", "exports.handler = async () => 1")
	data0, _ := os.ReadFile(e.m.paths.CredentialsEnv(nm("resize")))
	on := true
	c, err := e.m.RotateCredentials(bg, "resize", &on, actor)
	if err != nil {
		t.Fatal(err)
	}
	if c.Token == old.Token || !regexp.MustCompile(`\A[A-Z0-9]{20}\z`).MatchString(c.AccessKeyID) ||
		!regexp.MustCompile(`\A[A-Za-z0-9_-]{40}\z`).MatchString(c.SecretAccessKey) || strings.Contains(c.AccessKeyID, "RESIZE") {
		t.Fatalf("credentials not rotated or of an unexpected form: %q %d", c.AccessKeyID, len(c.SecretAccessKey))
	}
	data, _ := os.ReadFile(e.m.paths.CredentialsEnv(nm("resize")))
	sum := sha256.Sum256([]byte(c.Token))
	if string(data) != "LOCH_RUNTIME_ACCESS_KEY="+c.AccessKeyID+":"+c.SecretAccessKey+"\nLOCH_RUNTIME_TOKEN_SHA256="+hex.EncodeToString(sum[:])+"\n" ||
		bytes.Equal(data, data0) {
		t.Fatalf("credentials env_file not rewritten: %q", data)
	}
	if d, _ := e.m.Get(bg, "resize"); !d.SigV4 || d.State != StateRunning {
		t.Fatalf("SigV4 enabled and verified: %+v", d)
	}
	// Rotating keeps the SigV4 setting; disabling removes the pair.
	if c2, _ := e.m.RotateCredentials(bg, "resize", nil, actor); c2.AccessKeyID == "" {
		t.Fatal("SigV4 kept")
	}
	off := false
	if c3, _ := e.m.RotateCredentials(bg, "resize", &off, actor); c3.AccessKeyID != "" {
		t.Fatal("SigV4 disabled")
	}
	if data, _ := os.ReadFile(e.m.paths.CredentialsEnv(nm("resize"))); strings.Contains(string(data), "ACCESS_KEY") {
		t.Fatal("key pair left in the container")
	}
	if !slices.Contains(e.runner.last(), "--force-recreate") {
		t.Fatal("the runtime reads its credentials at start: recreate")
	}
	assertNoLeak(t, e, e.audit.String()+e.logs.String(), c.Token, c.AccessKeyID, c.SecretAccessKey)
	if !strings.Contains(e.audit.String(), "function credentials rotated") {
		t.Fatal("not audited")
	}
	// The value is never in a formatted error or log line.
	if s := c.String(); strings.Contains(s, c.Token) {
		t.Fatal("Credentials.String leaks")
	}
}

func TestLocks(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	unlock, err := e.m.lock(bg, nm("resize"))
	if err != nil {
		t.Fatal(err)
	}
	mem := 256
	_, err = e.m.Update(bg, "resize", UpdateRequest{MemoryMB: &mem}, actor)
	wantCode(t, err, CodeInProgress)
	unlock()
	if _, err := e.m.Update(bg, "resize", UpdateRequest{MemoryMB: &mem}, actor); err != nil {
		t.Fatal(err)
	}
	// Another function is not blocked.
	unlock, _ = e.m.lock(bg, nm("resize"))
	defer unlock()
	e.create(t, "report")
}

func TestInvoke(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	_, err := e.m.Invoke(bg, "resize", bytes.NewReader([]byte(`{}`)), actor)
	wantCode(t, err, CodeNotDeployed)
	e.deploy(t, "resize", "exports.handler = async () => 1")
	res, err := e.m.Invoke(bg, "resize", bytes.NewReader([]byte(`{"Records":[]}`)), actor)
	if err != nil || res.StatusCode != 200 || string(res.Payload) != `{"ok":true}` {
		t.Fatalf("invoke: %+v %v", res, err)
	}
	if string(e.invoker.events[0]) != "{\"Records\":[]}\n" {
		t.Fatal("event not passed")
	}
	_, err = e.m.Invoke(bg, "resize", bytes.NewReader(make([]byte, MaxEventBytes+1)), actor)
	wantCode(t, err, CodeEventTooLarge)
	// Aggregated audit: one entry for the minute, never the content.
	e.invoker.res = InvokeResult{StatusCode: 200, FunctionError: "Unhandled", Payload: []byte(`{"errorType":"Boom"}`)}
	if _, err := e.m.Invoke(bg, "resize", bytes.NewReader([]byte(`{"secret-event":1}`)), actor); err != nil {
		t.Fatal(err)
	}
	e.invoker.err = ErrInvokeTimeout
	_, err = e.m.Invoke(bg, "resize", bytes.NewReader([]byte(`{}`)), actor)
	wantCode(t, err, CodeInvokeTimeout)
	e.invoker.err = nil
	if strings.Contains(e.audit.String(), "function invoked") {
		t.Fatal("invocations must be aggregated")
	}
	e.m.FlushAudit()
	a := e.audit.String()
	if strings.Count(a, "function invoked") != 1 || !strings.Contains(a, `"count":3`) || !strings.Contains(a, `"errors":1`) ||
		!strings.Contains(a, `"failures":1`) || strings.Contains(a, "secret-event") || strings.Contains(a, "Boom") {
		t.Fatalf("aggregated audit: %s", a)
	}
	// A container that is not the reference's is never invoked.
	e.docker.mu.Lock()
	c := e.docker.containers["fn-resize"]
	c.HostConfig.CapAdd = []string{"SYS_ADMIN"}
	e.docker.containers["fn-resize"] = c
	e.docker.mu.Unlock()
	calls := len(e.invoker.events)
	_, err = e.m.Invoke(bg, "resize", bytes.NewReader([]byte(`{}`)), actor)
	wantCode(t, err, CodeVerificationFailed)
	if len(e.invoker.events) != calls {
		t.Fatal("an unverified container was invoked")
	}
	// Stopped.
	if err := e.m.Action(bg, "resize", ActionStop, actor); err != nil {
		t.Fatal(err)
	}
	_, err = e.m.Invoke(bg, "resize", bytes.NewReader([]byte(`{}`)), actor)
	wantCode(t, err, CodeNotRunning)
}

func TestInvokeConcurrencyIsBounded(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	e.deploy(t, "resize", "exports.handler = async () => 1")
	e.m.invokeWait = 50 * time.Millisecond
	e.invoker.block = make(chan struct{})
	done := make(chan error, 2)
	for range 2 {
		go func() { _, err := e.m.Invoke(bg, "resize", bytes.NewReader([]byte(`{}`)), actor); done <- err }()
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		e.invoker.mu.Lock()
		n := len(e.invoker.events)
		e.invoker.mu.Unlock()
		if n == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, err := e.m.Invoke(bg, "resize", bytes.NewReader([]byte(`{}`)), actor)
	wantCode(t, err, CodeInvokeBusy)
	close(e.invoker.block)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestNetworksAndEgress(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	e.deploy(t, "resize", "exports.handler = async () => 1")
	_, err := e.m.SetNetworks(bg, "resize", []string{"services"}, actor)
	wantCode(t, err, CodeInvalidNetwork)
	_, err = e.m.SetNetworks(bg, "resize", nil, actor)
	wantCode(t, err, CodeInvalidNetwork)
	nets, err := e.m.SetNetworks(bg, "resize", []string{"images", "fn-resize"}, actor)
	if err != nil || !slices.Equal(nets, []string{"fn-resize", "images"}) {
		t.Fatalf("%v %v", nets, err)
	}
	if !strings.Contains(e.audit.String(), `"msg":"function network attached","user":"admin@example.com","function":"resize","network":"images"`) {
		t.Fatalf("attach not audited: %s", e.audit.String())
	}
	// Detach: the container is cut first.
	if _, err := e.m.SetNetworks(bg, "resize", []string{"images"}, actor); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(e.docker.disconnected, "net-id-fn-resize") {
		t.Fatalf("not disconnected first: %v", e.docker.disconnected)
	}
	// Members for the networks module.
	members, _ := NetworkSource{M: e.m}.Members(bg)
	if len(members) != 1 || members[0].Container != "fn-resize" || !slices.Equal(members[0].Networks, []string{"images"}) {
		t.Fatalf("members: %+v", members)
	}
	// A failed recreation after a detach stops the function (never reconnects).
	e.runner.failUps = 1
	_, err = e.m.SetNetworks(bg, "resize", []string{"fn-resize"}, actor)
	wantCode(t, err, CodeStartFailed)
	if ref, _ := e.m.load(nm("resize")); !ref.Stopped || !slices.Equal(ref.Networks, []string{"images"}) {
		t.Fatalf("after a failed detach: %+v", ref)
	}
	// Egress.
	if err := e.m.Action(bg, "resize", ActionStart, actor); err != nil {
		t.Fatal(err)
	}
	if err := e.m.SetEgress(bg, "resize", true, actor); err != nil {
		t.Fatal(err)
	}
	if d, _ := e.m.Get(bg, "resize"); d.State != StateRunning || !d.Egress {
		t.Fatalf("egress: %+v", d)
	}
	if err := e.m.SetEgress(bg, "resize", false, actor); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(e.docker.removedNets, "fn-resize_fn_egress") || strings.Count(e.audit.String(), "function egress toggled") != 2 {
		t.Fatal("egress network not removed or toggle not audited")
	}
}

func TestStopStartAndStoppedUpdates(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	wantCode(t, e.m.Action(bg, "resize", ActionStart, actor), CodeNotDeployed)
	wantCode(t, e.m.Action(bg, "resize", "kill", actor), CodeInvalidAction)
	e.deploy(t, "resize", "exports.handler = async () => 1")
	if err := e.m.Action(bg, "resize", ActionStop, actor); err != nil {
		t.Fatal(err)
	}
	ups := e.runner.ups()
	mem := 1024
	if _, err := e.m.Update(bg, "resize", UpdateRequest{MemoryMB: &mem}, actor); err != nil {
		t.Fatal(err)
	}
	if e.runner.ups() != ups {
		t.Fatal("a stopped function is not started by a configuration change")
	}
	ref, _ := e.m.load(nm("resize"))
	if !ComposeIntact(ref, e.m.renderConfig(ref)) {
		t.Fatal("the compose file follows the reference while stopped")
	}
	if err := e.m.Action(bg, "resize", ActionStart, actor); err != nil {
		t.Fatal(err)
	}
	if d, _ := e.m.Get(bg, "resize"); d.State != StateRunning || d.MemoryMB != 1024 {
		t.Fatalf("%+v", d)
	}
}

func TestStartupSweepsAndReportsModified(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	e.deploy(t, "resize", "exports.handler = async () => 1")
	leftover := filepath.Join(e.m.paths.Uploads(nm("resize")), "upload-123.zip")
	mustWriteFile(t, leftover, []byte("x"), 0o600)
	mustWriteFile(t, filepath.Join(e.m.paths.ProjectDir(nm("resize")), ComposeFile), []byte("x"), 0o640)
	e.m.Startup(bg)
	if _, err := os.Lstat(leftover); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("leftover kept")
	}
	if !strings.Contains(e.logs.String(), "modified outside the panel") {
		t.Fatal("modified function not reported")
	}
}

func TestListAndInfo(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	e.create(t, "report")
	e.deploy(t, "resize", "exports.handler = async () => 1")
	list, err := e.m.List(bg)
	if err != nil || len(list) != 2 || list[0].Name != "report" || list[0].State != StateNotDeployed ||
		list[1].State != StateRunning || list[1].Health != "healthy" {
		t.Fatalf("list: %+v %v", list, err)
	}
	info := e.m.Info()
	if info.Count != 2 || info.UsedMemoryMB != 1024 || !info.RuntimeReady || info.Arch != "arm64" || len(info.Runtimes) != 3 {
		t.Fatalf("info: %+v", info)
	}
	// A broken reference is listed (purge only).
	mustWriteFile(t, filepath.Join(e.m.paths.Private(nm("report")), ReferenceFile), []byte("{"), 0o600)
	list, _ = e.m.List(bg)
	if list[0].State != StateInvalid {
		t.Fatalf("%+v", list[0])
	}
	_, err = e.m.Delete(bg, "report", actor)
	wantCode(t, err, CodeModified)
	if _, err := e.m.Purge(bg, "report", actor); err != nil {
		t.Fatal(err)
	}
}

func TestDockerLabelMatches(t *testing.T) {
	if LabelFunction != docker.FunctionLabel || ProjectPrefix != docker.FunctionNamePrefix {
		t.Fatal("the Docker layer protects function containers by the module's label and prefix")
	}
}

// The compose bytes run are the checked ones (stdin), and the project
// must hold exactly the function's container.
func TestComposeRunsTheCheckedBytes(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	e.deploy(t, "resize", "exports.handler = async () => 1")
	onDisk, _ := os.ReadFile(filepath.Join(e.m.paths.ProjectDir(nm("resize")), ComposeFile))
	if got := e.runner.composes[len(e.runner.composes)-1]; !bytes.Equal(got, onDisk) {
		t.Fatal("compose must run the rendered bytes")
	}
	// Another container in the project (started by hand): refused, rolled back.
	e.runner.tamper = func(*container.InspectResponse) {
		e.docker.mu.Lock()
		e.docker.containers["intruder"] = container.InspectResponse{ID: "intruder",
			Config: &container.Config{Labels: map[string]string{"com.docker.compose.project": "fn-resize"}}}
		e.docker.mu.Unlock()
	}
	mem := 256
	_, err := e.m.Update(bg, "resize", UpdateRequest{MemoryMB: &mem}, actor)
	wantCode(t, err, CodeVerificationFailed)
	if !strings.Contains(e.audit.String(), "other containers run in the function") {
		t.Fatalf("not audited: %s", e.audit.String())
	}
}

func TestBudgetNeverBlocksALowering(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	e.m.cfg.MaxTotalMemoryMB = 128 // lowered below what runs
	mem := 256
	if _, err := e.m.Update(bg, "resize", UpdateRequest{MemoryMB: &mem}, actor); err != nil {
		t.Fatal(err)
	}
	mem = 384
	_, err := e.m.Update(bg, "resize", UpdateRequest{MemoryMB: &mem}, actor)
	wantCode(t, err, CodeBudget)
}

// countingReader records whether the body was read.
type countingReader struct{ reads int }

func (c *countingReader) Read([]byte) (int, error) { c.reads++; return 0, io.EOF }

func TestInvokeChecksBeforeReadingAndKeepsSlots(t *testing.T) {
	e := newTestEnv(t)
	body := &countingReader{}
	_, err := e.m.Invoke(bg, "nope", body, actor)
	wantCode(t, err, CodeNotFound)
	e.create(t, "resize")
	_, err = e.m.Invoke(bg, "resize", body, actor)
	wantCode(t, err, CodeNotDeployed)
	if body.reads != 0 || len(e.m.invokeSlots) != 0 {
		t.Fatalf("body read %d times, slots %v", body.reads, e.m.invokeSlots)
	}
	e.deploy(t, "resize", "exports.handler = async () => 1")
	for _, ev := range []string{"", "not json", `{"a":1} {"b":2}`} {
		_, err = e.m.Invoke(bg, "resize", strings.NewReader(ev), actor)
		wantCode(t, err, CodeInvalidEvent)
	}
	// Into the verified container ID.
	if _, err := e.m.Invoke(bg, "resize", strings.NewReader(`{}`), actor); err != nil {
		t.Fatal(err)
	}
	if e.invoker.ids[0] != "container-resize" {
		t.Fatalf("invoked %v", e.invoker.ids)
	}
	// A cut exec keeps its slot until the runtime process is gone.
	slotReleaseDelay = 200 * time.Millisecond
	defer func() { slotReleaseDelay = time.Second }()
	e.m.cfg.InvokeConcurrency = 1
	e.m.forgetSlots(nm("resize"))
	e.m.invokeWait = 10 * time.Millisecond
	e.invoker.err = ErrInvokeTransport
	_, _ = e.m.Invoke(bg, "resize", strings.NewReader(`{}`), actor) // fails by design: the cut exec keeps its slot
	e.invoker.err = nil
	_, err = e.m.Invoke(bg, "resize", strings.NewReader(`{}`), actor)
	wantCode(t, err, CodeInvokeBusy)
	time.Sleep(300 * time.Millisecond)
	if _, err := e.m.Invoke(bg, "resize", strings.NewReader(`{}`), actor); err != nil {
		t.Fatal(err)
	}
	// Deleting forgets the slots.
	if _, err := e.m.Delete(bg, "resize", actor); err != nil {
		t.Fatal(err)
	}
	if len(e.m.invokeSlots) != 0 {
		t.Fatal("slots kept after deletion")
	}
}

// Every JSON type is accepted and ends with a newline (the runtime's
// decoder cannot finish a top-level scalar otherwise: stdin stays open).
func TestInvokeEventTypes(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	e.deploy(t, "resize", "exports.handler = async () => 1")
	for _, ev := range []string{`{}`, `[1,2]`, `42`, `-1.5e3`, `"hi"`, `true`, `false`, `null`, " {\"a\":1}\n"} {
		if _, err := e.m.Invoke(bg, "resize", strings.NewReader(ev), actor); err != nil {
			t.Fatalf("%q: %v", ev, err)
		}
		got := e.invoker.events[len(e.invoker.events)-1]
		if string(got) != ev+"\n" {
			t.Fatalf("%q sent as %q", ev, got)
		}
	}
}

func TestInvocationsAfterTheFinalFlushAreAudited(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	e.deploy(t, "resize", "exports.handler = async () => 1")
	if _, err := e.m.Invoke(bg, "resize", strings.NewReader(`{}`), actor); err != nil {
		t.Fatal(err)
	}
	e.m.CloseAudit()
	if _, err := e.m.Invoke(bg, "resize", strings.NewReader(`{}`), actor); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(e.audit.String(), "function invoked"); n != 2 {
		t.Fatalf("%d entries: %s", n, e.audit.String())
	}
}

type deadlineReader struct{}

func (deadlineReader) Read([]byte) (int, error) { return 0, os.ErrDeadlineExceeded }

func TestInvokeEventDeadline(t *testing.T) {
	e := newTestEnv(t)
	e.create(t, "resize")
	e.deploy(t, "resize", "exports.handler = async () => 1")
	_, err := e.m.Invoke(bg, "resize", deadlineReader{}, actor)
	wantCode(t, err, CodeEventTimeout)
}

func TestExistsAndOnDelete(t *testing.T) {
	e := newTestEnv(t)
	var deleted []string
	e.m.onDelete = func(name, a string) { deleted = append(deleted, name+"/"+a) }
	wantCode(t, e.m.Exists("Bad_Name"), CodeInvalidName)
	wantCode(t, e.m.Exists("resize"), CodeNotFound)
	e.create(t, "resize")
	if err := e.m.Exists("resize"); err != nil {
		t.Fatalf("exists: %v", err)
	}
	// A refused deletion calls no hook.
	_, err := e.m.Purge(bg, "resize", actor)
	wantCode(t, err, CodeNotModified)
	if len(deleted) != 0 {
		t.Fatalf("hook called on a refused purge: %v", deleted)
	}
	if _, err := e.m.Delete(bg, "resize", actor); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0] != "resize/"+actor {
		t.Fatalf("hook calls: %v", deleted)
	}
	wantCode(t, e.m.Exists("resize"), CodeNotFound)
}

func TestMCPInvocableFlag(t *testing.T) {
	e := newTestEnv(t)
	c, err := e.m.Create(bg, CreateRequest{Name: "resize", Runtime: "nodejs", RuntimeVersion: "22", Handler: "index.handler", MCPInvocable: true}, actor)
	if err != nil || !c.Function.MCPInvocable {
		t.Fatalf("create: %+v %v", c, err)
	}
	e.create(t, "plain")
	list, _ := e.m.List(bg)
	got := map[string]bool{}
	for _, s := range list {
		got[s.Name] = s.MCPInvocable
	}
	if !got["resize"] || got["plain"] {
		t.Fatalf("list: %v", got)
	}

	e.deploy(t, "resize", "exports.handler = async () => 1")
	ups := e.runner.ups()
	off := false
	d, err := e.m.Update(bg, "resize", UpdateRequest{MCPInvocable: &off}, actor)
	if err != nil || d.MCPInvocable {
		t.Fatalf("update: %+v %v", d, err)
	}
	if e.runner.ups() != ups {
		t.Fatal("the flag alone recreated the container")
	}
	if ref, _ := e.m.load(nm("resize")); ref.MCPInvocable {
		t.Fatal("not written to the reference")
	}
	if g, _ := e.m.Get(bg, "resize"); g.MCPInvocable || g.State != StateRunning {
		t.Fatalf("get: mcp=%v state=%s", g.MCPInvocable, g.State)
	}
	if !strings.Contains(e.audit.String(), `"fields":"mcpInvocable"`) || !strings.Contains(e.audit.String(), `"mcp_invocable":false`) {
		t.Fatalf("audit: %s", e.audit.String())
	}
	// With another field: applied the usual way.
	on, mem := true, 256
	if d, err = e.m.Update(bg, "resize", UpdateRequest{MCPInvocable: &on, MemoryMB: &mem}, actor); err != nil || !d.MCPInvocable || d.MemoryMB != 256 {
		t.Fatalf("update both: %+v %v", d, err)
	}
	// Refused on a function modified outside the panel.
	mustWriteFile(t, filepath.Join(e.m.paths.ProjectDir(nm("resize")), ComposeFile), []byte("services: {}\n"), 0o640)
	_, err = e.m.Update(bg, "resize", UpdateRequest{MCPInvocable: &off}, actor)
	wantCode(t, err, CodeModified)
}
