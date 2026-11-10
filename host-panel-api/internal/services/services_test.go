package services

import (
	"strings"
	"testing"
	"time"
)

func TestValidateName(t *testing.T) {
	ok := []string{"a", "shop", "shop_1", "a1", strings.Repeat("a", 32), "blog2026"}
	for _, n := range ok {
		if err := ValidateName(n); err != nil {
			t.Errorf("%q refused: %v", n, err)
		}
	}
	invalid := []string{
		"", "A", "Shop", "1shop", "_shop", "shop-1", "shop.1", "shop 1", "shop;", "sh\"op", "sh`op", "sh'op",
		"sh\\op", "shop\n", "\nshop", "shop\x00", "é", "ｓhop", strings.Repeat("a", 33),
	}
	for _, n := range invalid {
		if err := ValidateName(n); err == nil || err.Code != CodeInvalidName {
			t.Errorf("%q: err = %v, want %s", n, err, CodeInvalidName)
		}
	}
	reserved := []string{"root", "admin", "loch", "loch_meta", "default", "test"}
	for _, n := range reserved {
		if err := ValidateName(n); err == nil || err.Code != CodeNameReserved {
			t.Errorf("%q: err = %v, want %s", n, err, CodeNameReserved)
		}
	}
}

func TestGeneratePassword(t *testing.T) {
	seen := map[string]bool{}
	counts := map[byte]int{}
	for i := 0; i < 200; i++ {
		p, err := GeneratePassword()
		if err != nil {
			t.Fatal(err)
		}
		if !IsGeneratedPassword(p) || seen[p] {
			t.Fatalf("bad or repeated password %q", p)
		}
		seen[p] = true
		for j := 0; j < len(p); j++ {
			counts[p[j]]++
		}
	}
	if len(counts) < 60 { // 6400 characters over a 62-character alphabet
		t.Fatalf("only %d distinct characters", len(counts))
	}
	for _, p := range []string{"", "short", strings.Repeat("a", 31), strings.Repeat("a", 31) + "'", strings.Repeat("é", 16)} {
		if IsGeneratedPassword(p) {
			t.Fatalf("%q must not look generated", p)
		}
	}
}

func TestRegistry(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC) }
	reg, err := NewRegistry([]Engine{newFakeEngine()}, nil, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	vs := reg.Versions("fake")
	if len(vs) != 2 || vs[0].Version != "2" || vs[1].Version != "1.5" || vs[0].Deprecated || !vs[1].Deprecated {
		t.Fatalf("versions = %+v", vs)
	}
	if _, v, ok := reg.ByKey("fake-1-5"); !ok || v.Version != "1.5" {
		t.Fatal("ByKey(fake-1-5)")
	}
	// Any version the engine supports is found (an instance may run a
	// version no longer offered), never one older than its minimum.
	if _, v, ok := reg.ByKey("fake-3"); !ok || v.Version != "3" || v.Image != "fake:3" {
		t.Fatal("ByKey(fake-3)")
	}
	for _, key := range []string{"fake-0-5", "fake-3-", "fake-03", "fake-1-2-3"} {
		if _, _, ok := reg.ByKey(key); ok {
			t.Fatalf("ByKey(%s) resolved", key)
		}
	}
	if _, _, e := reg.Resolve("fake", "3"); e == nil || e.Code != CodeInvalidVersion {
		t.Fatalf("a version not offered cannot be created: %v", e)
	}
	if _, _, e := reg.Resolve("other", "1"); e == nil || e.Code != CodeInvalidEngine {
		t.Fatalf("Resolve(other) = %v", e)
	}
	if _, _, e := reg.Resolve("fake", "9"); e == nil || e.Code != CodeInvalidVersion {
		t.Fatalf("Resolve(fake, 9) = %v", e)
	}

	// Pinned versions are added to the offered ones (and marked).
	pinned, err := NewRegistry([]Engine{newFakeEngine()}, []string{"fake"}, map[string][]string{"fake": {"1.5", "3"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if vs := pinned.Versions("fake"); len(vs) != 3 || vs[0].Version != "3" || !vs[0].Pinned || vs[1].Pinned || !vs[2].Pinned {
		t.Fatalf("pinned registry: %+v", vs)
	}
	for _, tc := range []struct {
		enabled []string
		allowed map[string][]string
	}{
		{[]string{"nope"}, nil},
		{[]string{"fake", "fake"}, nil},
		{nil, map[string][]string{"fake": {"0.5"}}}, // older than the engine minimum
		{nil, map[string][]string{"fake": {"03"}}},
		{nil, map[string][]string{"fake": {"2", "2"}}},
		{nil, map[string][]string{"other": {"1"}}},
	} {
		if _, err := NewRegistry([]Engine{newFakeEngine()}, tc.enabled, tc.allowed, now); err == nil {
			t.Fatalf("config %+v must be refused", tc)
		}
	}
}

type badLifecycle struct{ *fakeEngine }

func (badLifecycle) Lifecycle() Lifecycle { return Dedicated }

type noProvisioner struct{ *fakeEngine }

func (noProvisioner) Provisioner() Provisioner { return nil }

type badFile struct {
	*fakeEngine
	name, target string
}

func (b badFile) Service(InstanceRef, Version, Options) ServiceSpec {
	return ServiceSpec{Files: []GeneratedFile{{Name: b.name, Target: b.target}}}
}

func TestRegistryRefusesInvalidEngines(t *testing.T) {
	bad := []Engine{
		badLifecycle{newFakeEngine()},
		noProvisioner{newFakeEngine()},
		badFile{newFakeEngine(), "../x", "/etc/x"},
		badFile{newFakeEngine(), "compose.yaml", "/etc/x"},
		badFile{newFakeEngine(), "secrets", "/etc/x"},
		badFile{newFakeEngine(), ".loch-managed", "/etc/x"},
		badFile{newFakeEngine(), "ok.conf", "relative"},
		badFile{newFakeEngine(), "ok.conf", "/data"}, // shadows the data mount
	}
	for i, e := range bad {
		if _, err := NewRegistry([]Engine{e}, nil, nil, nil); err == nil {
			t.Errorf("engine %d must be refused", i)
		}
	}
}
