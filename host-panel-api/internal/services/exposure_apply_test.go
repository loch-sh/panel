package services

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
)

func publicExposure(port int) Exposure {
	return Exposure{Address: netip.MustParseAddr("203.0.113.10"), Port: port}
}

func loopbackExposure(port int) Exposure {
	return Exposure{Address: netip.MustParseAddr("127.0.0.1"), Port: port}
}

func ptrExposure(e Exposure) *Exposure { return &e }

func readFile(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCreateWithPublishedPortAndTLS(t *testing.T) {
	env := newEnv(t)
	env.m.cfg.PublicHost = "db.example.com"
	mustCreate(t, env, "shop")
	env.expose(t, "fake-2", publicExposure(5416))
	creds, err := env.m.RotatePassword(context.Background(), "fake-2", "shop", "admin")
	if err != nil {
		t.Fatal(err)
	}

	over := readFile(t, filepath.Join(env.instanceDir(), OverrideFile))
	for _, want := range []string{"published: \"5416\"", "host_ip: 203.0.113.10", "tls: true", "--tls", "pub:"} {
		if !strings.Contains(over, want) {
			t.Fatalf("override misses %q:\n%s", want, over)
		}
	}
	if readFile(t, filepath.Join(env.instanceDir(), "fake-tls.conf")) != "tls\n" {
		t.Fatal("TLS file missing")
	}
	assertMode(t, filepath.Join(env.instanceDir(), CertsDir, KeyFile), 0o400)
	cert := readFile(t, filepath.Join(env.instanceDir(), CertsDir, CertFile))
	if !certificateUsable([]byte(cert), []string{"db.example.com", "svc-fake-2"}, time.Now()) {
		t.Fatal("the certificate must cover the public host and the instance name")
	}
	// compose.yaml itself never holds the exposure.
	if strings.Contains(readFile(t, filepath.Join(env.instanceDir(), ComposeFile)), "5416") {
		t.Fatal("the published port must stay out of compose.yaml")
	}
	got := map[string]string{}
	for _, c := range creds {
		got[c.Key] = c.Value
	}
	if got["external"] != "db.example.com:5416" || len(got["tlsFingerprint"]) != 64 {
		t.Fatalf("credentials = %v", got)
	}
	list, _ := env.m.Instances(context.Background())
	if len(list) != 1 || list[0].Exposure == nil || !list[0].Exposure.TLS || list[0].Exposure.Port != 5416 || !list[0].Ready {
		t.Fatalf("instances = %+v", list)
	}
}

func TestLoopbackExposureHasNoTLS(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	env.expose(t, "fake-2", loopbackExposure(5416))
	over := readFile(t, filepath.Join(env.instanceDir(), OverrideFile))
	if strings.Contains(over, "tls: true") || !strings.Contains(over, "host_ip: 127.0.0.1") {
		t.Fatalf("override:\n%s", over)
	}
	if exists(filepath.Join(env.instanceDir(), CertsDir)) {
		t.Fatal("no certificate without TLS")
	}
}

func TestTLSOffPublishesInClear(t *testing.T) {
	env := newEnv(t)
	env.m.cfg.TLS = TLSOff
	mustCreate(t, env, "shop")
	env.expose(t, "fake-2", publicExposure(5416))
	if strings.Contains(readFile(t, filepath.Join(env.instanceDir(), OverrideFile)), "tls: true") {
		t.Fatal("SERVICES_TLS=off disables TLS everywhere")
	}
}

func TestApplyExposuresAtStartup(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	ups := env.runner.count("svc-fake-2 up")

	// Nothing changed: nothing is recreated.
	env.m.applyAll(context.Background())
	if env.runner.count("svc-fake-2 up") != ups {
		t.Fatal("an up-to-date instance must not be recreated")
	}

	// A port is recorded: the instance is recreated with it, the data kept.
	env.setDesired(t, "fake-2", ptrExposure(publicExposure(5416)))
	env.m.applyAll(context.Background())
	if env.runner.count("svc-fake-2 up") != ups+1 {
		t.Fatalf("calls = %v", env.runner.commands())
	}
	if list, _ := env.m.Resources(context.Background(), "fake-2"); len(list) != 1 {
		t.Fatalf("resources lost: %+v", list)
	}
	if !strings.Contains(env.audit.String(), "service exposure applied") {
		t.Fatal("not audited")
	}

	// compose.yaml edited by hand (e.g. by CI): the panel never runs it. A
	// port to close is closed by stopping the instance (never fail-open).
	p := filepath.Join(env.instanceDir(), ComposeFile)
	os.WriteFile(p, append([]byte(readFile(t, p)), "# privileged: true\n"...), 0o640)
	ups = env.runner.count("svc-fake-2 up")
	env.setDesired(t, "fake-2", ptrExposure(publicExposure(5417))) // the 5416 port closes
	env.m.applyAll(context.Background())
	if env.runner.count("svc-fake-2 up") != ups || env.runner.count("svc-fake-2 stop") != 1 {
		t.Fatalf("an edited compose must not be run, the instance is stopped: %v", env.runner.commands())
	}
	if strings.Contains(readFile(t, filepath.Join(env.instanceDir(), OverrideFile)), "5416") {
		t.Fatal("the override must no longer publish the port")
	}
	list, _ := env.m.Instances(context.Background())
	if list[0].Ready || !list[0].ExposureError {
		t.Fatalf("instance = %+v", list[0])
	}
}

// A failed closing never brings the previous port back: the instance is stopped.
func TestFailedClosingStopsTheInstance(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	env.expose(t, "fake-2", publicExposure(5416))
	env.runner.failUpWhen = func(a AppliedExposure) bool { return a.Port == 0 }
	_, err := env.m.SetExposure(context.Background(), "fake-2", nil, "admin")
	wantCode(t, err, CodeExposureFailed)
	if strings.Contains(readFile(t, filepath.Join(env.instanceDir(), OverrideFile)), "5416") {
		t.Fatal("the closed port must not be restored")
	}
	if env.runner.count("svc-fake-2 stop") != 1 {
		t.Fatalf("calls = %v", env.runner.commands())
	}
}

func TestApplyExposureFailureRestoresAndIsNotRetried(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	before := readFile(t, filepath.Join(env.instanceDir(), OverrideFile))
	env.runner.failUpWhen = func(a AppliedExposure) bool { return a.Port == 5416 }
	env.setDesired(t, "fake-2", ptrExposure(loopbackExposure(5416)))

	env.m.applyAll(context.Background())
	if readFile(t, filepath.Join(env.instanceDir(), OverrideFile)) != before {
		t.Fatal("the previous override must be restored")
	}
	list, _ := env.m.Instances(context.Background())
	if !list[0].ExposureError || !list[0].Ready || list[0].Exposure != nil {
		t.Fatalf("instance = %+v: the previous exposure stays in effect", list[0])
	}
	ups := env.runner.count("svc-fake-2 up")
	env.m.applyAll(context.Background())
	if env.runner.count("svc-fake-2 up") != ups {
		t.Fatal("a failed configuration must not be retried at every start")
	}
	// A new configuration is tried again.
	env.runner.failUpWhen = nil
	env.setDesired(t, "fake-2", ptrExposure(loopbackExposure(5417)))
	env.m.applyAll(context.Background())
	list, _ = env.m.Instances(context.Background())
	if list[0].ExposureError || list[0].Exposure == nil || list[0].Exposure.Port != 5417 {
		t.Fatalf("instance = %+v", list[0])
	}
}

func TestApplyExposurePortTakenByAnotherContainer(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	env.docker.extra = []container.Summary{{ID: "web", Names: []string{"/site"}, Ports: []container.PortSummary{{PublicPort: 5416}}}}
	ups := env.runner.count("svc-fake-2 up")
	env.setDesired(t, "fake-2", ptrExposure(loopbackExposure(5416)))
	env.m.applyAll(context.Background())
	if env.runner.count("svc-fake-2 up") != ups {
		t.Fatal("the instance must not be touched when its port is taken")
	}
	if list, _ := env.m.Instances(context.Background()); !list[0].ExposureError {
		t.Fatal("the failure must be visible")
	}
}

func TestPublishedPortMustMatchTheOverride(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	env.expose(t, "fake-2", loopbackExposure(5416))
	c := env.docker.byProject("svc-fake-2")
	for port := range c.inspect.HostConfig.PortBindings {
		c.inspect.HostConfig.PortBindings[port][0].HostIP = netip.MustParseAddr("0.0.0.0")
	}
	_, err := env.m.Resources(context.Background(), "fake-2")
	wantCode(t, err, CodeNotReady)
}

func TestCertificateRenewal(t *testing.T) {
	now := time.Now()
	c, err := selfSignedCertificate([]string{"db.example.com", "svc-x"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !certificateUsable(c.CertPEM, []string{"db.example.com", "svc-x"}, now) {
		t.Fatal("fresh certificate")
	}
	if certificateUsable(c.CertPEM, []string{"other.example.com"}, now) {
		t.Fatal("a certificate must cover the names")
	}
	if certificateUsable(c.CertPEM, []string{"svc-x"}, now.Add(certValidity-certRenewBefore+time.Hour)) {
		t.Fatal("renewed 30 days before expiry")
	}
	if Fingerprint(c.CertPEM) == "" || ValidateCertificate(c, now) != nil {
		t.Fatal("fingerprint / validation")
	}
}

type noTLSEngine struct{ *fakeEngine }

func (noTLSEngine) TLS(InstanceRef, Version) (TLSSpec, bool) { return TLSSpec{}, false }

// Opening a port on an instance edited by hand is not applied (nothing runs).
func TestNoExposureChangeOnAModifiedInstance(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	p := filepath.Join(env.instanceDir(), ComposeFile)
	os.WriteFile(p, append([]byte(readFile(t, p)), "# privileged: true\n"...), 0o640)
	ups := env.runner.count("svc-fake-2 up")
	exp := loopbackExposure(5416)
	_, err := env.m.SetExposure(context.Background(), "fake-2", &exp, "admin")
	wantCode(t, err, CodeInstanceModified)
	env.setDesired(t, "fake-2", &exp)
	env.m.applyAll(context.Background())
	if env.runner.count("svc-fake-2 up") != ups || env.runner.count("svc-fake-2 stop") != 0 {
		t.Fatalf("calls = %v", env.runner.commands())
	}
	if strings.Contains(readFile(t, filepath.Join(env.instanceDir(), OverrideFile)), "5416") {
		t.Fatal("nothing must be written")
	}
}

// An override written by CI that is consistent with itself (publishing a
// port nobody chose) is not trusted: the instance is not used until the
// recorded exposure is applied again, which closes the port.
func TestSelfConsistentOverrideIsNotTrusted(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	inst, _ := env.m.load("fake-2")
	forged, err := RenderOverride(inst.engine, inst.ref, inst.version, nil, ptrExposure(Exposure{Address: netip.MustParseAddr("0.0.0.0"), Port: 5433}), false)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(env.instanceDir(), OverrideFile), forged, 0o640)
	_, rerr := env.m.Resources(context.Background(), "fake-2")
	wantCode(t, rerr, CodeNotReady)
	env.m.applyAll(context.Background())
	if strings.Contains(readFile(t, filepath.Join(env.instanceDir(), OverrideFile)), "5433") {
		t.Fatal("the recorded exposure (none) must be applied again")
	}
	if _, err := env.m.Resources(context.Background(), "fake-2"); err != nil {
		t.Fatal(err)
	}
}

// A policy tightened after the exposure was chosen closes it at the next
// application.
func TestExposureRevokedByPolicy(t *testing.T) {
	env := newEnv(t)
	mustCreate(t, env, "shop")
	env.expose(t, "fake-2", publicExposure(5416))
	env.m.cfg.Exposure.AllowedCIDRs = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	env.m.applyAll(context.Background())
	if strings.Contains(readFile(t, filepath.Join(env.instanceDir(), OverrideFile)), "5416") {
		t.Fatal("an exposure the policy no longer allows must be closed")
	}
	if !strings.Contains(env.audit.String(), "service exposure revoked by policy") {
		t.Fatal("not audited")
	}
	inst, _ := env.m.load("fake-2")
	if inst.desired != nil {
		t.Fatal("the record is cleared")
	}
}
