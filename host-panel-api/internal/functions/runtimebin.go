package functions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/moby/moby/api/types/image"
	"golang.org/x/sys/unix"

	"host-panel-api/internal/composekit"
)

// RuntimeBinaryName is the file name of the loch function runtime.
const RuntimeBinaryName = "loch-lambda-runtime"

// maxRuntimeBinary bounds a runtime binary (a static Go binary).
const maxRuntimeBinary = 64 << 20

var runtimeVersionRe = regexp.MustCompile(`\Av[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4}\z`)

// The runtime releases come from GitHub (decision log §20, 2026-11-12):
// the panel lists the releases of github.com/loch-sh/lambda-runtime, takes
// the newest stable one (MinRuntimeVersion or later) and the sha256 of its
// binaries from its SHA256SUMS, fetched over HTTPS from the release assets.
// GitHub (its TLS and the project's maintainers) is the trust root; no
// signature is verified. What the panel accepted is
// recorded in FUNCTIONS_DATA_DIRECTORY/.runtime/releases.json: a recorded
// sha256 never changes (a release republished with other bytes is refused
// and audited), a binary is only stored or mounted when it matches the
// record, and a function pins the sha256 it was created with.

// MinRuntimeVersion is the oldest release the panel accepts: the one the
// panel builds trusted before the discovery (functions created with it keep
// running).
const MinRuntimeVersion = "v1.0.1"

// TrustedRelease is one accepted runtime binary.
type TrustedRelease struct {
	Version string `json:"version"`
	Arch    string `json:"arch"`
	SHA256  string `json:"sha256"`
	// Size in bytes (0: unknown, bounded by maxRuntimeBinary).
	Size int64 `json:"size,omitempty"`
}

// TrustTable lists the accepted runtime binaries (the record).
type TrustTable struct {
	Releases []TrustedRelease `json:"releases"`
}

// ParseTrustTable parses and validates a record.
func ParseTrustTable(data []byte) (TrustTable, error) {
	var t TrustTable
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return TrustTable{}, fmt.Errorf("runtime record: %w", err)
	}
	seen := map[string]bool{}
	for _, r := range t.Releases {
		if err := validRelease(r); err != nil {
			return TrustTable{}, fmt.Errorf("runtime record: %w", err)
		}
		if k := r.Version + "/" + r.Arch; seen[k] {
			return TrustTable{}, fmt.Errorf("runtime record: %s listed twice", k)
		} else {
			seen[k] = true
		}
	}
	return t, nil
}

func validRelease(r TrustedRelease) error {
	if !runtimeVersionRe.MatchString(r.Version) || !ValidArch(r.Arch) || !hexSHA256Re.MatchString(r.SHA256) ||
		r.Size < 0 || r.Size > maxRuntimeBinary {
		return fmt.Errorf("invalid entry %s/%s", r.Version, r.Arch)
	}
	return nil
}

// Lookup returns the trusted sha256 of a runtime binary.
func (t TrustTable) Lookup(version, arch string) (string, bool) {
	r, ok := t.Release(version, arch)
	return r.SHA256, ok
}

// Release returns the trusted entry of a runtime binary.
func (t TrustTable) Release(version, arch string) (TrustedRelease, bool) {
	for _, r := range t.Releases {
		if r.Version == version && r.Arch == arch {
			return r, true
		}
	}
	return TrustedRelease{}, false
}

// Newest returns the newest trusted release of an architecture.
func (t TrustTable) Newest(arch string) (TrustedRelease, bool) {
	var best TrustedRelease
	found := false
	for _, r := range t.Releases {
		if r.Arch == arch && (!found || compareRuntimeVersions(r.Version, best.Version) > 0) {
			best, found = r, true
		}
	}
	return best, found
}

// RuntimeStore keeps the runtime binaries in FUNCTIONS_DATA_DIRECTORY/.runtime/
// <version>/<arch>/loch-lambda-runtime: 0555 files in panel-owned
// directories, written once (temp file + link), never modified in place.
type RuntimeStore struct {
	// installMu serializes installs; mu is only held for the record, the
	// checks and the link, never during a download.
	installMu sync.Mutex
	dir       string
	trust     TrustTable
	uid       int
	// mu serializes Install and Verify: while a binary is being linked to
	// its final name, it briefly has two links.
	mu sync.Mutex
	// verified caches the hash checks of Latest (never of Verify, the check
	// before a compose run), keyed on the file identity and times.
	verified map[string]verifiedFile
	now      func() time.Time
	hashes   int // binaries hashed (tests)
}

// verifiedCacheTTL bounds how long Latest trusts a previous hash check.
const verifiedCacheTTL = 10 * time.Second

// racyStampWindow: a verification is cached only when the file's ctime is
// at least this much older than the hash. File timestamps come from a
// coarse kernel clock (one tick, up to 10 ms on Linux): a write in the
// same tick as the hash, with the mtime restored, would leave the stamp
// unchanged ("racy git"). A file changed that recently is hashed again.
// A variable so that tests can exercise the cache on a fresh file.
var racyStampWindow = time.Second

// fileStamp identifies a file's content as far as the kernel tells: any
// write, rename over, chmod or link changes one of them.
type fileStamp struct {
	ino, size    int64
	mtime, ctime int64
	dev          int64
	mode, nlink  uint32
}

type verifiedFile struct {
	stamp fileStamp
	sha   string
	at    time.Time
}

func stampOf(f *os.File) (fileStamp, error) {
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		return fileStamp{}, err
	}
	return fileStamp{
		ino: int64(st.Ino), size: st.Size, dev: int64(st.Dev), mode: uint32(st.Mode), nlink: uint32(st.Nlink),
		mtime: st.Mtim.Sec*1e9 + int64(st.Mtim.Nsec), ctime: st.Ctim.Sec*1e9 + int64(st.Ctim.Nsec),
	}, nil
}

// NewRuntimeStore returns the store of a data directory, with the releases
// already accepted (LoadRuntimeRecord).
func NewRuntimeStore(p Paths, accepted TrustTable) *RuntimeStore {
	return &RuntimeStore{dir: p.Runtime(), trust: accepted, uid: os.Geteuid(), verified: map[string]verifiedFile{}, now: time.Now}
}

// legacyRuntimeReleases is the table the panel builds embedded before the
// discovery (v1.0.1, checked by hand against its cosign signature and build
// provenance): the record starts with it, so binaries installed by those
// builds stay usable, offline too. Its sha256 are the ones published on
// GitHub.
var legacyRuntimeReleases = []TrustedRelease{
	{Version: "v1.0.1", Arch: "amd64", SHA256: "829a2e4fc449234acff0332a9f702f928d65c35a99a315309669f011fd40f01d", Size: 8036476},
	{Version: "v1.0.1", Arch: "arm64", SHA256: "35146a3fd6685dc75ccfee8128b998d12732ed3b0e77922e76b2fd61658cd8d2", Size: 7471228},
}

// runtimeRecordName is the record of the accepted releases, in .runtime/.
const runtimeRecordName = "releases.json"

// LoadRuntimeRecord reads the releases accepted so far (the legacy table
// when the file does not exist yet). The file must be a regular 0600 file owned by the panel,
// in a 0700 .runtime directory owned by the panel.
func LoadRuntimeRecord(p Paths) (TrustTable, error) {
	dir := p.Runtime()
	uid := os.Geteuid()
	if fi, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return TrustTable{Releases: slices.Clone(legacyRuntimeReleases)}, nil
	} else if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 || !ownedBy(fi, uid) {
		return TrustTable{}, errors.New("runtime record: the runtime directory was modified outside the panel")
	}
	f, err := os.OpenFile(filepath.Join(dir, runtimeRecordName), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return TrustTable{Releases: slices.Clone(legacyRuntimeReleases)}, nil
	}
	if err != nil {
		return TrustTable{}, errors.New("runtime record: the file cannot be read")
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 || !ownedBy(fi, uid) {
		return TrustTable{}, errors.New("runtime record: the file was modified outside the panel")
	}
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return TrustTable{}, errors.New("runtime record: the file cannot be read")
	}
	return ParseTrustTable(data)
}

func ownedBy(fi os.FileInfo, uid int) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == uid
}

// Release returns the accepted entry of a runtime binary.
func (s *RuntimeStore) Release(version, arch string) (TrustedRelease, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.trust.Release(version, arch)
}

// Newest returns the newest accepted release of an architecture.
func (s *RuntimeStore) Newest(arch string) (TrustedRelease, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.trust.Newest(arch)
}

// errRecordConflict: a release already accepted is published again with
// another sha256 (a refusal, audited).
type errRecordConflict struct{ version, arch string }

func (e errRecordConflict) Error() string {
	return fmt.Sprintf("the published sha256 of %s for %s differs from the one accepted before: refused", e.version, e.arch)
}

// Accept records releases (validated; the versions MinRuntimeVersion or
// later). A release already recorded keeps its sha256: a different one is
// refused (errRecordConflict) and nothing is recorded. The record is
// written atomically before the call returns.
func (s *RuntimeStore) Accept(rels []TrustedRelease) error {
	for _, r := range rels {
		if runtimeVersionRe.MatchString(r.Version) && compareRuntimeVersions(r.Version, MinRuntimeVersion) < 0 {
			return fmt.Errorf("runtime %s is older than %s", r.Version, MinRuntimeVersion)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acceptLocked(rels)
}

// acceptLocked is Accept with mu held.
func (s *RuntimeStore) acceptLocked(rels []TrustedRelease) error {
	next := TrustTable{Releases: slices.Clone(s.trust.Releases)}
	added := false
	for _, r := range rels {
		if err := validRelease(r); err != nil {
			return err
		}
		if old, ok := next.Release(r.Version, r.Arch); ok {
			if old.SHA256 != r.SHA256 {
				return errRecordConflict{r.Version, r.Arch}
			}
			continue
		}
		next.Releases = append(next.Releases, r)
		added = true
	}
	if !added {
		return nil
	}
	if err := composekit.RealDir(filepath.Dir(s.dir)); err != nil {
		return newError(CodeStorage, "", "the functions data directory is not usable")
	}
	if created, err := composekit.EnsureDir(s.dir, 0o700); err != nil {
		return newError(CodeStorage, "", "the runtime directory is not usable")
	} else if created && os.Chmod(s.dir, 0o700) != nil { // umask
		return newError(CodeStorage, "", "the runtime directory is not usable")
	}
	if err := s.checkOwned(s.dir, 0o700, true); err != nil {
		return err
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".record-*")
	if err != nil {
		return newError(CodeStorage, "", "the runtime record cannot be written")
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil || tmp.Chmod(0o600) != nil || tmp.Sync() != nil || tmp.Close() != nil {
		tmp.Close()
		return newError(CodeStorage, "", "the runtime record cannot be written")
	}
	if err := os.Rename(tmp.Name(), filepath.Join(s.dir, runtimeRecordName)); err != nil {
		return newError(CodeStorage, "", "the runtime record cannot be written")
	}
	// The rename itself survives a power loss.
	if d, err := os.Open(s.dir); err == nil {
		_ = d.Sync() // best effort: the record is already in place
		d.Close()
	}
	s.trust = next
	return nil
}

// Path is where a runtime binary is stored (and the source of its bind
// mount: computed, never read from a file).
func (s *RuntimeStore) Path(version, arch string) string {
	return filepath.Join(s.dir, version, arch, RuntimeBinaryName)
}

func untrusted(format string, args ...any) *Error {
	return newError(CodeRuntimeUntrusted, "", format, args...)
}

// Install stores the binary of an accepted (recorded) release read from r;
// see InstallRelease.
func (s *RuntimeStore) Install(ctx context.Context, version, arch string, r io.Reader) (RuntimeBinary, error) {
	rel, ok := s.Release(version, arch)
	if !ok {
		return RuntimeBinary{}, untrusted("runtime %s for %s is not an accepted release", version, arch)
	}
	return s.InstallRelease(ctx, rel, r)
}

// InstallRelease stores a runtime binary read from r (a download, or a file
// of FUNCTIONS_RUNTIME_SOURCE_DIR) if, and only if, it has rel's sha256
// (and size, when known) and is an ELF binary of the architecture, then
// records rel: a release is recorded only once its binary checked out, and
// a recorded sha256 never changes (errRecordConflict). An already stored
// binary is verified, never rewritten. A binary refused for what it is
// returns FUNCTION_RUNTIME_UNTRUSTED; one that could not be read in full
// (network, timeout) FUNCTION_RUNTIME_UNAVAILABLE. The download runs
// without the store lock: Latest, Verify and Status stay responsive.
func (s *RuntimeStore) InstallRelease(ctx context.Context, rel TrustedRelease, r io.Reader) (RuntimeBinary, error) {
	s.installMu.Lock()
	defer s.installMu.Unlock()
	if err := validRelease(rel); err != nil {
		return RuntimeBinary{}, untrusted("invalid runtime version, architecture or sha256")
	}
	limit := int64(maxRuntimeBinary)
	if rel.Size > 0 {
		limit = rel.Size
	}
	b := binaryOf(rel)
	final := s.Path(rel.Version, rel.Arch)

	s.mu.Lock()
	if old, ok := s.trust.Release(rel.Version, rel.Arch); ok && old.SHA256 != rel.SHA256 {
		s.mu.Unlock()
		return RuntimeBinary{}, errRecordConflict{rel.Version, rel.Arch}
	}
	if _, err := s.ensureDirs(rel.Version, rel.Arch); err != nil {
		s.mu.Unlock()
		return RuntimeBinary{}, err
	}
	// No other install runs (installMu): earlier downloads are stale (an
	// install interrupted between its link and its removal leaves the
	// binary with two links until then).
	if err := sweepDownloads(s.dir); err != nil {
		s.mu.Unlock()
		return RuntimeBinary{}, newError(CodeStorage, "", "the runtime directory is not usable")
	}
	if _, err := os.Lstat(final); err == nil {
		defer s.mu.Unlock()
		if _, err := s.verify(b, false, false); err != nil {
			return RuntimeBinary{}, err
		}
		return b, s.acceptLocked([]TrustedRelease{rel})
	}
	tmp, err := os.CreateTemp(s.dir, ".download-*")
	s.mu.Unlock()
	if err != nil {
		return RuntimeBinary{}, newError(CodeStorage, "", "the runtime binary cannot be stored")
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	defer tmp.Close()
	n, err := io.Copy(tmp, ctxReader{ctx, io.LimitReader(r, limit+1)})
	if err != nil && n <= limit {
		return RuntimeBinary{}, newError(CodeRuntimeUnavailable, "", "the runtime binary could not be read in full")
	}
	if n > limit || n == 0 || rel.Size > 0 && n != rel.Size {
		return RuntimeBinary{}, untrusted("the runtime binary does not have its trusted size")
	}
	if err := tmp.Sync(); err != nil {
		return RuntimeBinary{}, newError(CodeStorage, "", "the runtime binary cannot be stored")
	}
	// Hash and check what is on disk, through the opened descriptor.
	if err := checkBinaryFD(tmp, rel.SHA256, rel.Arch); err != nil {
		if CodeOf(err) == CodeArtifactModified {
			return RuntimeBinary{}, untrusted("the runtime binary does not have its trusted sha256")
		}
		return RuntimeBinary{}, err
	}
	if err := tmp.Chmod(0o555); err != nil {
		return RuntimeBinary{}, newError(CodeStorage, "", "the runtime binary cannot be stored")
	}
	if err := tmp.Close(); err != nil {
		return RuntimeBinary{}, newError(CodeStorage, "", "the runtime binary cannot be stored")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.ensureDirs(rel.Version, rel.Arch); err != nil {
		return RuntimeBinary{}, err
	}
	// Link, not rename: never replaces a binary stored meanwhile.
	if err := os.Link(tmpName, final); err != nil && !errors.Is(err, os.ErrExist) {
		return RuntimeBinary{}, newError(CodeStorage, "", "the runtime binary cannot be stored")
	}
	if err := os.Remove(tmpName); err != nil {
		return RuntimeBinary{}, newError(CodeStorage, "", "the runtime binary cannot be stored")
	}
	if _, err := s.verify(b, false, false); err != nil {
		return RuntimeBinary{}, err
	}
	return b, s.acceptLocked([]TrustedRelease{rel})
}

// Cleanup removes the temporary files of interrupted installs (at startup:
// a binary left with two links is usable again).
func (s *RuntimeStore) Cleanup() error {
	s.installMu.Lock()
	defer s.installMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Lstat(s.dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := s.checkOwned(s.dir, 0o700, true); err != nil {
		return err
	}
	return sweepDownloads(s.dir)
}

// sweepDownloads removes the temporary files of interrupted downloads
// (installMu and mu held).
func sweepDownloads(dir string) error {
	matches, err := filepath.Glob(filepath.Join(dir, ".download-*"))
	if err != nil {
		return err
	}
	for _, m := range matches {
		if err := os.Remove(m); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *RuntimeStore) ensureDirs(version, arch string) (string, error) {
	dirs := []struct {
		path string
		perm os.FileMode
	}{
		{s.dir, 0o700},
		{filepath.Join(s.dir, version), 0o755},
		{filepath.Join(s.dir, version, arch), 0o755},
	}
	if err := composekit.RealDir(filepath.Dir(s.dir)); err != nil {
		return "", newError(CodeStorage, "", "the functions data directory is not usable")
	}
	for _, d := range dirs {
		created, err := composekit.EnsureDir(d.path, d.perm)
		if err != nil {
			return "", newError(CodeStorage, "", "the runtime directory is not usable")
		}
		if created {
			if err := os.Chmod(d.path, d.perm); err != nil { // umask
				return "", newError(CodeStorage, "", "the runtime directory is not usable")
			}
		}
	}
	if err := sweepIncoming(dirs[2].path); err != nil {
		return "", newError(CodeStorage, "", "the runtime directory is not usable")
	}
	return dirs[2].path, nil
}

// sweepIncoming removes the temporary files an interrupted Install left
// (a crash between Link and Remove leaves the binary with two links). Called
// with mu held: no Install is running.
func sweepIncoming(dir string) error {
	matches, err := filepath.Glob(filepath.Join(dir, ".incoming-*"))
	if err != nil {
		return err
	}
	for _, m := range matches {
		if err := os.Remove(m); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Verify re-hashes a stored runtime binary and checks it is the pinned one,
// still trusted by this build, with the expected modes and owner, and of the
// expected architecture. It returns the path to mount. Called before every
// compose up.
func (s *RuntimeStore) Verify(b RuntimeBinary) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verify(b, false, true)
}

// verify checks a stored binary; cached, a hash check of less than
// verifiedCacheTTL on a file with the same stamp is reused (Latest only).
// verify checks a stored binary; record requires its release to be
// recorded with that sha256 (false: an install about to record it).
func (s *RuntimeStore) verify(b RuntimeBinary, cached, record bool) (string, error) {
	key := b.Version + "/" + b.Arch
	p, err := s.verifyUncached(b, cached, record, key)
	if err != nil {
		delete(s.verified, key)
	}
	return p, err
}

func (s *RuntimeStore) verifyUncached(b RuntimeBinary, cached, record bool, key string) (string, error) {
	if !runtimeVersionRe.MatchString(b.Version) || !ValidArch(b.Arch) {
		return "", untrusted("invalid runtime version or architecture")
	}
	if want, ok := s.trust.Lookup(b.Version, b.Arch); record && (!ok || want != b.SHA256) {
		return "", untrusted("runtime %s for %s is not an accepted release", b.Version, b.Arch)
	}
	for _, d := range []struct {
		path string
		perm os.FileMode
	}{
		{s.dir, 0o700},
		{filepath.Join(s.dir, b.Version), 0o755},
		{filepath.Join(s.dir, b.Version, b.Arch), 0o755},
	} {
		if err := s.checkOwned(d.path, d.perm, true); err != nil {
			return "", err
		}
	}
	if err := sweepIncoming(filepath.Join(s.dir, b.Version, b.Arch)); err != nil {
		return "", newError(CodeStorage, "", "the runtime directory is not usable")
	}
	p := s.Path(b.Version, b.Arch)
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", untrusted("runtime %s for %s is not installed", b.Version, b.Arch)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", untrusted("the runtime binary cannot be read")
	}
	if err := s.checkInfo(fi, 0o555, false); err != nil {
		return "", err
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Nlink != 1 {
		return "", newError(CodeArtifactModified, "", "the runtime binary has other links")
	}
	stamp, err := stampOf(f)
	if err != nil {
		return "", untrusted("the runtime binary cannot be read")
	}
	if c, ok := s.verified[key]; cached && ok && c.stamp == stamp && c.sha == b.SHA256 && s.now().Sub(c.at) < verifiedCacheTTL {
		return p, nil
	}
	s.hashes++
	hashedAt := time.Now() // the kernel's clock, not s.now: compared with ctime
	if err := checkBinaryFD(f, b.SHA256, b.Arch); err != nil {
		return "", err
	}
	if hashedAt.UnixNano()-stamp.ctime >= int64(racyStampWindow) {
		s.verified[key] = verifiedFile{stamp: stamp, sha: b.SHA256, at: s.now()}
	} else {
		delete(s.verified, key)
	}
	return p, nil
}

func (s *RuntimeStore) checkOwned(p string, perm os.FileMode, dir bool) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return untrusted("the runtime directory is missing")
	}
	return s.checkInfo(fi, perm, dir)
}

func (s *RuntimeStore) checkInfo(fi os.FileInfo, perm os.FileMode, dir bool) error {
	typeOK := fi.Mode().IsRegular()
	if dir {
		typeOK = fi.IsDir()
	}
	if !typeOK || fi.Mode()&os.ModeSymlink != 0 || fi.Mode().Perm() != perm ||
		fi.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return newError(CodeArtifactModified, "", "the runtime binary or its directory was modified outside the panel")
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != s.uid {
		return newError(CodeArtifactModified, "", "the runtime binary or its directory is not owned by the panel")
	}
	return nil
}

// checkBinaryFD hashes an opened file from its start and checks its sha256
// and its ELF architecture.
func checkBinaryFD(f *os.File, wantSHA, arch string) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return untrusted("the runtime binary cannot be read")
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxRuntimeBinary+1)); err != nil {
		return untrusted("the runtime binary cannot be read")
	}
	if hex.EncodeToString(h.Sum(nil)) != wantSHA {
		return newError(CodeArtifactModified, "", "the runtime binary does not have its trusted sha256")
	}
	got, err := BinaryArch(f)
	if err != nil || got != arch {
		return untrusted("the runtime binary is not a Linux %s executable", arch)
	}
	return nil
}

// BinaryArch returns the architecture (Go name) of a Linux ELF executable.
func BinaryArch(r io.ReaderAt) (string, error) {
	f, err := elf.NewFile(r)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB || f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN {
		return "", errors.New("not a 64-bit little-endian executable")
	}
	switch f.Machine {
	case elf.EM_X86_64:
		return "amd64", nil
	case elf.EM_AARCH64:
		return "arm64", nil
	}
	return "", fmt.Errorf("unsupported machine %s", f.Machine)
}

// CheckImageArch checks that a runtime image runs on the architecture of a
// runtime binary (the AWS live tags are single-architecture).
func CheckImageArch(img image.InspectResponse, arch string) error {
	if img.Os != "linux" || img.Architecture != arch {
		return untrusted("the image is %s/%s, the runtime binary linux/%s", img.Os, img.Architecture, arch)
	}
	return nil
}

// Latest returns the newest runtime binary of an architecture that is both
// trusted by this build and installed (verified; a hash check of less than
// 10 s on an unchanged file is reused). RuntimeProvider installs the newest
// trusted one.
func (s *RuntimeStore) Latest(arch string) (RuntimeBinary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var candidates []TrustedRelease
	for _, r := range s.trust.Releases {
		if r.Arch == arch {
			candidates = append(candidates, r)
		}
	}
	slices.SortFunc(candidates, func(a, b TrustedRelease) int { return compareRuntimeVersions(b.Version, a.Version) })
	for _, r := range candidates {
		b := RuntimeBinary{Version: r.Version, Arch: r.Arch, SHA256: r.SHA256}
		if _, err := s.verify(b, true, true); err == nil {
			return b, nil
		}
	}
	return RuntimeBinary{}, untrusted("no trusted loch function runtime binary is installed for %s", arch)
}

// compareRuntimeVersions orders "v1.2.3" versions (validated) numerically.
func compareRuntimeVersions(a, b string) int {
	pa, pb := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := range min(len(pa), len(pb)) {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x - y
		}
	}
	return len(pa) - len(pb)
}
