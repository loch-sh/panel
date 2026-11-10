package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"host-panel-api/internal/releases"
)

// fakeSource is a registry and endoflife.date in memory.
type fakeSource struct {
	mu                    sync.Mutex
	tags                  map[string][]string                    // source -> tags
	digests               map[string]string                      // image -> digest ("" = missing)
	support               map[string]map[string]releases.Support // product -> cycle -> dates
	failTags, failSupport bool
	digestCalls           int
	digestErr             map[string]error // image -> transient error
}

func (f *fakeSource) Tags(_ context.Context, source string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failTags {
		return nil, errors.New("registry down")
	}
	t, ok := f.tags[source]
	if !ok {
		return nil, releases.ErrNotFound
	}
	return t, nil
}

func (f *fakeSource) Digest(_ context.Context, image string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.digestCalls++
	if err := f.digestErr[image]; err != nil {
		return "", err
	}
	d, ok := f.digests[image]
	if !ok || d == "" {
		return "", releases.ErrNotFound
	}
	return d, nil
}

func (f *fakeSource) Support(_ context.Context, product string) (map[string]releases.Support, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSupport {
		return nil, errors.New("endoflife.date down")
	}
	s, ok := f.support[product]
	if !ok {
		return nil, releases.ErrNotFound
	}
	return s, nil
}

func digestOf(c byte) string { return "sha256:" + strings.Repeat(string(c), 64) }

// newFakeSource publishes fake 1.5..5 (5 not on the mirror, 4 out of
// support, 0.9 older than the engine minimum) and kv 7.0.x / 8.0.x.
func newFakeSource() *fakeSource {
	return &fakeSource{
		tags: map[string][]string{
			"docker.io/library/fake": {"0.9", "1.5", "2", "2.1", "3", "3.1", "3.2", "4", "5", "latest", "3-alpine"},
			"docker.io/library/kv":   {"7.0.3", "7.0.4", "8.0.1", "8.0.2", "8.0.10", "8.0.x", "9.1.0"},
		},
		digests: map[string]string{
			"fake:1.5": digestOf('1'), "fake:2": digestOf('2'), "fake:2.1": digestOf('3'), "fake:3": digestOf('4'),
			"fake:3.1": digestOf('5'), "fake:3.2": digestOf('6'), "fake:4": digestOf('7'), "fake:5": "",
			"kv:7.0.4": digestOf('a'), "kv:8.0.10": digestOf('b'),
		},
		support: map[string]map[string]releases.Support{
			"fake": {
				"4":   {Active: "2025-01-01", Security: "2026-01-01"}, // over (testNow is 2026-11-20)
				"3.2": {Active: "2027-01-01", Security: "2029-01-01"},
				"1.5": {Security: "2026-11-12"},
			},
		},
	}
}

func newCatalogRegistry(t *testing.T, pinned map[string][]string) *Registry {
	t.Helper()
	reg, err := NewRegistry([]Engine{newFakeEngine(), fakeDedicatedEngine{}}, nil, pinned, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func versionsOf(vs []Version) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.Version
	}
	return strings.Join(out, ",")
}

func TestDiscoveryOffersLatestMajors(t *testing.T) {
	reg := newCatalogRegistry(t, nil)
	src := newFakeSource()
	status, errs := reg.Refresh(context.Background(), src, nil)
	if len(errs) != 0 || status.CheckedAt == nil || len(status.Failed) != 0 {
		t.Fatalf("refresh: %+v %v", status, errs)
	}
	// 5 is not on the mirror, 4 and 1.5 are out of support (major 1 has no
	// supported version): majors 3 and 2, each at its latest available
	// version; 0.9 is older than the engine minimum.
	vs := reg.Versions("fake")
	if got := versionsOf(vs); got != "3.2,2.1" {
		t.Fatalf("offered = %s", got)
	}
	if vs[0].SupportUntil != "2029-01-01" || vs[0].ActiveSupportUntil != "2027-01-01" || vs[0].Deprecated || vs[0].Image != "fake:3.2" {
		t.Fatalf("3.2 = %+v", vs[0])
	}
	// Still known (an instance may run it), with its dates.
	if v, ok := reg.Version("fake", "1.5"); !ok || !v.Deprecated || v.SupportUntil != "2026-11-12" {
		t.Fatalf("1.5 = %+v", v)
	}
	// Exact patch tags: the highest patch of each version ("8.0.10" > "8.0.2").
	kv := reg.Versions("kv")
	if versionsOf(kv) != "8,7" || kv[0].Image != "kv:8.0.10" || kv[1].Image != "kv:7.0.4" {
		t.Fatalf("kv = %+v", kv)
	}
	// 9.1.0 does not match the version pattern of kv (x.0.y).
	if _, ok := reg.Version("kv", "9"); ok {
		t.Fatal("kv 9 was never published")
	}
	if _, _, e := reg.Resolve("fake", "4"); e == nil {
		t.Fatal("an unsupported version cannot be created")
	}
	if _, v, e := reg.Resolve("fake", "2.1"); e != nil || v.Image != "fake:2.1" {
		t.Fatalf("Resolve(2.1) = %+v %v", v, e)
	}
}

func TestDiscoveryUpdateVersions(t *testing.T) {
	reg := newCatalogRegistry(t, nil)
	reg.Refresh(context.Background(), newFakeSource(), map[string][]string{"fake": {"2"}})
	// Newer, supported and available: at most UpdateTargets, newest first.
	if got := versionsOf(reg.UpdateVersions("fake", "2")); got != "3.2,3.1,3" {
		t.Fatalf("update targets of 2 = %s", got)
	}
	if got := versionsOf(reg.UpdateVersions("fake", "3.1")); got != "3.2" {
		t.Fatalf("update targets of 3.1 = %s", got)
	}
	if got := reg.UpdateVersions("fake", "3.2"); len(got) != 0 {
		t.Fatalf("latest has no target: %+v", got)
	}
	// The digest of a version in use is known (same-tag updates).
	if img, d, ok := reg.LatestImage("fake", "2"); !ok || img != "fake:2" || d != digestOf('2') {
		t.Fatalf("LatestImage(2) = %s %s %v", img, d, ok)
	}
}

func TestDiscoveryPinnedVersions(t *testing.T) {
	reg := newCatalogRegistry(t, map[string][]string{"fake": {"4", "2"}})
	reg.Refresh(context.Background(), newFakeSource(), nil)
	vs := reg.Versions("fake")
	if got := versionsOf(vs); got != "4,3.2,2.1,2" {
		t.Fatalf("offered = %s", got)
	}
	i := slices.IndexFunc(vs, func(v Version) bool { return v.Version == "4" })
	if !vs[i].Pinned || !vs[i].Deprecated {
		t.Fatalf("pinned out-of-support version still offered, flagged: %+v", vs[i])
	}
}

func TestDiscoveryFailureKeepsThePreviousCatalog(t *testing.T) {
	reg := newCatalogRegistry(t, nil)
	src := newFakeSource()
	reg.Refresh(context.Background(), src, nil)
	src.failTags = true
	status, errs := reg.Refresh(context.Background(), src, nil)
	if len(errs) != 2 || !slices.Equal(status.Failed, []string{"fake", "kv"}) {
		t.Fatalf("failures reported: %+v %v", status, errs)
	}
	if got := versionsOf(reg.Versions("fake")); got != "3.2,2.1" {
		t.Fatalf("previous catalog kept: %s", got)
	}
	// endoflife.date down: the previous dates are kept.
	src.failTags, src.failSupport = false, true
	reg.Refresh(context.Background(), src, nil)
	if v, _ := reg.Version("fake", "4"); !v.Deprecated {
		t.Fatal("support dates kept when endoflife.date fails")
	}
}

func TestBuiltInCatalogBeforeDiscovery(t *testing.T) {
	reg := newCatalogRegistry(t, nil)
	if got := versionsOf(reg.Versions("fake")); got != "2,1.5" {
		t.Fatalf("built-in catalog = %s", got)
	}
	if got := versionsOf(reg.Versions("kv")); got != "8,7" {
		t.Fatalf("built-in catalog = %s", got)
	}
	// Exact patch engines only know their built-in tags.
	if v, ok := reg.Version("kv", "8"); !ok || v.Image != "kv:8.0.1" {
		t.Fatalf("kv 8 = %+v", v)
	}
	if _, ok := reg.Version("kv", "9"); ok {
		t.Fatal("an unknown exact-tag version cannot be guessed")
	}
	// No discovered version reachable on the mirror: built-in catalog.
	src := newFakeSource()
	src.digests = map[string]string{}
	reg.Refresh(context.Background(), src, nil)
	if got := versionsOf(reg.Versions("fake")); got != "2,1.5" {
		t.Fatalf("nothing available: built-in catalog, got %s", got)
	}
}

func TestVersionImage(t *testing.T) {
	reg := newCatalogRegistry(t, nil)
	reg.Refresh(context.Background(), newFakeSource(), nil)
	// An instance keeps the exact tag it was created with.
	if v, ok := reg.VersionImage("kv", "8", "kv:8.0.1"); !ok || v.Image != "kv:8.0.1" {
		t.Fatalf("recorded image = %+v %v", v, ok)
	}
	for _, image := range []string{"kv:7.0.4", "evil:8.0.1", "kv:8.0.1@sha256:x", "registry/kv:8.0.1", "kv:8.1.1"} {
		if _, ok := reg.VersionImage("kv", "8", image); ok {
			t.Errorf("%s accepted for kv 8", image)
		}
	}
}

func TestCatalogPersistence(t *testing.T) {
	dir := t.TempDir()
	reg := newCatalogRegistry(t, nil)
	if err := reg.SetStore(dir); err != nil {
		t.Fatal(err)
	}
	reg.Refresh(context.Background(), newFakeSource(), nil)
	again := newCatalogRegistry(t, nil)
	if err := again.SetStore(dir); err != nil {
		t.Fatal(err)
	}
	if got := versionsOf(again.Versions("fake")); got != "3.2,2.1" {
		t.Fatalf("reloaded catalog = %s", got)
	}
	// Invalid entries are dropped (tag of another version, bad digest).
	data, _ := os.ReadFile(filepath.Join(dir, catalogFile))
	tampered := strings.Replace(string(data), `"tag":"3.2"`, `"tag":"9.9"`, 1)
	tampered = strings.Replace(tampered, digestOf('3'), "sha256:bad", 1)
	os.WriteFile(filepath.Join(dir, catalogFile), []byte(tampered), 0o640)
	third := newCatalogRegistry(t, nil)
	third.SetStore(dir)
	if got := versionsOf(third.Versions("fake")); got != "3.1" { // 2 was never checked on the mirror
		t.Fatalf("sanitized catalog = %s", got)
	}
	os.WriteFile(filepath.Join(dir, catalogFile), []byte("{"), 0o640)
	if err := newCatalogRegistry(t, nil).SetStore(dir); err == nil {
		t.Fatal("an invalid file is reported")
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"8.10", "8.4", 1}, {"8.4", "8.4", 0}, {"7.4", "8.0", -1}, {"17", "9", 1}, {"26.10", "9.7", 1}, {"8", "8.0", 0}, {"x", "1", -1}} {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%s, %s) = %d", c.a, c.b, got)
		}
	}
}

func TestDiscoveryOffersAtMostThreeMajors(t *testing.T) {
	reg := newCatalogRegistry(t, nil)
	src := newFakeSource()
	src.tags["docker.io/library/fake"] = append(src.tags["docker.io/library/fake"], "6", "7.1", "7.2", "8")
	src.digests["fake:6"], src.digests["fake:7.1"], src.digests["fake:7.2"] = digestOf('d'), digestOf('e'), ""
	src.digests["fake:8"] = "" // latest major not on the mirror: no 4th major instead
	reg.Refresh(context.Background(), src, nil)
	if got := versionsOf(reg.Versions("fake")); got != "7.1,6,3.2" {
		t.Fatalf("offered = %s", got)
	}
}

func TestDiscoveryPinnedUpdateTargetsAreCapped(t *testing.T) {
	reg := newCatalogRegistry(t, map[string][]string{"fake": {"2.1"}})
	reg.Refresh(context.Background(), newFakeSource(), nil)
	if got := versionsOf(reg.UpdateVersions("fake", "1.5")); got != "3.2,3.1,3" {
		t.Fatalf("at most %d targets: %s", UpdateTargets, got)
	}
	// A pinned version that the registry does not publish is not offered.
	reg = newCatalogRegistry(t, map[string][]string{"fake": {"9"}})
	reg.Refresh(context.Background(), newFakeSource(), nil)
	if got := versionsOf(reg.Versions("fake")); got != "3.2,2.1" {
		t.Fatalf("unpublished pinned version offered: %s", got)
	}
}

func TestFirstDiscoveryWithoutSupportDates(t *testing.T) {
	reg := newCatalogRegistry(t, nil)
	src := newFakeSource()
	src.failSupport = true
	status, errs := reg.Refresh(context.Background(), src, nil)
	if len(errs) == 0 || !slices.Contains(status.Failed, "fake") {
		t.Fatalf("reported: %+v %v", status, errs)
	}
	// End-of-life versions are not offered blindly: the built-in catalog stays.
	if got := versionsOf(reg.Versions("fake")); got != "2,1.5" {
		t.Fatalf("offered = %s", got)
	}
}

func TestDiscoveryTransientDigestError(t *testing.T) {
	reg := newCatalogRegistry(t, nil)
	src := newFakeSource()
	reg.Refresh(context.Background(), src, nil)
	src.digestErr = map[string]error{"fake:3.2": errors.New("429 too many requests")}
	status, _ := reg.Refresh(context.Background(), src, nil)
	if got := versionsOf(reg.Versions("fake")); got != "3.2,2.1" || !slices.Contains(status.Failed, "fake") {
		t.Fatalf("previous digest kept, engine reported: %s %+v", got, status)
	}
}
