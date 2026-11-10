package mosquitto

import (
	"crypto/pbkdf2"
	"crypto/sha512"
	"encoding/base64"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"host-panel-api/internal/services"
	"host-panel-api/internal/services/servicestest"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const testPassword = "Abcdefghijklmnopqrstuvwxyz012345"

func TestPasswordFile(t *testing.T) {
	e := New()
	digest, err := e.PasswordDigest(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	// Same entry as mosquitto_passwd: $7$<iterations>$<salt>$<PBKDF2-SHA512>.
	parts := strings.Split(digest, "$")
	if len(parts) != 5 || parts[1] != "7" || parts[2] != "101" {
		t.Fatalf("%q", digest)
	}
	salt, _ := base64.StdEncoding.DecodeString(parts[3])
	want, _ := pbkdf2.Key(sha512.New, testPassword, salt, 101, 64)
	if parts[4] != base64.StdEncoding.EncodeToString(want) {
		t.Fatal("hash differs from PBKDF2-SHA512")
	}
	// Reference produced by mosquitto_passwd 2.0.22 for this password.
	ref := "$7$101$kOYNS8F3ETHYb64+$IfCzMM93CSaEVvCv9lVcUYZGYBau41OBfmXVSmyMEqbw6gyCh1sxXMKiVGbRw4adawhxRpKG20eHEnlkybOLDQ=="
	if !digestRe.MatchString(ref) {
		t.Fatal("the reference entry must be accepted")
	}
	files, err := e.StateFiles(services.InstanceRef{Name: "iot"}, services.Version{}, nil, digest)
	if err != nil || len(files) != 1 || string(files[0].Content) != "iot:"+digest+"\n" {
		t.Fatalf("%v %v", files, err)
	}
	if _, err := e.PasswordDigest("chosen"); err == nil {
		t.Fatal("only generated passwords")
	}
	for _, bad := range []string{"", "plain", digest + "\nevil:$7$...", "$6$x$y"} {
		if _, err := e.StateFiles(services.InstanceRef{Name: "iot"}, services.Version{}, nil, bad); err == nil {
			t.Fatalf("digest %q accepted", bad)
		}
	}
}

func TestConfig(t *testing.T) {
	e := New()
	o, _ := services.ResolveOptions(e.Options(), map[string]string{"memory": "256", "persistence": "off", "message-size": "10", "queued": "10000"})
	spec := e.Service(services.InstanceRef{Engine: "mosquitto", Name: "iot"}, e.Catalog()[0], o)
	conf := string(spec.Files[0].Content)
	for _, want := range []string{"allow_anonymous false", "password_file /etc/loch/state/passwd", "persistence false", "memory_limit 201326592",
		"max_packet_size 10485760", "max_queued_messages 10000", "include_dir /etc/loch/listener.d"} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q in\n%s", want, conf)
		}
	}
	tls, ok := e.TLS(services.InstanceRef{}, e.Catalog()[0])
	if !ok || len(tls.Files) != 1 || tls.Files[0].Target != spec.Files[1].Target || !strings.Contains(string(tls.Files[0].Content), "certfile") {
		t.Fatalf("TLS replaces the listener file: %+v", tls)
	}
	if spec.User != "1883:1883" || !spec.Hardened || !spec.SecretState {
		t.Fatalf("%+v", spec)
	}
}

func TestRenderGolden(t *testing.T) {
	e := New()
	o, _ := services.ResolveOptions(e.Options(), nil)
	out, err := services.Render(e, services.InstanceRef{Engine: "mosquitto", Name: "iot"}, e.Catalog()[0], o, []string{"services"}, services.RenderConfig{DataDir: "/data/services", Networks: servicestest.Networks()})
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "compose-mosquitto-iot.golden.yaml")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil || string(out) != string(want) {
		t.Fatalf("compose differs from %s (run with -update):\n%s", golden, out)
	}
}
