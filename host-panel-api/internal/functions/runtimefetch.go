package functions

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"
)

// Release assets of the loch function runtime (decision log §20).
const (
	RuntimeReleaseBaseURL  = "https://github.com/loch-sh/lambda-runtime/releases/download"
	runtimeDownloadTimeout = 5 * time.Minute
	runtimeMaxRedirects    = 5
)

// RuntimeDownloadHosts are the only hosts a download may reach: the release
// URL on github.com, then the redirect to the asset storage.
var RuntimeDownloadHosts = []string{"github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"}

// RuntimeAssetName is the file name of a release asset (and of a file in
// FUNCTIONS_RUNTIME_SOURCE_DIR).
func RuntimeAssetName(arch string) string { return RuntimeBinaryName + "-linux-" + arch }

// RuntimeSource opens the binary of a trusted release; the content is only
// trusted once RuntimeStore.Install checked it against the table.
type RuntimeSource interface {
	Open(ctx context.Context, rel TrustedRelease) (io.ReadCloser, error)
	// Name is recorded in the audit trail ("download", "directory").
	Name() string
}

// refusal is a binary refused for what it is (audited), not for a failure
// to fetch it.
type refusal struct{ reason string }

func (r refusal) Error() string { return r.reason }

// RuntimeDownloader downloads release assets over HTTPS. Its client is its
// own (never the cron anti-SSRF transport, never a proxy-less default one):
// HTTPS only, redirects restricted to Hosts, a global timeout.
type RuntimeDownloader struct {
	BaseURL string
	Hosts   []string // allowed URL hosts (host[:port]), exact match
	Client  *http.Client
}

// NewRuntimeDownloader returns the downloader of the GitHub releases.
func NewRuntimeDownloader() *RuntimeDownloader {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          2,
		IdleConnTimeout:       30 * time.Second,
	}
	return newRuntimeDownloader(RuntimeReleaseBaseURL, RuntimeDownloadHosts, tr, runtimeDownloadTimeout)
}

func newRuntimeDownloader(base string, hosts []string, tr http.RoundTripper, timeout time.Duration) *RuntimeDownloader {
	d := &RuntimeDownloader{BaseURL: base, Hosts: hosts}
	d.Client = &http.Client{Transport: tr, Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= runtimeMaxRedirects {
			return errors.New("too many redirects")
		}
		return d.allowed(req.URL)
	}}
	return d
}

func (d *RuntimeDownloader) allowed(u *url.URL) error {
	if u.Scheme != "https" || u.User != nil || !slices.Contains(d.Hosts, u.Host) {
		return fmt.Errorf("refused redirect or URL to %s://%s", u.Scheme, u.Host)
	}
	return nil
}

// Name implements RuntimeSource.
func (d *RuntimeDownloader) Name() string { return "download" }

// Open implements RuntimeSource.
func (d *RuntimeDownloader) Open(ctx context.Context, rel TrustedRelease) (io.ReadCloser, error) {
	u, err := url.Parse(d.BaseURL + "/" + rel.Version + "/" + RuntimeAssetName(rel.Arch))
	if err != nil {
		return nil, err
	}
	if err := d.allowed(u); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := d.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s answered HTTP %d", resp.Request.URL.Host, resp.StatusCode)
	}
	if rel.Size > 0 && resp.ContentLength > rel.Size || resp.ContentLength > maxRuntimeBinary {
		resp.Body.Close()
		return nil, refusal{fmt.Sprintf("the download announces %d bytes, more than the trusted size", resp.ContentLength)}
	}
	return resp.Body, nil
}

// RuntimeSourceDir reads the binaries of FUNCTIONS_RUNTIME_SOURCE_DIR (offline
// installations): loch-lambda-runtime-linux-<arch>, the newest trusted
// release.
type RuntimeSourceDir struct{ Dir string }

// Name implements RuntimeSource.
func (d RuntimeSourceDir) Name() string { return "directory" }

// CheckRuntimeSourceDir checks that a source directory, and the runtime
// files it holds, are not writable by group or others and are owned by
// the panel's uid or root (environment lint at startup; again at every
// read).
func CheckRuntimeSourceDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return errors.New("the directory cannot be read")
	}
	if !fi.IsDir() {
		return errors.New("not a directory")
	}
	if err := checkSourceOwner(fi); err != nil {
		return err
	}
	for _, arch := range []string{"amd64", "arm64"} {
		fi, err := os.Lstat(filepath.Join(dir, RuntimeAssetName(arch)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("%s cannot be read", RuntimeAssetName(arch))
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", RuntimeAssetName(arch))
		}
		if err := checkSourceOwner(fi); err != nil {
			return fmt.Errorf("%s: %w", RuntimeAssetName(arch), err)
		}
	}
	return nil
}

func checkSourceOwner(fi os.FileInfo) error {
	if fi.Mode().Perm()&0o022 != 0 {
		return errors.New("writable by group or others")
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Geteuid() && st.Uid != 0 {
		return errors.New("owned by neither the panel's user nor root")
	}
	return nil
}

// Open implements RuntimeSource: never blocks on a FIFO or a device
// (O_NONBLOCK, then the descriptor must be a regular file), never follows
// a symlink for the file itself.
func (d RuntimeSourceDir) Open(ctx context.Context, rel TrustedRelease) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := CheckRuntimeSourceDir(d.Dir); err != nil {
		return nil, fmt.Errorf("FUNCTIONS_RUNTIME_SOURCE_DIR: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(d.Dir, RuntimeAssetName(rel.Arch)), os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("not a regular file")
	}
	if err := checkSourceOwner(fi); err != nil {
		f.Close()
		return nil, err
	}
	if rel.Size > 0 && fi.Size() != rel.Size {
		f.Close()
		return nil, refusal{fmt.Sprintf("the file has %d bytes, not the trusted size of %s", fi.Size(), rel.Version)}
	}
	// The read itself honours ctx (RuntimeStore.Install, ctxReader).
	return f, nil
}

// Runtime installation states (GET /functions/config, "runtime.state").
const (
	RuntimeIdle        = "idle"        // nothing attempted yet
	RuntimeInstalling  = "installing"  // a download or a copy runs
	RuntimeInstalled   = "installed"   // Version is ready
	RuntimeUnavailable = "unavailable" // failed, no source, or no release for the arch: see the logs
	RuntimeUntrusted   = "untrusted"   // a binary was refused (hash, size, modified store)
)

// RuntimeState is the state of the runtime binary of the host architecture.
type RuntimeState struct {
	State   string `json:"state"`
	Version string `json:"version,omitempty"`
}

// refusalBackoff: after a refused binary, no on-demand attempt for this
// long (a bad mirror or a modified store must not flood the audit trail).
const refusalBackoff = 30 * time.Minute

// RuntimeProvider is the RuntimeBinaries of the Manager: the RuntimeStore,
// plus the installation of the newest trusted release when it is missing,
// at startup (Run, with retries) and on demand (an operation needing a
// binary that is not installed starts an attempt and gets
// FUNCTION_RUNTIME_UNAVAILABLE meanwhile).
type RuntimeProvider struct {
	store  *RuntimeStore
	trust  TrustTable
	arch   string
	source RuntimeSource // nil: neither download nor source directory
	logger *slog.Logger
	audit  *slog.Logger
	// Backoff of Run; MinInterval between two on-demand attempts;
	// RefusalBackoff after a refused binary.
	Backoff        []time.Duration
	MinInterval    time.Duration
	RefusalBackoff time.Duration

	mu        sync.Mutex
	base      context.Context
	state     string
	running   bool
	last      time.Time
	refusedAt time.Time
	now       func() time.Time
}

// NewRuntimeProvider wraps a store. source may be nil (downloads disabled).
func NewRuntimeProvider(store *RuntimeStore, arch string, source RuntimeSource, logger, audit *slog.Logger) *RuntimeProvider {
	return &RuntimeProvider{
		store: store, trust: store.trust, arch: arch, source: source, logger: logger, audit: audit,
		Backoff:     []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 30 * time.Minute},
		MinInterval: time.Minute, RefusalBackoff: refusalBackoff,
		base: context.Background(), state: RuntimeIdle, now: time.Now,
	}
}

// Path implements RuntimeBinaries.
func (p *RuntimeProvider) Path(version, arch string) string { return p.store.Path(version, arch) }

// Latest implements RuntimeBinaries: when no trusted binary is installed,
// it starts an attempt and returns FUNCTION_RUNTIME_UNAVAILABLE.
func (p *RuntimeProvider) Latest(arch string) (RuntimeBinary, error) {
	b, err := p.store.Latest(arch)
	if err == nil {
		return b, nil
	}
	if arch != p.arch {
		return RuntimeBinary{}, err
	}
	if rel, ok := p.trust.Newest(arch); ok {
		p.trigger(rel)
	}
	return RuntimeBinary{}, p.unavailable()
}

// Verify implements RuntimeVerifier (always a full re-hash: it is the check
// before a compose run). A trusted binary that is missing (data directory
// restored without .runtime) is installed again on demand and reported as
// FUNCTION_RUNTIME_UNAVAILABLE, never as tampering.
func (p *RuntimeProvider) Verify(b RuntimeBinary) (string, error) {
	path, err := p.store.Verify(b)
	if err == nil {
		return path, nil
	}
	rel, ok := p.trust.Release(b.Version, b.Arch)
	if !ok || rel.SHA256 != b.SHA256 || !runtimeVersionRe.MatchString(b.Version) {
		return "", err
	}
	if _, lerr := os.Lstat(p.store.Path(b.Version, b.Arch)); !errors.Is(lerr, fs.ErrNotExist) {
		return "", err
	}
	p.trigger(rel)
	return "", p.unavailable()
}

func (p *RuntimeProvider) unavailable() *Error {
	return newError(CodeRuntimeUnavailable, "", "the loch function runtime binary is not installed yet (%s): retry later, details in the API logs", p.Status().State)
}

// Status reports the state of the runtime binary of the host architecture.
func (p *RuntimeProvider) Status() RuntimeState {
	if b, err := p.store.Latest(p.arch); err == nil {
		return RuntimeState{State: RuntimeInstalled, Version: b.Version}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == RuntimeInstalled { // removed since
		return RuntimeState{State: RuntimeUnavailable}
	}
	return RuntimeState{State: p.state}
}

func (p *RuntimeProvider) setState(s string) {
	p.mu.Lock()
	p.state = s
	p.mu.Unlock()
}

// Run installs the newest trusted release of the host architecture when it
// is missing, retrying with backoff; then on-demand attempts take over.
func (p *RuntimeProvider) Run(ctx context.Context) {
	p.mu.Lock()
	p.base = ctx
	p.mu.Unlock()
	rel, ok := p.trust.Newest(p.arch)
	if !ok {
		p.setState(RuntimeUnavailable)
		p.logger.Error("functions: this panel build trusts no loch function runtime release for the host architecture: no function can start", "arch", p.arch)
		return
	}
	if p.source == nil {
		if _, err := p.store.Verify(binaryOf(rel)); err != nil {
			p.setState(RuntimeUnavailable)
			p.logger.Warn("functions: the loch function runtime binary is not installed and FUNCTIONS_RUNTIME_DOWNLOAD is false: "+
				"set FUNCTIONS_RUNTIME_SOURCE_DIR or install it (docs/functions.md)", "version", rel.Version, "arch", p.arch)
		} else {
			p.setState(RuntimeInstalled)
		}
		return
	}
	for i := 0; ; i++ {
		if !p.begin(false) {
			return // an on-demand attempt runs
		}
		if err := p.attempt(ctx, rel); err == nil {
			return
		}
		if i >= len(p.Backoff) {
			p.logger.Error("functions: giving up installing the loch function runtime binary until a function operation needs it", "version", rel.Version, "arch", p.arch)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(p.Backoff[i]):
		}
	}
}

func binaryOf(rel TrustedRelease) RuntimeBinary {
	return RuntimeBinary{Version: rel.Version, Arch: rel.Arch, SHA256: rel.SHA256}
}

// begin marks an attempt as running, under the lock (at most one at a
// time). onDemand attempts also respect MinInterval and, after a refused
// binary, RefusalBackoff.
func (p *RuntimeProvider) begin(onDemand bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		return false
	}
	if onDemand {
		now := p.now()
		if !p.last.IsZero() && now.Sub(p.last) < p.MinInterval ||
			!p.refusedAt.IsZero() && now.Sub(p.refusedAt) < p.RefusalBackoff {
			return false
		}
	}
	p.running, p.state = true, RuntimeInstalling
	return true
}

// trigger starts an on-demand attempt in the background.
func (p *RuntimeProvider) trigger(rel TrustedRelease) {
	if p.source == nil || !p.begin(true) {
		return
	}
	p.mu.Lock()
	ctx := p.base
	p.mu.Unlock()
	go p.attempt(ctx, rel) //nolint:errcheck // logged and audited
}

// attempt runs one installation begun by begin, and sets the state on
// every exit path.
func (p *RuntimeProvider) attempt(ctx context.Context, rel TrustedRelease) error {
	state, err := p.install(ctx, rel)
	p.mu.Lock()
	p.running, p.last, p.state = false, p.now(), state
	if state == RuntimeUntrusted {
		p.refusedAt = p.last
	}
	p.mu.Unlock()
	return err
}

// install installs one trusted release (nothing done when it is already
// installed and verified, nor when a stored file fails verification: a
// stored binary is never replaced). It returns the resulting state.
func (p *RuntimeProvider) install(ctx context.Context, rel TrustedRelease) (string, error) {
	b := binaryOf(rel)
	src := p.source.Name()
	if _, err := p.store.Verify(b); err == nil {
		return RuntimeInstalled, nil
	} else if _, lerr := os.Lstat(p.store.Path(rel.Version, rel.Arch)); !errors.Is(lerr, fs.ErrNotExist) {
		p.audit.Warn("function runtime refused", "user", "panel", "version", rel.Version, "arch", rel.Arch,
			"source", "store", "reason", "stored binary modified")
		p.logger.Error("functions: the stored loch function runtime binary fails verification: it is not replaced (remove it to install it again)",
			"version", rel.Version, "arch", rel.Arch, "path", p.store.Path(rel.Version, rel.Arch), "error", err)
		return RuntimeUntrusted, err
	}
	p.logger.Info("functions: installing the loch function runtime binary", "version", rel.Version, "arch", rel.Arch, "source", src)
	r, err := p.source.Open(ctx, rel)
	if err == nil {
		_, err = p.store.Install(ctx, rel.Version, rel.Arch, r)
		r.Close()
	}
	if err != nil {
		state := RuntimeUnavailable
		var rf refusal
		if errors.As(err, &rf) || CodeOf(err) == CodeRuntimeUntrusted {
			state = RuntimeUntrusted
			p.audit.Warn("function runtime refused", "user", "panel", "version", rel.Version, "arch", rel.Arch,
				"source", src, "reason", truncate(err.Error(), 200))
		}
		p.logger.Error("functions: installing the loch function runtime binary failed", "version", rel.Version, "arch", rel.Arch,
			"source", src, "error", err)
		return state, err
	}
	p.audit.Info("function runtime installed", "user", "panel", "version", rel.Version, "arch", rel.Arch,
		"sha256", rel.SHA256, "source", src)
	p.logger.Info("functions: loch function runtime binary installed", "version", rel.Version, "arch", rel.Arch, "source", src)
	return RuntimeInstalled, nil
}
