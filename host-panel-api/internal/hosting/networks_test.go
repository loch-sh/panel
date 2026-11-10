package hosting

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"host-panel-api/internal/networks"
)

// testNetworks resolves the shared network and "jobs"; "broken" is
// registered but fails Verify.
type testNetworks struct{}

var testNetworkRefs = map[string]networks.Ref{
	networks.SharedName: {Name: networks.SharedName, DockerName: "loch-services", ID: "net-shared", System: true},
	"jobs":              {Name: "jobs", DockerName: "loch-net-jobs", ID: "net-jobs", Subnet: "10.20.0.0/24"},
	"broken":            {Name: "broken", DockerName: "loch-net-broken", ID: "net-broken"},
}

func (testNetworks) Lookup(name string) (networks.Ref, bool) {
	r, ok := testNetworkRefs[name]
	return r, ok
}

func (testNetworks) Verify(_ context.Context, name string) (string, bool) {
	r, ok := testNetworkRefs[name]
	return r.ID, ok && name != "broken"
}

func TestNetworksRendering(t *testing.T) {
	off, err := Render(validSpec("site"), testRender)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(off), "loch-") || strings.Contains(string(off), "NET_RAW") || strings.Contains(string(off), "network create") {
		t.Fatalf("a stack without networks renders without them:\n%s", off)
	}
	spec := validSpec("site")
	spec.Networks = []string{"services", "jobs"}
	Normalize(spec)
	on, err := Render(spec, testRender)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"networks:\n    - jobs\n    - services\n",
		"      - traefik\n      - jobs\n      - services\n",
		"  jobs:\n    name: loch-net-jobs\n    external: true",
		"  services:\n    name: loch-services\n    external: true",
		"cap_drop:\n      - NET_RAW",
		"#   docker network create --internal --subnet 10.20.0.0/24 --label sh.loch.managed=true --label sh.loch.network=jobs loch-net-jobs",
	} {
		if !strings.Contains(string(on), want) {
			t.Fatalf("missing %q:\n%s", want, on)
		}
	}
	parsed, err := ParseSpec(on)
	if err != nil || parsed == nil || !slices.Equal(parsed.Networks, []string{"jobs", "services"}) {
		t.Fatalf("round trip: %+v %v", parsed, err)
	}
	spec.Networks = []string{"nope"}
	if _, err := Render(spec, testRender); err == nil {
		t.Fatal("an unknown network cannot be rendered")
	}
}

func TestNetworksValidation(t *testing.T) {
	for _, nets := range [][]string{{"jobs", "jobs"}, {"Bad"}, {"pub"}} {
		spec := validSpec("site")
		spec.Networks = nets
		Normalize(spec)
		var herr *Error
		if err := Validate(spec, Snippets{}, testLimits); !errors.As(err, &herr) || herr.Code != CodeInvalidNetwork {
			t.Errorf("%v: err = %v", nets, err)
		}
	}
}

func TestCreateRefusesAnUntrustedNetwork(t *testing.T) {
	m, _ := newTestManager(t, &fakeRunner{}, &fakeDocker{})
	spec := validSpec("site")
	spec.Networks = []string{"broken"}
	_, err := m.Create(context.Background(), spec, Snippets{}, "admin")
	var herr *Error
	if !errors.As(err, &herr) || herr.Code != CodeInvalidNetwork {
		t.Fatalf("err = %v", err)
	}
}

func TestSetNetworksAndSource(t *testing.T) {
	runner := &fakeRunner{}
	m, _ := newTestManager(t, runner, &fakeDocker{})
	spec := validSpec("site")
	spec.Networks = []string{"services"}
	if _, err := m.Create(context.Background(), spec, Snippets{}, "admin"); err != nil {
		t.Fatal(err)
	}
	waitOp(t, m, "site")
	src := NetworkSource{M: m}
	op, err := src.SetNetworks(context.Background(), "site", []string{"jobs", "services"}, "admin")
	if err != nil || op == "" {
		t.Fatalf("%q %v", op, err)
	}
	waitOp(t, m, "site")
	list, err := src.Members(context.Background())
	if err != nil || len(list) != 1 || list[0].Project != "hosted-site" || !slices.Equal(list[0].Networks, []string{"jobs", "services"}) {
		t.Fatalf("%+v %v", list, err)
	}
	if s, _ := m.Get(context.Background(), "site"); s.Modified {
		t.Fatal("the stack written by SetNetworks is the panel's")
	}
}
