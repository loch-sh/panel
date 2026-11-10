package functions

import (
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
)

func TestVerifyContainerAcceptsRendered(t *testing.T) {
	for _, ref := range []*Reference{sampleRef(), pythonRef()} {
		cfg := testRenderConfig(ref)
		info := goodContainer(ref, cfg)
		if p := VerifyContainer(ref, info, testImage(ref), cfg, goodEgress(ref, info)); p != "" {
			t.Fatalf("%s: %s", ref.Name, p)
		}
		// Docker without swap accounting, containerd store without a recorded image ID.
		info.HostConfig.MemorySwap = -1
		ref.ImageID = ""
		if p := VerifyContainer(ref, info, testImage(ref), cfg, goodEgress(ref, info)); p != "" {
			t.Fatalf("%s: %s", ref.Name, p)
		}
	}
}

func TestVerifyContainerRefuses(t *testing.T) {
	setEnv := func(info *container.InspectResponse, name, value string) {
		for i, e := range info.Config.Env {
			if strings.HasPrefix(e, name+"=") {
				info.Config.Env[i] = name + "=" + value
				return
			}
		}
		info.Config.Env = append(info.Config.Env, name+"="+value)
	}
	dropEnv := func(info *container.InspectResponse, name string) {
		info.Config.Env = slices.DeleteFunc(info.Config.Env, func(e string) bool { return strings.HasPrefix(e, name+"=") })
	}
	cases := map[string]func(*container.InspectResponse, *image.InspectResponse){
		"name": func(i *container.InspectResponse, _ *image.InspectResponse) { i.Name = "/fn-other" },
		"image reference": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Config.Image = "mirror.gcr.io/amazon/aws-lambda-nodejs:22-arm64"
		},
		"image id": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Image = "sha256:" + strings.Repeat("d", 64)
		},
		"image not by digest": func(_ *container.InspectResponse, img *image.InspectResponse) { img.RepoDigests = nil },
		"image other repo": func(_ *container.InspectResponse, img *image.InspectResponse) {
			img.RepoDigests = []string{"evil/aws-lambda-nodejs@" + testDigest}
		},
		"image architecture": func(_ *container.InspectResponse, img *image.InspectResponse) { img.Architecture = "amd64" },
		"function label":     func(i *container.InspectResponse, _ *image.InspectResponse) { i.Config.Labels[LabelFunction] = "other" },
		"managed label":      func(i *container.InspectResponse, _ *image.InspectResponse) { delete(i.Config.Labels, LabelManaged) },
		"compose project": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Config.Labels[composeProjectLabel] = "other"
		},
		"compose service": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Config.Labels[composeServiceLabel] = "app"
		},
		"compose working dir": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Config.Labels[composeWorkingDirLabel] = "/tmp/fn-resize"
		},
		"entrypoint": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Config.Entrypoint = []string{"/lambda-entrypoint.sh"}
		},
		"entrypoint shell": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Config.Entrypoint = []string{"/bin/sh", "-c"}
		},
		"command handler": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Config.Cmd = []string{"--", "/lambda-entrypoint.sh", "other.handler"}
		},
		"command without --": func(i *container.InspectResponse, _ *image.InspectResponse) { i.Config.Cmd = []string{"index.handler"} },
		"user root":          func(i *container.InspectResponse, _ *image.InspectResponse) { i.Config.User = "" },
		"user other":         func(i *container.InspectResponse, _ *image.InspectResponse) { i.Config.User = "0:0" },
		"working dir":        func(i *container.InspectResponse, _ *image.InspectResponse) { i.Config.WorkingDir = "/tmp" },
		"extra variable": func(i *container.InspectResponse, _ *image.InspectResponse) {
			setEnv(i, "NODE_OPTIONS", "--require /tmp/x.js")
		},
		"LD_PRELOAD":      func(i *container.InspectResponse, _ *image.InspectResponse) { setEnv(i, "LD_PRELOAD", "/tmp/x.so") },
		"LD_LIBRARY_PATH": func(i *container.InspectResponse, _ *image.InspectResponse) { setEnv(i, "LD_LIBRARY_PATH", "/tmp") },
		"PATH":            func(i *container.InspectResponse, _ *image.InspectResponse) { setEnv(i, "PATH", "/tmp:/usr/bin") },
		"memory size variable": func(i *container.InspectResponse, _ *image.InspectResponse) {
			setEnv(i, "AWS_LAMBDA_FUNCTION_MEMORY_SIZE", "3008")
		},
		"runtime auth off":      func(i *container.InspectResponse, _ *image.InspectResponse) { setEnv(i, "LOCH_RUNTIME_AUTH", "off") },
		"user variable changed": func(i *container.InspectResponse, _ *image.InspectResponse) { setEnv(i, "OUTPUT_BUCKET", "other") },
		"missing variable":      func(i *container.InspectResponse, _ *image.InspectResponse) { dropEnv(i, "AWS_EC2_METADATA_DISABLED") },
		"missing secret":        func(i *container.InspectResponse, _ *image.InspectResponse) { dropEnv(i, "AWS_SECRET_ACCESS_KEY") },
		"duplicate variable": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Config.Env = append(i.Config.Env, "OUTPUT_BUCKET=thumbs")
		},
		"no healthcheck": func(i *container.InspectResponse, _ *image.InspectResponse) { i.Config.Healthcheck = nil },
		"healthcheck command": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Config.Healthcheck.Test = []string{"NONE"}
		},
		"healthcheck retries": func(i *container.InspectResponse, _ *image.InspectResponse) { i.Config.Healthcheck.Retries = 100 },
		"privileged":          func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.Privileged = true },
		"cap add": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.HostConfig.CapAdd = []string{"NET_RAW"}
		},
		"cap drop partial": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.HostConfig.CapDrop = []string{"NET_RAW"}
		},
		"writable rootfs": func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.ReadonlyRootfs = false },
		"seccomp unconfined": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.HostConfig.SecurityOpt = append(i.HostConfig.SecurityOpt, "seccomp=unconfined")
		},
		"no no-new-privileges": func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.SecurityOpt = nil },
		"host network":         func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.NetworkMode = "host" },
		"container network": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.HostConfig.NetworkMode = "container:abc"
		},
		"host pid":      func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.PidMode = "host" },
		"host ipc":      func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.IpcMode = "host" },
		"shareable ipc": func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.IpcMode = "shareable" },
		"host uts":      func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.UTSMode = "host" },
		"host userns":   func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.UsernsMode = "host" },
		"host cgroupns": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.HostConfig.CgroupnsMode = container.CgroupnsModeHost
		},
		"device": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.HostConfig.Devices = []container.DeviceMapping{{PathOnHost: "/dev/kmsg"}}
		},
		"device cgroup rule": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.HostConfig.DeviceCgroupRules = []string{"c 1:* rwm"}
		},
		"sysctl": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.HostConfig.Sysctls = map[string]string{"net.ipv4.ip_forward": "1"}
		},
		"extra host": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.HostConfig.ExtraHosts = []string{"api:10.0.0.1"}
		},
		"group add": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.HostConfig.GroupAdd = []string{"docker"}
		},
		"volumes from": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.HostConfig.VolumesFrom = []string{"loch-panel"}
		},
		"runtime": func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.Runtime = "nvidia" },
		"oom kill disabled": func(i *container.InspectResponse, _ *image.InspectResponse) {
			t := true
			i.HostConfig.OomKillDisable = &t
		},
		"published port": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.HostConfig.PortBindings = network.PortMap{network.MustParsePort("8080/tcp"): {{HostPort: "8080"}}}
		},
		"publish all":    func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.PublishAllPorts = true },
		"restart policy": func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.RestartPolicy.Name = "no" },
		"logging driver": func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.LogConfig.Type = "local" },
		"logging rotation": func(i *container.InspectResponse, _ *image.InspectResponse) {
			delete(i.HostConfig.LogConfig.Config, "max-size")
		},
		"tmpfs options": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.HostConfig.Tmpfs["/tmp"] = "size=512m,exec"
		},
		"ulimit nproc added": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.HostConfig.Ulimits = append(i.HostConfig.Ulimits, &container.Ulimit{Name: "nproc", Soft: 1, Hard: 1})
		},
		"traefik enabled": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Config.Labels["traefik.enable"] = "true"
		},
		"traefik router": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Config.Labels["traefik.http.routers.x.rule"] = "Host(`evil.example`)"
		},
		"traefik label from the image": func(i *container.InspectResponse, img *image.InspectResponse) {
			img.Config.Labels["traefik.http.routers.x.rule"] = "Host(`evil.example`)"
			i.Config.Labels["traefik.http.routers.x.rule"] = "Host(`evil.example`)"
		},
		"unknown label": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Config.Labels["sh.loch.service"] = "postgres"
		},
		"image label changed": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Config.Labels["com.amazonaws.lambda.platform.kernel"] = "other"
		},
		"cpus off by 0.01": func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.NanoCPUs = 510_000_000 },
		"extra tmpfs": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.HostConfig.Tmpfs["/var/task"] = "size=1m"
		},
		"shm size":      func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.ShmSize = 1 << 30 },
		"memory":        func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.Memory = 0 },
		"swap":          func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.MemorySwap = 0 },
		"cpus":          func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.NanoCPUs = 4e9 },
		"pids":          func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.PidsLimit = nil },
		"ulimit nofile": func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig.Ulimits[0].Hard = 1 << 20 },
		"ulimit missing": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.HostConfig.Ulimits = i.HostConfig.Ulimits[:1]
		},
		"writable code": func(i *container.InspectResponse, _ *image.InspectResponse) { i.Mounts[1].RW = true },
		"other deployment": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Mounts[1].Source = "/data/functions/fn-resize/deployments/20260101T000000Z-00000000"
		},
		"runtime from elsewhere": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Mounts[0].Source = "/tmp/loch-lambda-runtime"
		},
		"runtime as volume": func(i *container.InspectResponse, _ *image.InspectResponse) { i.Mounts[0].Type = mount.TypeVolume },
		"shared propagation": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Mounts[1].Propagation = mount.PropagationRShared
		},
		"docker socket": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Mounts = append(i.Mounts, container.MountPoint{Type: mount.TypeBind, Source: "/var/run/docker.sock", Destination: "/var/run/docker.sock"})
		},
		"missing mount": func(i *container.InspectResponse, _ *image.InspectResponse) { i.Mounts = i.Mounts[:1] },
		"plaintext token in the container": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Config.Env = append(i.Config.Env, "LOCH_RUNTIME_TOKEN=plaintext")
		},
		"key pair without sigv4": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Config.Env = append(i.Config.Env, "LOCH_RUNTIME_ACCESS_KEY=a:b")
		},
		"missing credentials": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Config.Env = slices.DeleteFunc(i.Config.Env, func(e string) bool { return strings.HasPrefix(e, "LOCH_RUNTIME_TOKEN_SHA256=") })
		},
		"duplicate mount": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.Mounts = []container.MountPoint{i.Mounts[0], i.Mounts[0]}
		},
		"extra network": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.NetworkSettings.Networks["loch-services"] = &network.EndpointSettings{NetworkID: "net-id-services"}
		},
		"replaced network": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.NetworkSettings.Networks["loch-net-images"].NetworkID = "impostor"
		},
		"missing network": func(i *container.InspectResponse, _ *image.InspectResponse) {
			delete(i.NetworkSettings.Networks, "loch-net-images")
		},
		"missing alias": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.NetworkSettings.Networks["loch-net-images"].Aliases = []string{"fn"}
		},
		"egress not enabled": func(i *container.InspectResponse, _ *image.InspectResponse) {
			i.NetworkSettings.Networks["fn-resize_fn_egress"] = &network.EndpointSettings{NetworkID: "x"}
		},
		"no network settings": func(i *container.InspectResponse, _ *image.InspectResponse) { i.NetworkSettings = nil },
		"no host config":      func(i *container.InspectResponse, _ *image.InspectResponse) { i.HostConfig = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			ref := sampleRef()
			cfg := testRenderConfig(ref)
			info := goodContainer(ref, cfg)
			img := testImage(ref)
			mutate(&info, &img)
			p := VerifyContainer(ref, info, img, cfg, nil)
			if p == "" {
				t.Fatal("must be refused")
			}
			t.Log(p)
		})
	}
}

func TestVerifyContainerEgress(t *testing.T) {
	ref := pythonRef()
	cfg := testRenderConfig(ref)
	info := goodContainer(ref, cfg)
	delete(info.NetworkSettings.Networks, "fn-report_fn_egress")
	if p := VerifyContainer(ref, info, testImage(ref), cfg, goodEgress(ref, info)); p == "" {
		t.Fatal("missing egress network accepted")
	}
}

func TestVerifyContainerEnvRules(t *testing.T) {
	ref := sampleRef()
	cfg := testRenderConfig(ref)
	// A secret may shadow an image variable (by name; the value is the env_file's).
	ref.Secrets = []string{"TZ"}
	info := goodContainer(ref, cfg)
	info.Config.Env = slices.DeleteFunc(info.Config.Env, func(e string) bool { return e == "TZ=:/etc/localtime" })
	if p := VerifyContainer(ref, info, testImage(ref), cfg, goodEgress(ref, info)); p != "" {
		t.Fatal(p)
	}
	// An image variable with another value is refused.
	info = goodContainer(sampleRef(), cfg)
	for i, e := range info.Config.Env {
		if e == "LANG=en_US.UTF-8" {
			info.Config.Env[i] = "LANG=C"
		}
	}
	if p := VerifyContainer(sampleRef(), info, testImage(ref), cfg, nil); p == "" {
		t.Fatal("changed image variable accepted")
	}
}

func TestVerifyContainerCPURounding(t *testing.T) {
	for _, cpus := range []float64{0.1, 0.29, 0.33, 0.5, 1.07, 2.99, 15.99} {
		ref := sampleRef()
		ref.CPUs = cpus
		cfg := testRenderConfig(ref)
		info := goodContainer(ref, cfg) // NanoCPUs computed through a float32, as compose does
		if p := VerifyContainer(ref, info, testImage(ref), cfg, nil); p != "" {
			t.Errorf("cpus %g (nano %d): %s", cpus, info.HostConfig.NanoCPUs, p)
		}
	}
}

func TestVerifyContainerEgressNetwork(t *testing.T) {
	cases := map[string]func(n *network.Inspect){
		"internal":         func(n *network.Inspect) { n.Internal = true },
		"other driver":     func(n *network.Inspect) { n.Driver = "macvlan" },
		"attachable":       func(n *network.Inspect) { n.Attachable = true },
		"other project":    func(n *network.Inspect) { n.Labels[composeProjectLabel] = "other" },
		"other key":        func(n *network.Inspect) { n.Labels[composeNetworkLabel] = "default" },
		"no compose label": func(n *network.Inspect) { n.Labels = nil },
		"panel label":      func(n *network.Inspect) { n.Labels["sh.loch.managed"] = "true" },
		"other ID":         func(n *network.Inspect) { n.ID = "impostor" },
		"other name":       func(n *network.Inspect) { n.Name = "loch-services" },
		"shared": func(n *network.Inspect) {
			n.Containers["other-container"] = network.EndpointResource{}
		},
		"config from": func(n *network.Inspect) { n.ConfigFrom.Network = "template" },
		"routed bridge": func(n *network.Inspect) {
			n.Options = map[string]string{"com.docker.network.bridge.gateway_mode_ipv4": "routed"}
		},
		"no masquerade": func(n *network.Inspect) {
			n.Options = map[string]string{"com.docker.network.bridge.enable_ip_masquerade": "false"}
		},
		"IPv4 disabled": func(n *network.Inspect) { n.Options["com.docker.network.enable_ipv4"] = "false" },
		"IPv6 option":   func(n *network.Inspect) { n.Options["com.docker.network.enable_ipv6"] = "true" },
		"empty option":  func(n *network.Inspect) { n.Options["com.docker.network.bridge.name"] = "" },
		"IPv6":          func(n *network.Inspect) { n.EnableIPv6 = true },
		"IPAM driver":   func(n *network.Inspect) { n.IPAM.Driver = "custom" },
		"IPAM options":  func(n *network.Inspect) { n.IPAM.Options = map[string]string{"x": "y"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			ref := pythonRef()
			cfg := testRenderConfig(ref)
			info := goodContainer(ref, cfg)
			egress := goodEgress(ref, info)
			mutate(egress)
			if p := VerifyContainer(ref, info, testImage(ref), cfg, egress); p == "" {
				t.Fatal("must be refused")
			}
		})
	}
	ref := pythonRef()
	cfg := testRenderConfig(ref)
	info := goodContainer(ref, cfg)
	// As compose creates it on recent Docker (IP family options) and on older
	// daemons (no option).
	if p := VerifyContainer(ref, info, testImage(ref), cfg, goodEgress(ref, info)); p != "" {
		t.Fatalf("egress network of recent Docker refused: %s", p)
	}
	older := goodEgress(ref, info)
	older.Options = nil
	if p := VerifyContainer(ref, info, testImage(ref), cfg, older); p != "" {
		t.Fatalf("egress network without options refused: %s", p)
	}
	if p := VerifyContainer(ref, info, testImage(ref), cfg, nil); p == "" {
		t.Fatal("egress network not inspected: must be refused")
	}
	// A function without egress given an egress network.
	plain := sampleRef()
	pinfo := goodContainer(plain, testRenderConfig(plain))
	if p := VerifyContainer(plain, pinfo, testImage(plain), testRenderConfig(plain), goodEgress(pythonRef(), info)); p == "" {
		t.Fatal("unexpected egress network accepted")
	}
}
