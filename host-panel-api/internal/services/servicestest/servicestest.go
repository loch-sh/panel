// Package servicestest provides the network registry used by the engine
// tests: a static one for the rendering tests, a real one (on a Docker
// network created by the test) for the integration tests.
package servicestest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"host-panel-api/internal/networks"
	"host-panel-api/internal/services"
)

// SharedNetwork is the reference of the shared network in Static.
var SharedNetwork = networks.Ref{Name: networks.SharedName, DockerName: "loch-services", ID: "0123456789ab", System: true}

// Static resolves the networks it holds; Verify trusts them.
type Static map[string]networks.Ref

// Networks returns a static registry with the shared network.
func Networks() Static { return Static{networks.SharedName: SharedNetwork} }

func (s Static) Lookup(name string) (networks.Ref, bool) {
	r, ok := s[name]
	return r, ok
}

func (s Static) Verify(_ context.Context, name string) (string, bool) {
	r, ok := s[name]
	return r.ID, ok
}

// Registry returns a real registry whose shared network is dockerName, an
// internal network the test created with the panel label
// (sh.loch.managed=true).
func Registry(t testing.TB, dock networks.Docker, dockerName string) *networks.Manager {
	t.Helper()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	m, err := networks.NewManager(networks.Config{File: filepath.Join(t.TempDir(), "networks.json"), Max: 10}, dock, discard, discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.EnsureShared(context.Background(), dockerName); err != nil {
		t.Fatal(err)
	}
	return m
}

// AllInterfaces is the exposure policy of the integration tests: every
// interface (SERVICES_EXPOSE_ALLOWED_CIDRS=0.0.0.0), optional TLS allowed.
func AllInterfaces() services.ExposurePolicy {
	return services.ExposurePolicy{AllowedCIDRs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/32")}, AllowTLSOptional: true}
}

// Expose publishes an instance on every interface, as the panel does, and
// fails the test when it cannot.
func Expose(t testing.TB, m *services.Manager, key string, port int, tlsOptional bool) {
	t.Helper()
	exp := services.Exposure{Address: netip.IPv4Unspecified(), Port: port, TLSOptional: tlsOptional}
	if _, err := m.SetExposure(context.Background(), key, &exp, "it"); err != nil {
		t.Fatalf("expose %s: %v", key, err)
	}
}

// pinnedImageRe matches the image line of a compose file pinned by digest.
var pinnedImageRe = regexp.MustCompile(`(?m)^(\s+image: )(\S+?)(@sha256:[0-9a-f]{64})$`)

// CheckPinning checks, on a real Docker, that a new dedicated instance runs
// its image pinned by digest, then that an instance created before images
// were pinned (no digest in instance.json, compose.yaml without it, a
// container created from the tag) is pinned by PinImages to the image its
// container runs, without being recreated nor reported as modified.
func CheckPinning(t testing.TB, m *services.Manager, stacksDir, dataDir, key, project string) {
	t.Helper()
	ctx := context.Background()
	dir := filepath.Join(stacksDir, project)
	composePath := filepath.Join(dir, services.ComposeFile)
	refPath := filepath.Join(dataDir, services.PrivateDir, project, "instance.json")
	compose, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatal(err)
	}
	match := pinnedImageRe.FindSubmatch(compose)
	if match == nil {
		t.Fatalf("compose image not pinned by digest:\n%s", compose)
	}
	tag, digest := string(match[2]), strings.TrimPrefix(string(match[3]), "@")
	var ref map[string]any
	data, _ := os.ReadFile(refPath)
	if json.Unmarshal(data, &ref) != nil || ref["digest"] != digest || ref["image"] != tag {
		t.Fatalf("instance.json does not pin %s@%s: %s", tag, digest, data)
	}
	configured := inspect(t, project, "{{.Config.Image}}")
	t.Logf("pinned %s@%s, container Config.Image %s", tag, digest, configured)

	// An instance from before the pinning, its container created from the tag.
	delete(ref, "digest")
	data, _ = json.Marshal(ref)
	if err := os.WriteFile(refPath, data, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(composePath, pinnedImageRe.ReplaceAll(compose, []byte("${1}${2}")), 0o640); err != nil {
		t.Fatal(err)
	}
	args := []string{"compose", "--project-name", project, "--project-directory", dir, "-f", composePath}
	if _, err := os.Stat(filepath.Join(dir, services.OverrideFile)); err == nil {
		args = append(args, "-f", filepath.Join(dir, services.OverrideFile))
	}
	if out, err := exec.Command("docker", append(args, "up", "-d", "--force-recreate", "--wait")...).CombinedOutput(); err != nil {
		t.Fatalf("recreate from the tag: %v\n%s", err, out)
	}
	if got := inspect(t, project, "{{.Config.Image}}"); got != tag {
		t.Fatalf("container created from the tag: %s", got)
	}
	id := inspect(t, project, "{{.Id}}")
	if s := summary(t, m, key); s.Modified || !s.Ready {
		t.Fatalf("older instance: modified=%v ready=%v", s.Modified, s.Ready)
	}

	m.PinImages(ctx)
	if got, _ := os.ReadFile(composePath); !bytes.Equal(got, compose) {
		t.Fatalf("compose not pinned again:\n%s", got)
	}
	data, _ = os.ReadFile(refPath)
	if json.Unmarshal(data, &ref) != nil || ref["digest"] != digest {
		t.Fatalf("instance.json not pinned again: %s", data)
	}
	if inspect(t, project, "{{.Id}}") != id {
		t.Fatal("the container is not recreated by the pinning")
	}
	if s := summary(t, m, key); s.Modified || !s.Ready {
		t.Fatalf("migrated instance: modified=%v ready=%v", s.Modified, s.Ready)
	}
}

func inspect(t testing.TB, container, format string) string {
	t.Helper()
	out, err := exec.Command("docker", "inspect", "-f", format, container).Output()
	if err != nil {
		t.Fatalf("docker inspect %s: %v", container, err)
	}
	return strings.TrimSpace(string(out))
}

func summary(t testing.TB, m *services.Manager, key string) services.InstanceSummary {
	t.Helper()
	list, err := m.Instances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("instance %s not listed", key)
	return services.InstanceSummary{}
}
