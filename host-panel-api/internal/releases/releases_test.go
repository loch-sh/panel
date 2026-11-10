package releases

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testDigest = "sha256:3e393dc9c57e628ebd5e0fc0bc4524f83632b6b873503d619e856af9322d4ef7"

// fakeRegistry answers like Docker Hub: a bearer challenge, a token realm,
// paginated tag lists and manifests.
func fakeRegistry(t *testing.T) (*Client, string) {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("scope") != "repository:library/redis:pull" || r.URL.Query().Get("service") != "registry.test" {
			http.Error(w, "bad scope", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"token":"tok"}`))
	})
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="registry.test",scope="x"`)
			w.WriteHeader(http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("/v2/library/redis/tags/list", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if r.URL.Query().Get("last") == "" {
			w.Header().Set("Link", `</v2/library/redis/tags/list?last=8.4-alpine&n=2>; rel="next"`)
			_, _ = w.Write([]byte(`{"name":"library/redis","tags":["8.2-alpine","8.4-alpine"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"name":"library/redis","tags":["8.10-alpine","latest"]}`))
	})
	mux.HandleFunc("/v2/library/redis/manifests/", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if r.Method != http.MethodHead || !strings.Contains(r.Header.Get("Accept"), "image.index") {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/8.10-alpine") {
			w.Header().Set("Docker-Content-Digest", testDigest)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/api/v1/products/redis", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":{"releases":[
			{"name":"8.10","eoasFrom":null,"eolFrom":null},
			{"name":"8.2","eoasFrom":"2025-11-18","eolFrom":"2030-09-01"},
			{"name":"7.4","eoasFrom":"not a date","eolFrom":"2026-01-01"}]}}`))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	return &Client{HTTP: srv.Client(), Scheme: "http", EOLBase: srv.URL + "/api/v1"}, host
}

func TestTagsFollowsChallengeAndPages(t *testing.T) {
	c, host := fakeRegistry(t)
	tags, err := c.Tags(context.Background(), host+"/library/redis")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(tags, ",") != "8.2-alpine,8.4-alpine,8.10-alpine,latest" {
		t.Fatalf("tags = %v", tags)
	}
}

func TestDigest(t *testing.T) {
	c, host := fakeRegistry(t)
	d, err := c.Digest(context.Background(), host+"/library/redis:8.10-alpine")
	if err != nil || d != testDigest {
		t.Fatalf("digest = %q, %v", d, err)
	}
	if _, err := c.Digest(context.Background(), host+"/library/redis:7.0-alpine"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing tag: %v", err)
	}
	if _, err := c.Digest(context.Background(), host+"/library/redis"); err == nil {
		t.Fatal("an image without tag is refused")
	}
}

func TestSupport(t *testing.T) {
	c, _ := fakeRegistry(t)
	s, err := c.Support(context.Background(), "redis")
	if err != nil {
		t.Fatal(err)
	}
	if s["8.2"] != (Support{Active: "2025-11-18", Security: "2030-09-01"}) || s["8.10"] != (Support{}) {
		t.Fatalf("support = %+v", s)
	}
	if s["7.4"].Active != "" || s["7.4"].Security != "2026-01-01" {
		t.Fatalf("invalid dates are dropped: %+v", s["7.4"])
	}
	if _, err := c.Support(context.Background(), "unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown product: %v", err)
	}
	if _, err := c.Support(context.Background(), "../x"); err == nil {
		t.Fatal("invalid product name refused")
	}
}

func TestNextPageStaysOnHost(t *testing.T) {
	if got := nextPage("https", "r.test", `<https://evil.test/v2/x/tags/list?last=a>; rel="next"`); got != "" {
		t.Fatalf("another host is never followed: %q", got)
	}
	if got := nextPage("https", "r.test", `</v2/x/tags/list?last=a&n=2>; rel="next"`); got != "https://r.test/v2/x/tags/list?last=a&n=2" {
		t.Fatalf("next = %q", got)
	}
	if got := nextPage("https", "r.test", `</other>; rel="next"`); got != "" {
		t.Fatalf("outside the API: %q", got)
	}
}

func TestSplitRepository(t *testing.T) {
	if h, r, err := splitRepository("docker.io/library/redis"); err != nil || h != "registry-1.docker.io" || r != "library/redis" {
		t.Fatal(h, r, err)
	}
	for _, bad := range []string{"docker.io", "docker.io/../x", "Docker.io/x", "ghcr.io/a b"} {
		if _, _, err := splitRepository(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestTokenRealmIsRestricted(t *testing.T) {
	c := New()
	for _, realm := range []string{"https://169.254.169.254/token", "http://auth.docker.io/token", "https://evil.test/token"} {
		if _, err := c.fetchToken(context.Background(), `Bearer realm="`+realm+`",service="x"`, "library/redis", "registry-1.docker.io"); err == nil {
			t.Errorf("realm %s accepted", realm)
		}
	}
}

func TestRedirectsStayOnKnownHosts(t *testing.T) {
	via := []*http.Request{httptest.NewRequest(http.MethodGet, "https://registry-1.docker.io/v2/x", nil)}
	for target, ok := range map[string]bool{
		"https://registry-1.docker.io/v2/y": true,
		"https://auth.docker.io/token":      true,
		"https://10.0.0.1/admin":            false,
		"http://registry-1.docker.io/v2/y":  false,
	} {
		err := checkRedirect(httptest.NewRequest(http.MethodGet, target, nil), via)
		if (err == nil) != ok {
			t.Errorf("redirect to %s: %v", target, err)
		}
	}
}

func TestTagsRefusesATruncatedList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Every page announces another one.
		w.Header().Set("Link", `</v2/library/redis/tags/list?last=x&n=1>; rel="next"`)
		_, _ = w.Write([]byte(`{"tags":["8.2-alpine"]}`))
	}))
	t.Cleanup(srv.Close)
	c := &Client{HTTP: srv.Client(), Scheme: "http"}
	if _, err := c.Tags(context.Background(), strings.TrimPrefix(srv.URL, "http://")+"/library/redis"); err == nil || !strings.Contains(err.Error(), "pages") {
		t.Fatalf("err = %v", err)
	}
}
