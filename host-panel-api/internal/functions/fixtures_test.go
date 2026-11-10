package functions

import (
	"context"
	"strings"
	"time"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"host-panel-api/internal/networks"
)

var (
	testDigest  = "sha256:" + strings.Repeat("a", 64)
	testImageID = "sha256:" + strings.Repeat("b", 64)
	testHash    = strings.Repeat("c", 64)
)

var testConfig = Config{Enabled: true, DataDir: "/data/functions", StacksDir: "/data/compose",
	Max: 50, MaxMemoryMB: 3008, MaxCodeBytes: 50 << 20, DeploymentsKept: 3, TmpSizeMB: 512}

// nm returns a validated name (test helper).
func nm(s string) Name {
	n, err := ParseName(s)
	if err != nil {
		panic(err)
	}
	return n
}

// sampleRef is a valid Node.js function on two networks.
func sampleRef() *Reference {
	return &Reference{
		Version: ReferenceVersion, Name: "resize", Runtime: "nodejs", RuntimeVersion: "22", Arch: "arm64",
		ImageTag: "22.2026.11.05.18-arm64", ImageDigest: testDigest, ImageID: testImageID,
		Entrypoint: []string{"/lambda-entrypoint.sh"}, Handler: "index.handler",
		Limits:   Limits{MemoryMB: 256, CPUs: 0.5, TimeoutSeconds: 60, TmpSizeMB: 512},
		Env:      []EnvVar{{Name: "OUTPUT_BUCKET", Value: "thumbs"}, {Name: "PRICE", Value: "costs $5"}},
		Secrets:  []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"},
		Networks: []string{"fn-resize", "images"},
		Deployment: &Deployment{ID: "20261106T120000Z-0123abcd", TreeHash: testHash, ZipSHA256: testHash,
			ZipSize: 1234, Files: 3, Bytes: 4567},
		RuntimeBinary: RuntimeBinary{Version: "v1.0.0", Arch: "arm64", SHA256: testHash},
	}
}

// pythonRef is a valid Python function with outbound access.
func pythonRef() *Reference {
	r := sampleRef()
	r.Name, r.Runtime, r.RuntimeVersion, r.ImageTag, r.Handler = "report", "python", "3.13", "3.13-x86_64", "app.main.handler"
	r.Arch, r.RuntimeBinary.Arch = "amd64", "amd64"
	r.Env, r.Secrets, r.Networks, r.Egress = nil, nil, []string{"fn-report"}, true
	r.Limits = Limits{MemoryMB: 1024, CPUs: 1, TimeoutSeconds: 900, TmpSizeMB: 2048}
	return r
}

// providedRef is a valid Go function on the custom runtime.
func providedRef() *Reference {
	r := sampleRef()
	r.Name, r.Runtime, r.RuntimeVersion, r.ImageTag, r.Handler = "gofn", "provided", "al2023", "al2023-arm64", "bootstrap"
	r.Env, r.Secrets, r.Networks = nil, nil, []string{"fn-gofn"}
	r.Deployment.Warnings = []string{WarnBootstrapDynamic}
	return r
}

type fakeResolver map[string]networks.Ref

func (f fakeResolver) Lookup(name string) (networks.Ref, bool) { r, ok := f[name]; return r, ok }
func (f fakeResolver) Verify(_ context.Context, name string) (string, bool) {
	r, ok := f[name]
	return r.ID, ok
}

func testResolver() fakeResolver {
	ref := func(name, id string) networks.Ref {
		return networks.Ref{Name: name, DockerName: networks.DockerPrefix + name, ID: id}
	}
	return fakeResolver{
		"fn-resize": ref("fn-resize", "net-id-resize"),
		"fn-report": ref("fn-report", "net-id-report"),
		"fn-gofn":   ref("fn-gofn", "net-id-gofn"),
		"images":    {Name: "images", DockerName: networks.DockerPrefix + "images", ID: "net-id-images", Subnet: "10.20.0.0/24"},
		"services":  {Name: "services", DockerName: "loch-services", ID: "net-id-services", System: true},
	}
}

func testRenderConfig(ref *Reference) RenderConfig {
	return RenderConfig{Paths: testConfig.Paths(), Networks: testResolver(),
		RuntimePath: "/data/functions/.runtime/" + ref.RuntimeBinary.Version + "/" + ref.RuntimeBinary.Arch + "/" + RuntimeBinaryName}
}

// testImage is the AWS image as `docker image inspect` reports it.
func testImage(ref *Reference) image.InspectResponse {
	return image.InspectResponse{
		ID: testImageID, Os: "linux", Architecture: ref.Arch,
		RepoDigests: []string{runtimes[ref.Runtime].ImageRepository() + "@" + testDigest},
		Config: &dockerspec.DockerOCIImageConfig{ImageConfig: ocispec.ImageConfig{
			Env: []string{
				"PATH=/var/lang/bin:/usr/local/bin:/usr/bin/:/bin:/opt/bin",
				"LD_LIBRARY_PATH=/var/lang/lib:/lib64:/usr/lib64:/var/runtime:/var/runtime/lib:/var/task:/var/task/lib:/opt/lib",
				"LANG=en_US.UTF-8", "TZ=:/etc/localtime",
				"LAMBDA_TASK_ROOT=/var/task", "LAMBDA_RUNTIME_DIR=/var/runtime",
			},
			Entrypoint: []string{"/lambda-entrypoint.sh"},
			WorkingDir: "/var/task",
			Labels:     map[string]string{"com.amazonaws.lambda.platform.kernel": "k5"},
		}},
	}
}

// goodContainer is the container compose creates from the rendered file.
func goodContainer(ref *Reference, cfg RenderConfig) container.InspectResponse {
	img := testImage(ref)
	env := append([]string{}, img.Config.Env...)
	for k, v := range renderedEnv(ref) {
		env = append(env, k+"="+v)
	}
	for _, s := range ref.Secrets {
		env = append(env, s+"=secret-value")
	}
	for _, s := range CredentialEnvNames(ref) {
		env = append(env, s+"=credential-value")
	}
	name := nm(ref.Name)
	project := name.Project()
	pids := int64(pidsLimit)
	nets := map[string]*network.EndpointSettings{}
	for _, n := range ref.Networks {
		r := testResolver()[n]
		nets[r.DockerName] = &network.EndpointSettings{NetworkID: r.ID, Aliases: []string{project, "fn"}}
	}
	if ref.Egress {
		nets[project+"_fn_egress"] = &network.EndpointSettings{NetworkID: "egress-id"}
	}
	labels := renderedLabels(name)
	for k, v := range map[string]string{
		composeProjectLabel: project, composeServiceLabel: serviceName,
		composeWorkingDirLabel:           cfg.Paths.ProjectDir(name),
		"com.docker.compose.config-hash": "abc", "com.docker.compose.version": "5.6.1",
		"com.amazonaws.lambda.platform.kernel": "k5", // image labels are kept
	} {
		labels[k] = v
	}
	return container.InspectResponse{
		ID:    "container-" + ref.Name,
		Name:  "/" + project,
		Image: testImageID,
		Config: &container.Config{
			Image: ref.Image(), User: functionUser, Env: env, WorkingDir: "/var/task",
			Entrypoint: []string{RuntimeMount}, Cmd: command(ref),
			Labels: labels,
			Healthcheck: &container.HealthConfig{
				Test: []string{"CMD", RuntimeMount, "health"}, Interval: 10 * time.Second,
				Timeout: 5 * time.Second, Retries: 3, StartPeriod: 30 * time.Second,
			},
		},
		HostConfig: &container.HostConfig{
			NetworkMode:    container.NetworkMode(networks.DockerPrefix + ref.Networks[0]),
			RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			LogConfig:      container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
			CapDrop:        []string{"ALL"},
			ReadonlyRootfs: true,
			SecurityOpt:    []string{"no-new-privileges:true"},
			IpcMode:        "private",
			CgroupnsMode:   container.CgroupnsModePrivate,
			Tmpfs:          map[string]string{"/tmp": tmpfsOptions(ref)},
			ShmSize:        64 << 20,
			Resources: container.Resources{
				Memory: int64(ref.MemoryMB) << 20, MemorySwap: int64(ref.MemoryMB) << 20,
				NanoCPUs: int64(float32(ref.CPUs) * 1e9), PidsLimit: &pids, // compose parses cpus as a float32
				Ulimits: []*container.Ulimit{
					{Name: "nofile", Soft: 1024, Hard: 1024}, {Name: "core", Soft: 0, Hard: 0},
				},
			},
		},
		Mounts: []container.MountPoint{
			{Type: mount.TypeBind, Source: cfg.RuntimePath, Destination: RuntimeMount, Propagation: mount.PropagationRPrivate},
			{Type: mount.TypeBind, Source: cfg.Paths.Deployment(name, ref.Deployment.ID), Destination: TaskMount, Propagation: mount.PropagationRPrivate},
		},
		NetworkSettings: &container.NetworkSettings{Networks: nets},
	}
}

// goodEgress is the egress network compose creates for a function (nil
// without egress).
func goodEgress(ref *Reference, info container.InspectResponse) *network.Inspect {
	if !ref.Egress {
		return nil
	}
	project := nm(ref.Name).Project()
	n := &network.Inspect{Containers: map[string]network.EndpointResource{info.ID: {}}}
	n.Name, n.ID, n.Driver, n.Scope = project+"_fn_egress", "egress-id", "bridge", "local"
	n.Labels = map[string]string{composeProjectLabel: project, composeNetworkLabel: egressNetworkKey, "com.docker.compose.version": "5.6.1"}
	// Recorded by recent Docker versions (measured on Docker 29.9 with compose 5.6).
	n.Options = map[string]string{"com.docker.network.enable_ipv4": "true", "com.docker.network.enable_ipv6": "false"}
	n.IPAM.Driver = "default"
	return n
}
