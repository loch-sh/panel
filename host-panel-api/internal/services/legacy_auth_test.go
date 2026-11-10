package services

import (
	"context"
	"testing"
)

func TestLegacyAuthRefusedWhereUnsupported(t *testing.T) {
	env := newEnv(t)
	_, err := env.m.Create(context.Background(), CreateRequest{Engine: "fake", Version: "2", Name: "shop", LegacyAuth: true}, "admin")
	wantCode(t, err, CodeInvalidOption)
	_, err = env.m.Create(context.Background(), CreateRequest{Engine: "kv", Version: "8", Name: "cache", LegacyAuth: true}, "admin")
	wantCode(t, err, CodeInvalidOption)
}

func TestCustomSizes(t *testing.T) {
	o := Option{Key: "memory", Values: []string{"128", "256"}, Default: "256", Custom: &CustomRange{Min: 128, Max: 4096, Step: 128}}
	for v, want := range map[string]bool{
		"128": true, "3072": true, "4096": true, "384": true,
		"4224": false, "100": false, "3000": false, "0": false, "": false,
		"03072": false, "3072 ": false, "-128": false, "1e3": false, "99999999999": false,
	} {
		if got := o.Accepts(v); got != want {
			t.Errorf("Accepts(%q) = %v, want %v", v, got, want)
		}
	}
	if (Option{Key: "policy", Values: []string{"lru"}, Default: "lru"}).Accepts("3072") {
		t.Fatal("no custom value without a range")
	}
	if _, err := ResolveOptions([]Option{o}, map[string]string{"memory": "3072"}); err != nil {
		t.Fatal(err)
	}
	if !validOptions([]Option{o}, Options{"memory": "3072"}) {
		t.Fatal("a custom value read back from a manifest is valid")
	}
	if err := checkOptions([]Option{{Key: "memory", Values: []string{"128"}, Default: "128", Custom: &CustomRange{Min: 100, Max: 50, Step: 128}}}); err == nil {
		t.Fatal("invalid range accepted")
	}
}
