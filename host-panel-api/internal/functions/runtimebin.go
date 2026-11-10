package functions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	_ "embed"
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

// The trust root of the runtime binaries is the panel build (decision log
// §20): a table of version, architecture, sha256 and size, never a
// SHA256SUMS downloaded next to the binary. The signing identity in the
// table documents how its hashes were checked; the panel never verifies a
// signature at run time.
//
// Adding a release (see docs/functions.md, "Runtime binary"):
//
//  1. download loch-lambda-runtime-linux-{amd64,arm64}, SHA256SUMS and
//     SHA256SUMS.cosign.bundle from
//     https://github.com/loch-sh/lambda-runtime/releases/tag/vX.Y.Z;
//  2. cosign verify-blob --bundle SHA256SUMS.cosign.bundle \
//     --certificate-identity https://github.com/loch-sh/lambda-runtime/.github/workflows/ci.yml@refs/tags/vX.Y.Z \
//     --certificate-oidc-issuer https://token.actions.githubusercontent.com SHA256SUMS
//     (cosign >= 2.4);
//  3. shasum -a 256 -c SHA256SUMS --ignore-missing
//  4. for each binary: gh attestation verify <file> -R loch-sh/lambda-runtime \
//     --signer-workflow loch-sh/lambda-runtime/.github/workflows/ci.yml --source-ref refs/tags/vX.Y.Z
//     (a gh release supporting --source-ref);
//  5. add one entry per architecture (version, arch, sha256, size in bytes
//     from wc -c) to runtime-trust.json.
//
//go:embed runtime-trust.json
var embeddedTrust []byte

// TrustedRelease is one trusted runtime binary.
type TrustedRelease struct {
	Version string `json:"version"`
	Arch    string `json:"arch"`
	SHA256  string `json:"sha256"`
	// Size in bytes (0: unknown, bounded by maxRuntimeBinary). Every entry
	// of the embedded table has one: it caps a download.
	Size int64 `json:"size,omitempty"`
}

// TrustSigning documents the keyless (Sigstore) identity that signed the
// SHA256SUMS of the releases: the certificate identity is
// <Identity>@refs/tags/<version>. Informational only.
type TrustSigning struct {
	Identity string `json:"identity"`
	Issuer   string `json:"issuer"`
}

// TrustTable lists the runtime binaries the panel build trusts.
type TrustTable struct {
	Signing  *TrustSigning    `json:"signing,omitempty"`
	Releases []TrustedRelease `json:"releases"`
}

var signingURLRe = regexp.MustCompile(`\Ahttps://[a-z0-9.-]+(/[A-Za-z0-9._/-]*)?\z`)

// EmbeddedTrust returns the trust table of the panel build.
func EmbeddedTrust() (TrustTable, error) { return ParseTrustTable(embeddedTrust) }

// ParseTrustTable parses and validates a trust table.
func ParseTrustTable(data []byte) (TrustTable, error) {
	var t TrustTable
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return TrustTable{}, fmt.Errorf("runtime trust table: %w", err)
	}
	if sg := t.Signing; sg != nil && (!signingURLRe.MatchString(sg.Identity) || !signingURLRe.MatchString(sg.Issuer)) {
		return TrustTable{}, errors.New("runtime trust table: the signing identity and issuer must be https URLs (the identity without @ref)")
	}
	if len(t.Releases) > 0 && t.Signing == nil {
		return TrustTable{}, errors.New("runtime trust table: releases listed without their signing identity")
	}
	seen := map[string]bool{}
	for _, r := range t.Releases {
		if !runtimeVersionRe.MatchString(r.Version) || !ValidArch(r.Arch) || !hexSHA256Re.MatchString(r.SHA256) ||
			r.Size < 0 || r.Size > maxRuntimeBinary {
			return TrustTable{}, fmt.Errorf("runtime trust table: invalid entry %s/%s", r.Version, r.Arch)
		}
		if k := r.Version + "/" + r.Arch; seen[k] {
			return TrustTable{}, fmt.Errorf("runtime trust table: %s listed twice", k)
		} else {
			seen[k] = true
		}
	}
	return t, nil
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
	dir   string
	trust TrustTable
	uid   int
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

// NewRuntimeStore returns the store of a data directory.
func NewRuntimeStore(p Paths, trust TrustTable) *RuntimeStore {
	return &RuntimeStore{dir: p.Runtime(), trust: trust, uid: os.Geteuid(), verified: map[string]verifiedFile{}, now: time.Now}
}

// Path is where a runtime binary is stored (and the source of its bind
// mount: computed, never read from a file).
func (s *RuntimeStore) Path(version, arch string) string {
	return filepath.Join(s.dir, version, arch, RuntimeBinaryName)
}

func untrusted(format string, args ...any) *Error {
	return newError(CodeRuntimeUntrusted, "", format, args...)
}

// Install stores a runtime binary read from r (a download, or a file of
// FUNCTIONS_RUNTIME_SOURCE_DIR) if, and only if, its size and sha256 are the
// ones of the trust table and it is an ELF binary of the architecture. An
// already stored binary is verified, never rewritten. A binary refused for
// what it is returns FUNCTION_RUNTIME_UNTRUSTED; one that could not be read
// in full (network, timeout) FUNCTION_RUNTIME_UNAVAILABLE.
func (s *RuntimeStore) Install(ctx context.Context, version, arch string, r io.Reader) (RuntimeBinary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !runtimeVersionRe.MatchString(version) || !ValidArch(arch) {
		return RuntimeBinary{}, untrusted("invalid runtime version or architecture")
	}
	rel, ok := s.trust.Release(version, arch)
	if !ok {
		return RuntimeBinary{}, untrusted("runtime %s for %s is not trusted by this panel build", version, arch)
	}
	want := rel.SHA256
	limit := int64(maxRuntimeBinary)
	if rel.Size > 0 {
		limit = rel.Size
	}
	b := RuntimeBinary{Version: version, Arch: arch, SHA256: want}
	final := s.Path(version, arch)
	dir, err := s.ensureDirs(version, arch)
	if err != nil {
		return RuntimeBinary{}, err
	}
	if _, err := os.Lstat(final); err == nil {
		if _, err := s.verify(b, false); err != nil {
			return RuntimeBinary{}, err
		}
		return b, nil
	}
	tmp, err := os.CreateTemp(dir, ".incoming-*")
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
	if err := checkBinaryFD(tmp, want, arch); err != nil {
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
	// Link, not rename: never replaces a binary stored concurrently.
	if err := os.Link(tmpName, final); err != nil && !errors.Is(err, os.ErrExist) {
		return RuntimeBinary{}, newError(CodeStorage, "", "the runtime binary cannot be stored")
	}
	if err := os.Remove(tmpName); err != nil {
		return RuntimeBinary{}, newError(CodeStorage, "", "the runtime binary cannot be stored")
	}
	if _, err := s.verify(b, false); err != nil {
		return RuntimeBinary{}, err
	}
	return b, nil
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
	return s.verify(b, false)
}

// verify checks a stored binary; cached, a hash check of less than
// verifiedCacheTTL on a file with the same stamp is reused (Latest only).
func (s *RuntimeStore) verify(b RuntimeBinary, cached bool) (string, error) {
	key := b.Version + "/" + b.Arch
	p, err := s.verifyUncached(b, cached, key)
	if err != nil {
		delete(s.verified, key)
	}
	return p, err
}

func (s *RuntimeStore) verifyUncached(b RuntimeBinary, cached bool, key string) (string, error) {
	if !runtimeVersionRe.MatchString(b.Version) || !ValidArch(b.Arch) {
		return "", untrusted("invalid runtime version or architecture")
	}
	want, ok := s.trust.Lookup(b.Version, b.Arch)
	if !ok || want != b.SHA256 {
		return "", untrusted("runtime %s for %s is not trusted by this panel build", b.Version, b.Arch)
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
		if _, err := s.verify(b, true); err == nil {
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
