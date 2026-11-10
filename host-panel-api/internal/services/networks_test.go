package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/network"
)

// openKV is the fake dedicated engine without authentication.
type openKV struct{ fakeDedicatedEngine }

func (openKV) Unauthenticated() bool { return true }

func TestCreateOnTheSharedNetworkByDefault(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	compose := readFile(t, filepath.Join(env.instanceDir(), ComposeFile))
	for _, want := range []string{"name: loch-services", "external: true", "--label sh.loch.managed=true loch-services"} {
		if !strings.Contains(compose, want) {
			t.Fatalf("compose misses %q:\n%s", want, compose)
		}
	}
	list, _ := env.m.Instances(context.Background())
	if !slices.Equal(list[0].Networks, []string{"services"}) || !list[0].Ready {
		t.Fatalf("instance = %+v", list[0])
	}
}

func TestCreateOnPrivateNetworks(t *testing.T) {
	env := newEnv(t)
	_, err := env.m.Create(context.Background(), CreateRequest{Engine: "kv", Version: "8", Name: "cache", Networks: []string{"jobs", "services"}}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	compose := readFile(t, filepath.Join(env.cfg.StacksDir, kvRef.Project(), ComposeFile))
	if !strings.Contains(compose, "name: loch-net-jobs") || !strings.Contains(compose, "--subnet 10.20.0.0/24") {
		t.Fatalf("compose:\n%s", compose)
	}
	inst, _ := env.m.load("kv-cache")
	if _, rerr := env.m.ready(context.Background(), inst); rerr != nil {
		t.Fatalf("container on both networks verified: %v", rerr)
	}
	for _, bad := range [][]string{{"nope"}, {"jobs", "jobs"}} {
		_, err := env.m.Create(context.Background(), CreateRequest{Engine: "kv", Version: "8", Name: "other", Networks: bad}, "admin")
		wantCode(t, err, CodeInvalidNetwork)
	}
	env.m.cfg.Render.Networks.(*fakeNetworks).broken["jobs"] = true
	_, err = env.m.Create(context.Background(), CreateRequest{Engine: "kv", Version: "8", Name: "other", Networks: []string{"jobs"}}, "admin")
	wantCode(t, err, CodeInvalidNetwork)
}

func TestUnauthenticatedEngineNeverOnTheSharedNetwork(t *testing.T) {
	env := newEnv(t)
	reg, err := NewRegistry([]Engine{openKV{}}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	env.m.reg, env.runner.reg = reg, reg
	for _, nets := range [][]string{nil, {"services"}, {"jobs", "services"}} {
		_, err := env.m.Create(context.Background(), CreateRequest{Engine: "kv", Version: "8", Name: "queue", Networks: nets}, "admin")
		wantCode(t, err, CodeInvalidNetwork)
	}
	if _, err := env.m.Create(context.Background(), CreateRequest{Engine: "kv", Version: "8", Name: "queue", Networks: []string{"jobs"}}, "admin"); err != nil {
		t.Fatal(err)
	}
	_, err = env.m.UpdateNetworks(context.Background(), "kv-queue", []string{"jobs", "services"}, "admin")
	wantCode(t, err, CodeInvalidNetwork)
	// Never published beyond the loopback either.
	exp, _ := ParseExposure("203.0.113.10", 16379)
	_, err = env.m.SetExposure(context.Background(), "kv-queue", &exp, "admin")
	wantCode(t, err, CodeInvalidExposure)
}

func TestUpdateNetworks(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	ctx := context.Background()
	nets, err := env.m.UpdateNetworks(ctx, "fake-2", []string{"services", "jobs"}, "admin")
	if err != nil || !slices.Equal(nets, []string{"jobs", "services"}) {
		t.Fatalf("%v %v", nets, err)
	}
	c := env.docker.byProject("svc-fake-2")
	if _, ok := c.inspect.NetworkSettings.Networks["loch-net-jobs"]; !ok {
		t.Fatal("the container is recreated on the new network")
	}
	if !strings.Contains(env.audit.String(), "service instance networks updated") {
		t.Fatal("not audited")
	}
	// Detaching cuts the network before the recreation.
	if _, err := env.m.UpdateNetworks(ctx, "fake-2", []string{"jobs"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env.docker.disconnects, "net-shared id-svc-fake-2") {
		t.Fatalf("disconnects = %v", env.docker.disconnects)
	}
	if list, _ := env.m.Resources(ctx, "fake-2"); len(list) != 1 {
		t.Fatalf("resources kept: %v", list)
	}
	_, err = env.m.UpdateNetworks(ctx, "fake-2", nil, "admin")
	wantCode(t, err, CodeLastNetwork)
}

func TestUpdateNetworksFailureNeverReconnects(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	ctx := context.Background()
	if _, err := env.m.UpdateNetworks(ctx, "fake-2", []string{"jobs", "services"}, "admin"); err != nil {
		t.Fatal(err)
	}
	env.runner.fail["up"] = errors.New("boom")
	_, err := env.m.UpdateNetworks(ctx, "fake-2", []string{"jobs"}, "admin")
	wantCode(t, err, CodeStartFailed)
	if env.runner.count("svc-fake-2 stop") != 1 {
		t.Fatalf("a failed detach stops the instance: %v", env.runner.commands())
	}
	if strings.Contains(readFile(t, filepath.Join(env.instanceDir(), ComposeFile)), "loch-services") {
		t.Fatal("the removed network must not come back")
	}
	// A failed attach restores the previous networks.
	delete(env.runner.fail, "up")
	if _, err := env.runner.Run(ctx, env.instanceDir(), "up"); err != nil { // the operator starts it again
		t.Fatal(err)
	}
	env.runner.failTimes["up"] = 1
	before := readFile(t, filepath.Join(env.instanceDir(), ComposeFile))
	_, err = env.m.UpdateNetworks(ctx, "fake-2", []string{"jobs", "services"}, "admin")
	wantCode(t, err, CodeStartFailed)
	if readFile(t, filepath.Join(env.instanceDir(), ComposeFile)) != before {
		t.Fatal("the previous compose file must be restored")
	}
}

func TestVerifyNetworksIsStrict(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	inst, _ := env.m.load("fake-2")
	c := env.docker.byProject("svc-fake-2")
	cases := map[string]func(eps map[string]*network.EndpointSettings){
		"extra network": func(eps map[string]*network.EndpointSettings) {
			eps["other"] = &network.EndpointSettings{NetworkID: "x"}
		},
		"missing network": func(eps map[string]*network.EndpointSettings) { delete(eps, "loch-services") },
		"replaced network": func(eps map[string]*network.EndpointSettings) {
			eps["loch-services"] = &network.EndpointSettings{NetworkID: "squatter", Aliases: []string{"svc-fake-2"}}
		},
		"missing alias": func(eps map[string]*network.EndpointSettings) {
			eps["loch-services"] = &network.EndpointSettings{NetworkID: "net-shared"}
		},
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			saved := map[string]*network.EndpointSettings{}
			for k, v := range c.inspect.NetworkSettings.Networks {
				saved[k] = v
			}
			spoil(c.inspect.NetworkSettings.Networks)
			if _, rerr := env.m.ready(context.Background(), inst); rerr == nil {
				t.Fatal("must be refused")
			}
			c.inspect.NetworkSettings.Networks = saved
		})
	}
	c.inspect.NetworkSettings = nil
	if _, rerr := env.m.ready(context.Background(), inst); rerr == nil {
		t.Fatal("no network settings must be refused")
	}
}

func TestBrokenNetworkMakesTheInstanceUntrusted(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	env.m.cfg.Render.Networks.(*fakeNetworks).broken["services"] = true
	_, err := env.m.Resources(context.Background(), "fake-2")
	wantCode(t, err, CodeNotReady)
}

func TestReferenceTamperingIsModified(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	// The networks come from the private reference: rewriting compose.yaml
	// to add a network makes the instance modified.
	p := filepath.Join(env.instanceDir(), ComposeFile)
	data := strings.Replace(readFile(t, p), "  services:\n    name: loch-services", "  jobs:\n    name: loch-net-jobs\n    external: true\n  services:\n    name: loch-services", 1)
	must(t, os.WriteFile(p, []byte(data), 0o640))
	if inst, _ := env.m.load("fake-2"); !inst.modified {
		t.Fatal("an added network must make the instance modified")
	}
	// A missing reference too.
	must(t, os.WriteFile(p, []byte(readFile(t, p)), 0o640))
	os.Remove(filepath.Join(env.cfg.Render.PrivatePath(ref2), referenceFile))
	if inst, _ := env.m.load("fake-2"); !inst.modified {
		t.Fatal("an instance without reference is modified")
	}
}

func TestNetworkSource(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	src := NetworkSource{M: env.m}
	list, err := src.Members(context.Background())
	if err != nil || len(list) != 1 || list[0].Name != "fake-2" || list[0].Project != "svc-fake-2" || !slices.Equal(list[0].Networks, []string{"services"}) {
		t.Fatalf("%+v %v", list, err)
	}
	if _, err := src.SetNetworks(context.Background(), "fake-2", []string{"jobs"}, "admin"); err != nil {
		t.Fatal(err)
	}
}
