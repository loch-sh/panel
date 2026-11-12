package functions

import (
	"context"
	"crypto/tls"
	"encoding/json"
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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Release assets of the loch function runtime (decision log §20).
const (
	// The release assets of github.com/loch-sh/lambda-runtime:
	// <base>/<version>/<asset>.
	RuntimeReleaseBaseURL = "https://github.com/loch-sh/lambda-runtime/releases/download"
	// The releases of the project (newest first), read anonymously (the
	// GitHub API allows 60 anonymous requests an hour per address).
	RuntimeReleasesURL     = "https://api.github.com/repos/loch-sh/lambda-runtime/releases?per_page=100"
	runtimeDownloadTimeout = 5 * time.Minute
	runtimeMaxRedirects    = 5
	// Caps on what GitHub answers besides the binaries.
	maxReleasesList = 1 << 20
	maxSHA256SUMS   = 64 << 10
	// runtimeSumsName lists the sha256 of the release files.
	runtimeSumsName = "SHA256SUMS"
)

// RuntimeDownloadHosts are the only hosts a download may reach: the GitHub
// API (releases list), github.com (release assets), then the redirect to
// the asset storage. Any other host is refused.
var RuntimeDownloadHosts = []string{"api.github.com", "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"}

// RuntimeAssetName is the file name of a release asset (and of a file in
// FUNCTIONS_RUNTIME_SOURCE_DIR).
func RuntimeAssetName(arch string) string { return RuntimeBinaryName + "-linux-" + arch }

// RuntimeSource finds the runtime releases and opens their binaries; a
// binary is only stored once RuntimeStore.Install checked it against the
// accepted sha256.
type RuntimeSource interface {
	// Release returns the binaries of a release (version "" = the newest
	// stable release, MinRuntimeVersion or later) with their sha256, from
	// the release's SHA256SUMS.
	Release(ctx context.Context, version string) ([]TrustedRelease, error)
	Open(ctx context.Context, rel TrustedRelease) (io.ReadCloser, error)
	// Name is recorded in the audit trail ("download", "directory").
	Name() string
}

// rateLimited: GitHub refused the request for the anonymous rate limit;
// no attempt is made before until.
type rateLimited struct{ until time.Time }

func (r rateLimited) Error() string {
	return "GitHub rate limit reached until " + r.until.UTC().Format(time.RFC3339)
}

// maxRateLimitWait bounds a wait announced by GitHub.
const maxRateLimitWait = time.Hour

// rateLimitOf reads a rate-limit answer (403 or 429 with an exhausted quota
// or a Retry-After): the time before which no request should be made.
func rateLimitOf(resp *http.Response, now time.Time) (time.Time, bool) {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return time.Time{}, false
	}
	until := time.Time{}
	if s := resp.Header.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			until = now.Add(time.Duration(n) * time.Second)
		}
	}
	if until.IsZero() && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		if n, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			until = time.Unix(n, 0)
		}
	}
	if until.IsZero() {
		if resp.StatusCode != http.StatusTooManyRequests && resp.Header.Get("X-RateLimit-Remaining") != "0" {
			return time.Time{}, false // another 403
		}
		until = now.Add(time.Minute)
	}
	if until.After(now.Add(maxRateLimitWait)) {
		until = now.Add(maxRateLimitWait)
	}
	return until, true
}

// refusal is a binary refused for what it is (audited), not for a failure
// to fetch it.
type refusal struct{ reason string }

func (r refusal) Error() string { return r.reason }

// parseSHA256SUMS returns the binaries of a release listed in its
// SHA256SUMS ("<sha256>  <file>" lines; other files ignored).
func parseSHA256SUMS(version string, data []byte) ([]TrustedRelease, error) {
	var rels []TrustedRelease
	seen := map[string]bool{}
	for line := range strings.SplitSeq(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 {
			// Lines of other files (or comments) are ignored; a malformed
			// line naming a runtime binary is not.
			if strings.Contains(line, RuntimeBinaryName+"-linux-") && !strings.Contains(line, ".cdx.json") {
				return nil, refusal{"SHA256SUMS: malformed line for a runtime binary"}
			}
			continue
		}
		sum, name := f[0], strings.TrimPrefix(f[1], "*")
		for _, arch := range []string{"amd64", "arm64"} {
			if name != RuntimeAssetName(arch) {
				continue
			}
			if !hexSHA256Re.MatchString(sum) || seen[arch] {
				return nil, refusal{"SHA256SUMS: invalid entry for " + name}
			}
			seen[arch] = true
			rels = append(rels, TrustedRelease{Version: version, Arch: arch, SHA256: sum})
		}
	}
	if len(rels) == 0 {
		return nil, refusal{"SHA256SUMS lists no runtime binary"}
	}
	return rels, nil
}

// newestRuntimeVersion picks the newest stable version, MinRuntimeVersion
// or later (prereleases and other tags ignored).
func newestRuntimeVersion(tags []string) (string, bool) {
	best := ""
	for _, t := range tags {
		if !runtimeVersionRe.MatchString(t) || compareRuntimeVersions(t, MinRuntimeVersion) < 0 {
			continue
		}
		if best == "" || compareRuntimeVersions(t, best) > 0 {
			best = t
		}
	}
	return best, best != ""
}

// RuntimeDownloader reads the releases from GitHub over HTTPS. Its client
// is its own (never the cron anti-SSRF transport, never a proxy-less
// default one): HTTPS only, redirects restricted to Hosts, a global
// timeout.
type RuntimeDownloader struct {
	BaseURL     string   // package registry: <BaseURL>/<version>/<file>
	ReleasesURL string   // releases list (JSON, tag_name)
	Hosts       []string // allowed URL hosts (host[:port]), exact match
	Client      *http.Client
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
	return newRuntimeDownloader(RuntimeReleaseBaseURL, RuntimeReleasesURL, RuntimeDownloadHosts, tr, runtimeDownloadTimeout)
}

func newRuntimeDownloader(base, releases string, hosts []string, tr http.RoundTripper, timeout time.Duration) *RuntimeDownloader {
	d := &RuntimeDownloader{BaseURL: base, ReleasesURL: releases, Hosts: hosts}
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

// get fetches an allowed URL; the caller closes the body.
func (d *RuntimeDownloader) get(ctx context.Context, raw, accept string) (*http.Response, error) {
	u, err := url.Parse(raw)
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
	req.Header.Set("Accept", accept)
	resp, err := d.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		if until, ok := rateLimitOf(resp, time.Now()); ok {
			return nil, rateLimited{until}
		}
		return nil, fmt.Errorf("%s answered HTTP %d", resp.Request.URL.Host, resp.StatusCode)
	}
	return resp, nil
}

// getSmall reads a whole bounded answer.
func (d *RuntimeDownloader) getSmall(ctx context.Context, raw, accept string, limit int64) ([]byte, error) {
	resp, err := d.get(ctx, raw, accept)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: answer larger than %d bytes", resp.Request.URL.Host, limit)
	}
	return data, nil
}

// Release implements RuntimeSource.
func (d *RuntimeDownloader) Release(ctx context.Context, version string) ([]TrustedRelease, error) {
	if version == "" {
		data, err := d.getSmall(ctx, d.ReleasesURL, "application/vnd.github+json", maxReleasesList)
		if err != nil {
			return nil, err
		}
		var list []struct {
			Tag        string `json:"tag_name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
		}
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, fmt.Errorf("releases list: %w", err)
		}
		tags := make([]string, 0, len(list))
		for _, r := range list {
			if r.Draft || r.Prerelease {
				continue
			}
			tags = append(tags, r.Tag)
		}
		v, ok := newestRuntimeVersion(tags)
		if !ok {
			return nil, fmt.Errorf("no release %s or later is published", MinRuntimeVersion)
		}
		version = v
	}
	if !runtimeVersionRe.MatchString(version) || compareRuntimeVersions(version, MinRuntimeVersion) < 0 {
		return nil, refusal{fmt.Sprintf("runtime %q is not a release %s or later", version, MinRuntimeVersion)}
	}
	data, err := d.getSmall(ctx, d.BaseURL+"/"+version+"/"+runtimeSumsName, "text/plain", maxSHA256SUMS)
	if err != nil {
		return nil, err
	}
	return parseSHA256SUMS(version, data)
}

// Open implements RuntimeSource.
func (d *RuntimeDownloader) Open(ctx context.Context, rel TrustedRelease) (io.ReadCloser, error) {
	if !runtimeVersionRe.MatchString(rel.Version) || !ValidArch(rel.Arch) {
		return nil, errors.New("invalid runtime version or architecture")
	}
	resp, err := d.get(ctx, d.BaseURL+"/"+rel.Version+"/"+RuntimeAssetName(rel.Arch), "application/octet-stream")
	if err != nil {
		return nil, err
	}
	if rel.Size > 0 && resp.ContentLength > rel.Size || resp.ContentLength > maxRuntimeBinary {
		resp.Body.Close()
		return nil, refusal{fmt.Sprintf("the download announces %d bytes, more than the trusted size", resp.ContentLength)}
	}
	return resp.Body, nil
}

// RuntimeSourceDir reads the releases from FUNCTIONS_RUNTIME_SOURCE_DIR
// (offline installations): one directory per release, named after its
// version, holding its SHA256SUMS and loch-lambda-runtime-linux-<arch>
// files as published on GitHub.
type RuntimeSourceDir struct{ Dir string }

// Name implements RuntimeSource.
func (d RuntimeSourceDir) Name() string { return "directory" }

// CheckRuntimeSourceDir checks that a source directory, its release
// directories and the files the panel reads there are not writable by group
// or others and are owned by the panel's uid or root (environment lint at
// startup; again at every read).
func CheckRuntimeSourceDir(dir string) error {
	if err := checkSourceDir(dir); err != nil {
		return err
	}
	versions, err := sourceVersions(dir)
	if err != nil {
		return err
	}
	for _, v := range versions {
		if err := checkSourceRelease(filepath.Join(dir, v)); err != nil {
			return fmt.Errorf("%s: %w", v, err)
		}
	}
	return nil
}

func checkSourceDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return errors.New("the directory cannot be read")
	}
	if !fi.IsDir() {
		return errors.New("not a directory")
	}
	return checkSourceOwner(fi)
}

// sourceVersions lists the release directories (vX.Y.Z names).
func sourceVersions(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, errors.New("the directory cannot be read")
	}
	var versions []string
	for _, e := range entries {
		if runtimeVersionRe.MatchString(e.Name()) {
			versions = append(versions, e.Name())
		}
	}
	return versions, nil
}

func checkSourceRelease(dir string) error {
	if err := checkSourceDir(dir); err != nil {
		return err
	}
	for _, name := range []string{runtimeSumsName, RuntimeAssetName("amd64"), RuntimeAssetName("arm64")} {
		fi, err := os.Lstat(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("%s cannot be read", name)
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", name)
		}
		if err := checkSourceOwner(fi); err != nil {
			return fmt.Errorf("%s: %w", name, err)
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

// openSourceFile opens a file of a release directory: never blocks on a
// FIFO or a device (O_NONBLOCK, then the descriptor must be a regular
// file), never follows a symlink for the file itself.
func (d RuntimeSourceDir) openSourceFile(version, name string) (*os.File, error) {
	if !runtimeVersionRe.MatchString(version) {
		return nil, errors.New("invalid runtime version")
	}
	if err := checkSourceDir(d.Dir); err != nil {
		return nil, fmt.Errorf("FUNCTIONS_RUNTIME_SOURCE_DIR: %w", err)
	}
	dir := filepath.Join(d.Dir, version)
	if err := checkSourceRelease(dir); err != nil {
		return nil, fmt.Errorf("FUNCTIONS_RUNTIME_SOURCE_DIR/%s: %w", version, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
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
	return f, nil
}

// Release implements RuntimeSource.
func (d RuntimeSourceDir) Release(ctx context.Context, version string) ([]TrustedRelease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if version != "" && (!runtimeVersionRe.MatchString(version) || compareRuntimeVersions(version, MinRuntimeVersion) < 0) {
		return nil, refusal{fmt.Sprintf("runtime %q is not a release %s or later", version, MinRuntimeVersion)}
	}
	if version == "" {
		if err := checkSourceDir(d.Dir); err != nil {
			return nil, fmt.Errorf("FUNCTIONS_RUNTIME_SOURCE_DIR: %w", err)
		}
		versions, err := sourceVersions(d.Dir)
		if err != nil {
			return nil, fmt.Errorf("FUNCTIONS_RUNTIME_SOURCE_DIR: %w", err)
		}
		v, ok := newestRuntimeVersion(versions)
		if !ok {
			return nil, fmt.Errorf("FUNCTIONS_RUNTIME_SOURCE_DIR holds no release directory %s or later", MinRuntimeVersion)
		}
		version = v
	}
	f, err := d.openSourceFile(version, runtimeSumsName)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSHA256SUMS+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSHA256SUMS {
		return nil, refusal{"SHA256SUMS is too large"}
	}
	return parseSHA256SUMS(version, data)
}

// Open implements RuntimeSource.
func (d RuntimeSourceDir) Open(ctx context.Context, rel TrustedRelease) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := d.openSourceFile(rel.Version, RuntimeAssetName(rel.Arch))
	if err != nil {
		return nil, err
	}
	if rel.Size > 0 {
		if fi, err := f.Stat(); err != nil || fi.Size() != rel.Size {
			f.Close()
			return nil, refusal{fmt.Sprintf("the file does not have the trusted size of %s", rel.Version)}
		}
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

// runtimeCheckInterval is how often Run looks for a new release.
const runtimeCheckInterval = time.Hour

// RuntimeProvider is the RuntimeBinaries of the Manager: the RuntimeStore,
// plus the discovery and installation of the newest release published on
// GitHub (or in FUNCTIONS_RUNTIME_SOURCE_DIR), at startup and every
// CheckInterval (Run, with retries), and on demand (an operation needing a
// binary that is not installed starts an attempt and gets
// FUNCTION_RUNTIME_UNAVAILABLE meanwhile).
type RuntimeProvider struct {
	store  *RuntimeStore
	arch   string
	source RuntimeSource // nil: neither download nor source directory
	logger *slog.Logger
	audit  *slog.Logger
	// Backoff of Run; MinInterval between two on-demand attempts;
	// RefusalBackoff after a refused binary; CheckInterval between two
	// discoveries of Run (0: at startup only).
	Backoff        []time.Duration
	MinInterval    time.Duration
	RefusalBackoff time.Duration
	CheckInterval  time.Duration

	mu        sync.Mutex
	base      context.Context
	state     string
	running   bool
	last      time.Time
	refusedAt time.Time
	// limitedUntil: GitHub's rate limit, no attempt before.
	limitedUntil time.Time
	// newest, newestAt: the newest version found by the last discovery,
	// reused by on-demand attempts for CheckInterval (the releases list
	// counts against GitHub's anonymous rate limit, the files do not).
	newest   string
	newestAt time.Time
	now      func() time.Time
}

// NewRuntimeProvider wraps a store. source may be nil (downloads disabled).
func NewRuntimeProvider(store *RuntimeStore, arch string, source RuntimeSource, logger, audit *slog.Logger) *RuntimeProvider {
	return &RuntimeProvider{
		store: store, arch: arch, source: source, logger: logger, audit: audit,
		Backoff:     []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 30 * time.Minute},
		MinInterval: time.Minute, RefusalBackoff: refusalBackoff, CheckInterval: runtimeCheckInterval,
		base: context.Background(), state: RuntimeIdle, now: time.Now,
	}
}

// Path implements RuntimeBinaries.
func (p *RuntimeProvider) Path(version, arch string) string { return p.store.Path(version, arch) }

// Latest implements RuntimeBinaries: the newest accepted release installed;
// when there is none, it starts an attempt and returns
// FUNCTION_RUNTIME_UNAVAILABLE.
func (p *RuntimeProvider) Latest(arch string) (RuntimeBinary, error) {
	b, err := p.store.Latest(arch)
	if err == nil {
		return b, nil
	}
	if arch != p.arch {
		return RuntimeBinary{}, err
	}
	p.trigger("", arch, "")
	return RuntimeBinary{}, p.unavailable()
}

// Verify implements RuntimeVerifier (always a full re-hash: it is the check
// before a compose run). A binary that is missing (data directory restored
// without .runtime) is installed again on demand from its release and
// reported as FUNCTION_RUNTIME_UNAVAILABLE, never as tampering; it is only
// installed when its sha256 is the one the function pinned.
func (p *RuntimeProvider) Verify(b RuntimeBinary) (string, error) {
	path, err := p.store.Verify(b)
	if err == nil {
		return path, nil
	}
	rel, ok := p.store.Release(b.Version, b.Arch)
	if ok && rel.SHA256 != b.SHA256 || !runtimeVersionRe.MatchString(b.Version) || !ValidArch(b.Arch) ||
		compareRuntimeVersions(b.Version, MinRuntimeVersion) < 0 {
		return "", err
	}
	// Recorded and present but failing: tampering, never reinstalled.
	if _, lerr := os.Lstat(p.store.Path(b.Version, b.Arch)); ok && !errors.Is(lerr, fs.ErrNotExist) {
		return "", err
	}
	// Missing, or not recorded (record lost, or a binary installed by a
	// build that embedded its table): fetched from its release, kept only
	// with the sha256 the function pinned.
	p.trigger(b.Version, b.Arch, b.SHA256)
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

// Run looks for the newest release and installs it for the host
// architecture, retrying with backoff, then again every CheckInterval;
// on-demand attempts fill the gaps.
func (p *RuntimeProvider) Run(ctx context.Context) {
	p.mu.Lock()
	p.base = ctx
	p.mu.Unlock()
	if err := p.store.Cleanup(); err != nil {
		p.logger.Error("functions: the runtime directory cannot be cleaned up", "error", err)
	}
	if p.source == nil {
		rel, ok := p.store.Newest(p.arch)
		if _, err := p.store.Verify(binaryOf(rel)); !ok || err != nil {
			p.setState(RuntimeUnavailable)
			p.logger.Warn("functions: no loch function runtime binary is installed and FUNCTIONS_RUNTIME_DOWNLOAD is false: "+
				"set FUNCTIONS_RUNTIME_SOURCE_DIR (docs/functions.md)", "arch", p.arch)
		} else {
			p.setState(RuntimeInstalled)
		}
		return
	}
	for {
		p.retry(ctx)
		if p.CheckInterval <= 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(p.CheckInterval):
		}
	}
}

// retry runs attempts with backoff until one succeeds, an on-demand one
// runs, the backoff is exhausted or ctx ends.
func (p *RuntimeProvider) retry(ctx context.Context) {
	for i := 0; ; i++ {
		if !p.begin(false) {
			return // an on-demand attempt runs
		}
		if err := p.attempt(ctx, "", p.arch); err == nil {
			return
		}
		p.mu.Lock()
		refused := p.state == RuntimeUntrusted || p.now().Before(p.limitedUntil)
		p.mu.Unlock()
		if refused {
			return // retrying cannot change what was refused: next check or on demand
		}
		if i >= len(p.Backoff) {
			p.logger.Error("functions: giving up looking for the loch function runtime until a function operation needs it or the next check", "arch", p.arch)
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
	if p.running || p.now().Before(p.limitedUntil) {
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

// trigger starts an on-demand attempt in the background (version "": the
// newest release).
func (p *RuntimeProvider) trigger(version, arch, pinned string) {
	if p.source == nil || !p.begin(true) {
		return
	}
	p.mu.Lock()
	ctx := p.base
	if version == "" && p.newest != "" && p.now().Sub(p.newestAt) < p.CheckInterval {
		version = p.newest // no new API request before the next check
	}
	p.mu.Unlock()
	go p.attemptPinned(ctx, version, arch, pinned) //nolint:errcheck // logged and audited
}

// attempt runs one discovery and installation begun by begin, and sets the
// state on every exit path.
func (p *RuntimeProvider) attempt(ctx context.Context, version, arch string) error {
	return p.attemptPinned(ctx, version, arch, "")
}

// attemptPinned is attempt for a binary a function pinned (pinned: its
// sha256, "" for the newest release).
func (p *RuntimeProvider) attemptPinned(ctx context.Context, version, arch, pinned string) error {
	state, found, err := p.fetch(ctx, version, arch, pinned)
	p.mu.Lock()
	p.running, p.last, p.state = false, p.now(), state
	if state == RuntimeUntrusted {
		p.refusedAt = p.last
	}
	var rl rateLimited
	if errors.As(err, &rl) {
		p.limitedUntil = rl.until
	}
	if version == "" && found != "" {
		p.newest, p.newestAt = found, p.now()
	}
	p.mu.Unlock()
	return err
}

// fetch reads a release from the source (version "": the newest) and
// installs its binary for arch with the sha256 the release publishes; the
// store records it once the binary checked out. It returns the resulting
// state.
func (p *RuntimeProvider) fetch(ctx context.Context, version, arch, pinned string) (state, found string, err error) {
	src := p.source.Name()
	rels, err := p.source.Release(ctx, version)
	if err == nil {
		found = rels[0].Version
	}
	var rl rateLimited
	if errors.As(err, &rl) {
		p.logger.Warn("functions: GitHub rate limit reached while looking for the loch function runtime release", "until", rl.until)
		return RuntimeUnavailable, "", err
	}
	if err != nil {
		var rf refusal
		if errors.As(err, &rf) {
			p.audit.Warn("function runtime refused", "user", "panel", "version", version, "arch", arch,
				"source", src, "reason", truncate(err.Error(), 200))
			p.logger.Error("functions: a loch function runtime release was refused", "version", version, "source", src, "error", err)
			return RuntimeUntrusted, "", err
		}
		p.logger.Error("functions: looking for the loch function runtime release failed", "version", version, "source", src, "error", err)
		return RuntimeUnavailable, "", err
	}
	for _, rel := range rels {
		if rel.Arch != arch {
			continue
		}
		if pinned != "" && rel.SHA256 != pinned {
			err := refusal{fmt.Sprintf("the published sha256 of %s for %s is not the one the function pinned", rel.Version, arch)}
			p.audit.Warn("function runtime refused", "user", "panel", "version", rel.Version, "arch", arch,
				"source", src, "reason", err.Error())
			p.logger.Error("functions: a loch function runtime release was refused", "version", rel.Version, "source", src, "error", err)
			return RuntimeUntrusted, found, err
		}
		state, err := p.install(ctx, rel)
		return state, found, err
	}
	err = fmt.Errorf("release %s has no binary for %s", rels[0].Version, arch)
	p.logger.Error("functions: the loch function runtime release has no binary for the host architecture", "version", rels[0].Version, "arch", arch)
	return RuntimeUnavailable, found, err
}

// install installs one trusted release (nothing done when it is already
// installed and verified, nor when a stored file fails verification: a
// stored binary is never replaced). It returns the resulting state.
func (p *RuntimeProvider) install(ctx context.Context, rel TrustedRelease) (string, error) {
	b := binaryOf(rel)
	src := p.source.Name()
	recorded, isRecorded := p.store.Release(rel.Version, rel.Arch)
	if isRecorded && recorded.SHA256 != rel.SHA256 {
		err := errRecordConflict{rel.Version, rel.Arch}
		p.audit.Warn("function runtime refused", "user", "panel", "version", rel.Version, "arch", rel.Arch,
			"source", src, "reason", truncate(err.Error(), 200))
		p.logger.Error("functions: a loch function runtime release was refused", "version", rel.Version, "source", src, "error", err)
		return RuntimeUntrusted, err
	}
	if _, err := p.store.Verify(b); err == nil {
		return RuntimeInstalled, nil
	} else if _, lerr := os.Lstat(p.store.Path(rel.Version, rel.Arch)); isRecorded && !errors.Is(lerr, fs.ErrNotExist) {
		p.audit.Warn("function runtime refused", "user", "panel", "version", rel.Version, "arch", rel.Arch,
			"source", "store", "reason", "stored binary modified")
		p.logger.Error("functions: the stored loch function runtime binary fails verification: it is not replaced (remove it to install it again)",
			"version", rel.Version, "arch", rel.Arch, "path", p.store.Path(rel.Version, rel.Arch), "error", err)
		return RuntimeUntrusted, err
	}
	// Not recorded yet (or a binary left by an install interrupted before
	// its record): InstallRelease checks the bytes, never replaces a file.
	p.logger.Info("functions: installing the loch function runtime binary", "version", rel.Version, "arch", rel.Arch, "source", src)
	r := io.NopCloser(strings.NewReader(""))
	var err error
	if _, lerr := os.Lstat(p.store.Path(rel.Version, rel.Arch)); errors.Is(lerr, fs.ErrNotExist) {
		r, err = p.source.Open(ctx, rel) // else InstallRelease checks the stored file
	}
	if err == nil {
		_, err = p.store.InstallRelease(ctx, rel, r)
		r.Close()
	}
	if err != nil {
		state := RuntimeUnavailable
		var rf refusal
		var conflict errRecordConflict
		if errors.As(err, &rf) || errors.As(err, &conflict) || CodeOf(err) == CodeRuntimeUntrusted {
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
