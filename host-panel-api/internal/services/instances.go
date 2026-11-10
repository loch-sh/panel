package services

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"

	"host-panel-api/internal/composekit"
	"host-panel-api/internal/docker"
	"host-panel-api/internal/releases"
)

const (
	composeProjectLabel    = "com.docker.compose.project"
	composeServiceLabel    = "com.docker.compose.service"
	composeWorkingDirLabel = "com.docker.compose.project.working_dir"
)

// instance is a managed instance found on disk.
type instance struct {
	ref     InstanceRef
	engine  Engine
	version Version
	opts    Options // dedicated instances only
	dir     string
	// networks and desired (the exposure chosen in the panel) come from the
	// reference in the private directory.
	networks []string
	desired  *Exposure
	modified bool // compose or generated files differ from what the panel writes
	// applied is the exposure recorded in the override, i.e. what the
	// running container is expected to match.
	applied AppliedExposure
	// exposureStale: the override is not the one rendered from the trusted
	// exposure (desired) — written by CI, or not applied yet. Only the
	// exposure path (applyExposureLocked) may run compose then.
	exposureStale bool
	// unpinnedCompose: the only difference with the panel's files is that
	// compose.yaml is the rendering without the pinned digest (an
	// interrupted PinImages, or CI). The instance is modified (no
	// operation runs compose on it); only PinImages repairs it, at the next
	// start, after checking what the container runs.
	unpinnedCompose bool
}

// runtime is the Docker state of an instance.
type runtime struct {
	containerID string
	// imageID and repoDigests: the image the container runs (an update
	// compares them with the registry).
	imageID     string
	repoDigests []string
	state       string // container state, "missing" when absent
	health      string
	ready       bool
	problem     string // why the container is not trusted, logged only
}

func (m *Manager) instanceDir(ref InstanceRef) string {
	return filepath.Join(m.cfg.StacksDir, ref.Project())
}

func instanceNotFound() *Error {
	return newError(CodeInstanceNotFound, "instance", "service instance not found")
}

// storageError logs err (paths only, never secrets) and returns a generic error.
func (m *Manager) storageError(err error) *Error {
	m.logger.Error("services: storage error", "error", err)
	return &Error{Code: CodeStorage, Message: "storage error"}
}

// load reads a managed instance by key. Only directories carrying the panel
// marker and a valid x-loch section for an enabled engine and version are
// managed: the stacks directory is shared with CI, its content is untrusted.
func (m *Manager) load(key string) (*instance, *Error) {
	e, v, shared := m.reg.ByKey(key)
	ref := InstanceRef{}
	tampered := false
	if shared {
		ref = InstanceRef{Engine: e.ID(), Version: v.Version}
	} else {
		de, name, ok := m.reg.DedicatedByKey(key)
		if !ok {
			return nil, instanceNotFound()
		}
		e, ref = de, InstanceRef{Engine: de.ID(), Name: name}
	}
	dir := m.instanceDir(ref)
	if composekit.RealDir(dir) != nil || !composekit.IsRegularFile(filepath.Join(dir, composekit.ManagedMarker)) {
		return nil, instanceNotFound()
	}
	data, err := composekit.ReadFileNoFollow(filepath.Join(dir, ComposeFile))
	if err != nil {
		return nil, instanceNotFound()
	}
	manifest, project, err := ParseManifest(data)
	invalid := err != nil || manifest == nil || project != ref.Project() ||
		manifest.Engine != e.ID() || manifest.Kind != e.Kind() || manifest.Name != ref.Name
	var opts Options
	if !invalid && shared {
		invalid = manifest.EngineVersion != v.Version || len(manifest.Options) != 0
	} else if !invalid {
		// Dedicated: the version and options come from the manifest, and
		// must be an enabled version and valid options.
		// An instance with a recorded image does not depend on the
		// discovered catalog (it may be gone, or lag behind the image).
		var known bool
		if rr, rerr := m.readReference(InstanceRef{Engine: e.ID(), Name: ref.Name}); rerr == nil && rr.Image != "" {
			v, known = m.reg.VersionImage(e.ID(), manifest.EngineVersion, rr.Image)
		}
		if !known {
			// No recorded image, or one that is not of this version (then
			// marked modified below, never run).
			v, known = m.reg.Version(e.ID(), manifest.EngineVersion)
		}
		opts = manifest.Options
		invalid = !known || !validOptions(e.(DedicatedEngine).Options(), opts)
		ref.Version = manifest.EngineVersion
	}
	if invalid {
		m.logger.Warn("services: ignoring instance directory with an invalid x-loch section", "instance", key)
		return nil, instanceNotFound()
	}
	// The trusted values are the panel's record; a manifest that differs was
	// rewritten (modified: never run by the panel). The networks and the
	// exposure come from the record only.
	r, rerr := m.readReference(ref)
	desired, expOK := r.exposure()
	if rerr != nil || !r.sameInstance(ref, opts) || !expOK {
		tampered = true
	}
	if !shared && rerr == nil {
		// The image is the recorded one: the catalog may have moved to a
		// newer patch tag since (an update the user starts).
		if r.Image != "" {
			if rv, ok := m.reg.VersionImage(e.ID(), ref.Version, r.Image); ok {
				v = rv
			} else {
				tampered = true
			}
		} else if fv, ok := m.reg.fallbackVersion(e.ID(), ref.Version); ok {
			// Recorded before images were: created from the built-in
			// catalog (RecordImages writes it at startup).
			v = fv
		}
		// The digest pins the recorded image (only with one, and only a
		// well-formed sha256 digest).
		switch {
		case r.Digest == "":
		case r.Image == "" || !releases.ValidDigest(r.Digest):
			tampered = true
		default:
			v.Digest = r.Digest
		}
	}
	inst := &instance{ref: ref, engine: e, version: v, opts: opts, dir: dir, networks: r.Networks, desired: desired}
	inst.modified = tampered || m.isModified(inst, data, manifest.Version)
	if over, err := composekit.ReadFileNoFollow(filepath.Join(dir, OverrideFile)); err == nil {
		if applied, ok := parseOverride(over); ok {
			if _, valid := applied.exposure(); valid {
				inst.applied = applied
			}
		}
		// The override is only trusted when it is what the panel renders
		// from the recorded exposure (a self-consistent override written by
		// CI could publish anything).
		want, rerr := RenderOverride(e, ref, v, opts, desired, m.tlsRequired(e, ref, v, desired))
		inst.exposureStale = rerr != nil || !bytes.Equal(want, over)
	} else {
		inst.exposureStale = true
	}
	return inst, nil
}

// isModified compares the files on disk with what the panel would write. A
// file written by an older panel version is not reported as modified.
func (m *Manager) isModified(inst *instance, compose []byte, version int) bool {
	// A manifest of another version is never trusted: no older layout
	// exists, and accepting one would exempt any compose file from the
	// check (the stacks directory is writable by CI). A future layout
	// change must rewrite the files (migration), not trust them.
	if version != ManifestVersion {
		return true
	}
	want, err := Render(inst.engine, inst.ref, inst.version, inst.opts, inst.networks, m.cfg.Render)
	if err != nil {
		return true
	}
	unpinned := false
	if !bytes.Equal(want, compose) {
		// The rendering without the digest (PinImages writes the reference,
		// then compose.yaml; or CI wrote it back) would run the shared tag:
		// the instance is modified, but only PinImages may repair it.
		if inst.version.Digest == "" {
			return true
		}
		bare := inst.version
		bare.Digest = ""
		old, uerr := Render(inst.engine, inst.ref, bare, inst.opts, inst.networks, m.cfg.Render)
		if uerr != nil || !bytes.Equal(old, compose) {
			return true
		}
		unpinned = true
	}
	if inst.engine.Lifecycle() == Dedicated && composekit.RealDir(m.cfg.Render.StatePath(inst.ref)) != nil {
		return true
	}
	if !m.overrideIntact(inst) {
		return true
	}
	for _, f := range inst.engine.Service(inst.ref, inst.version, inst.opts).Files {
		got, err := composekit.ReadFileNoFollow(filepath.Join(inst.dir, f.Name))
		if err != nil || !bytes.Equal(got, f.Content) {
			return true
		}
	}
	// Set only when the compose file is the only difference.
	inst.unpinnedCompose = unpinned
	return unpinned
}

// loadAll returns every managed instance of an enabled engine and version.
func (m *Manager) loadAll() ([]*instance, error) {
	entries, err := os.ReadDir(m.cfg.StacksDir)
	if err != nil {
		return nil, err
	}
	var out []*instance
	for _, ent := range entries {
		key, ok := strings.CutPrefix(ent.Name(), InstancePrefix)
		if !ok || !ent.IsDir() || ent.Type()&os.ModeSymlink != 0 {
			continue
		}
		if inst, err := m.load(key); err == nil {
			out = append(out, inst)
		}
	}
	return out, nil
}

// inspect finds and verifies the container of an instance. The container is
// trusted only when everything matches what the panel would run: compose
// labels can be set by any stack, so a container merely carrying them could
// otherwise receive the SQL (and the passwords) meant for the instance.
func (m *Manager) inspect(ctx context.Context, inst *instance, all []container.Summary) runtime {
	project := inst.ref.Project()
	var matches []container.Summary
	for _, c := range all {
		if c.Labels[composeProjectLabel] == project {
			matches = append(matches, c)
		}
	}
	switch len(matches) {
	case 0:
		return runtime{state: "missing"}
	case 1:
	default:
		return runtime{state: "conflict", problem: "several containers in the instance project"}
	}
	c := matches[0]
	rt := runtime{containerID: c.ID, state: string(c.State)}
	if c.Labels[composeServiceLabel] != serviceName {
		rt.problem = "unexpected service in the instance project"
		return rt
	}
	info, err := m.docker.InspectContainer(ctx, c.ID)
	if err != nil {
		rt.problem = "inspect failed"
		return rt
	}
	if info.State != nil {
		rt.state = string(info.State.Status)
		if info.State.Health != nil {
			rt.health = string(info.State.Health.Status)
		}
	}
	img, ierr := m.docker.InspectImage(ctx, info.Image)
	if ierr != nil {
		rt.problem = "image inspect failed"
		return rt
	}
	rt.imageID, rt.repoDigests = info.Image, img.RepoDigests
	if inst.exposureStale {
		rt.problem = "published port not applied yet (compose.override.yaml differs from the recorded exposure)"
		return rt
	}
	if p := verifyContainer(inst, info, img, m.cfg.Render); p != "" {
		rt.problem = p
		return rt
	}
	for _, name := range inst.networks {
		if _, ok := m.cfg.Render.Networks.Verify(ctx, name); !ok {
			rt.problem = "network " + name + " is missing or not the one the panel created"
			return rt
		}
	}
	rt.ready = info.State != nil && info.State.Running && rt.health == string(container.Healthy)
	return rt
}

// verifyContainer returns why a container is not the instance container ("" when it is).
// Labels can be forged by any stack, so the configuration that matters is
// checked: exact name, image, command, mounts and published port, and no
// other host-level access.
func verifyContainer(inst *instance, info container.InspectResponse, img image.InspectResponse, cfg RenderConfig) string {
	dataPath := cfg.DataPath(inst.ref)
	if info.Name != "/"+inst.ref.Project() {
		return "unexpected container name"
	}
	if info.Config == nil || !verifyImage(inst.version, info.Config.Image, img) {
		return "unexpected image"
	}
	if info.Config.Labels[LabelInstance] != inst.ref.Key() || info.Config.Labels[composeWorkingDirLabel] != inst.dir {
		return "unexpected labels"
	}
	spec := inst.engine.Service(inst.ref, inst.version, inst.opts)
	eff := effective(inst.engine, inst.ref, inst.version, inst.opts, inst.applied.TLS, inst.applied.TLSOptional)
	if len(eff.command) > 0 && !slices.Equal(info.Config.Cmd, eff.command) {
		return "unexpected command"
	}
	if spec.User != "" && info.Config.User != spec.User {
		return "unexpected user"
	}
	// Commands sent to the container (passwords on stdin) inherit its
	// entrypoint-free exec environment: PATH, LD_* and the entrypoint must
	// be the image's.
	if p := verifyImageConfig(info.Config, img, spec); p != "" {
		return p
	}
	hc := info.HostConfig
	if hc == nil || hc.Privileged || len(hc.CapAdd) > 0 ||
		hc.NetworkMode.IsHost() || hc.NetworkMode.IsContainer() || hc.PidMode != "" || hc.IpcMode.IsHost() || hc.UsernsMode.IsHost() {
		return "unexpected host configuration"
	}
	if spec.Hardened && (!hc.ReadonlyRootfs || !slices.Contains(hc.CapDrop, "ALL")) {
		return "unexpected privileges"
	}
	if p := verifyHostExtras(hc, spec); p != "" {
		return p
	}
	if p := verifyNetworks(info, inst, cfg); p != "" {
		return p
	}
	if !tmpfsMatch(hc.Tmpfs, spec.Tmpfs) {
		return "unexpected tmpfs"
	}
	// Without swap accounting, Docker reports no swap limit (-1).
	if hc.Memory != spec.MemLimit || hc.NanoCPUs != int64(math.Round(spec.CPUs*1e9)) ||
		(spec.MemLimit > 0 && hc.MemorySwap != spec.MemLimit && hc.MemorySwap != -1) {
		return "unexpected resource limits"
	}
	exp, _ := inst.applied.exposure()
	if !portsMatch(hc.PortBindings, exp, inst.engine.DefaultPort()) {
		return "unexpected published ports"
	}
	want := expectedMounts(inst, dataPath, eff, cfg)
	seen := 0
	for _, mp := range info.Mounts {
		src, ok := want[mp.Destination]
		if !ok || mp.Type != mount.TypeBind || mp.Source != src {
			return "unexpected mount " + mp.Destination
		}
		// Only the data directory is writable.
		if mp.RW && mp.Destination != inst.version.DataMount {
			return "writable mount " + mp.Destination
		}
		seen++
	}
	if seen != len(want) {
		return "missing mount"
	}
	return ""
}

// verifyImage reports whether a container runs the image of a version.
// Unpinned: the container was created from the tag. Pinned: the image the
// container runs (img, inspected by its content ID) must carry the pinned
// digest for the repository of the tag; the container may have been
// created from the pinned reference or, before the digest was recorded
// (PinImages), from the tag alone.
//
// The container may name another digest than the pinned one ("repo:tag@d"
// or "repo@d"), e.g. created before an update found it already on the
// latest image under another of its digests: it is accepted when the
// image carries both (an image may carry several digests for its
// repository; each one names that image).
func verifyImage(v Version, configured string, img image.InspectResponse) bool {
	if v.Digest == "" {
		return configured == v.Image
	}
	repo, _, ok := cutImageTag(v.Image)
	if !ok {
		return false
	}
	carried := repoDigestsOf(repo, img.RepoDigests)
	if !slices.Contains(carried, v.Digest) {
		return false
	}
	if configured == v.Image {
		return true
	}
	named, d, pinned := strings.Cut(configured, "@")
	return pinned && (named == v.Image || named == repo) && releases.ValidDigest(d) && slices.Contains(carried, d)
}

// tmpfsMatch reports whether the tmpfs mounts are exactly the expected
// ones, options included ("path:options").
func tmpfsMatch(got map[string]string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, w := range want {
		path, opts, _ := strings.Cut(w, ":")
		if o, ok := got[path]; !ok || o != opts {
			return false
		}
	}
	return true
}

// portsMatch reports whether the published ports are exactly the expected
// exposure (none when exp is nil). Docker may list an unspecified address
// once per family.
func portsMatch(bindings network.PortMap, exp *Exposure, port int) bool {
	if exp == nil {
		return len(bindings) == 0
	}
	count := 0
	for p, list := range bindings {
		if len(list) == 0 {
			continue
		}
		if int(p.Num()) != port || p.Proto() != network.TCP {
			return false
		}
		for _, b := range list {
			if b.HostPort != strconv.Itoa(exp.Port) {
				return false
			}
			if b.HostIP != exp.Address && !(exp.Address.IsUnspecified() && b.HostIP.IsUnspecified()) {
				return false
			}
			count++
		}
	}
	return count > 0
}

// ready returns the verified runtime of an instance, or CodeNotReady.
func (m *Manager) ready(ctx context.Context, inst *instance) (runtime, *Error) {
	all, err := m.docker.ListContainers(ctx)
	if err != nil {
		m.logger.Error("services: listing containers", "error", err)
		return runtime{}, newError(CodeNotReady, "", "the instance state cannot be read")
	}
	rt := m.inspect(ctx, inst, all)
	if rt.problem != "" {
		m.logger.Warn("services: refusing an untrusted instance container", "instance", inst.ref.Key(), "problem", rt.problem)
	}
	if !rt.ready {
		return rt, newError(CodeNotReady, "", "the instance is not running or not healthy")
	}
	return rt, nil
}

// target binds a provisioner to the verified container of an instance.
func (m *Manager) target(inst *instance, rt runtime) Target {
	return Target{Version: inst.version, Executor: executor{m.docker, rt.containerID}}
}

// executor runs commands in a verified container.
type executor struct {
	docker DockerAPI
	id     string
}

func (x executor) Exec(ctx context.Context, req docker.ExecRequest) (docker.ExecResult, error) {
	return x.docker.ExecRun(ctx, x.id, req)
}

// checkNameFree verifies that no directory, compose project or container
// already uses the name of a new instance.
func (m *Manager) checkNameFree(ctx context.Context, ref InstanceRef) *Error {
	project := ref.Project()
	if _, err := os.Lstat(m.instanceDir(ref)); !errors.Is(err, os.ErrNotExist) {
		return newError(CodeDirectoryConflict, "", "a directory with the instance name already exists in the stacks directory")
	}
	all, err := m.docker.ListContainers(ctx)
	if err != nil {
		return m.storageError(err)
	}
	for _, c := range all {
		if c.Labels[composeProjectLabel] == project || containerHasName(c, project) {
			return newError(CodeDirectoryConflict, "", "a container or compose project already uses the instance name")
		}
	}
	return nil
}

func containerNameOf(c container.Summary) string {
	if len(c.Names) == 0 {
		return c.ID
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

func containerHasName(c container.Summary, name string) bool {
	for _, n := range c.Names {
		if strings.TrimPrefix(n, "/") == name {
			return true
		}
	}
	return false
}

// verifyImageConfig: the entrypoint is the image's (the panel never sets
// one) and the environment is the image's plus the engine's, nothing else.
func verifyImageConfig(cfg *container.Config, img image.InspectResponse, spec ServiceSpec) string {
	var imgEnv []string
	var imgEntrypoint []string
	if img.Config != nil {
		imgEnv, imgEntrypoint = img.Config.Env, img.Config.Entrypoint
	}
	if !slices.Equal(cfg.Entrypoint, imgEntrypoint) {
		return "unexpected entrypoint"
	}
	allowed := map[string]bool{}
	for _, e := range imgEnv {
		allowed[e] = true
	}
	for k, v := range spec.Environment {
		allowed[k+"="+v] = true
	}
	seen := map[string]bool{}
	for _, e := range cfg.Env {
		if !allowed[e] {
			return "unexpected environment"
		}
		seen[e] = true
	}
	for k, v := range spec.Environment {
		if !seen[k+"="+v] {
			return "missing environment"
		}
	}
	return ""
}

// verifyHostExtras refuses what the panel never configures: devices,
// sysctls, other security options, runtimes, DNS or host entries, extra
// groups, another cgroup parent, a different process limit.
func verifyHostExtras(hc *container.HostConfig, spec ServiceSpec) string {
	if !slices.Equal(hc.SecurityOpt, []string{"no-new-privileges:true"}) {
		return "unexpected security options"
	}
	if len(hc.Devices) > 0 || len(hc.DeviceRequests) > 0 || len(hc.Sysctls) > 0 || len(hc.ExtraHosts) > 0 ||
		len(hc.DNS) > 0 || len(hc.GroupAdd) > 0 || hc.CgroupParent != "" || len(hc.Links) > 0 ||
		(hc.Runtime != "" && hc.Runtime != "runc") {
		return "unexpected host configuration"
	}
	pids := int64(spec.PidsLimit)
	if pids == 0 {
		pids = defaultPidsLimit
	}
	if hc.PidsLimit == nil || *hc.PidsLimit != pids {
		return "unexpected process limit"
	}
	return ""
}

// verifyNetworks: exactly the networks of the reference, by Docker ID, and
// the instance's own "pub" network when it is published; nothing else. Any
// stack can create a network with a given name: the ID is what the panel
// created (the live network is checked by Manager.inspect).
func verifyNetworks(info container.InspectResponse, inst *instance, cfg RenderConfig) string {
	if info.NetworkSettings == nil {
		return "no network settings"
	}
	refs, err := cfg.resolveNetworks(inst.networks)
	if err != nil {
		return "unknown network"
	}
	want := map[string]string{}
	for _, n := range refs {
		want[n.DockerName] = n.ID
	}
	if exp, _ := inst.applied.exposure(); exp != nil {
		want[inst.ref.Project()+"_"+pubNetworkKey] = ""
	}
	if len(info.NetworkSettings.Networks) != len(want) {
		return "unexpected networks"
	}
	for name, ep := range info.NetworkSettings.Networks {
		id, ok := want[name]
		if !ok {
			return "unexpected network " + name
		}
		if ep == nil || (id != "" && ep.NetworkID != id) {
			return "network " + name + " is not the one the panel created"
		}
		if !slices.Contains(ep.Aliases, inst.ref.Project()) && !slices.Contains(ep.DNSNames, inst.ref.Project()) {
			return "missing alias on " + name
		}
	}
	return ""
}

// overrideIntact reports whether compose.override.yaml is exactly what the
// panel renders for the exposure it records (docker compose loads it with
// compose.yaml: an edited override would be run as root).
func (m *Manager) overrideIntact(inst *instance) bool {
	data, err := composekit.ReadFileNoFollow(filepath.Join(inst.dir, OverrideFile))
	if err != nil {
		return false
	}
	applied, ok := parseOverride(data)
	if !ok {
		return false
	}
	exp, valid := applied.exposure()
	if !valid {
		return false
	}
	want, err := RenderOverride(inst.engine, inst.ref, inst.version, inst.opts, exp, applied.TLS)
	if err != nil || !bytes.Equal(want, data) {
		return false
	}
	if applied.TLS {
		for _, f := range []string{CertFile, KeyFile} {
			if !composekit.IsRegularFile(filepath.Join(inst.dir, CertsDir, f)) {
				return false
			}
		}
		for _, f := range tlsFiles(inst.engine, inst.ref, inst.version, true, applied.TLSOptional) {
			got, err := composekit.ReadFileNoFollow(filepath.Join(inst.dir, f.Name))
			if err != nil || !bytes.Equal(got, f.Content) {
				return false
			}
		}
	}
	return true
}
