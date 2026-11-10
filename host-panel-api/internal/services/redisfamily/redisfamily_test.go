package redisfamily

import (
	"strings"
	"testing"

	"host-panel-api/internal/services"
)

const testPassword = "Abcdefghijklmnopqrstuvwxyz012345"

func opts(e *Engine, over map[string]string) services.Options {
	o, err := services.ResolveOptions(e.Options(), over)
	if err != nil {
		panic(err)
	}
	return o
}

func TestRegistryAcceptsEngines(t *testing.T) {
	if _, err := services.NewRegistry([]services.Engine{NewRedis(), NewValkey()}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestConfig(t *testing.T) {
	e := NewRedis()
	ref := services.InstanceRef{Engine: "redis", Version: "8.4", Name: "cache"}
	spec := e.Service(ref, e.Catalog()[0], opts(e, map[string]string{"memory": "512", "persistence": "rdb"}))
	conf := string(spec.Files[0].Content)
	for _, want := range []string{"maxmemory 256mb", "enable-debug-command no", "enable-module-command no", "enable-protected-configs no",
		"sanitize-dump-payload yes", "appendonly no", "aclfile /etc/loch/state/users.acl", "proto-max-bulk-len 16mb"} {
		if !strings.Contains(conf, want) {
			t.Errorf("config lacks %q", want)
		}
	}
	if spec.MemLimit != 512<<20 || !spec.Hardened || !spec.NoAdminSecret || spec.User != "999:1000" {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestACL(t *testing.T) {
	r := NewRedis()
	v8 := services.Version{Version: "8.4"}
	v7 := services.Version{Version: "7.4"}
	ref := services.InstanceRef{Engine: "redis", Name: "cache"}
	acl := func(e *Engine, v services.Version, over map[string]string) string {
		digest, err := e.PasswordDigest(testPassword)
		if err != nil {
			t.Fatal(err)
		}
		files, err := e.StateFiles(ref, v, opts(e, over), digest)
		if err != nil {
			t.Fatal(err)
		}
		return string(files[0].Content)
	}
	got := acl(r, v8, nil)
	if strings.Contains(got, testPassword) || !strings.Contains(got, "user default off") || !strings.Contains(got, "-@admin -@dangerous -debug -migrate") {
		t.Fatalf("acl = %s", got)
	}
	if !strings.Contains(got, "-@json") {
		t.Fatal("Redis 8 modules are off by default")
	}
	if strings.Contains(acl(r, v8, map[string]string{"modules": "on"}), "-@json") {
		t.Fatal("modules can be turned on")
	}
	if strings.Contains(acl(r, v7, nil), "-@json") {
		t.Fatal("Redis 7 has no module categories (an unknown one stops the server)")
	}
	if !strings.Contains(acl(NewValkey(), services.Version{Version: "9.0"}, map[string]string{"scripting": "off"}), "-@scripting") {
		t.Fatal("scripting can be turned off")
	}
	if _, err := r.PasswordDigest("chosen-by-user"); err == nil {
		t.Fatal("only generated passwords")
	}
	if _, err := r.StateFiles(ref, v8, opts(r, nil), "not-a-digest\nuser evil on nopass +@all"); err == nil {
		t.Fatal("a digest read from disk must be checked (it is rendered in the ACL)")
	}
}

func TestConnectionInfo(t *testing.T) {
	creds := NewRedis().ConnectionInfo(services.ConnParams{Host: "svc-redis-cache", Port: 6379, Name: "cache", Password: testPassword, TLS: true})
	for _, c := range creds {
		if c.Key == "uri" && !strings.HasPrefix(c.Value, "rediss://cache:") {
			t.Fatalf("uri = %s", c.Value)
		}
	}
}
