package services

import (
	"context"
	"net/netip"
	"strings"
	"testing"
)

func TestParseAllowedCIDRs(t *testing.T) {
	got, err := ParseAllowedCIDRs("", false)
	if err != nil || len(got) != 1 || got[0].String() != "127.0.0.1/32" {
		t.Fatalf("default = %v, %v", got, err)
	}
	got, err = ParseAllowedCIDRs(" 127.0.0.1 , 10.0.0.0/24, ::1 ,203.0.113.10", true)
	if err != nil || len(got) != 4 || got[0].Bits() != 32 || got[2].Bits() != 128 {
		t.Fatalf("got %v, %v", got, err)
	}
	for spec, want := range map[string]string{
		"0.0.0.0/0":      "unspecified",
		"0.0.0.0/8":      "unspecified",
		"::/0":           "unspecified",
		"10.0.0.1/24":    "network address",
		"localhost":      "not an address",
		"10.0.0.0/33":    "not a CIDR",
		"224.0.0.1":      "unicast",
		"fe80::1":        "unicast",
		"::ffff:1.2.3.4": "IPv4 address itself",
	} {
		if _, err := ParseAllowedCIDRs(spec, true); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", spec, err, want)
		}
	}
	// Any non-loopback range needs SERVICES_ALLOW_PUBLIC_BIND.
	for _, spec := range []string{"10.0.0.0/24", "126.0.0.0/7", "203.0.113.10"} {
		if _, err := ParseAllowedCIDRs(spec, false); err == nil || !strings.Contains(err.Error(), "SERVICES_ALLOW_PUBLIC_BIND") {
			t.Errorf("%q: err = %v", spec, err)
		}
	}
	if _, err := ParseAllowedCIDRs("127.0.0.0/8,::1", false); err != nil {
		t.Fatalf("loopback ranges need no opt-in: %v", err)
	}
	// Every interface: listed alone, with the opt-in.
	if _, err := ParseAllowedCIDRs("0.0.0.0", false); err == nil {
		t.Fatal("0.0.0.0 needs SERVICES_ALLOW_PUBLIC_BIND")
	}
	all, err := ParseAllowedCIDRs("0.0.0.0", true)
	if err != nil || !(ExposurePolicy{AllowedCIDRs: all}).Allowed(netip.IPv4Unspecified()) {
		t.Fatalf("0.0.0.0 listed alone: %v", err)
	}
}

func TestExposurePolicyAllowed(t *testing.T) {
	cidrs, _ := ParseAllowedCIDRs("127.0.0.1,10.0.0.0/24", true)
	p := ExposurePolicy{AllowedCIDRs: cidrs}
	for addr, want := range map[string]bool{"127.0.0.1": true, "10.0.0.42": true, "10.0.1.1": false, "127.0.0.2": false, "0.0.0.0": false, "::": false} {
		if got := p.Allowed(netip.MustParseAddr(addr)); got != want {
			t.Errorf("Allowed(%s) = %v", addr, got)
		}
	}
}

func TestParseExposure(t *testing.T) {
	if exp, err := ParseExposure("127.0.0.1", 5416); err != nil || exp.String() != "127.0.0.1:5416" {
		t.Fatalf("%v %v", exp, err)
	}
	for _, c := range []struct {
		addr string
		port int
	}{{"localhost", 5416}, {"127.0.0.1", 80}, {"127.0.0.1", 40000}, {"fe80::1%eth0", 5416}} {
		if _, err := ParseExposure(c.addr, c.port); err == nil || err.Code != CodeInvalidExposure {
			t.Errorf("%v: err = %v", c, err)
		}
	}
}

func TestSetExposureValidation(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	ctx := context.Background()
	set := func(addr string, port int, optional bool) error {
		exp, perr := ParseExposure(addr, port)
		if perr != nil {
			return perr
		}
		exp.TLSOptional = optional
		_, err := env.m.SetExposure(ctx, "fake-2", &exp, "admin")
		return err
	}
	wantCode(t, set("10.9.9.9", 5416, false), CodeInvalidExposure)  // not allowed
	wantCode(t, set("127.0.0.1", 2538, false), CodeInvalidExposure) // reserved
	wantCode(t, set("203.0.113.10", 5416, true), CodeInvalidExposure)
	if strings.Contains(env.audit.String(), "service exposure changed") {
		t.Fatal("a refused exposure is not recorded")
	}
	// fake has no optional TLS; with it allowed, the engine still refuses.
	env.m.cfg.Exposure.AllowTLSOptional = true
	wantCode(t, set("203.0.113.10", 5416, true), CodeInvalidExposure)

	if err := set("203.0.113.10", 5416, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(env.audit.String(), "service exposure changed") {
		t.Fatal("not audited")
	}
	// Another instance cannot take the same port.
	createKV(t, env, "cache", nil)
	exp, _ := ParseExposure("203.0.113.10", 5416)
	_, err := env.m.SetExposure(ctx, "kv-cache", &exp, "admin")
	wantCode(t, err, CodePortInUse)
	// An out-of-support version stays on the loopback.
	if _, err := env.m.Create(ctx, CreateRequest{Engine: "fake", Version: "1.5", Name: "old"}, "admin"); err != nil {
		t.Fatal(err)
	}
	exp, _ = ParseExposure("203.0.113.10", 5420)
	_, err = env.m.SetExposure(ctx, "fake-1-5", &exp, "admin")
	wantCode(t, err, CodeInvalidExposure)
	exp, _ = ParseExposure("127.0.0.1", 5420)
	if _, err := env.m.SetExposure(ctx, "fake-1-5", &exp, "admin"); err != nil {
		t.Fatal(err)
	}
}

func TestSetExposureRefusesWithoutTLS(t *testing.T) {
	env := newEnv(t)
	reg, _ := NewRegistry([]Engine{noTLSEngine{newFakeEngine()}}, nil, nil, nil)
	env.m.reg = reg
	env.runner.reg = reg
	mustCreate(t, env, "shop")
	exp, _ := ParseExposure("203.0.113.10", 5416)
	_, err := env.m.SetExposure(context.Background(), "fake-2", &exp, "admin")
	wantCode(t, err, CodeInvalidExposure)
	env.m.cfg.TLS = TLSOff
	if _, err := env.m.SetExposure(context.Background(), "fake-2", &exp, "admin"); err != nil {
		t.Fatalf("SERVICES_TLS=off publishes in clear: %v", err)
	}
}

func TestExposureLimits(t *testing.T) {
	env := newEnv(t)
	l := env.m.ExposureLimits()
	if len(l.AllowedCIDRs) != 2 || l.MinPort != minExposedPort || l.TLSMode != TLSAuto || len(l.ReservedPorts) < len(alwaysReservedPorts) {
		t.Fatalf("%+v", l)
	}
}
