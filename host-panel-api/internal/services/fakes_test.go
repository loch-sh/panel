package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"go.yaml.in/yaml/v3"

	"host-panel-api/internal/docker"
	"host-panel-api/internal/networks"
)

// --- engine ---

type fakeEngine struct{ prov *fakeProvisioner }

func newFakeEngine() *fakeEngine { return &fakeEngine{prov: newFakeProvisioner()} }

func (*fakeEngine) ID() string                 { return "fake" }
func (*fakeEngine) Kind() Kind                 { return KindDatabase }
func (*fakeEngine) DefaultPort() int           { return 1234 }
func (*fakeEngine) Lifecycle() Lifecycle       { return OnDemand }
func (*fakeEngine) DataOwner() (int, int)      { return 999, 999 }
func (e *fakeEngine) Provisioner() Provisioner { return e.prov }
func (e *fakeEngine) Catalog() []Version {
	v2, v15 := e.VersionOf("2", "2"), e.VersionOf("1.5", "1.5")
	v2.SupportUntil, v15.SupportUntil = "2030-01-01", "2026-11-12"
	return []Version{v2, v15}
}
func (*fakeEngine) Releases() Releases {
	return Releases{Source: "docker.io/library/fake", Tag: regexp.MustCompile(`\A([0-9]+(?:\.[0-9]+)?)\z`), LineTag: "%s", EOL: "fake", Min: "1"}
}
func (*fakeEngine) VersionOf(version, tag string) Version {
	mount := "/data"
	if CompareVersions(version, "2") < 0 {
		mount = "/var/lib/fake"
	}
	return Version{Version: version, Image: "fake:" + tag, DataMount: mount}
}
func (*fakeEngine) Service(InstanceRef, Version, Options) ServiceSpec {
	return ServiceSpec{
		Command:     []string{"fake-server"},
		Healthcheck: []string{"CMD", "true"},
		Files:       []GeneratedFile{{Name: "fake.conf", Target: "/etc/fake.conf", Content: []byte("conf\n")}},
	}
}
func (*fakeEngine) TLS(InstanceRef, Version) (TLSSpec, bool) {
	return TLSSpec{Args: []string{"--tls"}, Files: []GeneratedFile{{Name: "fake-tls.conf", Target: "/etc/fake.conf", Content: []byte("tls\n")}}}, true
}

func (*fakeEngine) ConnectionInfo(p ConnParams) []Credential {
	return []Credential{{Key: "host", Value: p.Host}, {Key: "user", Value: p.Name}, {Key: "password", Value: p.Password, Secret: true}}
}

// fakeDedicatedEngine is a dedicated engine (one instance per resource, no
// provisioner): its password digest goes to a state file.
type fakeDedicatedEngine struct{}

func (fakeDedicatedEngine) ID() string               { return "kv" }
func (fakeDedicatedEngine) Kind() Kind               { return KindCache }
func (fakeDedicatedEngine) DefaultPort() int         { return 6379 }
func (fakeDedicatedEngine) Lifecycle() Lifecycle     { return Dedicated }
func (fakeDedicatedEngine) DataOwner() (int, int)    { return 999, 1000 }
func (fakeDedicatedEngine) Provisioner() Provisioner { return nil }
func (e fakeDedicatedEngine) Catalog() []Version {
	return []Version{e.VersionOf("8", "8.0.1"), e.VersionOf("7", "7.0.3")}
}

// Releases: exact patch tags ("8.0.1"), like Mosquitto or Loch S3.
func (fakeDedicatedEngine) Releases() Releases {
	return Releases{Source: "docker.io/library/kv", Tag: regexp.MustCompile(`\A([0-9]+)\.0\.([0-9]+)\z`), Min: "7"}
}
func (fakeDedicatedEngine) VersionOf(version, tag string) Version {
	return Version{Version: version, Image: "kv:" + tag, DataMount: "/data"}
}
func (fakeDedicatedEngine) Options() []Option {
	return []Option{
		{Key: "memory", Values: []string{"64", "128"}, Default: "64", Mutable: true},
		{Key: "policy", Values: []string{"noeviction", "allkeys-lru"}, Default: "noeviction", Mutable: true},
		{Key: "persistence", Values: []string{"aof", "none"}, Default: "aof"},
	}
}
func (fakeDedicatedEngine) ValidateName(name string) *Error {
	if name == "reserved" {
		return newError(CodeNameReserved, "name", "engine rule")
	}
	return nil
}
func (fakeDedicatedEngine) Service(_ InstanceRef, _ Version, o Options) ServiceSpec {
	mem, _ := strconv.Atoi(o["memory"])
	return ServiceSpec{
		Command:       []string{"kv-server", "--maxmemory", o["memory"] + "mb", "--policy", o["policy"]},
		Healthcheck:   []string{"CMD", "true"},
		User:          "999:1000",
		MemLimit:      int64(mem) << 20,
		CPUs:          1,
		NoAdminSecret: true,
		State:         true,
		Hardened:      true,
		Tmpfs:         []string{"/tmp:size=1m"},
		// A key generated once with the instance (like the S3 encryption key).
		GeneratedKeys: []string{"master.key"},
	}
}
func (fakeDedicatedEngine) PasswordDigest(password string) (string, error) {
	return hashOf([]byte(password)), nil
}
func (fakeDedicatedEngine) StateFiles(_ InstanceRef, _ Version, o Options, digest string) ([]GeneratedFile, error) {
	return []GeneratedFile{{Name: "users.acl", Content: []byte("user app #" + digest + " policy=" + o["policy"] + "\n")}}, nil
}
func (fakeDedicatedEngine) TLS(InstanceRef, Version) (TLSSpec, bool) {
	return TLSSpec{Args: []string{"--tls"}}, true
}
func (fakeDedicatedEngine) ConnectionInfo(p ConnParams) []Credential {
	scheme := "kv"
	if p.TLS {
		scheme = "kvs"
	}
	return []Credential{{Key: "host", Value: p.Host}, {Key: "password", Value: p.Password, Secret: true}, {Key: "uri", Value: scheme + "://" + p.Host, Secret: true}}
}

// fakeProvisioner keeps the resources of every instance in memory (keyed by
// container ID) and can fail chosen operations.
type fakeProvisioner struct {
	mu        sync.Mutex
	resources map[string]map[string]bool // container -> name -> managed
	inits     int
	fail      map[string]error // op -> error
	failNth   map[string]int   // op -> fail only on the n-th call (1-based); 0 = always
	calls     map[string]int
	passwords []string
	hook      func(op string) // called before each operation (concurrency tests)
}

func newFakeProvisioner() *fakeProvisioner {
	return &fakeProvisioner{resources: map[string]map[string]bool{}, fail: map[string]error{}, failNth: map[string]int{}, calls: map[string]int{}}
}

func (p *fakeProvisioner) step(op string) error {
	if p.hook != nil {
		p.hook(op)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[op]++
	if err, ok := p.fail[op]; ok && (p.failNth[op] == 0 || p.failNth[op] == p.calls[op]) {
		return err
	}
	return nil
}

func cid(t Target) string { return t.Executor.(executor).id }

func (p *fakeProvisioner) ValidateName(_ Version, name string) *Error {
	if name == "forbidden" {
		return newError(CodeNameReserved, "name", "engine rule")
	}
	return nil
}
func (p *fakeProvisioner) Init(_ context.Context, t Target) error {
	if err := p.step("init"); err != nil {
		return err
	}
	p.mu.Lock()
	p.inits++
	p.mu.Unlock()
	return nil
}
func (p *fakeProvisioner) List(_ context.Context, t Target) ([]Resource, error) {
	if err := p.step("list"); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []Resource
	for n, managed := range p.resources[cid(t)] {
		out = append(out, Resource{Name: n, Managed: managed})
	}
	slices.SortFunc(out, func(a, b Resource) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}
func (p *fakeProvisioner) Provision(_ context.Context, t Target, name, password string) error {
	err := p.step("provision")
	if err != nil && !errors.Is(err, ErrOutcomeUnknown) {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.resources[cid(t)] == nil {
		p.resources[cid(t)] = map[string]bool{}
	}
	if _, ok := p.resources[cid(t)][name]; ok {
		return ErrResourceExists
	}
	p.resources[cid(t)][name] = true
	p.passwords = append(p.passwords, password)
	return err
}
func (p *fakeProvisioner) Deprovision(_ context.Context, t Target, name string) error {
	if err := p.step("deprovision"); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.resources[cid(t)], name)
	return nil
}
func (p *fakeProvisioner) SetPassword(_ context.Context, t Target, name, password string) error {
	if err := p.step("set password"); err != nil {
		return err
	}
	p.mu.Lock()
	p.passwords = append(p.passwords, password)
	p.mu.Unlock()
	return nil
}

// forget drops the resources of a container whose data was deleted.
func (p *fakeProvisioner) forget(containerID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.resources, containerID)
}

func (p *fakeProvisioner) initCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inits
}

func (p *fakeProvisioner) add(containerID, name string, managed bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.resources[containerID] == nil {
		p.resources[containerID] = map[string]bool{}
	}
	p.resources[containerID][name] = managed
}

// --- docker and compose ---

// fakeDocker simulates the containers started by fakeRunner.
type fakeDocker struct {
	mu         sync.Mutex
	containers map[string]*fakeContainer // by ID
	extra      []container.Summary       // unrelated containers
	listErr    error
	// disconnects records "<network ID> <container ID>".
	disconnects []string
	// Images: tags (reference -> image ID), the digest of each image ID,
	// what the registry publishes (reference -> digest), pulls. An image
	// never pulled is present locally with a digest of its own
	// (localDigestOf), like an image pulled by tag before the test.
	tags    map[string]string
	digests map[string]string
	remote  map[string]string
	pulls   []string
	pullErr error
	// more: other digests an image carries for its repository.
	more map[string][]string
	// absent: references reported absent by InspectImage (until pulled).
	absent map[string]bool
}

// localDigestOf is the digest of the image a reference names before any pull.
func localDigestOf(ref string) string {
	sum := sha256.Sum256([]byte(ref))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// fakeRepoOf returns the repository of a fake image ID.
func fakeRepoOf(id string) string {
	repo := strings.TrimPrefix(id, "sha256:")
	repo, _, _ = strings.Cut(repo, "#")
	repo, _, _ = strings.Cut(repo, "@")
	repo, _, _ = strings.Cut(repo, ":")
	return repo
}

// fakeNetworks is the network registry of the tests: the shared network and
// a private one, "jobs"; broken networks fail Verify.
type fakeNetworks struct {
	mu     sync.Mutex
	refs   map[string]networks.Ref
	broken map[string]bool
}

func newFakeNetworks() *fakeNetworks {
	return &fakeNetworks{refs: map[string]networks.Ref{
		networks.SharedName: {Name: networks.SharedName, DockerName: "loch-services", ID: "net-shared", System: true},
		"jobs":              {Name: "jobs", DockerName: networks.DockerPrefix + "jobs", ID: "net-jobs", Subnet: "10.20.0.0/24"},
	}, broken: map[string]bool{}}
}

func (f *fakeNetworks) Lookup(name string) (networks.Ref, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.refs[name]
	return r, ok
}

func (f *fakeNetworks) Verify(_ context.Context, name string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.refs[name]
	return r.ID, ok && !f.broken[name]
}

type fakeContainer struct {
	summary container.Summary
	inspect container.InspectResponse
}

func (d *fakeDocker) ListContainers(context.Context) ([]container.Summary, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.listErr != nil {
		return nil, d.listErr
	}
	out := slices.Clone(d.extra)
	for _, c := range d.containers {
		out = append(out, c.summary)
	}
	return out, nil
}

// fakeImageEnv is the environment of every fake image.
var fakeImageEnv = []string{"PATH=/usr/local/bin:/usr/bin:/bin"}

func (d *fakeDocker) InspectImage(_ context.Context, ref string) (image.InspectResponse, error) {
	d.mu.Lock()
	gone := d.absent[ref]
	d.mu.Unlock()
	if gone {
		return image.InspectResponse{}, errors.New("no such image")
	}
	id := ref
	if !strings.HasPrefix(ref, "sha256:") {
		id = d.imageID(ref)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	res := image.InspectResponse{ID: id, Config: &dockerspec.DockerOCIImageConfig{ImageConfig: ocispec.ImageConfig{Env: fakeImageEnv}}}
	if dg := d.digests[id]; dg != "" {
		res.RepoDigests = []string{fakeRepoOf(id) + "@" + dg}
	}
	for _, dg := range d.more[id] {
		res.RepoDigests = append(res.RepoDigests, fakeRepoOf(id)+"@"+dg)
	}
	return res, nil
}

func (d *fakeDocker) InspectContainer(_ context.Context, id string) (container.InspectResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.containers[id]
	if !ok {
		return container.InspectResponse{}, errors.New("no such container")
	}
	return c.inspect, nil
}

func (d *fakeDocker) RemoveContainer(_ context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.containers[id]; ok {
		delete(d.containers, id)
		return nil
	}
	for i, c := range d.extra {
		if c.ID == id {
			d.extra = append(d.extra[:i], d.extra[i+1:]...)
			return nil
		}
	}
	return errors.New("no such container")
}
func (d *fakeDocker) RemoveNetwork(context.Context, string) error { return nil }
func (d *fakeDocker) DisconnectNetwork(_ context.Context, networkID, containerID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.disconnects = append(d.disconnects, networkID+" "+containerID)
	if c, ok := d.containers[containerID]; ok && c.inspect.NetworkSettings != nil {
		for name, ep := range c.inspect.NetworkSettings.Networks {
			if ep.NetworkID == networkID {
				delete(c.inspect.NetworkSettings.Networks, name)
			}
		}
	}
	return nil
}

// PullImage moves the local tag to the image the registry publishes.
func (d *fakeDocker) PullImage(_ context.Context, ref, _ string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pulls = append(d.pulls, ref)
	if d.pullErr != nil {
		return d.pullErr
	}
	delete(d.absent, ref)
	// By digest ("repo@sha256:..."): the image carrying that digest (an
	// image already present locally, or a new one).
	if repo, digest, ok := strings.Cut(ref, "@"); ok {
		for id, dg := range d.digests {
			if dg == digest && fakeRepoOf(id) == repo {
				d.tags[ref] = id
				return nil
			}
		}
		id := "sha256:" + repo + "#" + digest
		d.tags[ref] = id
		d.digests[id] = digest
		return nil
	}
	if remote, ok := d.remote[ref]; ok {
		d.tags[ref] = "sha256:" + ref + "@" + remote
		d.digests[d.tags[ref]] = remote
	}
	return nil
}

// imageID returns the local image of a reference (its own ID until pulled,
// with a digest of its own). A pinned reference ("repo:tag@digest", or
// "repo@digest") is the image carrying that digest, like Docker resolves
// it: the tag is ignored.
func (d *fakeDocker) imageID(ref string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if id, ok := d.tags[ref]; ok {
		return id
	}
	if named, digest, ok := strings.Cut(ref, "@"); ok {
		repo := named
		if i := strings.LastIndex(named, ":"); i >= 0 {
			repo = named[:i]
		}
		if id, ok := d.tags[repo+"@"+digest]; ok {
			return id
		}
		for id, dg := range d.digests {
			if dg == digest && fakeRepoOf(id) == repo {
				return id
			}
		}
		return "sha256:unknown-" + ref
	}
	id := "sha256:" + ref
	if _, ok := d.digests[id]; !ok {
		d.digests[id] = localDigestOf(ref)
	}
	return id
}

func (d *fakeDocker) ExecRun(context.Context, string, docker.ExecRequest) (docker.ExecResult, error) {
	return docker.ExecResult{}, errors.New("fake provisioner does not exec")
}

func (d *fakeDocker) byProject(project string) *fakeContainer {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.byProjectLocked(project)
}

func (d *fakeDocker) byProjectLocked(project string) *fakeContainer {
	for _, c := range d.containers {
		if c.summary.Labels[composeProjectLabel] == project {
			return c
		}
	}
	return nil
}

// fakeRunner records compose commands and starts/stops fake containers that
// look exactly like the ones compose would create.
type fakeRunner struct {
	mu     sync.Mutex
	calls  []string // "<project> <args>"
	docker *fakeDocker
	reg    *Registry
	cfg    RenderConfig
	fail   map[string]error // first arg -> error
	health container.HealthStatus
	prov   *fakeProvisioner
	// failUpWhen makes `up` fail for some overrides (e.g. port taken).
	failUpWhen func(AppliedExposure) bool
	restarts   int
	failTimes  map[string]int // first arg -> fail the next n calls
}

func (r *fakeRunner) Run(_ context.Context, dir string, args ...string) (string, error) {
	project := filepath.Base(dir)
	r.mu.Lock()
	r.calls = append(r.calls, project+" "+strings.Join(args, " "))
	err := r.fail[args[0]]
	if n := r.failTimes[args[0]]; n > 0 {
		r.failTimes[args[0]] = n - 1
		err = errors.New("transient failure")
	}
	r.mu.Unlock()
	if err != nil {
		return "compose output", err
	}
	switch args[0] {
	case "up":
		key := strings.TrimPrefix(project, InstancePrefix)
		e, v, shared := r.reg.ByKey(key)
		var ref InstanceRef
		var opts Options
		if shared {
			ref = InstanceRef{Engine: e.ID(), Version: v.Version}
		} else {
			// Like compose: read the file (dedicated: version and options).
			data, _ := os.ReadFile(filepath.Join(dir, ComposeFile))
			man, _, _ := ParseManifest(data)
			var name string
			e, name, _ = r.reg.DedicatedByKey(key)
			v, _ = r.reg.Version(e.ID(), man.EngineVersion)
			ref = InstanceRef{Engine: e.ID(), Version: v.Version, Name: name}
			opts = man.Options
		}
		// Like compose: the image of the file, as tagged locally.
		if data, err := os.ReadFile(filepath.Join(dir, ComposeFile)); err == nil {
			var doc composeFile
			if yaml.Unmarshal(data, &doc) == nil {
				if svc, ok := doc.Services[serviceName]; ok && svc.Image != "" {
					v.Image = svc.Image
				}
			}
		}
		spec := e.Service(ref, v, opts)
		health := r.health
		if health == "" {
			health = container.Healthy
		}
		labels := map[string]string{
			composeProjectLabel: project, composeServiceLabel: serviceName,
			composeWorkingDirLabel: dir, LabelInstance: key, LabelService: e.ID(),
		}
		id := "id-" + project
		// Like initdb: an empty data directory starts a fresh engine, with
		// no resources; existing data keeps them.
		marker := filepath.Join(r.cfg.DataPath(ref), "ENGINE_DATA")
		if _, err := os.Lstat(marker); err != nil {
			if err := os.WriteFile(marker, []byte("data\n"), 0o600); err != nil {
				return "", err
			}
			r.prov.forget(id)
		}
		// Like compose, merge the override: published port, TLS command and mounts.
		var applied AppliedExposure
		if data, err := os.ReadFile(filepath.Join(dir, OverrideFile)); err == nil {
			applied, _ = parseOverride(data)
		}
		eff := effective(e, ref, v, opts, applied.TLS, applied.TLSOptional)
		inst := &instance{ref: ref, engine: e, version: v, opts: opts, dir: dir}
		var mounts []container.MountPoint
		for target, source := range expectedMounts(inst, r.cfg.DataPath(ref), eff, r.cfg) {
			mounts = append(mounts, container.MountPoint{Type: mount.TypeBind, Source: source, Destination: target})
		}
		bindings := network.PortMap{}
		if exp, _ := applied.exposure(); exp != nil {
			bindings[network.MustParsePort(strconv.Itoa(e.DefaultPort())+"/tcp")] = []network.PortBinding{{HostIP: exp.Address, HostPort: strconv.Itoa(exp.Port)}}
		}
		r.mu.Lock()
		upFails := r.failUpWhen != nil && r.failUpWhen(applied)
		r.mu.Unlock()
		if upFails {
			return "port is already allocated", errors.New("exit status 1")
		}
		// Like compose: the networks of compose.yaml (and the "pub" network
		// of the override), each with the instance alias.
		eps := map[string]*network.EndpointSettings{}
		if data, err := os.ReadFile(filepath.Join(dir, ComposeFile)); err == nil {
			var doc composeFile
			if yaml.Unmarshal(data, &doc) == nil {
				for key, n := range doc.Networks {
					ref, _ := r.cfg.Networks.Lookup(key)
					eps[n.Name] = &network.EndpointSettings{NetworkID: ref.ID, Aliases: []string{project}}
				}
			}
		}
		if exp, _ := applied.exposure(); exp != nil {
			eps[project+"_"+pubNetworkKey] = &network.EndpointSettings{NetworkID: "pub-" + project, Aliases: []string{project}}
		}
		imageID := r.docker.imageID(v.Image)
		r.docker.mu.Lock()
		r.docker.containers[id] = &fakeContainer{
			summary: container.Summary{ID: id, Names: []string{"/" + project}, Labels: labels, State: container.StateRunning},
			inspect: container.InspectResponse{
				Name:   "/" + project,
				State:  &container.State{Status: container.StateRunning, Running: true, Health: &container.Health{Status: health}},
				Image:  imageID,
				Config: &container.Config{Image: v.Image, Labels: labels, Cmd: eff.command, User: spec.User, Env: fakeEnv(spec)},
				HostConfig: &container.HostConfig{NetworkMode: "loch-services", PortBindings: bindings,
					SecurityOpt:    []string{"no-new-privileges:true"},
					Resources:      container.Resources{Memory: spec.MemLimit, MemorySwap: spec.MemLimit, NanoCPUs: int64(spec.CPUs * 1e9), PidsLimit: pidsOf(spec)},
					ReadonlyRootfs: spec.Hardened, CapDrop: capDrop(spec), Tmpfs: tmpfsMap(spec.Tmpfs)},
				Mounts:          mounts,
				NetworkSettings: &container.NetworkSettings{Networks: eps},
			},
		}
		r.docker.mu.Unlock()
	case "stop":
		r.docker.mu.Lock()
		if c := r.docker.containers["id-"+project]; c != nil {
			c.inspect.State.Running = false
			c.inspect.State.Status = container.StateExited
			c.summary.State = container.StateExited
		}
		r.docker.mu.Unlock()
	case "restart":
		r.mu.Lock()
		r.restarts++
		r.mu.Unlock()
	case "down":
		r.docker.mu.Lock()
		delete(r.docker.containers, "id-"+project)
		r.docker.mu.Unlock()
	}
	return "", nil
}

func (r *fakeRunner) commands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

func (r *fakeRunner) count(prefix string) int {
	n := 0
	for _, c := range r.commands() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// --- manager ---

type testEnv struct {
	m      *Manager
	engine *fakeEngine
	prov   *fakeProvisioner
	runner *fakeRunner
	docker *fakeDocker
	cfg    Config
	audit  *strings.Builder
	logs   *strings.Builder
}

type syncBuilder struct {
	mu sync.Mutex
	b  *strings.Builder
}

func (s *syncBuilder) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	root := t.TempDir()
	cfg := Config{
		StacksDir: filepath.Join(root, "compose"),
		Render:    RenderConfig{DataDir: filepath.Join(root, "services"), Networks: newFakeNetworks()},
		Exposure: ExposurePolicy{AllowedCIDRs: []netip.Prefix{
			netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("203.0.113.0/24"),
		}, ReservedPorts: []int{2222}},
	}
	must(t, os.Mkdir(cfg.StacksDir, 0o755))
	must(t, os.Mkdir(cfg.Render.DataDir, 0o755))
	e := newFakeEngine()
	reg, err := NewRegistry([]Engine{e, fakeDedicatedEngine{}}, nil, nil, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDocker{containers: map[string]*fakeContainer{}, tags: map[string]string{}, digests: map[string]string{}, remote: map[string]string{}, absent: map[string]bool{}, more: map[string][]string{}}
	r := &fakeRunner{docker: d, reg: reg, cfg: cfg.Render, fail: map[string]error{}, failTimes: map[string]int{}, prov: e.prov}
	audit, logs := &strings.Builder{}, &strings.Builder{}
	m := NewManager(cfg, reg, r, d,
		slog.New(slog.NewTextHandler(&syncBuilder{b: logs}, nil)),
		slog.New(slog.NewTextHandler(&syncBuilder{b: audit}, nil)), nil)
	m.chown = func(string, int, int) error { return nil }
	return &testEnv{m: m, engine: e, prov: e.prov, runner: r, docker: d, cfg: m.cfg, audit: audit, logs: logs}
}

var ref2 = InstanceRef{Engine: "fake", Version: "2"}

// setDesired records an exposure in the reference of an instance, as
// SetExposure would, without applying it.
func (env *testEnv) setDesired(t *testing.T, key string, exp *Exposure) {
	t.Helper()
	inst, err := env.m.load(key)
	if err != nil {
		t.Fatal(err)
	}
	r := inst.reference()
	r.Exposure = recordOf(exp)
	if err := env.m.writeReference(inst.ref, r); err != nil {
		t.Fatal(err)
	}
}

// expose publishes an instance through SetExposure.
func (env *testEnv) expose(t *testing.T, key string, exp Exposure) {
	t.Helper()
	if _, err := env.m.SetExposure(context.Background(), key, &exp, "admin"); err != nil {
		t.Fatalf("expose %s: %v", key, err)
	}
}

func (env *testEnv) instanceDir() string { return filepath.Join(env.cfg.StacksDir, ref2.Project()) }
func (env *testEnv) dataDir() string     { return env.cfg.Render.DataPath(ref2) }

func mustCreate(t *testing.T, env *testEnv, name string) *CreatedResource {
	t.Helper()
	res, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: name}, "admin@example.com")
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return res
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil || CodeOf(err) != code {
		t.Fatalf("err = %v (%s), want %s", err, CodeOf(err), code)
	}
}

// must fails the test on a setup error.
func must(tb testing.TB, err error) {
	tb.Helper()
	if err != nil {
		tb.Fatal(err)
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// testNow is the fixed clock of the tests (version 1.5 of the fake engine
// is deprecated, version 2 is not).
var testNow = time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)

func tmpfsMap(list []string) map[string]string {
	if len(list) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, t := range list {
		p, o, _ := strings.Cut(t, ":")
		out[p] = o
	}
	return out
}

func fakeEnv(spec ServiceSpec) []string {
	env := slices.Clone(fakeImageEnv)
	for k, v := range spec.Environment {
		env = append(env, k+"="+v)
	}
	return env
}

func pidsOf(spec ServiceSpec) *int64 {
	n := int64(spec.PidsLimit)
	if n == 0 {
		n = defaultPidsLimit
	}
	return &n
}
