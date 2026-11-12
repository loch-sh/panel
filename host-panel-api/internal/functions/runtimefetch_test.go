package functions

import (
	"bytes"
	"context"
	"debug/elf"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fetchEnv is a provider whose source is an httptest TLS server.
type fetchEnv struct {
	p     *RuntimeProvider
	store *RuntimeStore
	rel   TrustedRelease
	audit *bytes.Buffer
	logs  *bytes.Buffer
}

func newFetchEnv(t *testing.T, bin []byte, source func(rel TrustedRelease) RuntimeSource) *fetchEnv {
	t.Helper()
	rel := TrustedRelease{Version: "v1.0.2", Arch: "arm64", SHA256: sha(bin), Size: int64(len(bin))}
	store, _ := testStore(t, rel)
	audit, logs := &bytes.Buffer{}, &bytes.Buffer{}
	p := NewRuntimeProvider(store, "arm64", source(rel), slog.New(slog.NewTextHandler(logs, nil)), slog.New(slog.NewJSONHandler(audit, nil)))
	p.Backoff, p.CheckInterval = nil, 0
	return &fetchEnv{p: p, store: store, rel: rel, audit: audit, logs: logs}
}

// tlsDownloader is a fake GitHub: the releases list (a prerelease and
// rel's version), rel's SHA256SUMS, and h for everything else (the binary).
func tlsDownloader(t *testing.T, rel TrustedRelease, h http.Handler, timeout time.Duration) (*RuntimeDownloader, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/releases", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `[{"tag_name":"v9.0.0-rc.1"},{"tag_name":%q},{"tag_name":"v1.0.1"}]`, rel.Version)
	})
	mux.HandleFunc("/pkg/"+rel.Version+"/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  %s\n%s  %s.cdx.json\n", rel.SHA256, RuntimeAssetName(rel.Arch), strings.Repeat("0", 64), RuntimeAssetName(rel.Arch))
	})
	mux.Handle("/", h)
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return newRuntimeDownloader(srv.URL+"/pkg", srv.URL+"/releases", []string{u.Host}, srv.Client().Transport, timeout), srv
}

func serveBinary(bin []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pkg/v1.0.2/loch-lambda-runtime-linux-arm64" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(bin)
	}
}

func (e *fetchEnv) assertNotStored(t *testing.T) {
	t.Helper()
	if _, err := os.Lstat(e.store.Path(e.rel.Version, e.rel.Arch)); err == nil {
		t.Fatal("a refused binary was stored")
	}
	if _, err := e.p.Latest("arm64"); CodeOf(err) != CodeRuntimeUnavailable {
		t.Fatalf("Latest = %v, want %s", err, CodeRuntimeUnavailable)
	}
}

func TestRuntimeDownloadInstalls(t *testing.T) {
	bin := fakeELF(elf.EM_AARCH64, "good")
	var dl *RuntimeDownloader
	e := newFetchEnv(t, bin, func(rel TrustedRelease) RuntimeSource {
		dl, _ = tlsDownloader(t, rel, serveBinary(bin), 5*time.Second)
		return dl
	})
	if s := e.p.Status(); s.State != RuntimeIdle {
		t.Fatalf("status before Run = %+v", s)
	}
	e.p.Run(context.Background())
	b, err := e.p.Latest("arm64")
	if err != nil || b.Version != "v1.0.2" || b.SHA256 != e.rel.SHA256 {
		t.Fatalf("Latest = %+v, %v", b, err)
	}
	if s := e.p.Status(); s != (RuntimeState{State: "installed", Version: "v1.0.2"}) {
		t.Fatalf("status = %+v", s)
	}
	a := e.audit.String()
	if !strings.Contains(a, `"msg":"function runtime installed"`) || !strings.Contains(a, `"source":"download"`) ||
		!strings.Contains(a, e.rel.SHA256) {
		t.Fatalf("audit = %s", a)
	}
	// Already installed: nothing downloaded nor audited again.
	e.audit.Reset()
	if err := e.p.attempt(context.Background(), "", "arm64"); err != nil || e.audit.Len() != 0 {
		t.Fatalf("second attempt: %v, audit %s", err, e.audit)
	}
}

func TestRuntimeDownloadRefusesWrongHash(t *testing.T) {
	good := fakeELF(elf.EM_AARCH64, "good")
	evil := fakeELF(elf.EM_AARCH64, "evil") // same size, other content
	e := newFetchEnv(t, good, func(rel TrustedRelease) RuntimeSource {
		dl, _ := tlsDownloader(t, rel, serveBinary(evil), 5*time.Second)
		return dl
	})
	if err := e.p.attempt(context.Background(), "", "arm64"); CodeOf(err) != CodeRuntimeUntrusted {
		t.Fatalf("attempt = %v", err)
	}
	e.assertNotStored(t)
	if !strings.Contains(e.audit.String(), `"msg":"function runtime refused"`) {
		t.Fatalf("audit = %s", e.audit)
	}
	if s := e.p.Status(); s.State != RuntimeUntrusted {
		t.Fatalf("status = %+v", s)
	}
}

func TestRuntimeDownloadRefusesTooLarge(t *testing.T) {
	good := fakeELF(elf.EM_AARCH64, "good")
	big := append(fakeELF(elf.EM_AARCH64, "good"), make([]byte, 4096)...)
	for name, h := range map[string]http.HandlerFunc{
		"announced": serveBinary(big),
		"chunked": func(w http.ResponseWriter, r *http.Request) {
			w.(http.Flusher).Flush() // no Content-Length
			_, _ = w.Write(big)
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newFetchEnv(t, good, func(rel TrustedRelease) RuntimeSource {
				dl, _ := tlsDownloader(t, rel, h, 5*time.Second)
				return dl
			})
			if err := e.p.attempt(context.Background(), "", "arm64"); err == nil {
				t.Fatal("too large binary accepted")
			}
			e.assertNotStored(t)
			if !strings.Contains(e.audit.String(), `"msg":"function runtime refused"`) {
				t.Fatalf("audit = %s", e.audit)
			}
		})
	}
}

func TestRuntimeDownloadRedirects(t *testing.T) {
	bin := fakeELF(elf.EM_AARCH64, "good")
	other := httptest.NewTLSServer(serveBinary(bin)) // a host that is not allowed
	t.Cleanup(other.Close)
	otherURL, _ := url.Parse(other.URL)
	for name, target := range map[string]string{
		"disallowed host": other.URL + "/pkg/v1.0.2/loch-lambda-runtime-linux-arm64",
		"plain http":      "http://" + otherURL.Host + "/pkg/v1.0.2/loch-lambda-runtime-linux-arm64",
	} {
		t.Run(name, func(t *testing.T) {
			hits := 0
			e := newFetchEnv(t, bin, func(rel TrustedRelease) RuntimeSource {
				dl, _ := tlsDownloader(t, rel, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits++
					http.Redirect(w, r, target, http.StatusFound)
				}), 5*time.Second)
				return dl
			})
			err := e.p.attempt(context.Background(), "", "arm64")
			if err == nil || !strings.Contains(err.Error(), "refused redirect") || hits != 1 {
				t.Fatalf("attempt = %v (hits %d)", err, hits)
			}
			e.assertNotStored(t)
			if strings.Contains(e.audit.String(), "function runtime") {
				t.Fatalf("a fetch failure is not a refused binary: %s", e.audit)
			}
		})
	}
	// An allowed redirect (same host) is followed.
	e := newFetchEnv(t, bin, func(rel TrustedRelease) RuntimeSource {
		mux := http.NewServeMux()
		mux.HandleFunc("/pkg/v1.0.2/loch-lambda-runtime-linux-arm64", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/asset", http.StatusFound)
		})
		mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(bin) })
		dl, _ := tlsDownloader(t, rel, mux, 5*time.Second)
		return dl
	})
	if err := e.p.attempt(context.Background(), "", "arm64"); err != nil {
		t.Fatalf("allowed redirect: %v", err)
	}
}

func TestRuntimeDownloadRefusesPlainBaseURL(t *testing.T) {
	d := newRuntimeDownloader("http://github.com/x", "http://api.github.com/r", RuntimeDownloadHosts, http.DefaultTransport, time.Second)
	if _, err := d.Open(context.Background(), TrustedRelease{Version: "v1.0.2", Arch: "arm64"}); err == nil {
		t.Fatal("plain HTTP base URL accepted")
	}
}

func TestRuntimeDownloadOrigin(t *testing.T) {
	// The releases come from github.com/loch-sh/lambda-runtime only.
	d := NewRuntimeDownloader()
	for raw, want := range map[string]string{
		RuntimeReleaseBaseURL + "/v1.0.2/" + RuntimeAssetName("arm64"): "https://github.com/loch-sh/lambda-runtime/releases/download/v1.0.2/loch-lambda-runtime-linux-arm64",
		RuntimeReleasesURL: "https://api.github.com/repos/loch-sh/lambda-runtime/releases?per_page=100",
	} {
		u, err := url.Parse(raw)
		if err != nil || u.String() != want || d.allowed(u) != nil {
			t.Fatalf("URL %s (%v)", u, err)
		}
	}
	for _, host := range []string{"release-assets.githubusercontent.com", "objects.githubusercontent.com"} {
		if d.allowed(&url.URL{Scheme: "https", Host: host, Path: "/x"}) != nil {
			t.Errorf("%s refused", host)
		}
	}
	for _, host := range []string{"gitlab.com", "evil.githubusercontent.com", "github.com:8443", "raw.githubusercontent.com"} {
		if d.allowed(&url.URL{Scheme: "https", Host: host, Path: "/x"}) == nil {
			t.Errorf("%s allowed", host)
		}
	}
}

func TestRuntimeDownloadTimeout(t *testing.T) {
	bin := fakeELF(elf.EM_AARCH64, "good")
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	e := newFetchEnv(t, bin, func(rel TrustedRelease) RuntimeSource {
		dl, _ := tlsDownloader(t, rel, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(len(bin)))
			_, _ = w.Write(bin[:10])
			w.(http.Flusher).Flush()
			select { // stalls mid-body
			case <-release:
			case <-r.Context().Done():
			}
		}), 300*time.Millisecond)
		return dl
	})
	start := time.Now()
	err := e.p.attempt(context.Background(), "", "arm64")
	if CodeOf(err) != CodeRuntimeUnavailable || time.Since(start) > 5*time.Second {
		t.Fatalf("attempt = %v after %s", err, time.Since(start))
	}
	e.assertNotStored(t)
	if strings.Contains(e.audit.String(), "function runtime refused") {
		t.Fatalf("a timeout is not a refused binary: %s", e.audit)
	}
}

// writeSourceRelease writes a release directory of a source directory:
// SHA256SUMS listing rel, and bin as rel's binary (nil: none).
func writeSourceRelease(t *testing.T, dir string, rel TrustedRelease, bin []byte) string {
	t.Helper()
	rd := filepath.Join(dir, rel.Version)
	if err := os.MkdirAll(rd, 0o755); err != nil {
		t.Fatal(err)
	}
	sums := fmt.Sprintf("%s  %s\n", rel.SHA256, RuntimeAssetName(rel.Arch))
	if err := os.WriteFile(filepath.Join(rd, "SHA256SUMS"), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}
	if bin != nil {
		if err := os.WriteFile(filepath.Join(rd, RuntimeAssetName(rel.Arch)), bin, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return rd
}

func TestRuntimeSourceDir(t *testing.T) {
	bin := fakeELF(elf.EM_AARCH64, "good")
	dir := t.TempDir()
	e := newFetchEnv(t, bin, func(TrustedRelease) RuntimeSource { return RuntimeSourceDir{Dir: dir} })
	// No release directory: unavailable, nothing audited.
	if err := e.p.attempt(context.Background(), "", "arm64"); err == nil || e.audit.Len() != 0 {
		t.Fatalf("no release: %v, audit %s", err, e.audit)
	}
	// Older releases and prereleases are ignored.
	writeSourceRelease(t, dir, TrustedRelease{Version: "v1.0.0", Arch: "arm64", SHA256: e.rel.SHA256}, bin)
	if err := os.Mkdir(filepath.Join(dir, "v9.0.0-rc.1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := e.p.attempt(context.Background(), "", "arm64"); err == nil || e.audit.Len() != 0 {
		t.Fatalf("only an old release: %v, audit %s", err, e.audit)
	}
	// Wrong file of the right size: refused.
	writeSourceRelease(t, dir, e.rel, fakeELF(elf.EM_AARCH64, "evil"))
	if err := e.p.attempt(context.Background(), "", "arm64"); CodeOf(err) != CodeRuntimeUntrusted {
		t.Fatalf("wrong file: %v", err)
	}
	e.assertNotStored(t)
	writeSourceRelease(t, dir, e.rel, bin)
	e.audit.Reset()
	if err := e.p.attempt(context.Background(), "", "arm64"); err != nil {
		t.Fatalf("good file: %v", err)
	}
	if _, err := e.p.Latest("arm64"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.audit.String(), `"source":"directory"`) {
		t.Fatalf("audit = %s", e.audit)
	}
}

func TestRuntimeProviderOnDemandAndDisabled(t *testing.T) {
	bin := fakeELF(elf.EM_AARCH64, "good")
	// Downloads disabled: unavailable, nothing attempted.
	e := newFetchEnv(t, bin, func(TrustedRelease) RuntimeSource { return nil })
	e.p.Run(context.Background())
	if _, err := e.p.Latest("arm64"); CodeOf(err) != CodeRuntimeUnavailable {
		t.Fatal(err)
	}
	if s := e.p.Status(); s.State != "unavailable" {
		t.Fatalf("status = %+v", s)
	}
	// On demand: Latest starts a background attempt.
	e = newFetchEnv(t, bin, func(rel TrustedRelease) RuntimeSource {
		dl, _ := tlsDownloader(t, rel, serveBinary(bin), 5*time.Second)
		return dl
	})
	if _, err := e.p.Latest("arm64"); CodeOf(err) != CodeRuntimeUnavailable {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := e.p.Latest("arm64"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the on-demand attempt did not install the binary")
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitIdle(t, e.p)
	// A missing pinned binary is unavailable (installed again), not tampered.
	b, _ := e.p.Latest("arm64")
	os.Remove(e.store.Path(b.Version, b.Arch))
	e.p.MinInterval = 0
	if _, err := e.p.Verify(b); CodeOf(err) != CodeRuntimeUnavailable {
		t.Fatalf("Verify of a missing binary = %v", err)
	}
	// ... and installed again in the background.
	for deadline := time.Now().Add(5 * time.Second); ; {
		if _, err := e.store.Verify(b); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the missing binary was not installed again")
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitIdle(t, e.p)
}

// waitIdle waits for a background attempt to end (before the temporary
// directories are removed).
func waitIdle(t *testing.T, p *RuntimeProvider) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		p.mu.Lock()
		running := p.running
		p.mu.Unlock()
		if !running {
			return
		}
	}
	t.Fatal("an installation attempt is still running")
}

func TestManagerRuntimeUnavailable(t *testing.T) {
	e := newTestEnv(t)
	bin := fakeELF(elf.EM_AARCH64, "good")
	rel := TrustedRelease{Version: "v1.0.2", Arch: "arm64", SHA256: sha(bin), Size: int64(len(bin))}
	store := NewRuntimeStore(e.m.paths, TrustTable{Releases: []TrustedRelease{rel}})
	dl, _ := tlsDownloader(t, rel, http.NotFoundHandler(), time.Second)
	p := NewRuntimeProvider(store, "arm64", dl, slog.New(slog.DiscardHandler), slog.New(slog.DiscardHandler))
	p.Backoff, p.CheckInterval = nil, 0
	p.Run(context.Background()) // the download fails
	e.m.runtimes = p
	_, err := e.m.Create(context.Background(), CreateRequest{Name: "resize", Runtime: "nodejs", RuntimeVersion: "22", Handler: "index.handler"}, "admin@example.com")
	if CodeOf(err) != CodeRuntimeUnavailable {
		t.Fatalf("create = %v", err)
	}
	info := e.m.Info()
	if info.RuntimeReady || info.Runtime.State != "unavailable" {
		t.Fatalf("info runtime = %+v", info.Runtime)
	}
}

// countingSource counts the opens of a source.
type countingSource struct {
	RuntimeSource
	mu    sync.Mutex
	opens int
}

func (c *countingSource) Open(ctx context.Context, rel TrustedRelease) (io.ReadCloser, error) {
	c.mu.Lock()
	c.opens++
	c.mu.Unlock()
	return c.RuntimeSource.Open(ctx, rel)
}

func (c *countingSource) count() int { c.mu.Lock(); defer c.mu.Unlock(); return c.opens }

func TestRuntimeSourceDirFIFO(t *testing.T) {
	bin := fakeELF(elf.EM_AARCH64, "good")
	dir := t.TempDir()
	e := newFetchEnv(t, bin, func(TrustedRelease) RuntimeSource { return RuntimeSourceDir{Dir: dir} })
	rd := writeSourceRelease(t, dir, e.rel, nil)
	if err := syscall.Mkfifo(filepath.Join(rd, "loch-lambda-runtime-linux-arm64"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckRuntimeSourceDir(dir); err == nil {
		t.Fatal("a FIFO passed the startup check")
	}
	done := make(chan error, 1)
	go func() { done <- e.p.attempt(context.Background(), "", "arm64") }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a FIFO was installed")
		}
	case <-time.After(time.Second):
		t.Fatal("the installer blocks on a FIFO")
	}
	// Opened directly too (the startup check bypassed): no block.
	done2 := make(chan error, 1)
	go func() {
		f, err := os.OpenFile(filepath.Join(rd, "loch-lambda-runtime-linux-arm64"), os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
		if err == nil {
			f.Close()
		}
		done2 <- err
	}()
	select {
	case err := <-done2:
		_ = err // only the absence of blocking is checked: the open may fail
	case <-time.After(time.Second):
		t.Fatal("O_NONBLOCK open blocks")
	}
}

func TestRuntimeSourceDirPermissions(t *testing.T) {
	dir := t.TempDir()
	rd := writeSourceRelease(t, dir, TrustedRelease{Version: "v1.0.2", Arch: "amd64", SHA256: strings.Repeat("a", 64)}, nil)
	file := filepath.Join(rd, "loch-lambda-runtime-linux-amd64")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckRuntimeSourceDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := CheckRuntimeSourceDir(file); err == nil {
		t.Error("a file accepted as the directory")
	}
	if err := CheckRuntimeSourceDir(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing directory accepted")
	}
	mustChmod(t, file, 0o666)
	if err := CheckRuntimeSourceDir(dir); err == nil {
		t.Error("a world-writable file accepted")
	}
	mustChmod(t, file, 0o644)
	mustChmod(t, dir, 0o770)
	if err := CheckRuntimeSourceDir(dir); err == nil {
		t.Error("a group-writable directory accepted")
	}
	mustChmod(t, dir, 0o700)
	mustChmod(t, rd, 0o777)
	if err := CheckRuntimeSourceDir(dir); err == nil {
		t.Error("a world-writable release directory accepted")
	}
	mustChmod(t, rd, 0o755)
	mustChmod(t, filepath.Join(rd, "SHA256SUMS"), 0o666)
	if err := CheckRuntimeSourceDir(dir); err == nil {
		t.Error("a world-writable SHA256SUMS accepted")
	}
	if _, err := (RuntimeSourceDir{Dir: dir}).Release(context.Background(), ""); err == nil {
		t.Error("a world-writable SHA256SUMS read")
	}
	mustChmod(t, filepath.Join(rd, "SHA256SUMS"), 0o644)
	os.Remove(file)
	if err := os.Symlink("/etc/hosts", file); err != nil {
		t.Fatal(err)
	}
	if err := CheckRuntimeSourceDir(dir); err == nil {
		t.Error("a symlinked runtime file accepted")
	}
	if _, err := (RuntimeSourceDir{Dir: dir}).Open(context.Background(), TrustedRelease{Version: "v1.0.2", Arch: "amd64"}); err == nil {
		t.Error("a symlinked runtime file opened")
	}
}

func TestRuntimeProviderStates(t *testing.T) {
	bin := fakeELF(elf.EM_AARCH64, "good")
	quiet := slog.New(slog.DiscardHandler)
	// No binary for the host architecture in the release: unavailable.
	store, _ := testStore(t)
	logs := &bytes.Buffer{}
	src := t.TempDir()
	writeSourceRelease(t, src, TrustedRelease{Version: "v1.0.2", Arch: "amd64", SHA256: sha(bin)}, nil)
	p := NewRuntimeProvider(store, "arm64", RuntimeSourceDir{Dir: src}, slog.New(slog.NewTextHandler(logs, nil)), quiet)
	p.Backoff, p.CheckInterval = nil, 0
	if p.Status().State != RuntimeIdle {
		t.Fatal(p.Status())
	}
	p.Run(context.Background())
	if p.Status().State != RuntimeUnavailable || !strings.Contains(logs.String(), "has no binary for the host architecture") {
		t.Fatalf("no release: %+v, logs %s", p.Status(), logs)
	}
	// installing while the download runs, then installed.
	release := make(chan struct{})
	e := newFetchEnv(t, bin, func(rel TrustedRelease) RuntimeSource {
		dl, _ := tlsDownloader(t, rel, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-release
			_, _ = w.Write(bin)
		}), 5*time.Second)
		return dl
	})
	done := make(chan struct{})
	go func() { e.p.Run(context.Background()); close(done) }()
	for deadline := time.Now().Add(5 * time.Second); e.p.Status().State != RuntimeInstalling; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("state = %+v", e.p.Status())
		}
	}
	close(release)
	<-done
	if s := e.p.Status(); s.State != RuntimeInstalled || s.Version != "v1.0.2" {
		t.Fatalf("state = %+v", s)
	}
	// Removed afterwards: unavailable.
	os.Remove(e.store.Path("v1.0.2", "arm64"))
	if s := e.p.Status(); s.State != RuntimeUnavailable {
		t.Fatalf("state after removal = %+v", s)
	}
	// A failed fetch: unavailable.
	e = newFetchEnv(t, bin, func(rel TrustedRelease) RuntimeSource {
		dl, _ := tlsDownloader(t, rel, http.NotFoundHandler(), time.Second)
		return dl
	})
	e.p.Run(context.Background())
	if s := e.p.Status(); s.State != RuntimeUnavailable {
		t.Fatalf("state after 404 = %+v", s)
	}
}

func TestRuntimeProviderBurstAndRefusalBackoff(t *testing.T) {
	good := fakeELF(elf.EM_AARCH64, "good")
	evil := fakeELF(elf.EM_AARCH64, "evil")
	release := make(chan struct{})
	var src *countingSource
	e := newFetchEnv(t, good, func(rel TrustedRelease) RuntimeSource {
		dl, _ := tlsDownloader(t, rel, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-release
			_, _ = w.Write(evil)
		}), 5*time.Second)
		src = &countingSource{RuntimeSource: dl}
		return src
	})
	e.p.MinInterval = 0
	clock := time.Now()
	var clockMu sync.Mutex
	e.p.now = func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.p.Latest("arm64") // the source is refused: the error is expected, its effect asserted below
		}()
	}
	wg.Wait()
	close(release)
	waitIdle(t, e.p)
	if n := src.count(); n != 1 {
		t.Fatalf("a burst of 50 opened the source %d times", n)
	}
	if e.p.Status().State != RuntimeUntrusted {
		t.Fatal(e.p.Status())
	}
	// Refused: no on-demand retry for 30 minutes.
	_, _ = e.p.Latest("arm64") // the error is irrelevant: only the source opening is counted
	waitIdle(t, e.p)
	if n := src.count(); n != 1 {
		t.Fatalf("retried %d times within the refusal backoff", n)
	}
	clockMu.Lock()
	clock = clock.Add(31 * time.Minute)
	clockMu.Unlock()
	_, _ = e.p.Latest("arm64") // triggers the retry counted below
	for deadline := time.Now().Add(5 * time.Second); src.count() != 2; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no retry after the refusal backoff")
		}
	}
	waitIdle(t, e.p)
}

func TestRuntimeProviderAuditsModifiedStore(t *testing.T) {
	bin := fakeELF(elf.EM_AARCH64, "good")
	e := newFetchEnv(t, bin, func(rel TrustedRelease) RuntimeSource {
		dl, _ := tlsDownloader(t, rel, serveBinary(bin), 5*time.Second)
		return dl
	})
	e.p.Run(context.Background())
	e.p.MinInterval = 0
	path := e.store.Path("v1.0.2", "arm64")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	e.audit.Reset()
	if _, err := e.p.Latest("arm64"); CodeOf(err) != CodeRuntimeUnavailable {
		t.Fatal(err)
	}
	waitIdle(t, e.p) // running is set before Latest returns
	a := e.audit.String()
	if !strings.Contains(a, `"msg":"function runtime refused"`) || !strings.Contains(a, `"reason":"stored binary modified"`) ||
		!strings.Contains(a, `"source":"store"`) {
		t.Fatalf("audit = %s", a)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o755 {
		t.Fatal("the stored binary was replaced")
	}
	if e.p.Status().State != RuntimeUntrusted {
		t.Fatal(e.p.Status())
	}
}

func TestRuntimeStoreSweepsIncoming(t *testing.T) {
	arm := fakeELF(elf.EM_AARCH64, "arm")
	store, _ := testStore(t, TrustedRelease{Version: "v1.0.0", Arch: "arm64", SHA256: sha(arm)})
	b, err := store.Install(context.Background(), "v1.0.0", "arm64", bytes.NewReader(arm))
	if err != nil {
		t.Fatal(err)
	}
	// A crash between Link and Remove: the binary has two links.
	stale := filepath.Join(filepath.Dir(store.Path("v1.0.0", "arm64")), ".incoming-123")
	if err := os.Link(store.Path("v1.0.0", "arm64"), stale); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Verify(b); err != nil {
		t.Fatalf("Verify after an interrupted install: %v", err)
	}
	if _, err := os.Lstat(stale); err == nil {
		t.Fatal("stale temporary file kept")
	}
	// Through Install (ensureDirs) as well.
	if err := os.Link(store.Path("v1.0.0", "arm64"), stale); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Install(context.Background(), "v1.0.0", "arm64", failingReader{}); err != nil {
		t.Fatalf("Install after an interrupted install: %v", err)
	}
	if _, err := os.Lstat(stale); err == nil {
		t.Fatal("stale temporary file kept by Install")
	}
}

func TestRuntimeStoreLatestCache(t *testing.T) {
	// The file is fresh: without this, no verification would be cached
	// (see TestRuntimeStoreLatestCacheRacyStamp).
	racyStampWindow = 0
	t.Cleanup(func() { racyStampWindow = time.Second })
	arm := fakeELF(elf.EM_AARCH64, "arm")
	store, _ := testStore(t, TrustedRelease{Version: "v1.0.0", Arch: "arm64", SHA256: sha(arm)})
	clock := time.Now()
	store.now = func() time.Time { return clock }
	b, err := store.Install(context.Background(), "v1.0.0", "arm64", bytes.NewReader(arm))
	if err != nil {
		t.Fatal(err)
	}
	h := store.hashes
	for range 5 {
		if _, err := store.Latest("arm64"); err != nil {
			t.Fatal(err)
		}
	}
	if store.hashes != h {
		t.Fatalf("Latest re-hashed %d times within the cache TTL", store.hashes-h)
	}
	// Verify (before a compose run) always re-hashes.
	if _, err := store.Verify(b); err != nil || store.hashes != h+1 {
		t.Fatalf("Verify: %v, hashes %d", err, store.hashes-h)
	}
	// After the TTL: re-hashed.
	clock = clock.Add(11 * time.Second)
	if _, err := store.Latest("arm64"); err != nil {
		t.Fatal(err)
	}
	if store.hashes != h+2 {
		t.Fatalf("hashes after TTL = %d", store.hashes-h)
	}
	// Content changed within the TTL (same size, mtime restored): ctime
	// differs, the cache is invalidated and the change detected. The kernel
	// clock is coarse: wait until a change gets another ctime.
	path := store.Path("v1.0.0", "arm64")
	fi, _ := os.Stat(path)
	time.Sleep(50 * time.Millisecond)
	mustChmod(t, path, 0o755)
	evil := fakeELF(elf.EM_AARCH64, "evi")
	if err := os.WriteFile(path, evil, 0o755); err != nil {
		t.Fatal(err)
	}
	mustChmod(t, path, 0o555)
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Latest("arm64"); err == nil {
		t.Fatal("a modified binary served from the cache")
	}
}

// A binary changed in the same coarse clock tick as its verification, with
// its mtime restored, keeps its stamp: such a fresh file is never cached,
// so the change is always detected (the CI failure this guards against).
// A regression guard, not a reproducer: without the guard it fails only
// when the write lands in the same tick as the hash.
func TestRuntimeStoreLatestCacheRacyStamp(t *testing.T) {
	arm := fakeELF(elf.EM_AARCH64, "arm")
	store, _ := testStore(t, TrustedRelease{Version: "v1.0.0", Arch: "arm64", SHA256: sha(arm)})
	if _, err := store.Install(context.Background(), "v1.0.0", "arm64", bytes.NewReader(arm)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Latest("arm64"); err != nil {
		t.Fatal(err)
	}
	path := store.Path("v1.0.0", "arm64")
	fi, _ := os.Stat(path)
	mustChmod(t, path, 0o755)
	if err := os.WriteFile(path, fakeELF(elf.EM_AARCH64, "evi"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustChmod(t, path, 0o555)
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Latest("arm64"); err == nil {
		t.Fatal("a binary changed right after its verification served from the cache")
	}
}

func TestNewestRuntimeVersion(t *testing.T) {
	for _, c := range []struct {
		tags []string
		want string
	}{
		{[]string{"v1.0.2", "v1.0.10", "v1.0.9"}, "v1.0.10"},
		{[]string{"v2.0.0-rc.1", "v1.1.0", "latest", "1.2.0", "v1.0.0"}, "v1.1.0"},
		{[]string{"v1.0.0", "v0.9.0"}, ""},
		{[]string{"v1.0.1", "v0.9.0"}, "v1.0.1"},
		{nil, ""},
	} {
		got, ok := newestRuntimeVersion(c.tags)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("%v = %q, %v; want %q", c.tags, got, ok, c.want)
		}
	}
}

func TestParseSHA256SUMS(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	rels, err := parseSHA256SUMS("v1.0.2", []byte(a+"  loch-lambda-runtime-linux-amd64\n"+b+" *loch-lambda-runtime-linux-arm64\n"+a+"  loch-lambda-runtime-linux-amd64.cdx.json\n"))
	if err != nil || len(rels) != 2 || rels[0] != (TrustedRelease{Version: "v1.0.2", Arch: "amd64", SHA256: a}) || rels[1].SHA256 != b {
		t.Fatalf("rels = %+v, %v", rels, err)
	}
	for name, doc := range map[string]string{
		"empty":     "",
		"no binary": a + "  other\n",
		"malformed": a + "\n",
		"bad sha":   "ABC  loch-lambda-runtime-linux-amd64\n",
		"duplicate": a + "  loch-lambda-runtime-linux-amd64\n" + b + "  loch-lambda-runtime-linux-amd64\n",
	} {
		if _, err := parseSHA256SUMS("v1.0.2", []byte(doc)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// fakeGitHub publishes one release at a time (changeable), next to a draft
// and a prerelease that must be ignored.
type fakeGitHub struct {
	mu        sync.Mutex
	lists     int  // releases list requests
	rateLimit bool // answer the releases list with a rate-limit 403
	version   string
	sums      string // sha256 listed for the arm64 binary
	bin       []byte
}

func (f *fakeGitHub) set(version string, sums string, bin []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.version, f.sums, f.bin = version, sums, bin
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/releases":
		f.lists++
		if f.rateLimit {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(20*time.Minute).Unix(), 10))
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = fmt.Fprintf(w, `[{"tag_name":"v9.0.0","draft":true},{"tag_name":"v8.0.0","prerelease":true},{"tag_name":%q}]`, f.version)
	case "/pkg/" + f.version + "/SHA256SUMS":
		_, _ = fmt.Fprintf(w, "\n%s  loch-lambda-runtime-linux-arm64\n# a comment\n\n", f.sums)
	case "/pkg/" + f.version + "/loch-lambda-runtime-linux-arm64":
		_, _ = w.Write(f.bin)
	default:
		http.NotFound(w, r)
	}
}

func newFakeGitHubProvider(t *testing.T, gl *fakeGitHub) (*RuntimeProvider, *RuntimeStore, Paths, *bytes.Buffer) {
	t.Helper()
	srv := httptest.NewTLSServer(gl)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	dl := newRuntimeDownloader(srv.URL+"/pkg", srv.URL+"/releases", []string{u.Host}, srv.Client().Transport, 5*time.Second)
	store, p := testStore(t)
	audit := &bytes.Buffer{}
	prov := NewRuntimeProvider(store, "arm64", dl, slog.New(slog.DiscardHandler), slog.New(slog.NewJSONHandler(audit, nil)))
	prov.Backoff, prov.CheckInterval = nil, 0
	return prov, store, p, audit
}

func TestRuntimeProviderDiscoversNewReleases(t *testing.T) {
	v2, v3 := fakeELF(elf.EM_AARCH64, "v1.0.2"), fakeELF(elf.EM_AARCH64, "v1.0.3")
	gl := &fakeGitHub{}
	gl.set("v1.0.2", sha(v2), v2)
	p, store, paths, audit := newFakeGitHubProvider(t, gl)
	p.Run(context.Background())
	if b, err := p.Latest("arm64"); err != nil || b.Version != "v1.0.2" || b.SHA256 != sha(v2) {
		t.Fatalf("Latest = %+v, %v", b, err)
	}
	// Recorded on disk: a restart knows it without GitHub.
	if rec, err := LoadRuntimeRecord(paths); err != nil || len(rec.Releases) != 1 || rec.Releases[0].SHA256 != sha(v2) {
		t.Fatalf("record = %+v, %v", rec, err)
	}
	// A new release: the next check installs it; v1.0.2 stays (pinned by
	// the functions created with it).
	gl.set("v1.0.3", sha(v3), v3)
	if err := p.attempt(context.Background(), "", "arm64"); err != nil {
		t.Fatal(err)
	}
	if b, err := p.Latest("arm64"); err != nil || b.Version != "v1.0.3" {
		t.Fatalf("Latest after the new release = %+v, %v", b, err)
	}
	if _, err := p.Verify(RuntimeBinary{Version: "v1.0.2", Arch: "arm64", SHA256: sha(v2)}); err != nil {
		t.Fatalf("the older release is still usable: %v", err)
	}
	// v1.0.3 republished with other bytes: refused and audited, the record
	// and the installed binary unchanged.
	evil := fakeELF(elf.EM_AARCH64, "evil3")
	gl.set("v1.0.3", sha(evil), evil)
	audit.Reset()
	if err := p.attempt(context.Background(), "", "arm64"); err == nil || p.Status().State != RuntimeUntrusted && p.Status().State != RuntimeInstalled {
		t.Fatalf("republished release: %v (%+v)", err, p.Status())
	}
	if !strings.Contains(audit.String(), `"msg":"function runtime refused"`) {
		t.Fatalf("audit = %s", audit)
	}
	if r, _ := store.Release("v1.0.3", "arm64"); r.SHA256 != sha(v3) {
		t.Fatalf("record changed: %+v", r)
	}
	if _, err := store.Verify(RuntimeBinary{Version: "v1.0.3", Arch: "arm64", SHA256: sha(v3)}); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeProviderPeriodicCheck(t *testing.T) {
	v2, v3 := fakeELF(elf.EM_AARCH64, "v1.0.2"), fakeELF(elf.EM_AARCH64, "v1.0.3")
	gl := &fakeGitHub{}
	gl.set("v1.0.2", sha(v2), v2)
	p, _, _, _ := newFakeGitHubProvider(t, gl)
	p.CheckInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	waitVersion := func(v string) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
			if b, err := p.store.Latest("arm64"); err == nil && b.Version == v {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never installed: %+v", v, p.Status())
			}
		}
	}
	waitVersion("v1.0.2")
	gl.set("v1.0.3", sha(v3), v3)
	waitVersion("v1.0.3")
}

func TestRuntimeProviderReinstallsAPinnedRelease(t *testing.T) {
	// A function pinned to v1.0.2 whose binary and record were lost (data
	// directory restored without .runtime): fetched again from its release,
	// installed only with the sha256 the function pinned.
	v2 := fakeELF(elf.EM_AARCH64, "v1.0.2")
	gl := &fakeGitHub{}
	gl.set("v1.0.2", sha(v2), v2)
	p, _, _, _ := newFakeGitHubProvider(t, gl)
	p.MinInterval = 0
	b := RuntimeBinary{Version: "v1.0.2", Arch: "arm64", SHA256: sha(v2)}
	if _, err := p.Verify(b); CodeOf(err) != CodeRuntimeUnavailable {
		t.Fatalf("Verify = %v", err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, err := p.store.Verify(b); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not reinstalled: %+v", p.Status())
		}
	}
	// Pinned to another sha256 than the published one: never usable.
	other := RuntimeBinary{Version: "v1.0.2", Arch: "arm64", SHA256: strings.Repeat("c", 64)}
	if _, err := p.Verify(other); err == nil || CodeOf(err) == CodeRuntimeUnavailable {
		t.Fatalf("Verify of another sha256 = %v", err)
	}
}

func TestRuntimeDownloaderCaps(t *testing.T) {
	big := strings.Repeat("a", maxSHA256SUMS+1)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases":
			_, _ = w.Write([]byte(`[{"tag_name":"v1.0.2"}]`))
		default:
			_, _ = w.Write([]byte(big))
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	dl := newRuntimeDownloader(srv.URL+"/pkg", srv.URL+"/releases", []string{u.Host}, srv.Client().Transport, 5*time.Second)
	if _, err := dl.Release(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("oversized SHA256SUMS: %v", err)
	}
	if _, err := dl.Release(context.Background(), "v1.0.2;rm"); err == nil {
		t.Fatal("invalid version accepted")
	}
}

func TestRuntimeProviderRecordsOnlyCheckedBinaries(t *testing.T) {
	// A release whose SHA256SUMS does not match its binary: refused, nothing
	// recorded, so the corrected release installs afterwards.
	v2 := fakeELF(elf.EM_AARCH64, "v1.0.2")
	gl := &fakeGitHub{}
	gl.set("v1.0.2", strings.Repeat("d", 64), v2)
	p, store, paths, audit := newFakeGitHubProvider(t, gl)
	if err := p.attempt(context.Background(), "", "arm64"); err == nil || p.Status().State != RuntimeUntrusted {
		t.Fatalf("wrong SHA256SUMS: %v (%+v)", err, p.Status())
	}
	if !strings.Contains(audit.String(), `"msg":"function runtime refused"`) {
		t.Fatalf("audit = %s", audit)
	}
	if _, ok := store.Release("v1.0.2", "arm64"); ok {
		t.Fatal("a release whose binary did not check out was recorded")
	}
	if rec, _ := LoadRuntimeRecord(paths); slices.ContainsFunc(rec.Releases, func(r TrustedRelease) bool { return r.Version == "v1.0.2" }) {
		t.Fatalf("record = %+v", rec)
	}
	gl.set("v1.0.2", sha(v2), v2)
	if err := p.attempt(context.Background(), "", "arm64"); err != nil {
		t.Fatal(err)
	}
	if r, ok := store.Release("v1.0.2", "arm64"); !ok || r.SHA256 != sha(v2) {
		t.Fatalf("record = %+v", r)
	}
}

func TestRuntimeStoreNotLockedDuringDownload(t *testing.T) {
	bin := fakeELF(elf.EM_AARCH64, "slow")
	store, _ := testStore(t)
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := store.InstallRelease(context.Background(), TrustedRelease{Version: "v1.0.2", Arch: "arm64", SHA256: sha(bin)}, pr)
		done <- err
	}()
	if _, err := pw.Write(bin[:10]); err != nil { // the download has started
		t.Fatal(err)
	}
	checked := make(chan struct{})
	go func() {
		_, _ = store.Latest("arm64")
		_, _ = store.Release("v1.0.2", "arm64")
		_ = store.Accept(nil)
		close(checked)
	}()
	select {
	case <-checked:
	case <-time.After(2 * time.Second):
		t.Fatal("the store is locked during a download")
	}
	if _, err := pw.Write(bin[10:]); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if b, err := store.Latest("arm64"); err != nil || b.SHA256 != sha(bin) {
		t.Fatalf("Latest = %+v, %v", b, err)
	}
}

func TestRuntimeProviderStopsRetryingAfterARefusal(t *testing.T) {
	v2 := fakeELF(elf.EM_AARCH64, "v1.0.2")
	gl := &fakeGitHub{}
	gl.set("v1.0.2", strings.Repeat("d", 64), v2) // never matches
	p, _, _, audit := newFakeGitHubProvider(t, gl)
	p.Backoff = []time.Duration{time.Hour}
	done := make(chan struct{})
	go func() { p.Run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run kept retrying a refused release")
	}
	if n := strings.Count(audit.String(), "function runtime refused"); n != 1 {
		t.Fatalf("%d refusals audited", n)
	}
}

func (f *fakeGitHub) listRequests() int { f.mu.Lock(); defer f.mu.Unlock(); return f.lists }

func TestRuntimeStoreCleansInterruptedInstalls(t *testing.T) {
	bin := fakeELF(elf.EM_AARCH64, "v1.0.2")
	rel := TrustedRelease{Version: "v1.0.2", Arch: "arm64", SHA256: sha(bin)}
	store, p := testStore(t)
	if _, err := store.InstallRelease(context.Background(), rel, bytes.NewReader(bin)); err != nil {
		t.Fatal(err)
	}
	// A crash between the link and the removal: two links.
	if err := os.Link(store.Path("v1.0.2", "arm64"), filepath.Join(p.Runtime(), ".download-crashed")); err != nil {
		t.Fatal(err)
	}
	b := binaryOf(rel)
	if _, err := store.Verify(b); err == nil {
		t.Fatal("a binary with two links verified")
	}
	if err := store.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Verify(b); err != nil {
		t.Fatalf("after cleanup: %v", err)
	}
	// An install sweeps them too.
	if err := os.Link(store.Path("v1.0.2", "arm64"), filepath.Join(p.Runtime(), ".download-crashed")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InstallRelease(context.Background(), rel, strings.NewReader("")); err != nil {
		t.Fatalf("install over an interrupted one: %v", err)
	}
}

func TestRuntimeProviderAdoptsUnrecordedBinaries(t *testing.T) {
	// A panel upgraded from a build that embedded its table: the binary is on
	// disk, the record does not list it. It becomes usable once its release
	// publishes the sha256 the function pinned, without a new download.
	v2 := fakeELF(elf.EM_AARCH64, "v1.0.2")
	rel := TrustedRelease{Version: "v1.0.2", Arch: "arm64", SHA256: sha(v2)}
	gl := &fakeGitHub{}
	gl.set("v1.0.2", sha(v2), []byte("never downloaded"))
	p, store, paths, _ := newFakeGitHubProvider(t, gl)
	p.MinInterval = 0
	if _, err := store.InstallRelease(context.Background(), rel, bytes.NewReader(v2)); err != nil {
		t.Fatal(err)
	}
	// The record is lost (or was never written).
	p.store = NewRuntimeStore(paths, TrustTable{})
	p.store.uid = store.uid
	b := binaryOf(rel)
	if _, err := p.Verify(b); CodeOf(err) != CodeRuntimeUnavailable {
		t.Fatalf("Verify = %v", err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, err := p.store.Verify(b); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not adopted: %+v", p.Status())
		}
	}
}

func TestRuntimeProviderRefusesAnotherPinnedSHA256(t *testing.T) {
	v2 := fakeELF(elf.EM_AARCH64, "v1.0.2")
	gl := &fakeGitHub{}
	gl.set("v1.0.2", sha(v2), v2)
	p, store, _, audit := newFakeGitHubProvider(t, gl)
	pinned := strings.Repeat("e", 64)
	if err := p.attemptPinned(context.Background(), "v1.0.2", "arm64", pinned); err == nil {
		t.Fatal("a release whose sha256 is not the pinned one was installed")
	}
	if _, ok := store.Release("v1.0.2", "arm64"); ok {
		t.Fatal("recorded")
	}
	if !strings.Contains(audit.String(), "not the one the function pinned") {
		t.Fatalf("audit = %s", audit)
	}
	// Older than the minimum: never fetched.
	n := gl.listRequests()
	if _, err := p.Verify(RuntimeBinary{Version: "v1.0.0", Arch: "arm64", SHA256: pinned}); err == nil || CodeOf(err) == CodeRuntimeUnavailable {
		t.Fatalf("Verify of an old version = %v", err)
	}
	if err := p.attempt(context.Background(), "v1.0.0", "arm64"); err == nil || gl.listRequests() != n {
		t.Fatalf("old version fetched: %v", err)
	}
}

func TestRuntimeProviderRateLimit(t *testing.T) {
	v2 := fakeELF(elf.EM_AARCH64, "v1.0.2")
	gl := &fakeGitHub{rateLimit: true}
	gl.set("v1.0.2", sha(v2), v2)
	p, _, _, _ := newFakeGitHubProvider(t, gl)
	p.MinInterval = 0
	p.Backoff = []time.Duration{time.Hour}
	done := make(chan struct{})
	go func() { p.Run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run kept retrying while rate limited")
	}
	if gl.listRequests() != 1 {
		t.Fatalf("%d list requests", gl.listRequests())
	}
	// No on-demand attempt before the reset.
	if _, err := p.Latest("arm64"); CodeOf(err) != CodeRuntimeUnavailable {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if gl.listRequests() != 1 {
		t.Fatalf("on-demand attempt while rate limited: %d list requests", gl.listRequests())
	}
}

func TestRuntimeProviderReusesTheNewestVersion(t *testing.T) {
	// On-demand attempts within CheckInterval reuse the version the last
	// discovery found: no new releases-list request.
	v2 := fakeELF(elf.EM_AARCH64, "v1.0.2")
	gl := &fakeGitHub{}
	gl.set("v1.0.2", sha(v2), v2)
	p, store, _, _ := newFakeGitHubProvider(t, gl)
	p.MinInterval, p.CheckInterval = 0, time.Hour
	if err := p.attempt(context.Background(), "", "arm64"); err != nil {
		t.Fatal(err)
	}
	os.Remove(store.Path("v1.0.2", "arm64")) // lost: Latest asks for it again
	if _, err := p.Latest("arm64"); CodeOf(err) != CodeRuntimeUnavailable {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, err := store.Latest("arm64"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not reinstalled: %+v", p.Status())
		}
	}
	if gl.listRequests() != 1 {
		t.Fatalf("%d list requests", gl.listRequests())
	}
}

func TestRateLimitOf(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	resp := func(code int, h map[string]string) *http.Response {
		r := &http.Response{StatusCode: code, Header: http.Header{}}
		for k, v := range h {
			r.Header.Set(k, v)
		}
		return r
	}
	for name, c := range map[string]struct {
		r    *http.Response
		ok   bool
		wait time.Duration
	}{
		"reset":       {resp(403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1800000600"}), true, 10 * time.Minute},
		"retry-after": {resp(429, map[string]string{"Retry-After": "120"}), true, 2 * time.Minute},
		"capped":      {resp(403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1900000000"}), true, time.Hour},
		"429 bare":    {resp(429, nil), true, time.Minute},
		"other 403":   {resp(403, nil), false, 0},
		"404":         {resp(404, nil), false, 0},
	} {
		until, ok := rateLimitOf(c.r, now)
		if ok != c.ok || ok && until.Sub(now) != c.wait {
			t.Errorf("%s: %v %v", name, until.Sub(now), ok)
		}
	}
}
