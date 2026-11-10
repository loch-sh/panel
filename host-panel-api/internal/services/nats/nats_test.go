package nats

import (
	"regexp"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"host-panel-api/internal/services"
)

const testPassword = "Abcdefghijklmnopqrstuvwxyz012345"

func TestConfigAndState(t *testing.T) {
	e := New()
	if _, err := services.NewRegistry([]services.Engine{e}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	ref := services.InstanceRef{Engine: "nats", Version: "2.12", Name: "bus"}
	o, _ := services.ResolveOptions(e.Options(), map[string]string{"memory": "512"})
	conf := string(e.Service(ref, e.Catalog()[0], o).Files[0].Content)
	if !strings.Contains(conf, `http: "127.0.0.1:8222"`) || !strings.Contains(conf, "max_memory_store: 128MB") {
		t.Fatalf("config = %s", conf)
	}
	for _, forbidden := range []string{"cluster", "gateway", "leafnodes", "no_auth_user", "system_account"} {
		if strings.Contains(conf, forbidden) {
			t.Fatalf("config must not declare %s", forbidden)
		}
	}
	digest, err := e.PasswordDigest(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	files, err := e.StateFiles(ref, e.Catalog()[0], o, digest)
	if err != nil {
		t.Fatal(err)
	}
	state := string(files[0].Content)
	if strings.Contains(state, testPassword) {
		t.Fatal("password in clear")
	}
	hash := regexp.MustCompile(`\$2a\$08\$[./A-Za-z0-9]{53}`).FindString(state)
	if hash == "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(testPassword)) != nil {
		t.Fatalf("bcrypt digest expected: %s", state)
	}
	if _, err := e.PasswordDigest("chosen"); err == nil {
		t.Fatal("only generated passwords")
	}
	if _, err := e.StateFiles(ref, e.Catalog()[0], o, `x" } ] } } include "/etc/passwd`); err == nil {
		t.Fatal("a digest read from disk must be checked (it is rendered in the config)")
	}
}
