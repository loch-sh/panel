package functions

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

// ZipLimits bound the validation and extraction of an uploaded zip
// (decision log §20, plan reviews). Sizes are bytes actually written, never
// the sizes declared by the archive.
type ZipLimits struct {
	MaxZipBytes   int64 // uploaded archive (FUNCTIONS_MAX_CODE_SIZE)
	MaxTotalBytes int64 // extracted, all files
	MaxFileBytes  int64 // extracted, one file
	MaxFiles      int   // entries of the archive
	MaxDirs       int   // directories, explicit or implied by a path
	MaxRatio      int64 // extracted / compressed, per file and in total
	// RatioFloor: sizes below it are never refused for their ratio (small,
	// very repetitive files are common).
	RatioFloor int64
	Timeout    time.Duration
}

// Fixed bounds of the names.
const (
	maxNameBytes      = 4096
	maxComponentBytes = 255
	maxDepth          = 64
	maxLinkTarget     = 1024
	maxLinkHops       = 40
)

// DefaultZipLimits returns the limits of the plan for an archive cap.
func DefaultZipLimits(maxZipBytes int64) ZipLimits {
	if maxZipBytes <= 0 {
		maxZipBytes = DefaultMaxCodeBytes
	}
	return ZipLimits{
		MaxZipBytes:   maxZipBytes,
		MaxTotalBytes: 250 << 20,
		MaxFileBytes:  250 << 20,
		MaxFiles:      50_000,
		MaxDirs:       20_000,
		MaxRatio:      100,
		RatioFloor:    1 << 20,
		Timeout:       2 * time.Minute,
	}
}

// Extractor validates and extracts uploaded zips, a bounded number at a
// time (a zip costs disk and CPU: uploads beyond the bound are refused, not
// queued).
type Extractor struct {
	limits ZipLimits
	sem    chan struct{}
	// statfs returns the free space of the file system of a directory (tests).
	statfs func(dir string) (fsSpace, error)

	mu sync.Mutex
	// reserved is what running uploads may still write, per file system:
	// concurrent uploads must not each count the same free space.
	reserved map[uint64]fsSpace
}

// blockSize is the allocation unit counted per file and directory.
const blockSize = 4096

// fsSpace is the free space of a file system (or a reservation on it).
type fsSpace struct {
	dev    uint64
	bytes  uint64
	inodes uint64
	// inodesKnown: some file systems (btrfs) report no inode count.
	inodesKnown bool
}

// NewExtractor returns an extractor running at most concurrency extractions.
func NewExtractor(limits ZipLimits, concurrency int) *Extractor {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Extractor{limits: limits, sem: make(chan struct{}, concurrency), statfs: statfs, reserved: map[uint64]fsSpace{}}
}

// Deploy streams an uploaded zip to a temporary file in the uploads
// directory of a function, validates it, extracts it into a new read-only
// directory of its deployments (renamed atomically to its id) and returns
// its description. Nothing is left behind on failure. The caller bounds the
// request body (http.MaxBytesReader) and holds the function lock; check
// adds the checks of the function's runtime.
func (x *Extractor) Deploy(ctx context.Context, upload io.Reader, p Paths, n Name, now time.Time, check CodeCheck) (*Deployment, error) {
	select {
	case x.sem <- struct{}{}:
		defer func() { <-x.sem }()
	default:
		return nil, newError(CodeBusy, "", "too many code uploads in progress, retry later")
	}
	ctx, cancel := context.WithTimeout(ctx, x.limits.Timeout)
	defer cancel()

	if !n.valid() {
		return nil, invalidName()
	}
	uploadsDir, deploymentsDir := p.Uploads(n), p.Deployments(n)
	if p.dataDirs(n) != nil || checkPanelDir(uploadsDir) != nil {
		return nil, newError(CodeStorage, "", "the functions data directory is not usable")
	}
	release, err := x.reserve(uploadsDir, deploymentsDir)
	if err != nil {
		return nil, err
	}
	defer release()

	zipPath, sum, size, err := x.spool(ctx, upload, uploadsDir)
	if err != nil {
		return nil, err
	}
	defer os.Remove(zipPath)

	zf, err := os.OpenFile(zipPath, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, newError(CodeStorage, "", "the uploaded code cannot be read")
	}
	defer zf.Close()
	if n, found := countDirectoryRecords(zf, size, x.limits.MaxFiles); found && n > x.limits.MaxFiles {
		return nil, invalid("the zip holds more than %d entries", x.limits.MaxFiles)
	}
	zr, err := zip.NewReader(zf, size)
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) { // insecure names are refused below, with their name
		return nil, newError(CodeInvalidCode, "code", "the code is not a valid zip archive")
	}
	plan, err := validateArchive(ctx, zr.File, size, x.limits)
	if err != nil {
		return nil, err
	}
	if check.BootstrapArch != "" {
		if err := plan.requireBootstrap(check.BootstrapArch); err != nil {
			return nil, err
		}
	}

	id := NewDeploymentID(now)
	final := filepath.Join(deploymentsDir, id)
	if _, err := os.Lstat(final); !errors.Is(err, os.ErrNotExist) {
		return nil, newError(CodeStorage, "", "deployment %s already exists", id)
	}
	staging, err := os.MkdirTemp(deploymentsDir, stagingPattern)
	if err != nil {
		return nil, newError(CodeStorage, "", "the deployment directory cannot be created")
	}
	done := false
	defer func() {
		if !done {
			_ = removeTree(staging)
		}
	}()
	written, err := plan.extract(ctx, staging, x.limits)
	if err != nil {
		return nil, err
	}
	var warnings []string
	if check.BootstrapArch != "" {
		if warnings, err = checkBootstrap(staging, check.BootstrapArch); err != nil {
			return nil, err
		}
	}
	hash, err := TreeHash(staging, true)
	if err != nil {
		return nil, newError(CodeStorage, "", "the extracted code cannot be hashed")
	}
	if err := os.Rename(staging, final); err != nil {
		return nil, newError(CodeStorage, "", "the deployment cannot be activated on disk")
	}
	done = true
	return &Deployment{ID: id, TreeHash: hash, ZipSHA256: sum, ZipSize: size, Files: len(plan.entries), Bytes: written, Warnings: warnings}, nil
}

// reserve checks that the file systems of the uploads and deployments
// directories can receive a whole upload (zip, extracted code, inodes),
// counting what running uploads have reserved, and reserves it until
// release is called.
func (x *Extractor) reserve(uploadsDir, deploymentsDir string) (release func(), err error) {
	inodes := uint64(x.limits.MaxFiles + x.limits.MaxDirs + 2)
	// Every file and directory takes at least a block (4 KiB) on disk,
	// whatever its size.
	need := map[string]fsSpace{
		uploadsDir:     {bytes: uint64(x.limits.MaxZipBytes) + blockSize, inodes: 1},
		deploymentsDir: {bytes: uint64(x.limits.MaxTotalBytes) + inodes*blockSize, inodes: inodes},
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	perDev := map[uint64]fsSpace{}
	free := map[uint64]fsSpace{}
	for dir, n := range need {
		st, err := x.statfs(dir)
		if err != nil {
			return nil, newError(CodeStorage, "", "the free space cannot be checked")
		}
		r := perDev[st.dev]
		r.bytes += n.bytes
		r.inodes += n.inodes
		perDev[st.dev] = r
		free[st.dev] = st
	}
	for dev, n := range perDev {
		f, held := free[dev], x.reserved[dev]
		if f.bytes < held.bytes+n.bytes || (f.inodesKnown && f.inodes < held.inodes+n.inodes) {
			return nil, newError(CodeNoSpace, "", "not enough free space to receive the code")
		}
	}
	for dev, n := range perDev {
		r := x.reserved[dev]
		r.bytes += n.bytes
		r.inodes += n.inodes
		x.reserved[dev] = r
	}
	return func() {
		x.mu.Lock()
		defer x.mu.Unlock()
		for dev, n := range perDev {
			r := x.reserved[dev]
			r.bytes -= n.bytes
			r.inodes -= n.inodes
			if r.bytes == 0 && r.inodes == 0 {
				delete(x.reserved, dev)
			} else {
				x.reserved[dev] = r
			}
		}
	}, nil
}

// spool writes the upload to a 0600 temporary file, bounded, hashing it.
func (x *Extractor) spool(ctx context.Context, upload io.Reader, dir string) (string, string, int64, error) {
	f, err := os.CreateTemp(dir, uploadPattern)
	if err != nil {
		return "", "", 0, newError(CodeStorage, "", "the upload cannot be stored")
	}
	name := f.Name()
	fail := func(e error) (string, string, int64, error) {
		f.Close()
		os.Remove(name)
		return "", "", 0, e
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), ctxReader{ctx, io.LimitReader(upload, x.limits.MaxZipBytes+1)})
	if err != nil {
		if ctx.Err() != nil {
			return fail(newError(CodeCodeTimeout, "code", "the upload took too long"))
		}
		return fail(newError(CodeInvalidCode, "code", "the upload was interrupted"))
	}
	if n > x.limits.MaxZipBytes {
		return fail(newError(CodeCodeTooLarge, "code", "the zip exceeds %s", sizeText(x.limits.MaxZipBytes)))
	}
	if n == 0 {
		return fail(newError(CodeInvalidCode, "code", "the zip is empty"))
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", "", 0, newError(CodeStorage, "", "the upload cannot be stored")
	}
	return name, hex.EncodeToString(h.Sum(nil)), n, nil
}

// ctxReader stops reading once the context is done (extraction timeout).
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

type entryKind uint8

const (
	kindDir entryKind = iota + 1
	kindFile
	kindLink
)

type plannedEntry struct {
	f      *zip.File
	name   string // validated, slash-separated, no trailing slash
	kind   entryKind
	exec   bool
	target string // links
}

// archivePlan is a validated archive.
type archivePlan struct {
	entries []plannedEntry
	dirs    []string // every directory, explicit or implied, sorted
	zipSize int64
}

func invalid(format string, args ...any) *Error {
	return newError(CodeInvalidCode, "code", format, args...)
}

// validateArchive checks every entry of an archive before anything is
// written: names, types, compression, encryption, declared sizes, conflicts
// and symlink targets.
func validateArchive(ctx context.Context, files []*zip.File, zipSize int64, l ZipLimits) (*archivePlan, error) {
	if len(files) == 0 {
		return nil, invalid("the zip is empty")
	}
	if len(files) > l.MaxFiles {
		return nil, invalid("the zip holds more than %d entries", l.MaxFiles)
	}
	plan := &archivePlan{zipSize: zipSize}
	kinds := map[string]entryKind{}
	var declared uint64
	for _, f := range files {
		e, err := planEntry(f, l)
		if err != nil {
			return nil, err
		}
		if _, dup := kinds[e.name]; dup {
			return nil, invalid("%s appears twice in the zip", quoteName(e.name))
		}
		kinds[e.name] = e.kind
		declared += f.UncompressedSize64
		if declared > uint64(l.MaxTotalBytes) {
			return nil, newError(CodeCodeTooLarge, "code", "the extracted code would exceed %s", sizeText(l.MaxTotalBytes))
		}
		plan.entries = append(plan.entries, e)
	}
	// A file or a link is never a directory of another entry: no "a" and
	// "a/b", and no writing through a link ("lnk" -> elsewhere, "lnk/x").
	// Implied directories count: 50,000 deeply nested paths would
	// otherwise create millions of directories from a small zip.
	dirs := map[string]bool{}
	tooMany := func() error {
		return invalid("the zip holds more than %d directories", l.MaxDirs)
	}
	for _, e := range plan.entries {
		if e.kind == kindDir {
			if dirs[e.name] = true; len(dirs) > l.MaxDirs {
				return nil, tooMany()
			}
		}
		for p := path.Dir(e.name); p != "."; p = path.Dir(p) {
			switch kinds[p] {
			case kindFile:
				return nil, invalid("%s is both a file and a directory", quoteName(p))
			case kindLink:
				return nil, invalid("%s is inside the symlink %s", quoteName(e.name), quoteName(p))
			}
			if dirs[p] {
				break // its parents were checked when it was first seen
			}
			if dirs[p] = true; len(dirs) > l.MaxDirs {
				return nil, tooMany()
			}
		}
	}
	kindOf := func(p string) entryKind {
		if dirs[p] {
			return kindDir
		}
		return kinds[p]
	}
	targets := map[string]string{}
	for _, e := range plan.entries {
		if e.kind == kindLink {
			targets[e.name] = e.target
		}
	}
	for _, e := range plan.entries {
		if e.kind == kindLink {
			if ctx.Err() != nil {
				return nil, newError(CodeCodeTimeout, "code", "the validation took too long")
			}
			if err := resolveLink(e.name, kindOf, targets); err != nil {
				return nil, err
			}
		}
	}
	for d := range dirs {
		plan.dirs = append(plan.dirs, d)
	}
	slices.Sort(plan.dirs)
	return plan, nil
}

// planEntry validates one entry.
func planEntry(f *zip.File, l ZipLimits) (plannedEntry, error) {
	name := f.Name
	if f.Flags&0x1 != 0 || f.Flags&0x40 != 0 {
		return plannedEntry{}, invalid("%s is encrypted: encrypted zips are not supported", quoteName(name))
	}
	if f.Method != zip.Store && f.Method != zip.Deflate {
		return plannedEntry{}, invalid("%s uses an unsupported compression method (only stored and deflate)", quoteName(name))
	}
	mode := f.Mode()
	isDir := strings.HasSuffix(name, "/")
	if isDir {
		name = strings.TrimSuffix(name, "/")
	}
	if err := validName(name); err != nil {
		return plannedEntry{}, invalid("invalid name %s: %s", quoteName(f.Name), err)
	}
	e := plannedEntry{f: f, name: name}
	switch {
	case isDir:
		if !mode.IsDir() || f.UncompressedSize64 != 0 {
			return plannedEntry{}, invalid("%s is not a valid directory entry", quoteName(f.Name))
		}
		e.kind = kindDir
	case mode&fs.ModeSymlink != 0:
		e.kind = kindLink
		target, err := readLinkTarget(f)
		if err != nil {
			return plannedEntry{}, err
		}
		e.target = target
	case mode.IsDir():
		return plannedEntry{}, invalid("%s: a directory entry must end with /", quoteName(f.Name))
	case mode.IsRegular():
		e.kind = kindFile
		e.exec = mode.Perm()&0o111 != 0 // setuid, setgid and sticky are dropped
		if f.UncompressedSize64 > uint64(l.MaxFileBytes) {
			return plannedEntry{}, newError(CodeCodeTooLarge, "code", "%s exceeds %s once extracted", quoteName(name), sizeText(l.MaxFileBytes))
		}
		if f.UncompressedSize64 > uint64(ratioAllowance(int64(f.CompressedSize64), l)) {
			return plannedEntry{}, invalid("%s has a suspicious compression ratio", quoteName(name))
		}
	default:
		return plannedEntry{}, invalid("%s is not a regular file, a directory or a symlink", quoteName(f.Name))
	}
	return e, nil
}

// ratioAllowance is the most a compressed size may expand to.
func ratioAllowance(compressed int64, l ZipLimits) int64 {
	if compressed > (1<<62)/max(l.MaxRatio, 1) {
		return 1 << 62
	}
	return max(compressed*l.MaxRatio, l.RatioFloor)
}

// validName checks an entry name: UTF-8, no control character, no
// backslash, relative, no empty / "." / ".." component, bounded.
func validName(name string) error {
	if name == "" {
		return errors.New("empty name")
	}
	if len(name) > maxNameBytes {
		return errors.New("name too long")
	}
	if err := validChars(name); err != nil {
		return err
	}
	if strings.HasPrefix(name, "/") {
		return errors.New("absolute path")
	}
	parts := strings.Split(name, "/")
	if len(parts) > maxDepth {
		return errors.New("too deeply nested")
	}
	for _, p := range parts {
		switch {
		case p == "":
			return errors.New("empty path component")
		case p == "." || p == "..":
			return errors.New("relative path component")
		case len(p) > maxComponentBytes:
			return errors.New("path component too long")
		}
	}
	return nil
}

func validChars(s string) error {
	if !utf8.ValidString(s) {
		return errors.New("not UTF-8")
	}
	for _, r := range s {
		if r == '\\' {
			return errors.New("backslash")
		}
		if unicode.IsControl(r) || r == utf8.RuneError {
			return errors.New("control character")
		}
	}
	return nil
}

// readLinkTarget reads and validates the target of a symlink entry: a
// relative path ("." and ".." allowed), resolved later against the tree.
func readLinkTarget(f *zip.File) (string, error) {
	if f.UncompressedSize64 == 0 || f.UncompressedSize64 > maxLinkTarget {
		return "", invalid("%s: invalid symlink target", quoteName(f.Name))
	}
	rc, err := f.Open()
	if err != nil {
		return "", invalid("%s cannot be read", quoteName(f.Name))
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxLinkTarget+1))
	if err != nil || len(data) > maxLinkTarget || uint64(len(data)) != f.UncompressedSize64 {
		return "", invalid("%s: invalid symlink target", quoteName(f.Name))
	}
	t := string(data)
	if err := validChars(t); err != nil || strings.HasPrefix(t, "/") {
		return "", invalid("%s: the symlink target must be a relative path inside the code", quoteName(f.Name))
	}
	for _, p := range strings.Split(t, "/") {
		if p == "" || len(p) > maxComponentBytes {
			return "", invalid("%s: invalid symlink target", quoteName(f.Name))
		}
	}
	return t, nil
}

// resolveLink follows a symlink through the tree as the kernel would, links
// included, and refuses it unless every step stays inside the tree and the
// final target exists. A lexical check is not enough: with "d/up" -> "..",
// "d/esc" -> "up/.." is lexically "d" but resolves above the root.
func resolveLink(name string, kindOf func(string) entryKind, targets map[string]string) error {
	var stack []string
	if dir := path.Dir(name); dir != "." {
		stack = strings.Split(dir, "/") // real directories (checked by the caller)
	}
	pending := strings.Split(targets[name], "/")
	hops := 0
	for len(pending) > 0 {
		c := pending[0]
		pending = pending[1:]
		switch c {
		case ".":
			continue
		case "..":
			if len(stack) == 0 {
				return invalid("the symlink %s points outside the code", quoteName(name))
			}
			stack = stack[:len(stack)-1]
			continue
		}
		stack = append(stack, c)
		p := strings.Join(stack, "/")
		switch kindOf(p) {
		case kindDir:
		case kindFile:
			if len(pending) > 0 {
				return invalid("the symlink %s goes through the file %s", quoteName(name), quoteName(p))
			}
		case kindLink:
			if hops++; hops > maxLinkHops {
				return invalid("the symlink %s loops", quoteName(name))
			}
			stack = stack[:len(stack)-1]
			pending = append(strings.Split(targets[p], "/"), pending...)
		default:
			return invalid("the symlink %s points to a missing file", quoteName(name))
		}
	}
	return nil
}

// extract writes a validated plan into dir through os.Root: directories,
// then files, then symlinks (so that nothing is ever written through a
// link), then makes the tree read-only. It returns the bytes written.
func (p *archivePlan) extract(ctx context.Context, dir string, l ZipLimits) (int64, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, newError(CodeStorage, "", "the deployment directory cannot be opened")
	}
	defer root.Close()
	storage := func() error { return newError(CodeStorage, "", "the code cannot be written") }
	for _, d := range p.dirs {
		if ctx.Err() != nil {
			return 0, newError(CodeCodeTimeout, "code", "the extraction took too long")
		}
		if err := root.MkdirAll(d, 0o700); err != nil {
			return 0, storage()
		}
	}
	var total int64
	for _, e := range p.entries {
		if e.kind != kindFile {
			continue
		}
		if err := ctx.Err(); err != nil {
			return 0, newError(CodeCodeTimeout, "code", "the extraction took too long")
		}
		n, err := writeEntry(ctx, root, e, total, p.zipSize, l)
		if err != nil {
			return 0, err
		}
		total += n
	}
	for _, e := range p.entries {
		if e.kind == kindLink {
			if err := root.Symlink(e.target, e.name); err != nil {
				return 0, storage()
			}
		}
	}
	// Read-only: files first, then directories from the deepest.
	for _, e := range p.entries {
		if e.kind != kindFile {
			continue
		}
		mode := os.FileMode(roFileMode)
		if e.exec {
			mode = roExecMode
		}
		if err := root.Chmod(e.name, mode); err != nil {
			return 0, storage()
		}
	}
	for i := len(p.dirs) - 1; i >= 0; i-- {
		if err := root.Chmod(p.dirs[i], roDirMode); err != nil {
			return 0, storage()
		}
	}
	if err := root.Chmod(".", roDirMode); err != nil {
		return 0, storage()
	}
	return total, nil
}

// writeEntry extracts one file, counting the bytes actually written
// against the file, total and ratio limits (declared sizes are not trusted).
func writeEntry(ctx context.Context, root *os.Root, e plannedEntry, total, zipSize int64, l ZipLimits) (int64, error) {
	rc, err := e.f.Open()
	if err != nil {
		return 0, invalid("%s cannot be read", quoteName(e.name))
	}
	defer rc.Close()
	out, err := root.OpenFile(e.name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, newError(CodeStorage, "", "the code cannot be written")
	}
	defer out.Close()

	sizeCap := min(l.MaxFileBytes, l.MaxTotalBytes-total)
	fileRatioCap := ratioAllowance(int64(e.f.CompressedSize64), l)
	totalRatioCap := ratioAllowance(zipSize, l) - total
	limit := min(sizeCap, fileRatioCap, totalRatioCap)
	n, err := io.Copy(out, ctxReader{ctx, io.LimitReader(rc, limit+1)})
	if err != nil {
		if ctx.Err() != nil {
			return 0, newError(CodeCodeTimeout, "code", "the extraction took too long")
		}
		return 0, invalid("%s is corrupted", quoteName(e.name))
	}
	switch {
	case n > limit && limit == sizeCap:
		return 0, newError(CodeCodeTooLarge, "code", "the code exceeds %s per file or %s in total once extracted",
			sizeText(l.MaxFileBytes), sizeText(l.MaxTotalBytes))
	case n > limit && limit == fileRatioCap:
		return 0, invalid("%s has a suspicious compression ratio", quoteName(e.name))
	case n > limit:
		return 0, invalid("the zip has a suspicious compression ratio")
	case uint64(n) != e.f.UncompressedSize64:
		return 0, invalid("%s does not have its declared size", quoteName(e.name))
	}
	if err := out.Close(); err != nil {
		return 0, newError(CodeStorage, "", "the code cannot be written")
	}
	return n, nil
}

// sizeText formats a limit for a message.
func sizeText(n int64) string {
	if n >= 1<<20 && n%(1<<20) == 0 {
		return strconv.FormatInt(n>>20, 10) + " MB"
	}
	return strconv.FormatInt(n, 10) + " bytes"
}

// statfs returns the space available to the panel in the file system of dir.
func statfs(dir string) (fsSpace, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return fsSpace{}, err
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return fsSpace{}, err
	}
	var dev uint64
	if s, ok := fi.Sys().(*syscall.Stat_t); ok {
		dev = uint64(s.Dev)
	}
	return fsSpace{dev: dev, bytes: uint64(st.Bavail) * uint64(st.Bsize), inodes: uint64(st.Ffree), inodesKnown: st.Files > 0}, nil
}
