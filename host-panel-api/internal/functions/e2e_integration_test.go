//go:build integration

package functions

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"host-panel-api/internal/composekit"
	"host-panel-api/internal/docker"
	"host-panel-api/internal/networks"
	"host-panel-api/internal/releases"
)

// End-to-end tests of the functions module against a real Docker daemon,
// the real loch-lambda-runtime binary and the official AWS Lambda base
// images (pulled by digest through mirror.gcr.io). A real Manager is wired
// with the real Docker client, the compose runner and a networks manager
// whose registry lives in a temporary directory. Binaries of the newest
// release of the embedded trust table (LOCH_E2E_RUNTIME_DIR) are installed
// from that directory like FUNCTIONS_RUNTIME_SOURCE_DIR; any other build
// gets a test trust table holding its sha256.
//
// Run:
//
//	LOCH_E2E_RUNTIME_DIR=<dir with loch-lambda-runtime-linux-{amd64,arm64}> \
//	  go test -tags integration -run E2E -count=1 -timeout 40m -v ./internal/functions/
//
// LOCH_E2E_RUNTIME_BINARY can point at the binary of the host architecture
// instead. Everything created is named fn-e2e* / loch-net-fn-e2e* / e2efn-*
// and removed at the end.

const e2eRuntimeVersion = "v0.0.0"

// e2eBuffer is a concurrency-safe audit sink.
type e2eBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *e2eBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *e2eBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type e2e struct {
	t     *testing.T
	m     *Manager
	mcfg  ManagerConfig
	deps  Deps
	dc    *docker.Client
	nets  *networks.Manager
	store *RuntimeStore
	paths Paths
	arch  string
	base  string
	audit *e2eBuffer
}

func e2eRuntimeBinary(t *testing.T, arch string) string {
	if p := os.Getenv("LOCH_E2E_RUNTIME_BINARY"); p != "" {
		return p
	}
	if d := os.Getenv("LOCH_E2E_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, RuntimeBinaryName+"-linux-"+arch)
	}
	t.Skip("set LOCH_E2E_RUNTIME_DIR or LOCH_E2E_RUNTIME_BINARY to a loch-lambda-runtime build")
	return ""
}

func newE2E(t *testing.T) *e2e {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker not available")
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	audit := &e2eBuffer{}
	auditLogger := slog.New(slog.NewJSONHandler(audit, nil))
	dc, err := docker.NewClient(logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dc.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	arch, err := dc.HostArch(ctx)
	if err != nil || !ValidArch(arch) {
		t.Skipf("host architecture %q: %v", arch, err)
	}
	binPath := e2eRuntimeBinary(t, arch)
	bin, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("runtime binary: %v", err)
	}
	sum := sha256.Sum256(bin)

	// Real directories (no symlink on the path: macOS /var -> /private/var),
	// owned by the test process, 0700.
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(tmp, "e2e")
	dataDir, stacksDir, panelData := filepath.Join(base, "functions"), filepath.Join(base, "stacks"), filepath.Join(base, "panel")
	for _, d := range []string{base, dataDir, stacksDir, panelData} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Deployments are read-only: make the tree removable for t.TempDir.
	t.Cleanup(func() { makeWritable(base) })

	cfg := Config{Enabled: true, DataDir: dataDir, StacksDir: stacksDir, Max: 10, MaxMemoryMB: DefaultMaxMemoryMB,
		MaxCodeBytes: DefaultMaxCodeBytes, DeploymentsKept: DefaultDeploymentsKept, TmpSizeMB: 64}
	if _, err := Preflight(ctx, cfg, composekit.ExecRunner{}, dc); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	nets, err := networks.NewManager(networks.Config{File: filepath.Join(panelData, "networks.json"), Max: 50}, dc, logger, auditLogger)
	if err != nil {
		t.Fatal(err)
	}
	// A binary of the newest release of the embedded table is installed the
	// way the panel does (RuntimeProvider, source directory); any other
	// build gets a test table.
	hash := hex.EncodeToString(sum[:])
	trust := TrustTable{Releases: []TrustedRelease{{Version: e2eRuntimeVersion, Arch: arch, SHA256: hash}}}
	embedded, err := EmbeddedTrust()
	if err != nil {
		t.Fatal(err)
	}
	newest, ok := embedded.Newest(arch)
	useEmbedded := ok && newest.SHA256 == hash && os.Getenv("LOCH_E2E_RUNTIME_DIR") != ""
	if useEmbedded {
		trust = embedded
	}
	store := NewRuntimeStore(cfg.Paths(), trust)
	var runtimes RuntimeBinaries = store
	if useEmbedded {
		p := NewRuntimeProvider(store, arch, RuntimeSourceDir{Dir: os.Getenv("LOCH_E2E_RUNTIME_DIR")}, logger, auditLogger)
		p.Backoff = nil
		p.Run(ctx)
		if b, err := p.Latest(arch); err != nil || b.Version != newest.Version {
			t.Fatalf("installing the runtime binary from the source directory: %+v, %v", b, err)
		}
		if !strings.Contains(audit.String(), `"msg":"function runtime installed"`) {
			t.Fatalf("audit = %s", audit)
		}
		t.Logf("runtime binary %s installed with the embedded trust table", newest.Version)
		runtimes = p
	} else if _, err := store.Install(ctx, e2eRuntimeVersion, arch, bytes.NewReader(bin)); err != nil {
		t.Fatalf("installing the runtime binary: %v", err)
	}
	mcfg := ManagerConfig{Config: cfg, Arch: arch, MaxTotalMemoryMB: 8192, MaxTotalCPUs: 8}
	deps := Deps{
		Runner: composekit.ExecRunner{}, Docker: dc, Networks: nets, Runtimes: runtimes,
		Digests: releases.New(), Invoker: ExecInvoker{Docker: dc},
		Logger: logger, Audit: auditLogger,
	}
	m := NewManager(mcfg, deps)
	nets.AddSource(networks.KindFunction, NetworkSource{M: m})
	m.Startup(ctx)
	e := &e2e{t: t, m: m, mcfg: mcfg, deps: deps, dc: dc, nets: nets, store: store, paths: cfg.Paths(), arch: arch, base: base, audit: audit}
	e2eCleanupDocker(t) // leftovers of an interrupted run
	t.Cleanup(func() { e2eCleanupDocker(t) })
	return e
}

func makeWritable(root string) {
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type()&fs.ModeSymlink == 0 {
			if d.IsDir() {
				os.Chmod(p, 0o700)
			} else {
				os.Chmod(p, 0o600)
			}
		}
		return nil
	})
}

// e2eCleanupDocker removes every container and network of the suite (and
// only those: names fn-e2e*, e2efn-*, loch-net-fn-e2e*).
func e2eCleanupDocker(t *testing.T) {
	out, _ := exec.Command("docker", "ps", "-aq", "--filter", "name=^/fn-e2e", "--filter", "name=^/e2efn-").Output()
	ids := strings.Fields(string(out))
	out, _ = exec.Command("docker", "ps", "-aq", "--filter", "label=com.docker.compose.project").Output()
	for _, id := range strings.Fields(string(out)) {
		p, _ := exec.Command("docker", "inspect", "-f", `{{index .Config.Labels "com.docker.compose.project"}}`, id).Output()
		if strings.HasPrefix(strings.TrimSpace(string(p)), "fn-e2e") {
			ids = append(ids, id)
		}
	}
	if len(ids) > 0 {
		exec.Command("docker", append([]string{"rm", "-f"}, ids...)...).Run()
	}
	out, _ = exec.Command("docker", "network", "ls", "--format", "{{.Name}}").Output()
	for _, n := range strings.Fields(string(out)) {
		if strings.HasPrefix(n, "loch-net-fn-e2e") || strings.HasPrefix(n, "fn-e2e") || strings.HasPrefix(n, "e2efn-") {
			if err := exec.Command("docker", "network", "rm", n).Run(); err != nil {
				t.Logf("cleanup: network %s: %v", n, err)
			}
		}
	}
}

// --- helpers ---

func ctxT(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func makeZip(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := slices.Sorted(func(yield func(string) bool) {
		for k := range files {
			if !yield(k) {
				return
			}
		}
	})
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, files[name])
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// goBootstrap builds testdata/gofunc (a copy of the lambda-runtime test
// function, aws-lambda-go) as a static Linux bootstrap and returns it. It
// needs github.com/aws/aws-lambda-go in the module cache, or the network
// to download it (testdata/gofunc/go.sum pins it).
func goBootstrap(t *testing.T, arch string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "bootstrap")
	cmd := exec.Command("go", "build", "-tags", "lambda.norpc", "-trimpath", "-o", out, ".")
	cmd.Dir = filepath.Join("testdata", "gofunc")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch, "GOFLAGS=-mod=mod")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the Go bootstrap: %v\n%s", err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func e2eWantCode(t *testing.T, err error, code string) {
	t.Helper()
	if CodeOf(err) != code {
		t.Fatalf("want %s, got %v (%s)", code, err, CodeOf(err))
	}
}

func (e *e2e) invoke(t *testing.T, name, event string) *InvokeResult {
	t.Helper()
	res, err := e.m.Invoke(ctxT(t, 3*time.Minute), name, strings.NewReader(event), "e2e@test")
	if err != nil {
		t.Fatalf("invoke %s %s: %v (%s)", name, event, err, CodeOf(err))
	}
	return res
}

// invokeOK invokes and decodes a successful JSON payload.
func (e *e2e) invokeOK(t *testing.T, name, event string) map[string]any {
	t.Helper()
	res := e.invoke(t, name, event)
	if res.StatusCode != 200 || res.FunctionError != "" {
		t.Fatalf("invoke %s: status %d, error %q, payload %s, logs %s", name, res.StatusCode, res.FunctionError, res.Payload, res.LogTail)
	}
	var out map[string]any
	if err := json.Unmarshal(res.Payload, &out); err != nil {
		t.Fatalf("payload %q: %v", res.Payload, err)
	}
	return out
}

func (e *e2e) detail(t *testing.T, name string) *Detail {
	t.Helper()
	d, err := e.m.Get(ctxT(t, time.Minute), name)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (e *e2e) wantState(t *testing.T, name, state string) *Detail {
	t.Helper()
	d := e.detail(t, name)
	if d.State != state {
		t.Fatalf("%s: state %s (problem %q), want %s", name, d.State, d.Problem, state)
	}
	return d
}

func (e *e2e) ref(t *testing.T, name string) *Reference {
	t.Helper()
	ref, err := e.m.load(nm(name))
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

// dockerCmd runs the docker CLI; env is added to the environment (secrets
// passed by name with -e NAME, never on the command line).
func dockerCmd(env []string, args ...string) (string, error) {
	cmd := exec.Command("docker", args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

const curlImage = "mirror.gcr.io/curlimages/curl:8.16.0"
const awsCLIImage = "mirror.gcr.io/amazon/aws-cli:2.39.5"

// curlInvoke calls the Invoke endpoint of a function from a container on
// network (a Docker network name) and returns the HTTP status and body.
func curlInvoke(t *testing.T, network, fn, token, body string) (int, string) {
	t.Helper()
	auth := ""
	if token != "" {
		auth = `-H "Authorization: Bearer $TOKEN"`
	}
	script := fmt.Sprintf(`curl -sS -m 30 -o /tmp/body -w '%%{http_code}' %s -H 'Content-Type: application/json' --data-binary "$BODY" http://fn-%s:8080/2015-03-31/functions/%s/invocations; echo; cat /tmp/body`, auth, fn, fn)
	out, err := dockerCmd([]string{"TOKEN=" + token, "BODY=" + body}, "run", "--rm", "--name", "e2efn-curl-"+randHex(4),
		"--network", network, "-e", "TOKEN", "-e", "BODY", "--entrypoint", "sh", curlImage, "-c", script)
	if err != nil {
		t.Logf("curl: %v: %s", err, out)
		return 0, out // no answer (DNS, connection)
	}
	status, rest, _ := strings.Cut(out, "\n")
	var code int
	fmt.Sscanf(strings.TrimSpace(status), "%d", &code)
	return code, rest
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hostHasInternet() bool {
	c, err := net.DialTimeout("tcp", "1.1.1.1:443", 3*time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// --- function code ---

func nodeCode(version string) map[string]string {
	return map[string]string{"index.js": `
const net = require("net");
exports.handler = async (event, context) => {
  const mode = event && typeof event === "object" ? event.mode : undefined;
  if (mode === "error") throw new Error("boom from node");
  if (mode === "sleep") { await new Promise(r => setTimeout(r, event.ms)); return "slept"; }
  if (mode === "oom") { const a = []; for (;;) a.push(Buffer.alloc(16 << 20, 1)); }
  if (mode === "net") {
    return await new Promise(resolve => {
      const s = net.connect({ host: event.host, port: event.port });
      const t = setTimeout(() => { s.destroy(); resolve({ ok: false, error: "timeout" }); }, event.ms || 3000);
      s.on("connect", () => { clearTimeout(t); s.destroy(); resolve({ ok: true }); });
      s.on("error", e => { clearTimeout(t); resolve({ ok: false, error: String(e.code || e) }); });
    });
  }
  if (mode === "call") {
    const r = await fetch(event.url, { method: "POST", headers: { Authorization: "Bearer " + process.env.TARGET_TOKEN },
      body: JSON.stringify({ from: "caller" }), signal: AbortSignal.timeout(10000) });
    return { status: r.status, body: await r.text() };
  }
  if (mode === "write") {
    const fs = require("fs");
    const out = {};
    for (const p of ["/var/task/x", "/x", "/tmp/x"]) {
      try { fs.writeFileSync(p, "x"); out[p] = "ok"; } catch (e) { out[p] = e.code; }
    }
    return out;
  }
  console.log("e2e node log line");
  return { version: "` + version + `", event, greeting: process.env.GREETING ?? null,
    secret: process.env.E2E_SECRET ?? null, requestId: context.awsRequestId, uid: process.getuid(),
    tokenVar: process.env.LOCH_RUNTIME_TOKEN_SHA256 ?? null };
};
`}
}

const pythonCode = `
import os

def handler(event, context):
    if isinstance(event, dict) and event.get("mode") == "error":
        raise ValueError("boom from python")
    if isinstance(event, dict) and event.get("mode") == "oom":
        keep = []
        while True:
            keep.append(b"x" * (16 << 20))
    print("e2e python log line")
    return {"event": event, "greeting": os.environ.get("GREETING"), "secret": os.environ.get("E2E_SECRET"),
            "requestId": context.aws_request_id, "uid": os.getuid()}
`

// --- the suite ---

func TestE2EFunctions(t *testing.T) {
	e := newE2E(t)
	secret := "e2e-secret-" + randHex(12)
	var nodeCreds *Credentials
	var nodeV1, nodeV2 string

	step := func(name string, fn func(t *testing.T)) {
		if !t.Run(name, fn) {
			t.FailNow()
		}
	}

	step("NodeCreateDeployInvoke", func(t *testing.T) {
		ctx := ctxT(t, 15*time.Minute)
		created, err := e.m.Create(ctx, CreateRequest{Name: "e2enode", Runtime: "nodejs", RuntimeVersion: "22",
			Handler: "index.handler", MemoryMB: 256, CPUs: 0.5, TimeoutSeconds: 10,
			Env: []EnvVar{{Name: "GREETING", Value: "hello $USER ${HOME}"}}}, "e2e@test")
		if err != nil {
			t.Fatalf("create: %v (%s)", err, CodeOf(err))
		}
		nodeCreds = created.Credentials
		if created.Function.State != StateNotDeployed || nodeCreds.Token == "" || nodeCreds.AccessKeyID != "" {
			t.Fatalf("created: %+v", created.Function)
		}
		// A secret before the first deployment.
		if err := e.m.SetSecret(ctx, "e2enode", "E2E_SECRET", secret, "e2e@test"); err != nil {
			t.Fatalf("secret: %v", err)
		}
		d, err := e.m.Deploy(ctx, "e2enode", bytes.NewReader(makeZip(t, nodeCode("v1"))), "e2e@test")
		if err != nil {
			t.Fatalf("deploy: %v (%s)", err, CodeOf(err))
		}
		nodeV1 = d.ID
		det := e.wantState(t, "e2enode", StateRunning)
		if det.Health != "healthy" {
			t.Fatalf("health %q", det.Health)
		}
		res := e.invoke(t, "e2enode", `{"name":"e2e"}`)
		if res.StatusCode != 200 || res.FunctionError != "" || res.RequestID == "" || res.DurationMs < 0 {
			t.Fatalf("envelope: %+v", res)
		}
		var out map[string]any
		if err := json.Unmarshal(res.Payload, &out); err != nil {
			t.Fatal(err)
		}
		if out["version"] != "v1" || out["greeting"] != "hello $USER ${HOME}" || out["secret"] != secret ||
			out["requestId"] != res.RequestID || out["uid"] != float64(FunctionUID) || out["tokenVar"] != nil {
			t.Fatalf("payload: %v", out)
		}
		if ev, _ := out["event"].(map[string]any); ev["name"] != "e2e" {
			t.Fatalf("event: %v", out["event"])
		}
		logs := string(res.LogTail)
		if !strings.Contains(logs, "REPORT RequestId: "+res.RequestID) || !strings.Contains(logs, "e2e node log line") ||
			!strings.Contains(logs, "START RequestId: "+res.RequestID) {
			t.Fatalf("log tail: %q", logs)
		}
		// JSON scalar and array events (the newline ends a scalar).
		for _, ev := range []string{`42`, `"hi"`, `true`, `null`, `[1,2,3]`, `{}`, "{\n  \"multi\": [1,\n 2]\n}"} {
			out := e.invokeOK(t, "e2enode", ev)
			got, _ := json.Marshal(out["event"])
			var want any
			json.Unmarshal([]byte(ev), &want)
			wantJSON, _ := json.Marshal(want)
			if !bytes.Equal(got, wantJSON) {
				t.Fatalf("event %s echoed as %s", ev, got)
			}
		}
		// Not one JSON value: refused by the panel.
		_, err = e.m.Invoke(ctx, "e2enode", strings.NewReader(`{"a":1} {"b":2}`), "e2e@test")
		e2eWantCode(t, err, CodeInvalidEvent)
	})

	step("Hardening", func(t *testing.T) {
		ctx := ctxT(t, time.Minute)
		info, err := e.dc.InspectContainerRaw(ctx, "fn-e2enode")
		if err != nil {
			t.Fatal(err)
		}
		c, h := info.Config, info.HostConfig
		var problems []string
		check := func(ok bool, format string, args ...any) {
			if !ok {
				problems = append(problems, fmt.Sprintf(format, args...))
			}
		}
		check(c.User == "10001:10001", "user %q", c.User)
		check(h.ReadonlyRootfs, "read_only")
		check(slices.Equal(h.CapDrop, []string{"ALL"}) && len(h.CapAdd) == 0, "caps %v %v", h.CapDrop, h.CapAdd)
		check(slices.Equal(h.SecurityOpt, []string{"no-new-privileges:true"}), "security_opt %v", h.SecurityOpt)
		check(strings.HasPrefix(h.Tmpfs["/tmp"], "size=64m") && strings.Contains(h.Tmpfs["/tmp"], "nosuid") &&
			strings.Contains(h.Tmpfs["/tmp"], "nodev"), "tmpfs %v", h.Tmpfs)
		check(h.Memory == 256<<20 && h.MemorySwap == 256<<20, "memory %d swap %d", h.Memory, h.MemorySwap)
		check(h.NanoCPUs == 5e8, "cpus %d", h.NanoCPUs)
		check(h.PidsLimit != nil && *h.PidsLimit == 128, "pids %v", h.PidsLimit)
		check(h.LogConfig.Type == "json-file" && h.LogConfig.Config["max-size"] == "10m" && h.LogConfig.Config["max-file"] == "3",
			"logging %+v", h.LogConfig)
		check(string(h.IpcMode) == "private", "ipc %q", h.IpcMode)
		check(!h.Privileged && len(h.PortBindings) == 0 && len(h.Devices) == 0, "privileged/ports/devices")
		check(h.RestartPolicy.Name == "unless-stopped", "restart %v", h.RestartPolicy)
		check(info.State.Health != nil && info.State.Health.Status == "healthy", "health %+v", info.State.Health)
		check(slices.Equal(c.Entrypoint, []string{RuntimeMount}), "entrypoint %v", c.Entrypoint)
		for _, mp := range info.Mounts {
			check(!mp.RW, "mount %s is writable", mp.Destination)
		}
		check(len(info.Mounts) == 2, "mounts %d", len(info.Mounts))
		if len(problems) > 0 {
			t.Fatalf("hardening: %s", strings.Join(problems, "; "))
		}
		// From inside: only /tmp is writable.
		out := e.invokeOK(t, "e2enode", `{"mode":"write"}`)
		if out["/tmp/x"] != "ok" || out["/var/task/x"] == "ok" || out["/x"] == "ok" {
			t.Fatalf("writes: %v", out)
		}
	})

	step("ImagePinningContainerdStore", func(t *testing.T) {
		ctx := ctxT(t, time.Minute)
		ref := e.ref(t, "e2enode")
		rt := ref.runtime()
		img, err := e.dc.InspectImage(ctx, rt.ImageRepository()+"@"+ref.ImageDigest)
		if err != nil {
			t.Fatal(err)
		}
		info, err := e.dc.InspectContainerRaw(ctx, "fn-e2enode")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("tag %s digest %s recorded ID %s image ID %s container image %s repo digests %v",
			ref.ImageTag, ref.ImageDigest, ref.ImageID, img.ID, info.Image, img.RepoDigests)
		if perr := checkPinnedImage(img, rt, ref.ImageDigest, e.arch, ref.ImageID); perr != nil {
			t.Fatalf("pinned image: %v", perr)
		}
		if p := verifyImage(ref, info, img); p != "" {
			t.Fatalf("container image: %s", p)
		}
		if err := e.m.ensureImage(ctx, ref); err != nil {
			t.Fatalf("ensureImage: %v", err)
		}
		if ref.ImageTag != LineTag("22", e.arch) || info.Config.Image != ref.Image() {
			t.Fatalf("tag %s, image %s", ref.ImageTag, info.Config.Image)
		}
	})

	step("SecretsMasked", func(t *testing.T) {
		ctx := ctxT(t, time.Minute)
		info, err := e.dc.InspectContainer(ctx, "fn-e2enode") // the client-facing (masked) inspect
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(info)
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("the secret value is in the masked inspect")
		}
		sawSecret := false
		for _, kv := range info.Config.Env {
			k, v, _ := strings.Cut(kv, "=")
			if v != "***" {
				t.Fatalf("unmasked variable %s", k)
			}
			sawSecret = sawSecret || k == "E2E_SECRET"
		}
		if !sawSecret {
			t.Fatal("secret name not listed")
		}
		// Not in the compose project, the reference, the detail or the trail.
		n := nm("e2enode")
		for _, p := range []string{filepath.Join(e.paths.ProjectDir(n), ComposeFile), filepath.Join(e.paths.Private(n), ReferenceFile)} {
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte(secret)) || bytes.Contains(data, []byte(nodeCreds.Token)) {
				t.Fatalf("%s holds a secret", p)
			}
		}
		det, _ := json.Marshal(e.detail(t, "e2enode"))
		if bytes.Contains(det, []byte(secret)) || !bytes.Contains(det, []byte(`"E2E_SECRET"`)) {
			t.Fatalf("detail: %s", det)
		}
		if strings.Contains(e.audit.String(), secret) || strings.Contains(e.audit.String(), nodeCreds.Token) {
			t.Fatal("a secret is in the audit trail")
		}
		// The token itself never enters the container: only its SHA-256.
		rawInfo, _ := e.dc.InspectContainerRaw(ctx, "fn-e2enode")
		rawJSON, _ := json.Marshal(rawInfo)
		if bytes.Contains(rawJSON, []byte(nodeCreds.Token)) {
			t.Fatal("the bearer token is in the container")
		}
		sum := sha256.Sum256([]byte(nodeCreds.Token))
		if !slices.Contains(rawInfo.Config.Env, envTokenSHA256+"="+hex.EncodeToString(sum[:])) {
			t.Fatal("token SHA-256 not in the container")
		}
	})

	step("BigEventsConcurrencyCancel", func(t *testing.T) {
		ctx := ctxT(t, 5*time.Minute)
		// A 3 MiB event comes back whole.
		big := `"` + strings.Repeat("a", 3<<20) + `"`
		out := e.invokeOK(t, "e2enode", big)
		if s, _ := out["event"].(string); len(s) != 3<<20 {
			t.Fatalf("3 MiB event echoed with %d bytes", len(s))
		}
		// A 6 MiB event is accepted, its 6 MiB+ echo is too large.
		max := `"` + strings.Repeat("b", MaxEventBytes-2) + `"`
		res := e.invoke(t, "e2enode", max)
		if res.FunctionError != "Unhandled" || !bytes.Contains(res.Payload, []byte("Function.ResponseSizeTooLarge")) {
			t.Fatalf("too large response: %d %q %.300s", res.StatusCode, res.FunctionError, res.Payload)
		}
		// Over 6 MiB: refused by the panel.
		_, err := e.m.Invoke(ctx, "e2enode", strings.NewReader(max+" "), "e2e@test")
		e2eWantCode(t, err, CodeEventTooLarge)
		e.invokeOK(t, "e2enode", `{}`)

		// Two invocations in flight from the panel, a third waits then
		// gives up.
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, err := e.m.Invoke(ctx, "e2enode", strings.NewReader(`{"mode":"sleep","ms":6000}`), "e2e@test")
				if err == nil && (r.FunctionError != "" || string(r.Payload) != `"slept"`) {
					err = fmt.Errorf("sleep: %q %s", r.FunctionError, r.Payload)
				}
				errs[i] = err
			}()
		}
		time.Sleep(500 * time.Millisecond)
		_, err = e.m.Invoke(ctx, "e2enode", strings.NewReader(`{}`), "e2e@test")
		e2eWantCode(t, err, CodeInvokeBusy)
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatalf("concurrent invocation: %v", err)
			}
		}

		// A caller gone mid-invocation: the exec is cut, the runtime's
		// invoke process exits, the slot comes back.
		cctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		_, err = e.m.Invoke(cctx, "e2enode", strings.NewReader(`{"mode":"sleep","ms":4000}`), "e2e@test")
		cancel()
		if err == nil {
			t.Fatal("cancelled invocation succeeded")
		}
		t.Logf("cancelled invocation: %v (%s)", err, CodeOf(err))
		time.Sleep(2500 * time.Millisecond)
		top, terr := dockerCmd(nil, "top", "fn-e2enode", "-o", "pid,args")
		if terr != nil {
			t.Fatalf("docker top: %v %s", terr, top)
		}
		if strings.Contains(top, " invoke") {
			t.Errorf("invoke process still running after the caller left:\n%s", top)
		}
		out = e.invokeOK(t, "e2enode", `{"after":"cancel"}`)
		if ev, _ := out["event"].(map[string]any); ev["after"] != "cancel" {
			t.Fatalf("after cancel: %v", out)
		}
	})

	step("FunctionErrorTimeoutOOM", func(t *testing.T) {
		ctx := ctxT(t, 10*time.Minute)
		// Unhandled exception.
		res := e.invoke(t, "e2enode", `{"mode":"error"}`)
		if res.StatusCode != 200 || res.FunctionError != "Unhandled" || !bytes.Contains(res.Payload, []byte("boom from node")) {
			t.Fatalf("error: %+v %s", res, res.Payload)
		}
		var perr map[string]any
		json.Unmarshal(res.Payload, &perr)
		if perr["errorType"] != "Error" {
			t.Fatalf("errorType %v", perr["errorType"])
		}
		// Short timeout and low memory.
		mem, timeout, tmp := 128, 2, 16
		if _, err := e.m.Update(ctx, "e2enode", UpdateRequest{MemoryMB: &mem, TimeoutSeconds: &timeout, TmpSizeMB: &tmp}, "e2e@test"); err != nil {
			t.Fatalf("update: %v (%s)", err, CodeOf(err))
		}
		start := time.Now()
		res = e.invoke(t, "e2enode", `{"mode":"sleep","ms":10000}`)
		if res.FunctionError != "Unhandled" || !bytes.Contains(res.Payload, []byte("Sandbox.Timedout")) ||
			!bytes.Contains(res.Payload, []byte("Task timed out after 2.00 seconds")) || time.Since(start) > 20*time.Second {
			t.Fatalf("timeout: %+v %s (%s)", res, res.Payload, time.Since(start))
		}
		if !strings.Contains(string(res.LogTail), "Task timed out after 2.00 seconds") {
			t.Fatalf("timeout log tail: %q", res.LogTail)
		}
		// Re-init after the timeout: the next invocation works.
		e.invokeOK(t, "e2enode", `{}`)
		res = e.invoke(t, "e2enode", `{"mode":"oom"}`)
		if res.FunctionError != "Unhandled" || !bytes.Contains(res.Payload, []byte("Runtime.OutOfMemory")) {
			t.Fatalf("oom: %+v %s logs %s", res, res.Payload, res.LogTail)
		}
		e.invokeOK(t, "e2enode", `{}`)
		e.wantState(t, "e2enode", StateRunning)
		// Back to the original limits.
		mem, timeout, tmp = 256, 10, 64
		if _, err := e.m.Update(ctx, "e2enode", UpdateRequest{MemoryMB: &mem, TimeoutSeconds: &timeout, TmpSizeMB: &tmp}, "e2e@test"); err != nil {
			t.Fatalf("update back: %v", err)
		}
	})

	step("PythonBasics", func(t *testing.T) {
		ctx := ctxT(t, 15*time.Minute)
		if _, err := e.m.Create(ctx, CreateRequest{Name: "e2epy", Runtime: "python", RuntimeVersion: "3.13",
			Handler: "app.handler", MemoryMB: 256, TimeoutSeconds: 10,
			Env: []EnvVar{{Name: "GREETING", Value: "bonjour"}}}, "e2e@test"); err != nil {
			t.Fatalf("create: %v (%s)", err, CodeOf(err))
		}
		if err := e.m.SetSecret(ctx, "e2epy", "E2E_SECRET", secret, "e2e@test"); err != nil {
			t.Fatal(err)
		}
		if _, err := e.m.Deploy(ctx, "e2epy", bytes.NewReader(makeZip(t, map[string]string{"app.py": pythonCode})), "e2e@test"); err != nil {
			t.Fatalf("deploy: %v (%s)", err, CodeOf(err))
		}
		e.wantState(t, "e2epy", StateRunning)
		res := e.invoke(t, "e2epy", `{"k":[1,"two"]}`)
		var out map[string]any
		if err := json.Unmarshal(res.Payload, &out); err != nil || res.FunctionError != "" {
			t.Fatalf("payload %s: %v", res.Payload, err)
		}
		if out["greeting"] != "bonjour" || out["secret"] != secret || out["uid"] != float64(FunctionUID) || out["requestId"] != res.RequestID {
			t.Fatalf("payload: %v", out)
		}
		if !strings.Contains(string(res.LogTail), "e2e python log line") || !strings.Contains(string(res.LogTail), "REPORT RequestId: "+res.RequestID) {
			t.Fatalf("log tail %q", res.LogTail)
		}
		for _, ev := range []string{`7`, `"x"`, `false`, `null`, `[]`} {
			out := e.invokeOK(t, "e2epy", ev)
			got, _ := json.Marshal(out["event"])
			if string(got) != ev {
				t.Fatalf("event %s echoed as %s", ev, got)
			}
		}
		res = e.invoke(t, "e2epy", `{"mode":"error"}`)
		if res.FunctionError != "Unhandled" || !bytes.Contains(res.Payload, []byte("ValueError")) {
			t.Fatalf("python error: %+v %s", res, res.Payload)
		}
		ref := e.ref(t, "e2epy")
		img, err := e.dc.InspectImage(ctx, ref.runtime().ImageRepository()+"@"+ref.ImageDigest)
		if err != nil {
			t.Fatal(err)
		}
		if perr := checkPinnedImage(img, ref.runtime(), ref.ImageDigest, e.arch, ref.ImageID); perr != nil {
			t.Fatalf("pinned python image: %v", perr)
		}
	})

	step("ProvidedGo", func(t *testing.T) {
		ctx := ctxT(t, 15*time.Minute)
		// No handler: the custom runtime defaults to bootstrap.
		created, err := e.m.Create(ctx, CreateRequest{Name: "e2ego", Runtime: "provided", RuntimeVersion: "al2023",
			MemoryMB: 128, TimeoutSeconds: 10}, "e2e@test")
		if err != nil {
			t.Fatalf("create: %v (%s)", err, CodeOf(err))
		}
		if created.Function.Handler != BootstrapFile {
			t.Fatalf("handler = %q", created.Function.Handler)
		}
		// A binary of the other architecture is refused at deploy time.
		other := map[string]string{"arm64": "amd64", "amd64": "arm64"}[e.arch]
		_, err = e.m.Deploy(ctx, "e2ego", bytes.NewReader(makeZip(t, map[string]string{"bootstrap": goBootstrap(t, other)})), "e2e@test")
		e2eWantCode(t, err, CodeBootstrapArch)
		// makeZip writes no Unix mode: the exec bit is set by the panel.
		d, err := e.m.Deploy(ctx, "e2ego", bytes.NewReader(makeZip(t, map[string]string{"bootstrap": goBootstrap(t, e.arch)})), "e2e@test")
		if err != nil {
			t.Fatalf("deploy: %v (%s)", err, CodeOf(err))
		}
		if len(d.Warnings) != 0 {
			t.Fatalf("static bootstrap warnings: %v", d.Warnings)
		}
		if fi, err := os.Lstat(filepath.Join(e.paths.Deployment(nm("e2ego"), d.ID), BootstrapFile)); err != nil || fi.Mode() != roExecMode {
			t.Fatalf("bootstrap mode: %v %v", fi.Mode(), err)
		}
		e.wantState(t, "e2ego", StateRunning)
		out := e.invokeOK(t, "e2ego", `{"name":"x"}`)
		if out["hello"] != "x" || out["runtime"] != "go" || out["requestId"] == "" {
			t.Fatalf("go function: %v", out)
		}
		res := e.invoke(t, "e2ego", `{"op":"throw"}`)
		if res.FunctionError != "Unhandled" || !bytes.Contains(res.Payload, []byte("thrown on purpose")) {
			t.Fatalf("go error: %+v %s", res, res.Payload)
		}
		// A bootstrap that passes the checks but cannot run never becomes
		// healthy: the deployment fails, the previous one keeps serving.
		_, err = e.m.Deploy(ctx, "e2ego", bytes.NewReader(makeZip(t, map[string]string{"bootstrap": "#!/bin/sh\necho broken >&2\nexit 3\n"})), "e2e@test")
		e2eWantCode(t, err, CodeStartFailed)
		if ref := e.ref(t, "e2ego"); ref.Deployment == nil || ref.Deployment.ID != d.ID {
			t.Fatalf("previous deployment not kept: %+v", ref.Deployment)
		}
		e.wantState(t, "e2ego", StateRunning)
		if out := e.invokeOK(t, "e2ego", `{"name":"again"}`); out["hello"] != "again" {
			t.Fatalf("after the failed deployment: %v", out)
		}
		if res, err := e.m.Delete(ctx, "e2ego", "e2e@test"); err != nil || res.Containers != 1 || res.NetworkKept {
			t.Fatalf("delete: %+v %v", res, err)
		}
	})

	step("RedeployRollbackTamper", func(t *testing.T) {
		ctx := ctxT(t, 15*time.Minute)
		d, err := e.m.Deploy(ctx, "e2enode", bytes.NewReader(makeZip(t, nodeCode("v2"))), "e2e@test")
		if err != nil {
			t.Fatalf("deploy v2: %v (%s)", err, CodeOf(err))
		}
		nodeV2 = d.ID
		if out := e.invokeOK(t, "e2enode", `{}`); out["version"] != "v2" {
			t.Fatalf("after redeploy: %v", out["version"])
		}
		deps, _ := e.m.Deployments(ctx, "e2enode")
		if len(deps) != 2 || deps[0].ID != nodeV2 || !deps[0].Active || deps[1].ID != nodeV1 {
			t.Fatalf("deployments %+v", deps)
		}
		// Rollback.
		if err := e.m.Activate(ctx, "e2enode", nodeV1, "e2e@test"); err != nil {
			t.Fatalf("rollback: %v (%s)", err, CodeOf(err))
		}
		if out := e.invokeOK(t, "e2enode", `{}`); out["version"] != "v1" {
			t.Fatalf("after rollback: %v", out["version"])
		}
		if !strings.Contains(e.audit.String(), `"function deployment rolled back"`) {
			t.Fatal("rollback not audited")
		}
		// Tamper with the kept deployment (v2): its activation is refused.
		n := nm("e2enode")
		file := filepath.Join(e.paths.Deployment(n, nodeV2), "index.js")
		orig, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		tamper := func(data []byte) {
			if err := os.Chmod(file, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, data, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(file, 0o444); err != nil {
				t.Fatal(err)
			}
		}
		tamper(append(slices.Clone(orig), []byte("\n// injected\n")...))
		err = e.m.Activate(ctx, "e2enode", nodeV2, "e2e@test")
		e2eWantCode(t, err, CodeArtifactModified)
		tamper(orig)
		// Tamper with the active one (v1): a restart is refused, the
		// running container is left alone.
		activeFile := filepath.Join(e.paths.Deployment(n, nodeV1), "index.js")
		activeOrig, _ := os.ReadFile(activeFile)
		file = activeFile
		tamper([]byte(`exports.handler = async () => "pwned";`))
		err = e.m.Action(ctx, "e2enode", ActionRestart, "e2e@test")
		e2eWantCode(t, err, CodeArtifactModified)
		tamper(activeOrig)
		if !strings.Contains(e.audit.String(), `"function verification refused"`) {
			t.Fatal("refusal not audited")
		}
		// Restored: activation works again.
		if err := e.m.Activate(ctx, "e2enode", nodeV2, "e2e@test"); err != nil {
			t.Fatalf("activate restored v2: %v (%s)", err, CodeOf(err))
		}
		if out := e.invokeOK(t, "e2enode", `{}`); out["version"] != "v2" {
			t.Fatalf("after activation: %v", out["version"])
		}
	})

	step("CredentialsOverTheNetwork", func(t *testing.T) {
		ctx := ctxT(t, 10*time.Minute)
		netName := networks.DockerPrefix + "fn-e2enode"
		code, body := curlInvoke(t, netName, "e2enode", nodeCreds.Token, `{"via":"curl"}`)
		if code != 200 || !strings.Contains(body, `"via":"curl"`) {
			t.Fatalf("bearer: %d %s", code, body)
		}
		if code, _ := curlInvoke(t, netName, "e2enode", "", `{}`); code != 401 {
			t.Fatalf("no token: %d", code)
		}
		if code, _ := curlInvoke(t, netName, "e2enode", "wrong-"+nodeCreds.Token, `{}`); code != 403 {
			t.Fatalf("wrong token: %d", code)
		}
		// Rotation with SigV4.
		on := true
		creds, err := e.m.RotateCredentials(ctx, "e2enode", &on, "e2e@test")
		if err != nil {
			t.Fatalf("rotate: %v (%s)", err, CodeOf(err))
		}
		if creds.Token == nodeCreds.Token || creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
			t.Fatal("rotation did not produce new credentials")
		}
		if code, _ := curlInvoke(t, netName, "e2enode", nodeCreds.Token, `{}`); code != 403 {
			t.Fatalf("old token after rotation: %d", code)
		}
		if code, body := curlInvoke(t, netName, "e2enode", creds.Token, `{}`); code != 200 {
			t.Fatalf("new token: %d %s", code, body)
		}
		nodeCreds = creds
		e.wantState(t, "e2enode", StateRunning)
		awsInvoke := func(keyID, secretKey string) (string, error) {
			script := `aws lambda invoke --function-name e2enode --endpoint-url http://fn-e2enode:8080 --region us-east-1 ` +
				`--cli-binary-format raw-in-base64-out --payload '{"via":"sigv4"}' /tmp/out.json >/tmp/meta.json && cat /tmp/out.json`
			return dockerCmd([]string{"AWS_ACCESS_KEY_ID=" + keyID, "AWS_SECRET_ACCESS_KEY=" + secretKey},
				"run", "--rm", "--name", "e2efn-awscli-"+randHex(4), "--network", netName,
				"-e", "AWS_ACCESS_KEY_ID", "-e", "AWS_SECRET_ACCESS_KEY", "-e", "AWS_EC2_METADATA_DISABLED=true",
				"-e", "AWS_MAX_ATTEMPTS=1", "--entrypoint", "sh", awsCLIImage, "-c", script)
		}
		out, err := awsInvoke(creds.AccessKeyID, creds.SecretAccessKey)
		if err != nil || !strings.Contains(out, `"via":"sigv4"`) {
			t.Fatalf("aws lambda invoke: %v: %s", err, out)
		}
		if out, err := awsInvoke(creds.AccessKeyID, "x"+creds.SecretAccessKey[1:]); err == nil {
			t.Fatalf("wrong SigV4 secret accepted: %s", out)
		} else if !strings.Contains(out, "InvalidSignatureException") {
			t.Logf("wrong secret: %s", out)
		}
	})

	step("ChokePointComposeEdited", func(t *testing.T) {
		ctx := ctxT(t, 10*time.Minute)
		n := nm("e2enode")
		composePath := filepath.Join(e.paths.ProjectDir(n), ComposeFile)
		orig, err := os.ReadFile(composePath)
		if err != nil {
			t.Fatal(err)
		}
		// CI edits the file (here: adds a capability).
		edited := bytes.Replace(orig, []byte("    cap_drop:\n"), []byte("    cap_add:\n      - NET_ADMIN\n    cap_drop:\n"), 1)
		if bytes.Equal(edited, orig) {
			t.Fatal("compose edit did not apply")
		}
		if err := os.WriteFile(composePath, edited, 0o640); err != nil {
			t.Fatal(err)
		}
		e.wantState(t, "e2enode", StateModified)
		mem := 300
		_, err = e.m.Update(ctx, "e2enode", UpdateRequest{MemoryMB: &mem}, "e2e@test")
		e2eWantCode(t, err, CodeModified)
		e2eWantCode(t, e.m.Action(ctx, "e2enode", ActionRestart, "e2e@test"), CodeModified)
		e2eWantCode(t, e.m.Action(ctx, "e2enode", ActionStart, "e2e@test"), CodeModified)
		e2eWantCode(t, e.m.SetSecret(ctx, "e2enode", "OTHER", "v", "e2e@test"), CodeModified)
		_, err = e.m.RotateCredentials(ctx, "e2enode", nil, "e2e@test")
		e2eWantCode(t, err, CodeModified)
		e2eWantCode(t, e.m.SetEgress(ctx, "e2enode", true, "e2e@test"), CodeModified)
		_, err = e.m.Deploy(ctx, "e2enode", bytes.NewReader(makeZip(t, nodeCode("v3"))), "e2e@test")
		e2eWantCode(t, err, CodeModified)
		e2eWantCode(t, e.m.Activate(ctx, "e2enode", nodeV1, "e2e@test"), CodeModified)
		_, err = e.m.Invoke(ctx, "e2enode", strings.NewReader(`{}`), "e2e@test")
		e2eWantCode(t, err, CodeModified)
		_, err = e.m.Delete(ctx, "e2enode", "e2e@test")
		e2eWantCode(t, err, CodeModified)
		// The container still runs the panel's configuration (never the
		// edited file).
		info, _ := e.dc.InspectContainerRaw(ctx, "fn-e2enode")
		if len(info.HostConfig.CapAdd) != 0 {
			t.Fatal("the edited compose file was run")
		}
		// Restored: back to running.
		if err := os.WriteFile(composePath, orig, 0o640); err != nil {
			t.Fatal(err)
		}
		e.wantState(t, "e2enode", StateRunning)
	})

	step("ChokePointContainerRecreatedByHand", func(t *testing.T) {
		ctx := ctxT(t, 10*time.Minute)
		n := nm("e2enode")
		orig, err := os.ReadFile(filepath.Join(e.paths.ProjectDir(n), ComposeFile))
		if err != nil {
			t.Fatal(err)
		}
		// Someone runs a modified copy of the file under the function's
		// project (the panel's file stays intact).
		dir := filepath.Join(e.base, "byhand")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		edited := bytes.Replace(orig, []byte("    cap_drop:\n"), []byte("    cap_add:\n      - NET_RAW\n    cap_drop:\n"), 1)
		if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), edited, 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := dockerCmd(nil, "compose", "-p", n.Project(), "--project-directory", dir, "-f", filepath.Join(dir, "compose.yaml"),
			"up", "-d", "--force-recreate", "--pull", "never"); err != nil {
			t.Fatalf("compose by hand: %v: %s", err, out)
		}
		d := e.wantState(t, "e2enode", StateUnverified)
		if d.Problem != "unexpected labels" { // another working directory
			t.Fatalf("problem %q", d.Problem)
		}
		_, err = e.m.Invoke(ctx, "e2enode", strings.NewReader(`{}`), "e2e@test")
		e2eWantCode(t, err, CodeVerificationFailed)
		// The same, from the panel's project directory and the file on
		// stdin (as the panel runs it): only the capability differs.
		cmd := exec.Command("docker", "compose", "-p", n.Project(), "--project-directory", e.paths.ProjectDir(n), "-f", "-",
			"up", "-d", "--force-recreate", "--pull", "never")
		cmd.Stdin = bytes.NewReader(edited)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compose by hand on stdin: %v: %s", err, out)
		}
		if d := e.wantState(t, "e2enode", StateUnverified); d.Problem != "unexpected privileges" {
			t.Fatalf("problem %q", d.Problem)
		}
		_, err = e.m.Invoke(ctx, "e2enode", strings.NewReader(`{}`), "e2e@test")
		e2eWantCode(t, err, CodeVerificationFailed)
		// A container by another name in the project is refused too.
		if out, err := dockerCmd(nil, "run", "-d", "--name", "e2efn-intruder", "--label", composeProjectLabel+"="+n.Project(),
			"--entrypoint", "sleep", curlImage, "300"); err != nil {
			t.Fatalf("intruder: %v: %s", err, out)
		}
		// The panel recreates its own container; the intruder makes the
		// verification fail, so the restart is rolled back.
		err = e.m.Action(ctx, "e2enode", ActionRestart, "e2e@test")
		if err == nil {
			t.Fatal("restart accepted with another container in the project")
		}
		t.Logf("restart with an intruder: %v (%s)", err, CodeOf(err))
		dockerCmd(nil, "rm", "-f", "e2efn-intruder")
		if err := e.m.Action(ctx, "e2enode", ActionRestart, "e2e@test"); err != nil {
			t.Fatalf("restart: %v (%s)", err, CodeOf(err))
		}
		e.wantState(t, "e2enode", StateRunning)
		if out := e.invokeOK(t, "e2enode", `{}`); out["version"] != "v2" {
			t.Fatalf("after restart: %v", out["version"])
		}
	})

	step("NetworksAndEgress", func(t *testing.T) {
		ctx := ctxT(t, 15*time.Minute)
		if _, err := e.m.Create(ctx, CreateRequest{Name: "e2ecaller", Runtime: "nodejs", RuntimeVersion: "22",
			Handler: "index.handler", MemoryMB: 256, TimeoutSeconds: 20, Networks: []string{"fn-e2enode"}}, "e2e@test"); err != nil {
			t.Fatalf("create: %v (%s)", err, CodeOf(err))
		}
		if err := e.m.SetSecret(ctx, "e2ecaller", "TARGET_TOKEN", nodeCreds.Token, "e2e@test"); err != nil {
			t.Fatal(err)
		}
		if _, err := e.m.Deploy(ctx, "e2ecaller", bytes.NewReader(makeZip(t, nodeCode("caller"))), "e2e@test"); err != nil {
			t.Fatalf("deploy: %v (%s)", err, CodeOf(err))
		}
		det := e.wantState(t, "e2ecaller", StateRunning)
		if !slices.Equal(det.Networks, []string{"fn-e2ecaller", "fn-e2enode"}) {
			t.Fatalf("networks %v", det.Networks)
		}
		// The node function is only on its own network.
		if det := e.detail(t, "e2enode"); !slices.Equal(det.Networks, []string{"fn-e2enode"}) {
			t.Fatalf("node networks %v", det.Networks)
		}
		out := e.invokeOK(t, "e2ecaller", `{"mode":"call","url":"`+nodeCreds.Endpoint+`"}`)
		if out["status"] != float64(200) || !strings.Contains(fmt.Sprint(out["body"]), `"from":"caller"`) {
			t.Fatalf("call through the network: %v", out)
		}
		// A container on another function's network cannot even resolve it.
		if code, body := curlInvoke(t, networks.DockerPrefix+"fn-e2epy", "e2enode", nodeCreds.Token, `{}`); code == 200 {
			t.Fatalf("reached from another network: %s", body)
		}
		// Egress off: no Internet.
		out = e.invokeOK(t, "e2ecaller", `{"mode":"net","host":"1.1.1.1","port":443,"ms":3000}`)
		if out["ok"] == true {
			t.Fatal("Internet reachable without egress")
		}
		if !hostHasInternet() {
			t.Log("the host has no Internet access: egress on not checked")
			return
		}
		if err := e.m.SetEgress(ctx, "e2ecaller", true, "e2e@test"); err != nil {
			t.Fatalf("egress on: %v (%s)", err, CodeOf(err))
		}
		e.wantState(t, "e2ecaller", StateRunning)
		out = e.invokeOK(t, "e2ecaller", `{"mode":"net","host":"1.1.1.1","port":443,"ms":5000}`)
		if out["ok"] != true {
			t.Fatalf("Internet not reachable with egress: %v", out)
		}
		// Still reaches the node function.
		out = e.invokeOK(t, "e2ecaller", `{"mode":"call","url":"`+nodeCreds.Endpoint+`"}`)
		if out["status"] != float64(200) {
			t.Fatalf("call with egress: %v", out)
		}
		if err := e.m.SetEgress(ctx, "e2ecaller", false, "e2e@test"); err != nil {
			t.Fatalf("egress off: %v (%s)", err, CodeOf(err))
		}
		out = e.invokeOK(t, "e2ecaller", `{"mode":"net","host":"1.1.1.1","port":443,"ms":3000}`)
		if out["ok"] == true {
			t.Fatal("Internet reachable after egress off")
		}
		if _, err := e.dc.InspectNetwork(ctx, "fn-e2ecaller_"+egressNetworkKey); err == nil {
			t.Fatal("egress network left behind")
		}
		// Detach the caller from the node network: it can no longer call.
		if _, err := e.m.SetNetworks(ctx, "e2ecaller", []string{"fn-e2ecaller"}, "e2e@test"); err != nil {
			t.Fatalf("detach: %v (%s)", err, CodeOf(err))
		}
		out = e.invokeOK(t, "e2ecaller", `{"mode":"net","host":"fn-e2enode","port":8080,"ms":3000}`)
		if out["ok"] == true {
			t.Fatal("the node function is still reachable after the detach")
		}
		if _, err := e.m.SetNetworks(ctx, "e2ecaller", []string{"fn-e2ecaller", "fn-e2enode"}, "e2e@test"); err != nil {
			t.Fatalf("attach: %v (%s)", err, CodeOf(err))
		}
		e.wantState(t, "e2ecaller", StateRunning)
	})

	step("StopStart", func(t *testing.T) {
		ctx := ctxT(t, 5*time.Minute)
		if err := e.m.Action(ctx, "e2epy", ActionStop, "e2e@test"); err != nil {
			t.Fatal(err)
		}
		e.wantState(t, "e2epy", StateStopped)
		_, err := e.m.Invoke(ctx, "e2epy", strings.NewReader(`{}`), "e2e@test")
		e2eWantCode(t, err, CodeNotRunning)
		if err := e.m.Action(ctx, "e2epy", ActionStart, "e2e@test"); err != nil {
			t.Fatalf("start: %v (%s)", err, CodeOf(err))
		}
		e.invokeOK(t, "e2epy", `{}`)
	})

	step("PanelRestart", func(t *testing.T) {
		// A new Manager on the same directories (the API restarted) finds
		// the functions running and verified, and can invoke them.
		m2 := NewManager(e.mcfg, e.deps)
		m2.Startup(ctxT(t, time.Minute))
		list, err := m2.List(ctxT(t, time.Minute))
		if err != nil || len(list) != 3 {
			t.Fatalf("list: %+v %v", list, err)
		}
		for _, s := range list {
			if s.State != StateRunning || s.Health != "healthy" {
				t.Fatalf("%s: %s %s", s.Name, s.State, s.Health)
			}
		}
		res, err := m2.Invoke(ctxT(t, time.Minute), "e2epy", strings.NewReader(`{"restart":1}`), "e2e@test")
		if err != nil || res.FunctionError != "" {
			t.Fatalf("invoke after restart: %v", err)
		}
	})

	step("Audit", func(t *testing.T) {
		e.m.FlushAudit()
		trail := e.audit.String()
		for _, ev := range []string{"function created", "function code deployed", "function secret set", "function credentials rotated",
			"function invoked", "function egress toggled", "function network attached", "function network detached", "function verification refused"} {
			if !strings.Contains(trail, `"msg":"`+ev+`"`) {
				t.Errorf("audit event %q missing", ev)
			}
		}
		if strings.Contains(trail, secret) {
			t.Fatal("secret in the audit trail")
		}
	})

	step("DeleteAndPurge", func(t *testing.T) {
		ctx := ctxT(t, 10*time.Minute)
		// The node network still has the caller: kept.
		res, err := e.m.Delete(ctx, "e2enode", "e2e@test")
		if err != nil {
			t.Fatalf("delete node: %v (%s)", err, CodeOf(err))
		}
		if res.Containers != 1 || !res.NetworkKept {
			t.Fatalf("delete node: %+v", res)
		}
		res, err = e.m.Delete(ctx, "e2ecaller", "e2e@test")
		if err != nil || res.Containers != 1 || res.NetworkKept {
			t.Fatalf("delete caller: %+v %v", res, err)
		}
		// The caller is gone: the kept network can be deleted now.
		if err := e.nets.Delete(ctx, "fn-e2enode", "e2e@test"); err != nil {
			t.Fatalf("deleting the kept network: %v", err)
		}
		// Purge: the python function edited by CI.
		n := nm("e2epy")
		composePath := filepath.Join(e.paths.ProjectDir(n), ComposeFile)
		data, _ := os.ReadFile(composePath)
		os.WriteFile(composePath, append(data, []byte("# edited by CI\n")...), 0o640)
		_, err = e.m.Delete(ctx, "e2epy", "e2e@test")
		e2eWantCode(t, err, CodeModified)
		res, err = e.m.Purge(ctx, "e2epy", "e2e@test")
		if err != nil || res.Containers != 1 || res.NetworkKept {
			t.Fatalf("purge: %+v %v", res, err)
		}
		// Nothing left: containers, networks, directories.
		list, err := e.m.List(ctx)
		if err != nil || len(list) != 0 {
			t.Fatalf("list after delete: %+v %v", list, err)
		}
		for _, name := range []string{"e2enode", "e2ecaller", "e2epy"} {
			n := nm(name)
			for _, p := range []string{e.paths.Private(n), e.paths.FunctionData(n), e.paths.ProjectDir(n)} {
				if _, err := os.Lstat(p); !os.IsNotExist(err) {
					t.Errorf("%s left behind", p)
				}
			}
			if _, ok := e.nets.Lookup(n.DedicatedNetwork()); ok {
				t.Errorf("network %s still registered", n.DedicatedNetwork())
			}
		}
		out, _ := exec.Command("docker", "ps", "-a", "--format", "{{.Names}} {{.Label \"com.docker.compose.project\"}}").Output()
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "fn-e2e") || strings.Contains(line, "e2efn-") {
				t.Errorf("container left behind: %s", line)
			}
		}
		out, _ = exec.Command("docker", "network", "ls", "--format", "{{.Name}}").Output()
		for _, name := range strings.Fields(string(out)) {
			if strings.Contains(name, "fn-e2e") || strings.HasPrefix(name, "e2efn-") {
				t.Errorf("network left behind: %s", name)
			}
		}
		// What remains in the data directory: the runtime binaries and the
		// shared parents only.
		entries, _ := os.ReadDir(e.paths.DataDir)
		for _, en := range entries {
			if en.Name() != RuntimeDir && en.Name() != PrivateDir {
				t.Errorf("data directory leftover: %s", en.Name())
			}
		}
		if entries, _ := os.ReadDir(e.paths.PrivateRoot()); len(entries) != 0 {
			t.Errorf("private leftovers: %v", entries)
		}
		if entries, _ := os.ReadDir(e.paths.StacksDir); len(entries) != 0 {
			t.Errorf("stacks leftovers: %v", entries)
		}
	})
}
