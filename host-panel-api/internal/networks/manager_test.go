package networks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"

	"host-panel-api/internal/docker"
)

// fakeDocker keeps networks in memory, like the daemon: unique names,
// overlapping subnets refused, removal refused with endpoints.
type fakeDocker struct {
	mu          sync.Mutex
	nets        map[string]network.Inspect // by name
	containers  []container.Summary
	self        string
	next        int
	disconnects []string
}

func newFakeDocker() *fakeDocker { return &fakeDocker{nets: map[string]network.Inspect{}} }

func (d *fakeDocker) InspectNetwork(_ context.Context, name string) (network.Inspect, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n, ok := d.nets[name]
	if !ok {
		return network.Inspect{}, docker.ErrNetworkNotFound
	}
	return n, nil
}

func (d *fakeDocker) CreateInternalNetwork(_ context.Context, name string, subnet netip.Prefix, labels map[string]string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.nets[name]; ok {
		return "", errors.New("network with name " + name + " already exists")
	}
	if !subnet.IsValid() {
		subnet = netip.MustParsePrefix(fmt.Sprintf("172.30.%d.0/24", d.next))
	}
	for _, n := range d.nets {
		for _, c := range n.IPAM.Config {
			if c.Subnet.Overlaps(subnet) {
				return "", errors.New("Pool overlaps with other one on this address space")
			}
		}
	}
	d.next++
	id := fmt.Sprintf("%064d", d.next)
	gw := subnet.Addr().Next()
	d.nets[name] = network.Inspect{
		Network: network.Network{Name: name, ID: id, Driver: "bridge", Scope: "local", Internal: true, Labels: labels,
			IPAM: network.IPAM{Config: []network.IPAMConfig{{Subnet: subnet, Gateway: gw}}}},
		Containers: map[string]network.EndpointResource{},
	}
	return id, nil
}

func (d *fakeDocker) RemoveNetworkByID(_ context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for name, n := range d.nets {
		if n.ID == id {
			if len(n.Containers) > 0 {
				return errors.New("error while removing network: network has active endpoints")
			}
			delete(d.nets, name)
		}
	}
	return nil
}

func (d *fakeDocker) DisconnectNetwork(_ context.Context, networkID, containerID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.disconnects = append(d.disconnects, networkID+" "+containerID)
	for _, n := range d.nets {
		if n.ID == networkID {
			for id := range n.Containers {
				if strings.HasPrefix(id, containerID) {
					delete(n.Containers, id)
				}
			}
		}
	}
	return nil
}

func (d *fakeDocker) ListContainers(context.Context) ([]container.Summary, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.containers), nil
}

func (d *fakeDocker) IsSelf(_ context.Context, ref string) bool { return ref == d.self && ref != "" }

func (d *fakeDocker) ListNetworks(context.Context) ([]network.Summary, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []network.Summary
	for _, n := range d.nets {
		out = append(out, network.Summary{Network: n.Network})
	}
	return out, nil
}

func (d *fakeDocker) InspectNetworkByID(_ context.Context, id string) (network.Inspect, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, n := range d.nets {
		if n.ID == id {
			return n, nil
		}
	}
	return network.Inspect{}, docker.ErrNetworkNotFound
}

// connect attaches a container (project label) to a network.
func (d *fakeDocker) connect(netName, id, name, project, ip string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.nets[netName]
	n.Containers[id] = network.EndpointResource{Name: name, IPv4Address: netip.MustParsePrefix(ip + "/24")}
	d.containers = append(d.containers, container.Summary{ID: id, Names: []string{"/" + name}, State: container.StateRunning,
		Labels:          map[string]string{"com.docker.compose.project": project},
		NetworkSettings: &container.NetworkSettingsSummary{Networks: map[string]*network.EndpointSettings{netName: {NetworkID: n.ID}}}})
}

// fakeSource is a kind of member with fixed networks.
type fakeSource struct {
	mu      sync.Mutex
	members []DeclaredMember
	err     error
}

func (s *fakeSource) Members(context.Context) ([]DeclaredMember, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.members), nil
}

func (s *fakeSource) SetNetworks(_ context.Context, name string, nets []string, _ string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return "", s.err
	}
	for i := range s.members {
		if s.members[i].Name == name {
			s.members[i].Networks = nets
		}
	}
	return "op-1", nil
}

func newTestManager(t *testing.T) (*Manager, *fakeDocker, *strings.Builder) {
	t.Helper()
	d := newFakeDocker()
	audit := &strings.Builder{}
	m, err := NewManager(Config{File: filepath.Join(t.TempDir(), "networks.json"), Max: 3}, d,
		slog.New(slog.NewTextHandler(io.Discard, nil)), slog.New(slog.NewTextHandler(audit, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return m, d, audit
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	e, ok := AsError(err)
	if !ok || e.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
}

func TestValidateName(t *testing.T) {
	for name, ok := range map[string]bool{"jobs": true, "a": true, "site-1": true, "a-": false, "-a": false, "A": false,
		"services": false, "pub": false, "default": false, strings.Repeat("a", 24): true, strings.Repeat("a", 25): false} {
		if got := ValidateName(name) == nil; got != ok {
			t.Errorf("ValidateName(%q) = %v", name, got)
		}
	}
}

func TestParseSubnet(t *testing.T) {
	if p, err := ParseSubnet("10.20.0.0/24"); err != nil || p.String() != "10.20.0.0/24" {
		t.Fatalf("%v %v", p, err)
	}
	for _, s := range []string{"10.20.0.1/24", "10.0.0.0/8", "10.0.0.0/30", "8.8.8.0/24", "fd00::/64", "x"} {
		if _, err := ParseSubnet(s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
}

func TestCreateListDelete(t *testing.T) {
	m, d, audit := newTestManager(t)
	ctx := context.Background()
	n, err := m.Create(ctx, CreateRequest{Name: "jobs", Description: "queues", Subnet: "10.20.0.0/24"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if n.DockerName != "loch-net-jobs" || n.Subnet != "10.20.0.0/24" || n.Gateway != "10.20.0.1" {
		t.Fatalf("%+v", n)
	}
	live := d.nets["loch-net-jobs"]
	if !live.Internal || live.Labels[LabelNetwork] != "jobs" || live.Labels[LabelManaged] != "true" {
		t.Fatalf("docker network = %+v", live)
	}
	if _, err := m.Create(ctx, CreateRequest{Name: "auto"}, "admin"); err != nil {
		t.Fatalf("Docker allocates a subnet when none is given: %v", err)
	}
	_, err = m.Create(ctx, CreateRequest{Name: "jobs"}, "admin")
	wantCode(t, err, CodeNameTaken)
	_, err = m.Create(ctx, CreateRequest{Name: "other", Subnet: "10.20.0.0/25"}, "admin")
	wantCode(t, err, CodeInvalidSubnet)
	_, err = m.Create(ctx, CreateRequest{Name: "bad", Description: "a\nb"}, "admin")
	wantCode(t, err, CodeInvalidName)

	// A Docker network created outside the panel is never adopted.
	if _, err := d.CreateInternalNetwork(ctx, "loch-net-squat", netip.Prefix{}, nil); err != nil {
		t.Fatal(err)
	}
	_, err = m.Create(ctx, CreateRequest{Name: "squat"}, "admin")
	wantCode(t, err, CodeNameTaken)

	list, _ := m.List(ctx)
	if len(list) != 2 || list[0].Name != "auto" || list[1].State != StateOK {
		t.Fatalf("list = %+v", list)
	}
	// The registry survives a restart.
	m2, err := NewManager(m.cfg, d, m.logger, m.audit)
	if err != nil {
		t.Fatal(err)
	}
	if ref, ok := m2.Lookup("jobs"); !ok || ref.ID != n.ID {
		t.Fatal("registry not persisted")
	}

	if err := m.Delete(ctx, "jobs", "admin"); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.nets["loch-net-jobs"]; ok {
		t.Fatal("the Docker network is removed")
	}
	for _, want := range []string{"network created", "network deleted"} {
		if !strings.Contains(audit.String(), want) {
			t.Fatalf("audit misses %q", want)
		}
	}
	_, err = m.Create(ctx, CreateRequest{Name: "x"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Create(ctx, CreateRequest{Name: "y"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Create(ctx, CreateRequest{Name: "z"}, "admin")
	wantCode(t, err, CodeLimit)
}

func TestDeleteRefusedWithMembers(t *testing.T) {
	m, d, _ := newTestManager(t)
	ctx := context.Background()
	if _, err := m.Create(ctx, CreateRequest{Name: "jobs"}, "admin"); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{members: []DeclaredMember{{Name: "site", Project: "hosted-site", Networks: []string{"jobs"}}}}
	m.AddSource(KindSite, src)
	wantCode(t, m.Delete(ctx, "jobs", "admin"), CodeInUse)
	src.members[0].Networks = nil
	d.connect("loch-net-jobs", "c1", "stray", "ci", "172.30.0.5")
	wantCode(t, m.Delete(ctx, "jobs", "admin"), CodeInUse)
}

func TestSharedNetwork(t *testing.T) {
	m, d, _ := newTestManager(t)
	ctx := context.Background()
	if err := m.EnsureShared(ctx, "loch-services"); err != nil {
		t.Fatal(err)
	}
	ref, ok := m.Lookup(SharedName)
	if !ok || !ref.System || ref.DockerName != "loch-services" || !strings.Contains(ref.CreateCommand(), "--internal --subnet 172.30.0.0/24 --label sh.loch.managed=true loch-services") {
		t.Fatalf("%+v", ref)
	}
	wantCode(t, m.Delete(ctx, SharedName, "admin"), CodeSystem)
	// An existing network without the panel label is refused.
	m2, _, _ := newTestManager(t)
	m2.docker = d
	if _, err := d.CreateInternalNetwork(ctx, "other-services", netip.Prefix{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := m2.EnsureShared(ctx, "other-services"); err == nil {
		t.Fatal("a network the panel did not create must not be adopted")
	}
}

func TestStatesAndReconcile(t *testing.T) {
	m, d, audit := newTestManager(t)
	ctx := context.Background()
	n, _ := m.Create(ctx, CreateRequest{Name: "jobs", Subnet: "10.20.0.0/24"}, "admin")
	if id, ok := m.Verify(ctx, "jobs"); !ok || id != n.ID {
		t.Fatal("verified")
	}
	// Removed outside the panel: recreated with the same subnet at startup.
	delete(d.nets, "loch-net-jobs")
	if _, ok := m.Verify(ctx, "jobs"); ok {
		t.Fatal("a missing network is not verified")
	}
	m.Reconcile(ctx)
	live := d.nets["loch-net-jobs"]
	ref, _ := m.Lookup("jobs")
	if live.ID != ref.ID || live.IPAM.Config[0].Subnet.String() != "10.20.0.0/24" || !strings.Contains(audit.String(), "network recreated") {
		t.Fatalf("live = %+v ref = %+v", live, ref)
	}
	// Replaced by a network of the same name: compromised, never trusted.
	delete(d.nets, "loch-net-jobs")
	if _, err := d.CreateInternalNetwork(ctx, "loch-net-jobs", netip.MustParsePrefix("10.20.0.0/24"), map[string]string{LabelManaged: "true", LabelNetwork: "jobs"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Verify(ctx, "jobs"); ok {
		t.Fatal("a replaced network must not be trusted")
	}
	list, _ := m.List(ctx)
	if list[0].State != StateCompromised {
		t.Fatalf("state = %+v", list[0])
	}
	m.Reconcile(ctx)
	if !strings.Contains(audit.String(), "network compromised") {
		t.Fatal("not audited")
	}
	_, err := m.Attach(ctx, "jobs", KindSite, "site", "admin")
	wantCode(t, err, CodeCompromised)
}

func TestCheck(t *testing.T) {
	n := Network{Name: "jobs", ID: "id", Subnet: "10.20.0.0/24"}
	good := network.Inspect{Network: network.Network{ID: "id", Driver: "bridge", Scope: "local", Internal: true,
		Labels: map[string]string{LabelManaged: "true", LabelNetwork: "jobs"},
		IPAM:   network.IPAM{Config: []network.IPAMConfig{{Subnet: netip.MustParsePrefix("10.20.0.0/24")}}}}}
	if p := check(n, good); p != "" {
		t.Fatal(p)
	}
	spoil := map[string]func(i *network.Inspect){
		"id":       func(i *network.Inspect) { i.ID = "other" },
		"external": func(i *network.Inspect) { i.Internal = false },
		"driver":   func(i *network.Inspect) { i.Driver = "macvlan" },
		"label":    func(i *network.Inspect) { i.Labels = map[string]string{LabelManaged: "true"} },
		"compose": func(i *network.Inspect) {
			i.Labels = map[string]string{LabelManaged: "true", LabelNetwork: "jobs", "com.docker.compose.project": "ci"}
		},
		"subnet": func(i *network.Inspect) {
			i.IPAM.Config = []network.IPAMConfig{{Subnet: netip.MustParsePrefix("10.21.0.0/24")}}
		},
	}
	for name, f := range spoil {
		i := good
		i.Labels = map[string]string{LabelManaged: "true", LabelNetwork: "jobs"}
		f(&i)
		if check(n, i) == "" {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestMembersAttachDetachDisconnect(t *testing.T) {
	m, d, audit := newTestManager(t)
	ctx := context.Background()
	n, _ := m.Create(ctx, CreateRequest{Name: "jobs", Subnet: "10.20.0.0/24"}, "admin")
	sites := &fakeSource{members: []DeclaredMember{{Name: "shop", Project: "hosted-shop", Container: "hosted-shop-web-1", Networks: []string{"jobs"}}, {Name: "blog", Project: "hosted-blog", Container: "hosted-blog-web-1"}}}
	insts := &fakeSource{members: []DeclaredMember{{Name: "elasticmq-jobs", Project: "svc-elasticmq-jobs", Container: "svc-elasticmq-jobs", Networks: []string{"jobs"}}}}
	m.AddSource(KindSite, sites)
	m.AddSource(KindInstance, insts)
	d.connect("loch-net-jobs", strings.Repeat("a", 64), "hosted-shop-web-1", "hosted-shop", "10.20.0.2")
	d.connect("loch-net-jobs", strings.Repeat("b", 64), "intruder", "ci-stack", "10.20.0.9")
	// A container carrying the compose label of a member is not that member.
	d.connect("loch-net-jobs", strings.Repeat("e", 64), "fake-shop", "hosted-shop", "10.20.0.10")
	d.self = strings.Repeat("c", 64)
	d.connect("loch-net-jobs", d.self, "loch-api", "panel", "10.20.0.3")

	det, err := m.Get(ctx, "jobs")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]Member{}
	for _, mem := range det.Members {
		kinds[mem.Name] = mem
	}
	if s := kinds["shop"]; s.Kind != KindSite || !s.Expected || !s.Present || s.IPv4 != "10.20.0.2" || s.ContainerID != strings.Repeat("a", 12) {
		t.Fatalf("site = %+v", s)
	}
	if q := kinds["elasticmq-jobs"]; q.Kind != KindInstance || !q.Expected || q.Present {
		t.Fatalf("declared but absent instance = %+v", q)
	}
	if kinds["intruder"].Kind != KindUnmanaged || kinds["fake-shop"].Kind != KindUnmanaged || kinds["loch-api"].Kind != KindPanel || det.Summary.Members != 4 {
		t.Fatalf("members = %+v", det.Members)
	}

	op, err := m.Attach(ctx, "jobs", KindSite, "blog", "admin")
	if err != nil || op != "op-1" || !slices.Contains(sites.members[1].Networks, "jobs") {
		t.Fatalf("attach: %q %v %+v", op, err, sites.members)
	}
	_, err = m.Attach(ctx, "jobs", KindSite, "blog", "admin")
	wantCode(t, err, CodeAlreadyAttached)
	_, err = m.Attach(ctx, "jobs", "router", "blog", "admin")
	wantCode(t, err, CodeInvalidKind)
	_, err = m.Attach(ctx, "jobs", KindSite, "nope", "admin")
	wantCode(t, err, CodeMemberNotFound)
	if _, err := m.Detach(ctx, "jobs", KindSite, "blog", "admin"); err != nil || slices.Contains(sites.members[1].Networks, "jobs") {
		t.Fatalf("detach: %v", err)
	}
	_, err = m.Detach(ctx, "jobs", KindSite, "blog", "admin")
	wantCode(t, err, CodeNotAttached)
	insts.err = errors.New("member failure")
	if _, err := m.Detach(ctx, "jobs", KindInstance, "elasticmq-jobs", "admin"); err == nil || err.Error() != "member failure" {
		t.Fatalf("the member error is returned as is: %v", err)
	}

	// Only containers the panel does not manage are disconnected directly.
	wantCode(t, m.Disconnect(ctx, "jobs", strings.Repeat("a", 12), "admin"), CodeMemberManaged)
	wantCode(t, m.Disconnect(ctx, "jobs", d.self[:12], "admin"), CodeMemberManaged)
	wantCode(t, m.Disconnect(ctx, "jobs", "nope", "admin"), CodeMemberNotFound)
	if err := m.Disconnect(ctx, "jobs", strings.Repeat("b", 12), "admin"); err != nil {
		t.Fatal(err)
	}
	if len(d.disconnects) != 1 || d.disconnects[0] != n.ID+" "+strings.Repeat("b", 12) {
		t.Fatalf("disconnects = %v", d.disconnects)
	}
	for _, want := range []string{"network member attached", "network member detached", "network container disconnected"} {
		if !strings.Contains(audit.String(), want) {
			t.Fatalf("audit misses %q", want)
		}
	}
}

func TestRegistryFileIsPrivateAndValidated(t *testing.T) {
	m, _, _ := newTestManager(t)
	if _, err := m.Create(context.Background(), CreateRequest{Name: "jobs"}, "admin"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(m.cfg.File)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v %v", info.Mode(), err)
	}
	bad := filepath.Join(t.TempDir(), "networks.json")
	if err := os.WriteFile(bad, []byte(`[{"name":"x","dockerName":"elsewhere"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager(Config{File: bad, Max: 3}, newFakeDocker(), m.logger, m.audit); err == nil {
		t.Fatal("a registry entry with another Docker name must be refused")
	}
}

func TestReservedRanges(t *testing.T) {
	m, d, _ := newTestManager(t)
	m.cfg.Reserved = []netip.Prefix{netip.MustParsePrefix("10.8.0.0/16"), netip.MustParsePrefix("172.30.0.0/16")}
	ctx := context.Background()
	_, err := m.Create(ctx, CreateRequest{Name: "vpn", Subnet: "10.8.4.0/24"}, "admin")
	wantCode(t, err, CodeInvalidSubnet)
	if e, _ := AsError(err); !strings.Contains(e.Message, "10.8.0.0/16") {
		t.Fatalf("the reserved range is named: %v", err)
	}
	// Docker allocates in 172.30.x (the fake's pool): given back.
	_, err = m.Create(ctx, CreateRequest{Name: "auto"}, "admin")
	wantCode(t, err, CodeInvalidSubnet)
	if _, ok := d.nets["loch-net-auto"]; ok {
		t.Fatal("a network allocated in a reserved range is removed")
	}
	if _, ok := m.Lookup("auto"); ok {
		t.Fatal("and not recorded")
	}
	if _, err := m.Create(ctx, CreateRequest{Name: "ok", Subnet: "10.20.0.0/24"}, "admin"); err != nil {
		t.Fatal(err)
	}
	// Ranges reserved afterwards: the overlap is reported.
	m.cfg.Reserved = append(m.cfg.Reserved, netip.MustParsePrefix("10.20.0.0/16"))
	list, _ := m.List(ctx)
	if len(list) != 1 || list[0].ReservedOverlap != "10.20.0.0/16" {
		t.Fatalf("list = %+v", list)
	}
	l := m.Limits()
	if l.Max != 3 || l.Count != 1 || len(l.ReservedRanges) != 3 || len(l.PrivateRanges) != 4 || l.MinPrefix != 16 || l.MaxPrefix != 28 {
		t.Fatalf("limits = %+v", l)
	}
}

func TestExternalNetworks(t *testing.T) {
	m, d, _ := newTestManager(t)
	ctx := context.Background()
	m.cfg.Reserved = []netip.Prefix{netip.MustParsePrefix("172.30.0.0/16")}
	mine, _ := m.Create(ctx, CreateRequest{Name: "jobs", Subnet: "10.20.0.0/24"}, "admin")
	if _, err := d.CreateInternalNetwork(ctx, "bridge", netip.MustParsePrefix("172.17.0.0/16"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CreateInternalNetwork(ctx, "ci_default", netip.MustParsePrefix("172.30.5.0/24"), map[string]string{"com.docker.compose.project": "ci"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CreateInternalNetwork(ctx, "manual", netip.MustParsePrefix("192.168.50.0/24"), nil); err != nil {
		t.Fatal(err)
	}
	d.connect("ci_default", strings.Repeat("a", 64), "ci-app-1", "ci", "172.30.5.2")
	sites := &fakeSource{members: []DeclaredMember{{Name: "shop", Project: "hosted-shop", Container: "hosted-shop-web-1"}}}
	m.AddSource(KindSite, sites)
	d.connect("ci_default", strings.Repeat("f", 64), "hosted-shop-web-1", "hosted-shop", "172.30.5.3")

	list, err := m.ListExternal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range list {
		names = append(names, e.Name)
	}
	if !slices.Equal(names, []string{"ci_default", "manual", "bridge"}) {
		t.Fatalf("panel networks excluded, Docker's own last: %v", names)
	}
	ci := list[0]
	if ci.Origin != OriginCompose || ci.ComposeProject != "ci" || ci.Containers != 2 || ci.ReservedOverlap != "172.30.0.0/16" || ci.Subnets[0] != "172.30.5.0/24" {
		t.Fatalf("ci = %+v", ci)
	}
	if list[1].Origin != OriginOther || list[2].Origin != OriginDocker {
		t.Fatalf("origins = %+v", list)
	}
	det, err := m.GetExternal(ctx, ci.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(det.Members) != 2 || det.Containers != 2 {
		t.Fatalf("detail = %+v", det)
	}
	for _, mem := range det.Members {
		if mem.Name == "shop" && (mem.Kind != KindSite || mem.Expected) {
			t.Fatalf("a site on a foreign network is recognised, not expected: %+v", mem)
		}
		if mem.Name == "ci-app-1" && (mem.Kind != KindUnmanaged || mem.IPv4 != "172.30.5.2") {
			t.Fatalf("%+v", mem)
		}
	}
	_, err = m.GetExternal(ctx, mine.ID)
	wantCode(t, err, CodeNotFound)
	_, err = m.GetExternal(ctx, ci.ID[:12])
	wantCode(t, err, CodeNotFound)
	_, err = m.GetExternal(ctx, "../etc")
	wantCode(t, err, CodeNotFound)
}
