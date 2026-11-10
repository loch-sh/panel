package functions

import (
	"bytes"
	"context"
	"debug/elf"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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
	rel := TrustedRelease{Version: "v1.0.1", Arch: "arm64", SHA256: sha(bin), Size: int64(len(bin))}
	store, _ := testStore(t, rel)
	audit, logs := &bytes.Buffer{}, &bytes.Buffer{}
	p := NewRuntimeProvider(store, "arm64", source(rel), slog.New(slog.NewTextHandler(logs, nil)), slog.New(slog.NewJSONHandler(audit, nil)))
	p.Backoff = nil
	return &fetchEnv{p: p, store: store, rel: rel, audit: audit, logs: logs}
}

func tlsDownloader(t *testing.T, h http.Handler, timeout time.Duration) (*RuntimeDownloader, *httptest.Server) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return newRuntimeDownloader(srv.URL+"/releases/download", []string{u.Host}, srv.Client().Transport, timeout), srv
}

func serveBinary(bin []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/releases/download/v1.0.1/loch-lambda-runtime-linux-arm64" {
			http.NotFound(w, r)
			return
		}
		w.Write(bin)
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
	e := newFetchEnv(t, bin, func(TrustedRelease) RuntimeSource {
		dl, _ = tlsDownloader(t, serveBinary(bin), 5*time.Second)
		return dl
	})
	if s := e.p.Status(); s.State != RuntimeIdle {
		t.Fatalf("status before Run = %+v", s)
	}
	e.p.Run(context.Background())
	b, err := e.p.Latest("arm64")
	if err != nil || b.Version != "v1.0.1" || b.SHA256 != e.rel.SHA256 {
		t.Fatalf("Latest = %+v, %v", b, err)
	}
	if s := e.p.Status(); s != (RuntimeState{State: "installed", Version: "v1.0.1"}) {
		t.Fatalf("status = %+v", s)
	}
	a := e.audit.String()
	if !strings.Contains(a, `"msg":"function runtime installed"`) || !strings.Contains(a, `"source":"download"`) ||
		!strings.Contains(a, e.rel.SHA256) {
		t.Fatalf("audit = %s", a)
	}
	// Already installed: nothing downloaded nor audited again.
	e.audit.Reset()
	if err := e.p.attempt(context.Background(), e.rel); err != nil || e.audit.Len() != 0 {
		t.Fatalf("second attempt: %v, audit %s", err, e.audit)
	}
}

func TestRuntimeDownloadRefusesWrongHash(t *testing.T) {
	good := fakeELF(elf.EM_AARCH64, "good")
	evil := fakeELF(elf.EM_AARCH64, "evil") // same size, other content
	e := newFetchEnv(t, good, func(TrustedRelease) RuntimeSource {
		dl, _ := tlsDownloader(t, serveBinary(evil), 5*time.Second)
		return dl
	})
	if err := e.p.attempt(context.Background(), e.rel); CodeOf(err) != CodeRuntimeUntrusted {
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
			w.Write(big)
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newFetchEnv(t, good, func(TrustedRelease) RuntimeSource {
				dl, _ := tlsDownloader(t, h, 5*time.Second)
				return dl
			})
			if err := e.p.attempt(context.Background(), e.rel); err == nil {
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
		"disallowed host": other.URL + "/releases/download/v1.0.1/loch-lambda-runtime-linux-arm64",
		"plain http":      "http://" + otherURL.Host + "/releases/download/v1.0.1/loch-lambda-runtime-linux-arm64",
	} {
		t.Run(name, func(t *testing.T) {
			hits := 0
			e := newFetchEnv(t, bin, func(TrustedRelease) RuntimeSource {
				dl, _ := tlsDownloader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits++
					http.Redirect(w, r, target, http.StatusFound)
				}), 5*time.Second)
				return dl
			})
			err := e.p.attempt(context.Background(), e.rel)
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
	e := newFetchEnv(t, bin, func(TrustedRelease) RuntimeSource {
		mux := http.NewServeMux()
		mux.HandleFunc("/releases/download/v1.0.1/loch-lambda-runtime-linux-arm64", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/asset", http.StatusFound)
		})
		mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) { w.Write(bin) })
		dl, _ := tlsDownloader(t, mux, 5*time.Second)
		return dl
	})
	if err := e.p.attempt(context.Background(), e.rel); err != nil {
		t.Fatalf("allowed redirect: %v", err)
	}
}

func TestRuntimeDownloadRefusesPlainBaseURL(t *testing.T) {
	d := newRuntimeDownloader("http://github.com/x", RuntimeDownloadHosts, http.DefaultTransport, time.Second)
	if _, err := d.Open(context.Background(), TrustedRelease{Version: "v1.0.1", Arch: "arm64"}); err == nil {
		t.Fatal("plain HTTP base URL accepted")
	}
}

func TestRuntimeDownloadTimeout(t *testing.T) {
	bin := fakeELF(elf.EM_AARCH64, "good")
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	e := newFetchEnv(t, bin, func(TrustedRelease) RuntimeSource {
		dl, _ := tlsDownloader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(len(bin)))
			w.Write(bin[:10])
			w.(http.Flusher).Flush()
			select { // stalls mid-body
			case <-release:
			case <-r.Context().Done():
			}
		}), 300*time.Millisecond)
		return dl
	})
	start := time.Now()
	err := e.p.attempt(context.Background(), e.rel)
	if CodeOf(err) != CodeRuntimeUnavailable || time.Since(start) > 5*time.Second {
		t.Fatalf("attempt = %v after %s", err, time.Since(start))
	}
	e.assertNotStored(t)
	if strings.Contains(e.audit.String(), "function runtime refused") {
		t.Fatalf("a timeout is not a refused binary: %s", e.audit)
	}
}

func TestRuntimeSourceDir(t *testing.T) {
	bin := fakeELF(elf.EM_AARCH64, "good")
	dir := t.TempDir()
	e := newFetchEnv(t, bin, func(TrustedRelease) RuntimeSource { return RuntimeSourceDir{Dir: dir} })
	// Missing file: unavailable, nothing audited.
	if err := e.p.attempt(context.Background(), e.rel); err == nil || e.audit.Len() != 0 {
		t.Fatalf("missing file: %v, audit %s", err, e.audit)
	}
	// Wrong file of the right size: refused.
	if err := os.WriteFile(filepath.Join(dir, "loch-lambda-runtime-linux-arm64"), fakeELF(elf.EM_AARCH64, "evil"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.p.attempt(context.Background(), e.rel); CodeOf(err) != CodeRuntimeUntrusted {
		t.Fatalf("wrong file: %v", err)
	}
	e.assertNotStored(t)
	if err := os.WriteFile(filepath.Join(dir, "loch-lambda-runtime-linux-arm64"), bin, 0o644); err != nil {
		t.Fatal(err)
	}
	e.audit.Reset()
	if err := e.p.attempt(context.Background(), e.rel); err != nil {
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
	e = newFetchEnv(t, bin, func(TrustedRelease) RuntimeSource {
		dl, _ := tlsDownloader(t, serveBinary(bin), 5*time.Second)
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
	rel := TrustedRelease{Version: "v1.0.1", Arch: "arm64", SHA256: sha(bin), Size: int64(len(bin))}
	store := NewRuntimeStore(e.m.paths, TrustTable{Releases: []TrustedRelease{rel}})
	dl, _ := tlsDownloader(t, http.NotFoundHandler(), time.Second)
	p := NewRuntimeProvider(store, "arm64", dl, slog.New(slog.DiscardHandler), slog.New(slog.DiscardHandler))
	p.Backoff = nil
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
	if err := syscall.Mkfifo(filepath.Join(dir, "loch-lambda-runtime-linux-arm64"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckRuntimeSourceDir(dir); err == nil {
		t.Fatal("a FIFO passed the startup check")
	}
	e := newFetchEnv(t, bin, func(TrustedRelease) RuntimeSource { return RuntimeSourceDir{Dir: dir} })
	done := make(chan error, 1)
	go func() { done <- e.p.attempt(context.Background(), e.rel) }()
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
		f, err := os.OpenFile(filepath.Join(dir, "loch-lambda-runtime-linux-arm64"), os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
		if err == nil {
			f.Close()
		}
		done2 <- err
	}()
	select {
	case <-done2:
	case <-time.After(time.Second):
		t.Fatal("O_NONBLOCK open blocks")
	}
}

func TestRuntimeSourceDirPermissions(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "loch-lambda-runtime-linux-amd64")
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
	os.Chmod(file, 0o666)
	if err := CheckRuntimeSourceDir(dir); err == nil {
		t.Error("a world-writable file accepted")
	}
	os.Chmod(file, 0o644)
	os.Chmod(dir, 0o770)
	if err := CheckRuntimeSourceDir(dir); err == nil {
		t.Error("a group-writable directory accepted")
	}
	os.Chmod(dir, 0o700)
	os.Remove(file)
	if err := os.Symlink("/etc/hosts", file); err != nil {
		t.Fatal(err)
	}
	if err := CheckRuntimeSourceDir(dir); err == nil {
		t.Error("a symlinked runtime file accepted")
	}
	if _, err := (RuntimeSourceDir{Dir: dir}).Open(context.Background(), TrustedRelease{Version: "v1.0.1", Arch: "amd64"}); err == nil {
		t.Error("a symlinked runtime file opened")
	}
}

func TestRuntimeProviderStates(t *testing.T) {
	bin := fakeELF(elf.EM_AARCH64, "good")
	quiet := slog.New(slog.DiscardHandler)
	// No release for the host architecture: unavailable, not installing.
	store, _ := testStore(t, TrustedRelease{Version: "v1.0.1", Arch: "amd64", SHA256: sha(bin)})
	logs := &bytes.Buffer{}
	p := NewRuntimeProvider(store, "arm64", RuntimeSourceDir{Dir: t.TempDir()}, slog.New(slog.NewTextHandler(logs, nil)), quiet)
	if p.Status().State != RuntimeIdle {
		t.Fatal(p.Status())
	}
	p.Run(context.Background())
	if p.Status().State != RuntimeUnavailable || !strings.Contains(logs.String(), "trusts no loch function runtime release") {
		t.Fatalf("no release: %+v, logs %s", p.Status(), logs)
	}
	// installing while the download runs, then installed.
	release := make(chan struct{})
	e := newFetchEnv(t, bin, func(TrustedRelease) RuntimeSource {
		dl, _ := tlsDownloader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-release
			w.Write(bin)
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
	if s := e.p.Status(); s.State != RuntimeInstalled || s.Version != "v1.0.1" {
		t.Fatalf("state = %+v", s)
	}
	// Removed afterwards: unavailable.
	os.Remove(e.store.Path("v1.0.1", "arm64"))
	if s := e.p.Status(); s.State != RuntimeUnavailable {
		t.Fatalf("state after removal = %+v", s)
	}
	// A failed fetch: unavailable.
	e = newFetchEnv(t, bin, func(TrustedRelease) RuntimeSource {
		dl, _ := tlsDownloader(t, http.NotFoundHandler(), time.Second)
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
	e := newFetchEnv(t, good, func(TrustedRelease) RuntimeSource {
		dl, _ := tlsDownloader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-release
			w.Write(evil)
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
		go func() { defer wg.Done(); e.p.Latest("arm64") }()
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
	e.p.Latest("arm64")
	waitIdle(t, e.p)
	if n := src.count(); n != 1 {
		t.Fatalf("retried %d times within the refusal backoff", n)
	}
	clockMu.Lock()
	clock = clock.Add(31 * time.Minute)
	clockMu.Unlock()
	e.p.Latest("arm64")
	for deadline := time.Now().Add(5 * time.Second); src.count() != 2; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no retry after the refusal backoff")
		}
	}
	waitIdle(t, e.p)
}

func TestRuntimeProviderAuditsModifiedStore(t *testing.T) {
	bin := fakeELF(elf.EM_AARCH64, "good")
	e := newFetchEnv(t, bin, func(TrustedRelease) RuntimeSource {
		dl, _ := tlsDownloader(t, serveBinary(bin), 5*time.Second)
		return dl
	})
	e.p.Run(context.Background())
	e.p.MinInterval = 0
	path := e.store.Path("v1.0.1", "arm64")
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
	store.Latest("arm64")
	if store.hashes != h+2 {
		t.Fatalf("hashes after TTL = %d", store.hashes-h)
	}
	// Content changed within the TTL (same size, mtime restored): ctime
	// differs, the cache is invalidated and the change detected. The kernel
	// clock is coarse: wait until a change gets another ctime.
	path := store.Path("v1.0.0", "arm64")
	fi, _ := os.Stat(path)
	time.Sleep(50 * time.Millisecond)
	os.Chmod(path, 0o755)
	evil := fakeELF(elf.EM_AARCH64, "evi")
	if err := os.WriteFile(path, evil, 0o755); err != nil {
		t.Fatal(err)
	}
	os.Chmod(path, 0o555)
	os.Chtimes(path, fi.ModTime(), fi.ModTime())
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
	os.Chmod(path, 0o755)
	if err := os.WriteFile(path, fakeELF(elf.EM_AARCH64, "evi"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.Chmod(path, 0o555)
	os.Chtimes(path, fi.ModTime(), fi.ModTime())
	if _, err := store.Latest("arm64"); err == nil {
		t.Fatal("a binary changed right after its verification served from the cache")
	}
}
