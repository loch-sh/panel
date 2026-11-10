package functions

import (
	"bytes"
	"maps"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"

	"host-panel-api/internal/composekit"
)

const (
	composeProjectLabel    = "com.docker.compose.project"
	composeServiceLabel    = "com.docker.compose.service"
	composeWorkingDirLabel = "com.docker.compose.project.working_dir"
	composeNetworkLabel    = "com.docker.compose.network"
)

// CheckArtifacts verifies, before every compose up, that what the compose
// file mounts is what the reference pins: the runtime binary (re-hashed,
// trusted by this build) and the active deployment (read-only, tree hash).
// It returns the runtime binary path to render.
func CheckArtifacts(ref *Reference, p Paths, store RuntimeVerifier) (string, error) {
	name, nerr := ref.FunctionName()
	if nerr != nil {
		return "", nerr
	}
	runtimePath, err := store.Verify(ref.RuntimeBinary)
	if err != nil {
		return "", err
	}
	if err := p.VerifyDeployment(name, ref.Deployment); err != nil {
		return "", err
	}
	return runtimePath, nil
}

// RuntimeVerifier re-verifies a pinned runtime binary (RuntimeStore).
type RuntimeVerifier interface {
	Verify(b RuntimeBinary) (string, error)
}

// ComposeIntact reports whether the compose project of a function is
// exactly what the panel renders from its reference (with its managed
// marker): the stacks directory is writable by CI, an edited file is never
// run by the panel.
func ComposeIntact(ref *Reference, cfg RenderConfig) bool {
	name, nerr := ref.FunctionName()
	if nerr != nil {
		return false
	}
	dir := cfg.Paths.ProjectDir(name)
	if composekit.RealDir(dir) != nil || !composekit.IsRegularFile(filepath.Join(dir, composekit.ManagedMarker)) {
		return false
	}
	got, err := composekit.ReadFileNoFollow(filepath.Join(dir, ComposeFile))
	if err != nil {
		return false
	}
	want, err := Render(ref, cfg)
	return err == nil && bytes.Equal(got, want)
}

// VerifyContainer returns why a container is not the function container of
// a reference ("" when it is). Labels can be forged by any stack: the
// configuration that matters is compared with what the panel renders, and
// every host-level access the panel never configures is refused. egress is
// the inspected egress network of the container (nil when the function has
// no outbound access).
func VerifyContainer(ref *Reference, info container.InspectResponse, img image.InspectResponse, cfg RenderConfig, egress *network.Inspect) string {
	name, nerr := ref.FunctionName()
	if nerr != nil || ref.Deployment == nil || !ValidDeploymentID(ref.Deployment.ID) {
		return "invalid reference"
	}
	project := name.Project()
	if info.Name != "/"+project {
		return "unexpected container name"
	}
	c := info.Config
	if c == nil || info.HostConfig == nil {
		return "incomplete container configuration"
	}
	if p := verifyImage(ref, info, img); p != "" {
		return p
	}
	if p := verifyLabels(name, c.Labels, img, cfg); p != "" {
		return p
	}
	if !slices.Equal(c.Entrypoint, []string{RuntimeMount}) {
		return "unexpected entrypoint"
	}
	if !slices.Equal(c.Cmd, command(ref)) {
		return "unexpected command"
	}
	if c.User != functionUser {
		return "unexpected user"
	}
	var imgWorkdir string
	if img.Config != nil {
		imgWorkdir = img.Config.WorkingDir
	}
	if c.WorkingDir != imgWorkdir {
		return "unexpected working directory"
	}
	if p := verifyEnv(ref, c.Env, img); p != "" {
		return p
	}
	if p := verifyHealthcheck(c.Healthcheck); p != "" {
		return p
	}
	if p := verifyHost(ref, info.HostConfig); p != "" {
		return p
	}
	if p := verifyMounts(name, ref, info.Mounts, cfg); p != "" {
		return p
	}
	return verifyNetworks(name, ref, info, cfg, egress)
}

// verifyLabels: the labels are the image's, the rendered ones and the
// compose ones, nothing else; no Traefik label but the rendered
// traefik.enable=false (a router label would publish the function).
func verifyLabels(n Name, labels map[string]string, img image.InspectResponse, cfg RenderConfig) string {
	want := renderedLabels(n)
	for k, v := range want {
		if labels[k] != v {
			return "unexpected labels"
		}
	}
	if labels[composeProjectLabel] != n.Project() || labels[composeServiceLabel] != serviceName ||
		labels[composeWorkingDirLabel] != cfg.Paths.ProjectDir(n) {
		return "unexpected labels"
	}
	var imageLabels map[string]string
	if img.Config != nil {
		imageLabels = img.Config.Labels
	}
	for k, v := range labels {
		if w, ok := want[k]; ok && v == w {
			continue
		}
		if strings.HasPrefix(strings.ToLower(k), "traefik.") {
			return "unexpected label " + k
		}
		if strings.HasPrefix(k, "com.docker.compose.") {
			continue
		}
		if iv, ok := imageLabels[k]; ok && iv == v {
			continue
		}
		return "unexpected label " + k
	}
	return ""
}

// verifyImage: the container runs the pinned image, pulled by digest, of
// the reference's architecture.
func verifyImage(ref *Reference, info container.InspectResponse, img image.InspectResponse) string {
	if info.Config.Image != ref.Image() {
		return "unexpected image"
	}
	if img.ID == "" || info.Image != img.ID || (ref.ImageID != "" && info.Image != ref.ImageID) {
		return "unexpected image"
	}
	if !slices.Contains(img.RepoDigests, runtimes[ref.Runtime].ImageRepository()+"@"+ref.ImageDigest) {
		return "image not pulled by its pinned digest"
	}
	if CheckImageArch(img, ref.Arch) != nil {
		return "unexpected image architecture"
	}
	return ""
}

// verifyEnv: the environment is the image's, plus the rendered variables
// (exact values), plus the secrets and the invoke credentials (by name:
// their values are the env_files'). LD_LIBRARY_PATH (with /var/task) is accepted only as the
// image sets it, since its name is reserved.
func verifyEnv(ref *Reference, env []string, img image.InspectResponse) string {
	imageEnv := map[string]bool{}
	if img.Config != nil {
		for _, e := range img.Config.Env {
			imageEnv[e] = true
		}
	}
	rendered := renderedEnv(ref)
	secrets := map[string]bool{}
	for _, s := range ref.Secrets {
		secrets[s] = true
	}
	// The invoke credentials come from their own env_file, by name too.
	for _, s := range CredentialEnvNames(ref) {
		secrets[s] = true
	}
	seen := map[string]bool{}
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok || seen[k] {
			return "unexpected environment"
		}
		seen[k] = true
		if want, ok := rendered[k]; ok {
			if v != want {
				return "unexpected environment"
			}
			continue
		}
		if secrets[k] || imageEnv[e] {
			continue
		}
		return "unexpected environment"
	}
	for k := range rendered {
		if !seen[k] {
			return "missing environment"
		}
	}
	for k := range secrets {
		if !seen[k] {
			return "missing secret"
		}
	}
	return ""
}

func verifyHealthcheck(h *container.HealthConfig) string {
	d := func(s string) time.Duration { v, _ := time.ParseDuration(s); return v }
	if h == nil || !slices.Equal(h.Test, []string{"CMD", RuntimeMount, "health"}) ||
		h.Interval != d(healthInterval) || h.Timeout != d(healthTimeout) ||
		h.Retries != healthRetries || h.StartPeriod != d(healthStart) {
		return "unexpected healthcheck"
	}
	return ""
}

// verifyHost compares the host configuration with the rendered hardening.
func verifyHost(ref *Reference, hc *container.HostConfig) string {
	if hc.Privileged || len(hc.CapAdd) > 0 || !slices.Equal(hc.CapDrop, []string{"ALL"}) || !hc.ReadonlyRootfs {
		return "unexpected privileges"
	}
	if !slices.Equal(hc.SecurityOpt, []string{"no-new-privileges:true"}) {
		return "unexpected security options"
	}
	if hc.NetworkMode.IsHost() || hc.NetworkMode.IsContainer() || hc.NetworkMode.IsNone() ||
		hc.PidMode != "" || hc.UTSMode != "" || hc.UsernsMode != "" || hc.Cgroup != "" ||
		(hc.CgroupnsMode != "" && hc.CgroupnsMode != container.CgroupnsModePrivate) ||
		hc.IpcMode != "private" {
		return "unexpected namespaces"
	}
	if len(hc.Devices) > 0 || len(hc.DeviceRequests) > 0 || len(hc.DeviceCgroupRules) > 0 ||
		len(hc.Sysctls) > 0 || len(hc.ExtraHosts) > 0 || len(hc.DNS) > 0 || len(hc.DNSOptions) > 0 ||
		len(hc.DNSSearch) > 0 || len(hc.GroupAdd) > 0 || hc.CgroupParent != "" || len(hc.Links) > 0 ||
		len(hc.VolumesFrom) > 0 || len(hc.StorageOpt) > 0 || hc.VolumeDriver != "" ||
		(hc.Runtime != "" && hc.Runtime != "runc") ||
		(hc.Init != nil && *hc.Init) || (hc.OomKillDisable != nil && *hc.OomKillDisable) || hc.OomScoreAdj != 0 {
		return "unexpected host configuration"
	}
	if hc.PublishAllPorts || len(hc.PortBindings) > 0 {
		return "unexpected published ports"
	}
	if hc.RestartPolicy.Name != restartPolicy {
		return "unexpected restart policy"
	}
	if hc.LogConfig.Type != "json-file" || !maps.Equal(hc.LogConfig.Config, map[string]string{"max-size": logMaxSize, "max-file": logMaxFile}) {
		return "unexpected logging"
	}
	if !maps.Equal(hc.Tmpfs, map[string]string{"/tmp": tmpfsOptions(ref)}) || hc.ShmSize != shmSizeBytes {
		return "unexpected tmpfs"
	}
	mem := int64(ref.MemoryMB) << 20
	// Without swap accounting, Docker reports no swap limit (-1).
	if hc.Memory != mem || (hc.MemorySwap != mem && hc.MemorySwap != -1) ||
		!cpusMatch(hc.NanoCPUs, ref.CPUs) || hc.PidsLimit == nil || *hc.PidsLimit != pidsLimit {
		return "unexpected resource limits"
	}
	want := map[string][2]int64{"nofile": {nofileLimit, nofileLimit}, "core": {0, 0}}
	if len(hc.Ulimits) != len(want) {
		return "unexpected ulimits"
	}
	for _, u := range hc.Ulimits {
		if u == nil {
			return "unexpected ulimits"
		}
		if w, ok := want[u.Name]; !ok || u.Soft != w[0] || u.Hard != w[1] {
			return "unexpected ulimits"
		}
	}
	return ""
}

// cpusMatch compares the CPU limit with a tolerance of 0.001 CPU: compose
// parses cpus as a float32 (0.29 becomes 289999991 nano CPUs); the limits
// are in steps of 0.01.
func cpusMatch(nano int64, cpus float64) bool {
	return math.Abs(float64(nano)-cpus*1e9) <= 1e6
}

// verifyMounts: exactly the runtime binary and the active deployment,
// read-only binds from the panel-computed paths; nothing
// else (no volume, no other bind, no tmpfs mount besides /tmp).
func verifyMounts(n Name, ref *Reference, mounts []container.MountPoint, cfg RenderConfig) string {
	want := map[string]string{
		RuntimeMount: cfg.RuntimePath,
		TaskMount:    cfg.Paths.Deployment(n, ref.Deployment.ID),
	}
	if len(mounts) != len(want) {
		return "unexpected mounts"
	}
	seen := map[string]bool{}
	for _, m := range mounts {
		src, ok := want[m.Destination]
		if !ok || seen[m.Destination] || m.Type != mount.TypeBind || m.Source != src || cfg.RuntimePath == "" {
			return "unexpected mount " + m.Destination
		}
		if m.RW {
			return "writable mount " + m.Destination
		}
		if m.Propagation != "" && m.Propagation != mount.PropagationRPrivate {
			return "unexpected mount propagation " + m.Destination
		}
		seen[m.Destination] = true
	}
	return ""
}

// verifyNetworks: exactly the reference's networks, by Docker ID, with the
// function's alias, plus its own egress network when enabled.
func verifyNetworks(n Name, ref *Reference, info container.InspectResponse, cfg RenderConfig, egress *network.Inspect) string {
	if info.NetworkSettings == nil {
		return "no network settings"
	}
	refs, err := resolveNetworks(ref.Networks, cfg.Networks)
	if err != nil {
		return "unknown network"
	}
	project := n.Project()
	egressName := project + "_" + egressNetworkKey
	want := map[string]string{}
	for _, r := range refs {
		if r.ID == "" {
			return "network " + r.Name + " has no registered ID"
		}
		want[r.DockerName] = r.ID
	}
	if ref.Egress {
		want[egressName] = ""
	} else if egress != nil {
		return "unexpected egress network"
	}
	if len(info.NetworkSettings.Networks) != len(want) {
		return "unexpected networks"
	}
	for name, ep := range info.NetworkSettings.Networks {
		id, ok := want[name]
		if !ok {
			return "unexpected network " + name
		}
		if ep == nil {
			return "network " + name + " has no settings"
		}
		if name == egressName {
			if p := verifyEgress(project, info.ID, ep, egress); p != "" {
				return p
			}
			continue
		}
		if ep.NetworkID != id {
			return "network " + name + " is not the one the panel created"
		}
		if !slices.Contains(ep.Aliases, project) && !slices.Contains(ep.DNSNames, project) {
			return "missing alias on " + name
		}
	}
	return ""
}

// defaultEgressOptions are the driver options recent Docker versions record
// on a bridge created without any: the enabled IP families.
var defaultEgressOptions = map[string]string{
	"com.docker.network.enable_ipv4": "true",
	"com.docker.network.enable_ipv6": "false",
}

// verifyEgress: the egress network is compose-owned (created by the
// function's project), a plain non-internal bridge, and shared with no other
// container.
func verifyEgress(project, containerID string, ep *network.EndpointSettings, egress *network.Inspect) string {
	if egress == nil || ep.NetworkID == "" || egress.ID != ep.NetworkID || egress.Name != project+"_"+egressNetworkKey {
		return "egress network not verified"
	}
	if egress.Driver != "bridge" || egress.Internal || egress.Attachable || egress.Ingress || egress.ConfigOnly ||
		egress.ConfigFrom.Network != "" || egress.Scope != "local" || egress.EnableIPv6 {
		return "unexpected egress network configuration"
	}
	// Compose renders no driver option and no IPAM setting: a routed or
	// NAT-less bridge, or a chosen subnet, was made by someone else. Docker
	// records the IP families as options (IPv4 on, IPv6 off by default).
	for k, v := range egress.Options {
		if want, ok := defaultEgressOptions[k]; !ok || v != want {
			return "unexpected egress network options"
		}
	}
	if (egress.IPAM.Driver != "" && egress.IPAM.Driver != "default") || len(egress.IPAM.Options) > 0 {
		return "unexpected egress network options"
	}
	if egress.Labels[composeProjectLabel] != project || egress.Labels[composeNetworkLabel] != egressNetworkKey {
		return "egress network not created by the function's project"
	}
	for k := range egress.Labels {
		if !strings.HasPrefix(k, "com.docker.compose.") {
			return "unexpected egress network label " + k
		}
	}
	if len(egress.Containers) != 1 {
		return "egress network shared with other containers"
	}
	if _, ok := egress.Containers[containerID]; !ok {
		return "egress network shared with other containers"
	}
	return ""
}
