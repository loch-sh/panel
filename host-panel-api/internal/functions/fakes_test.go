package functions

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/network"

	"host-panel-api/internal/networks"
)

var errNotFound = errors.New("not found")

// fakeDocker is an in-memory daemon: compose up (fakeRunner) creates the
// container a real compose would create from the rendered file.
type fakeDocker struct {
	mu           sync.Mutex
	containers   map[string]container.InspectResponse // by name (without "/")
	images       map[string]image.InspectResponse     // by repo@digest
	egress       map[string]network.Inspect
	pulls        int
	pullFailures int
	stopped      []string
	removed      []string
	disconnected []string
	removedNets  []string
}

func newFakeDocker() *fakeDocker {
	return &fakeDocker{containers: map[string]container.InspectResponse{}, images: map[string]image.InspectResponse{}, egress: map[string]network.Inspect{}}
}

func (d *fakeDocker) ListContainers(context.Context) ([]container.Summary, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []container.Summary
	for name, c := range d.containers {
		state := container.StateExited
		if c.State != nil && c.State.Running {
			state = container.StateRunning
		}
		var labels map[string]string
		if c.Config != nil {
			labels = c.Config.Labels
		}
		out = append(out, container.Summary{ID: c.ID, Names: []string{"/" + name}, Labels: labels, State: state, Status: "Up 1 minute (healthy)"})
	}
	return out, nil
}

func (d *fakeDocker) InspectContainerRaw(_ context.Context, id string) (container.InspectResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for name, c := range d.containers {
		if name == id || c.ID == id {
			return c, nil
		}
	}
	return container.InspectResponse{}, errNotFound
}

func (d *fakeDocker) InspectImage(_ context.Context, ref string) (image.InspectResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for key, img := range d.images {
		if key == ref || img.ID == ref {
			return img, nil
		}
	}
	return image.InspectResponse{}, errNotFound
}

func (d *fakeDocker) InspectNetwork(_ context.Context, name string) (network.Inspect, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if n, ok := d.egress[name]; ok {
		return n, nil
	}
	return network.Inspect{}, errNotFound
}

func (d *fakeDocker) PullImage(_ context.Context, ref, platform string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pulls++
	if d.pullFailures > 0 {
		d.pullFailures--
		return errors.New("toomanyrequests")
	}
	repo, digest, ok := strings.Cut(ref, "@")
	if !ok {
		return errors.New("not pulled by digest")
	}
	if i := strings.LastIndex(repo, ":"); i > strings.LastIndex(repo, "/") {
		repo = repo[:i]
	}
	arch := strings.TrimPrefix(platform, "linux/")
	rt := Runtime{}
	for _, r := range runtimes {
		if r.ImageRepository() == repo {
			rt = r
		}
	}
	img := testImage(&Reference{Runtime: rt.ID, Arch: arch})
	img.RepoDigests = []string{repo + "@" + digest}
	d.images[repo+"@"+digest] = img
	return nil
}

func (d *fakeDocker) StopContainer(_ context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for name, c := range d.containers {
		if c.ID == id {
			c.State = &container.State{Running: false}
			d.containers[name] = c
			d.stopped = append(d.stopped, name)
		}
	}
	return nil
}

func (d *fakeDocker) RemoveContainer(_ context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for name, c := range d.containers {
		if c.ID == id {
			delete(d.containers, name)
			d.removed = append(d.removed, name)
		}
	}
	return nil
}

func (d *fakeDocker) RemoveNetwork(_ context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.removedNets = append(d.removedNets, name)
	delete(d.egress, name)
	return nil
}

func (d *fakeDocker) DisconnectNetwork(_ context.Context, networkID, _ string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.disconnected = append(d.disconnected, networkID)
	return nil
}

// fakeNets is the networks registry.
type fakeNets struct {
	mu      sync.Mutex
	refs    map[string]networks.Ref
	inUse   map[string]bool
	created []string
	deleted []string
}

func newFakeNets() *fakeNets {
	return &fakeNets{refs: map[string]networks.Ref{
		"images":   {Name: "images", DockerName: networks.DockerPrefix + "images", ID: "net-id-images"},
		"services": {Name: "services", DockerName: "loch-services", ID: "net-id-services", System: true},
	}, inUse: map[string]bool{}}
}

func (f *fakeNets) Lookup(name string) (networks.Ref, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.refs[name]
	return r, ok
}

func (f *fakeNets) Verify(_ context.Context, name string) (string, bool) {
	r, ok := f.Lookup(name)
	return r.ID, ok
}

func (f *fakeNets) Create(_ context.Context, req networks.CreateRequest, _ string) (*networks.Network, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.refs[req.Name]; ok {
		return nil, &networks.Error{Code: networks.CodeNameTaken, Message: "taken"}
	}
	n := networks.Network{Name: req.Name, DockerName: networks.DockerPrefix + req.Name, ID: "net-id-" + req.Name}
	f.refs[req.Name] = n.Ref()
	f.created = append(f.created, req.Name)
	return &n, nil
}

func (f *fakeNets) Delete(_ context.Context, name, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.inUse[name] {
		return &networks.Error{Code: networks.CodeInUse, Message: "in use"}
	}
	delete(f.refs, name)
	f.deleted = append(f.deleted, name)
	return nil
}

// fakeRuntimes is the runtime binary store.
type fakeRuntimes struct {
	dir       string
	verifyErr error
	noBinary  bool
}

func (f *fakeRuntimes) Verify(b RuntimeBinary) (string, error) {
	if f.verifyErr != nil {
		return "", f.verifyErr
	}
	return f.Path(b.Version, b.Arch), nil
}

func (f *fakeRuntimes) Latest(arch string) (RuntimeBinary, error) {
	if f.noBinary {
		return RuntimeBinary{}, untrusted("no trusted loch function runtime binary is installed for %s", arch)
	}
	return RuntimeBinary{Version: "v1.0.0", Arch: arch, SHA256: testHash}, nil
}

func (f *fakeRuntimes) Path(version, arch string) string {
	return filepath.Join(f.dir, version, arch, RuntimeBinaryName)
}

type fakeDigests struct{ failures int }

func (f *fakeDigests) Digest(context.Context, string) (string, error) {
	if f.failures > 0 {
		f.failures--
		return "", errors.New("not found")
	}
	return testDigest, nil
}

// fakeRunner simulates docker compose: up creates (or recreates) the
// container rendered from the reference on disk.
type fakeRunner struct {
	mu       sync.Mutex
	m        *Manager
	d        *fakeDocker
	calls    [][]string
	composes [][]byte // the compose bytes given on stdin
	failUps  int      // the next n up commands fail
	// tamper changes the container compose creates (a hostile daemon or
	// a race).
	tamper func(*container.InspectResponse)
}

func (r *fakeRunner) RunStdin(_ context.Context, dir string, compose []byte, args ...string) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string{filepath.Base(dir)}, args...))
	r.composes = append(r.composes, slices.Clone(compose))
	fail := false
	if args[0] == "up" && r.failUps > 0 {
		r.failUps--
		fail = true
	}
	r.mu.Unlock()
	if args[0] != "up" {
		return "", nil
	}
	if fail {
		return "container unhealthy", errors.New("exit status 1")
	}
	n := nm(strings.TrimPrefix(filepath.Base(dir), ProjectPrefix))
	ref, err := r.m.paths.ReadReference(n, r.m.cfg.Config)
	if err != nil {
		return "", err
	}
	rc := r.m.renderConfig(ref)
	info := goodContainer(ref, rc)
	nets := map[string]*network.EndpointSettings{}
	for _, name := range ref.Networks {
		nr, _ := r.m.nets.Lookup(name)
		nets[nr.DockerName] = &network.EndpointSettings{NetworkID: nr.ID, Aliases: []string{n.Project()}}
	}
	if ref.Egress {
		nets[n.Project()+"_fn_egress"] = &network.EndpointSettings{NetworkID: "egress-id"}
	}
	info.NetworkSettings.Networks = nets
	info.State = &container.State{Running: true, Status: container.StateRunning}
	img := r.d.images[runtimes[ref.Runtime].ImageRepository()+"@"+ref.ImageDigest]
	info.Image = img.ID
	if r.tamper != nil {
		r.tamper(&info)
	}
	r.d.mu.Lock()
	r.d.containers[n.Project()] = info
	if eg := goodEgress(ref, info); eg != nil {
		r.d.egress[eg.Name] = *eg
	}
	r.d.mu.Unlock()
	return "", nil
}

func (r *fakeRunner) ups() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if c[1] == "up" {
			n++
		}
	}
	return n
}

func (r *fakeRunner) last() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return nil
	}
	return r.calls[len(r.calls)-1]
}

// fakeInvoker answers every invocation.
type fakeInvoker struct {
	mu     sync.Mutex
	ids    []string
	events [][]byte
	block  chan struct{}
	res    InvokeResult
	err    error
}

func (f *fakeInvoker) Invoke(ctx context.Context, id string, event []byte, _ time.Duration) (InvokeResult, error) {
	f.mu.Lock()
	f.ids = append(f.ids, id)
	f.events = append(f.events, slices.Clone(event))
	block := f.block
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return InvokeResult{}, ctx.Err()
		}
	}
	return f.res, f.err
}

// testEnv is a Manager on temporary directories with fakes.
type testEnv struct {
	m        *Manager
	docker   *fakeDocker
	nets     *fakeNets
	runner   *fakeRunner
	runtimes *fakeRuntimes
	digests  *fakeDigests
	invoker  *fakeInvoker
	logs     *bytes.Buffer
	audit    *bytes.Buffer
	clock    time.Time
}

// syncBuffer is a bytes.Buffer safe for the concurrent writes of slog.
type syncBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Buffer.Write(p)
}

func newTestEnv(t *testing.T, tweak ...func(*ManagerConfig)) *testEnv {
	t.Helper()
	root := t.TempDir()
	data, stacks, rtDir := filepath.Join(root, "data"), filepath.Join(root, "stacks"), filepath.Join(root, "runtime")
	for _, d := range []string{data, stacks, rtDir} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if err := removeTree(data); err != nil {
			t.Error(err)
		}
	})
	cfg := ManagerConfig{
		Config: Config{Enabled: true, DataDir: data, StacksDir: stacks, Max: 10, MaxMemoryMB: 3008,
			MaxCodeBytes: 1 << 20, DeploymentsKept: 3, TmpSizeMB: 512},
		Arch: "arm64", MaxTotalMemoryMB: 8192, MaxTotalCPUs: 8, LockTimeout: 200 * time.Millisecond,
	}
	for _, f := range tweak {
		f(&cfg)
	}
	logs, audit := &syncBuffer{}, &syncBuffer{}
	env := &testEnv{docker: newFakeDocker(), nets: newFakeNets(), runtimes: &fakeRuntimes{dir: rtDir},
		digests: &fakeDigests{}, invoker: &fakeInvoker{res: InvokeResult{StatusCode: 200, RequestID: "abc-123", Payload: []byte(`{"ok":true}`)}},
		clock: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)}
	env.runner = &fakeRunner{d: env.docker}
	env.m = NewManager(cfg, Deps{
		Runner: env.runner, Docker: env.docker, Networks: env.nets, Runtimes: env.runtimes,
		Digests: env.digests, Invoker: env.invoker,
		Logger: slog.New(slog.NewJSONHandler(logs, nil)), Audit: slog.New(slog.NewJSONHandler(audit, nil)),
	})
	env.runner.m = env.m
	env.m.sleep = func(context.Context, time.Duration) error { return nil }
	env.m.extractor.statfs = bigFS
	env.m.now = func() time.Time { env.clock = env.clock.Add(time.Minute); return env.clock }
	env.m.invocations.now = time.Now
	env.logs, env.audit = &logs.Buffer, &audit.Buffer
	return env
}

func (e *testEnv) create(t *testing.T, name string) *Created {
	t.Helper()
	c, err := e.m.Create(context.Background(), CreateRequest{Name: name, Runtime: "nodejs", RuntimeVersion: "22", Handler: "index.handler",
		Env: []EnvVar{{Name: "OUTPUT_BUCKET", Value: "thumbs"}}}, "admin@example.com")
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return c
}

func codeZip(t *testing.T, body string) []byte {
	return buildZip(t, zentry{name: "index.js", body: []byte(body)})
}

func (e *testEnv) deploy(t *testing.T, name, body string) *Deployment {
	t.Helper()
	d, err := e.m.Deploy(context.Background(), name, bytes.NewReader(codeZip(t, body)), "admin@example.com")
	if err != nil {
		t.Fatalf("deploy %s: %v", name, err)
	}
	return d
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if CodeOf(err) != code {
		t.Fatalf("error = %v (%q), want %s", err, CodeOf(err), code)
	}
}
