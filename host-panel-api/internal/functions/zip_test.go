package functions

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash/crc32"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// zentry describes one entry of a generated zip. raw entries are written
// with CreateRaw: their header fields are written as given (lies included).
type zentry struct {
	name   string
	mode   fs.FileMode // 0: regular 0644 (or a directory when the name ends with /)
	body   []byte
	method uint16
	raw    *zip.FileHeader // body is then the raw (compressed) data
}

func buildZip(t *testing.T, entries ...zentry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		if e.raw != nil {
			fw, err := w.CreateRaw(e.raw)
			if err != nil {
				t.Fatal(err)
			}
			fw.Write(e.body)
			continue
		}
		h := &zip.FileHeader{Name: e.name, Method: e.method}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
			if strings.HasSuffix(e.name, "/") {
				mode = fs.ModeDir | 0o755
			}
		}
		h.SetMode(mode)
		fw, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func deflateBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	fw, _ := flate.NewWriter(&buf, flate.BestCompression)
	fw.Write(data)
	fw.Close()
	return buf.Bytes()
}

type deployEnv struct {
	paths       Paths
	name        Name
	uploads     string
	deployments string
	x           *Extractor
}

func newDeployEnv(t *testing.T, limits ZipLimits) *deployEnv {
	t.Helper()
	p := Paths{DataDir: t.TempDir(), StacksDir: t.TempDir()}
	t.Cleanup(func() { removeTree(p.DataDir) }) // read-only deployments
	n := nm("hello")
	if err := p.EnsureFunctionDirs(n); err != nil {
		t.Fatal(err)
	}
	x := NewExtractor(limits, 2)
	x.statfs = bigFS
	return &deployEnv{paths: p, name: n, uploads: p.Uploads(n), deployments: p.Deployments(n), x: x}
}

func bigFS(string) (fsSpace, error) {
	return fsSpace{dev: 1, bytes: 1 << 40, inodes: 1 << 30, inodesKnown: true}, nil
}

func (e *deployEnv) deploy(t *testing.T, data []byte) (*Deployment, error) {
	t.Helper()
	return e.x.Deploy(context.Background(), bytes.NewReader(data), e.paths, e.name, time.Now(), CodeCheck{})
}

// assertClean checks that a refused upload left nothing behind.
func (e *deployEnv) assertClean(t *testing.T) {
	t.Helper()
	for _, d := range []string{e.uploads, e.deployments} {
		entries, err := os.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("%s not cleaned up: %v", d, entries)
		}
	}
}

func modeOf(t *testing.T, p string) fs.FileMode {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode()
}

func TestDeployValidZip(t *testing.T) {
	env := newDeployEnv(t, DefaultZipLimits(0))
	data := buildZip(t,
		zentry{name: "index.js", body: []byte("exports.handler = async () => 'ok'\n"), method: zip.Deflate},
		zentry{name: "lib/util.js", mode: 0o600, body: []byte("module.exports = 1\n")},    // private: made world-readable
		zentry{name: "bin/run", mode: fs.ModeSetuid | 0o755, body: []byte("#!/bin/sh\n")}, // setuid stripped
		zentry{name: "empty/", mode: fs.ModeDir | 0o700},
		zentry{name: "node_modules/tool/cli.js", body: []byte("cli")},
		zentry{name: "node_modules/.bin/tool", mode: fs.ModeSymlink | 0o777, body: []byte("../tool/cli.js")},
		zentry{name: "current", mode: fs.ModeSymlink | 0o777, body: []byte("./lib")},
	)
	d, err := env.deploy(t, data)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if d.ZipSHA256 != hex.EncodeToString(sum[:]) || d.ZipSize != int64(len(data)) || d.Files != 7 || !ValidDeploymentID(d.ID) {
		t.Fatalf("deployment = %+v", d)
	}
	dir := env.paths.Deployment(env.name, d.ID)
	for p, want := range map[string]fs.FileMode{
		".":                        fs.ModeDir | 0o555,
		"index.js":                 0o444,
		"lib":                      fs.ModeDir | 0o555,
		"lib/util.js":              0o444,
		"bin/run":                  0o555,
		"empty":                    fs.ModeDir | 0o555,
		"node_modules/.bin":        fs.ModeDir | 0o555,
		"node_modules/tool/cli.js": 0o444,
	} {
		if got := modeOf(t, filepath.Join(dir, p)); got != want {
			t.Errorf("%s: mode %v, want %v", p, got, want)
		}
	}
	if target, err := os.Readlink(filepath.Join(dir, "node_modules/.bin/tool")); err != nil || target != "../tool/cli.js" {
		t.Fatalf("link = %q, %v", target, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "node_modules/.bin/tool")); string(got) != "cli" {
		t.Fatalf("link content = %q", got)
	}
	if h, err := TreeHash(dir, true); err != nil || h != d.TreeHash {
		t.Fatalf("tree hash %s, %v; recorded %s", h, err, d.TreeHash)
	}
	if err := env.paths.VerifyDeployment(env.name, d); err != nil {
		t.Fatal(err)
	}
	// Uploads are temporary; only the deployment is left.
	if entries, _ := os.ReadDir(env.uploads); len(entries) != 0 {
		t.Fatalf("upload left behind: %v", entries)
	}
	if entries, _ := os.ReadDir(env.deployments); len(entries) != 1 || entries[0].Name() != d.ID {
		t.Fatalf("deployments = %v", entries)
	}

	// The same zip gives the same tree hash (sorted, mode-normalized).
	d2, err := env.deploy(t, data)
	if err != nil {
		t.Fatal(err)
	}
	if d2.TreeHash != d.TreeHash || d2.ID == d.ID {
		t.Fatalf("second deployment %+v vs %+v", d2, d)
	}

	// Modified outside the panel: content, mode, added file, replaced link.
	file := filepath.Join(dir, "index.js")
	os.Chmod(file, 0o644)
	os.WriteFile(file, []byte("evil"), 0o644)
	os.Chmod(file, 0o444)
	if err := env.paths.VerifyDeployment(env.name, d); CodeOf(err) != CodeArtifactModified {
		t.Fatalf("modified content: %v", err)
	}
	if err := env.paths.VerifyDeployment(env.name, d2); err != nil {
		t.Fatal(err)
	}
	dir2 := env.paths.Deployment(env.name, d2.ID)
	os.Chmod(dir2, 0o755)
	os.WriteFile(filepath.Join(dir2, "extra.js"), []byte("x"), 0o444)
	os.Chmod(dir2, 0o555)
	if err := env.paths.VerifyDeployment(env.name, d2); CodeOf(err) != CodeArtifactModified {
		t.Fatalf("added file: %v", err)
	}
	os.Chmod(dir2, 0o755)
	os.Remove(filepath.Join(dir2, "extra.js"))
	os.Chmod(dir2, 0o555)
	if err := env.paths.VerifyDeployment(env.name, d2); err != nil {
		t.Fatalf("restored: %v", err)
	}
	os.Chmod(filepath.Join(dir2, "index.js"), 0o644) // writable again
	if err := env.paths.VerifyDeployment(env.name, d2); CodeOf(err) != CodeArtifactModified {
		t.Fatalf("writable file: %v", err)
	}

	// Read-only trees are removed.
	if err := env.paths.RemoveDeployment(env.name, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("deployment not removed: %v", err)
	}
	if err := env.paths.RemoveFunctionDirs(env.name); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(env.paths.FunctionData(env.name)); !os.IsNotExist(err) {
		t.Fatal("function data not removed")
	}
}

func TestDeployRefusesUnsafeArchives(t *testing.T) {
	link := func(name, target string) zentry {
		return zentry{name: name, mode: fs.ModeSymlink | 0o777, body: []byte(target)}
	}
	file := func(name string) zentry { return zentry{name: name, body: []byte("x")} }
	long := strings.Repeat("a", 256)
	cases := map[string][]zentry{
		"zip-slip":                {file("../evil.js")},
		"zip-slip nested":         {file("a/../../evil.js")},
		"dot-dot inside":          {file("a/../b.js")},
		"dot-dot alone":           {file("..")},
		"dot component":           {file("./a.js")},
		"inner dot component":     {file("a/./b.js")},
		"empty component":         {file("a//b.js")},
		"absolute path":           {file("/etc/passwd")},
		"backslash traversal":     {file("..\\evil.js")},
		"backslash separator":     {file("a\\b.js")},
		"NUL in name":             {file("a\x00b.js")},
		"control character":       {file("a\x01b.js")},
		"C1 control character":    {file("a\u0085b.js")},
		"invalid UTF-8":           {file("\xff.js")},
		"component too long":      {file(long + ".js")},
		"name too long":           {file(strings.Repeat(strings.Repeat("a", 250)+"/", 17) + "f")},
		"too deep":                {file(strings.Repeat("d/", 64) + "f")},
		"duplicate":               {file("a.js"), file("a.js")},
		"duplicate dir and file":  {file("a.js"), {name: "a.js/"}},
		"file is a dir prefix":    {file("a"), file("a/b.js")},
		"file is a deeper prefix": {file("a/b"), file("a/b/c/d.js")},
		"directory without slash": {{name: "d", mode: fs.ModeDir | 0o755}},
		"directory with data":     {dirWithData()},
		"named pipe":              {{name: "fifo", mode: fs.ModeNamedPipe | 0o644}},
		"device":                  {{name: "sda", mode: fs.ModeDevice | 0o644}},
		"char device":             {{name: "tty", mode: fs.ModeDevice | fs.ModeCharDevice | 0o644}},
		"socket":                  {{name: "sock", mode: fs.ModeSocket | 0o644}},
		"symlink escape":          {link("l", "../outside")},
		"symlink absolute":        {link("l", "/etc/passwd")},
		"symlink backslash":       {file("x.js"), link("l", "..\\x.js")},
		"symlink empty component": {file("x.js"), link("l", ".//x.js")},
		"symlink NUL":             {file("x.js"), link("l", "x.js\x00")},
		"symlink empty target":    {link("l", "")},
		"symlink deep escape":     {file("a/b/c.js"), link("a/b/l", "../../../x")},
		// Lexically "d", resolves above the root once d/up is followed.
		"symlink chain escape": {file("d/x.js"), link("d/up", ".."), link("d/esc", "up/..")},
		"symlink chain via link": {
			file("a/x.js"), link("a/top", ".."), link("b", "a/top/../outside"),
		},
		"symlink loop":           {link("a", "b"), link("b", "a")},
		"overlong UTF-8 dots":    {file("a/\xc0\xae\xc0\xae/x")},
		"symlink via dir link":   {file("d/x"), link("l", "d"), link("m", "l/../..")},
		"symlink trailing slash": {file("d/x"), link("l", "d/")},
		"file under dot link":    {link("a", "."), file("a/b")},
		"symlink chain of chain": {file("d/x"), link("d/up", ".."), link("d/esc", "up/.."), link("e", "d/esc/..")},
		"symlink self loop":      {link("a", "a")},
		"symlink missing target": {link("l", "nothere.js")},
		"symlink through file":   {file("f.js"), link("l", "f.js/x")},
		"write through symlink":  {{name: "dir/"}, link("lnk", "dir"), file("lnk/x.js")},
		"write through abs link": {link("lib", "/tmp"), file("lib/evil.js")},
		"symlink dup of file":    {file("x.js"), link("x.js", "y.js")},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			env := newDeployEnv(t, DefaultZipLimits(0))
			_, err := env.deploy(t, buildZip(t, entries...))
			if CodeOf(err) != CodeInvalidCode {
				t.Fatalf("err = %v (code %q), want %s", err, CodeOf(err), CodeInvalidCode)
			}
			t.Log(err)
			env.assertClean(t)
		})
	}
}

func TestDeployAcceptsSafeSymlinks(t *testing.T) {
	link := func(name, target string) zentry {
		return zentry{name: name, mode: fs.ModeSymlink | 0o777, body: []byte(target)}
	}
	env := newDeployEnv(t, DefaultZipLimits(0))
	_, err := env.deploy(t, buildZip(t,
		zentry{name: "pkg/lib/index.js", body: []byte("x")},
		link("pkg/up", ".."),                     // the root itself
		link("pkg/chain", "up/pkg/lib"),          // through another link, inside
		link("pkg/latest", "chain/index.js"),     // chain of links to a file
		link("dir-link", "pkg/lib"),              // to a directory
		zentry{name: ".hidden/.keep", body: nil}, // dot names are allowed
		zentry{name: "．．/x"},                     // fullwidth dots are plain characters
		link("pkg/lib/self", "."),
	))
	if err != nil {
		t.Fatal(err)
	}
}

func dirWithData() zentry {
	data := []byte("data")
	h := &zip.FileHeader{Name: "d/", Method: zip.Store, CRC32: crc32.ChecksumIEEE(data),
		CompressedSize64: uint64(len(data)), UncompressedSize64: uint64(len(data))}
	h.SetMode(fs.ModeDir | 0o755)
	return zentry{raw: h, body: data}
}

func rawEntry(t *testing.T, name string, method uint16, flags uint16, data []byte, declared uint64) zentry {
	t.Helper()
	body := data
	if method == zip.Deflate {
		body = deflateBytes(t, data)
	}
	h := &zip.FileHeader{
		Name: name, Method: method, Flags: flags, CRC32: crc32.ChecksumIEEE(data),
		CompressedSize64: uint64(len(body)), UncompressedSize64: declared,
	}
	h.SetMode(0o644)
	return zentry{raw: h, body: body}
}

func TestDeployRefusesEncodingTricks(t *testing.T) {
	data := []byte("exports.handler = 1")
	cases := map[string]zentry{
		"encrypted":            rawEntry(t, "a.js", zip.Store, 0x1, data, uint64(len(data))),
		"strong encryption":    rawEntry(t, "a.js", zip.Store, 0x40, data, uint64(len(data))),
		"unknown method":       rawEntry(t, "a.js", 99, 0, data, uint64(len(data))),
		"bzip2 method":         rawEntry(t, "a.js", 12, 0, data, uint64(len(data))),
		"declared too small":   rawEntry(t, "a.js", zip.Deflate, 0, data, 3),
		"declared too large":   rawEntry(t, "a.js", zip.Store, 0, data, uint64(len(data))+10),
		"stored size mismatch": rawEntry(t, "a.js", zip.Store, 0, data, 1),
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			env := newDeployEnv(t, DefaultZipLimits(0))
			_, err := env.deploy(t, buildZip(t, e))
			if CodeOf(err) != CodeInvalidCode {
				t.Fatalf("err = %v (code %q)", err, CodeOf(err))
			}
			t.Log(err)
			env.assertClean(t)
		})
	}
}

func TestDeployLimits(t *testing.T) {
	zeros := make([]byte, 20<<20)
	small := func(l *ZipLimits) { l.MaxFiles, l.MaxTotalBytes, l.MaxFileBytes = 5, 1000, 600 }
	cases := []struct {
		name   string
		limits func(*ZipLimits)
		data   func(t *testing.T) []byte
		code   string
	}{
		{"bomb by declared ratio", nil, func(t *testing.T) []byte {
			return buildZip(t, zentry{name: "zeros", body: zeros, method: zip.Deflate})
		}, CodeInvalidCode},
		{"bomb with a lying declared size", nil, func(t *testing.T) []byte {
			return buildZip(t, rawEntry(t, "zeros", zip.Deflate, 0, zeros, 1000))
		}, CodeInvalidCode},
		// Every file is under the ratio floor; together they are not.
		{"bomb by total ratio", nil, func(t *testing.T) []byte {
			var es []zentry
			for i := range 10 {
				es = append(es, zentry{name: "z" + string(rune('a'+i)), body: make([]byte, 512<<10), method: zip.Deflate})
			}
			return buildZip(t, es...)
		}, CodeInvalidCode},
		{"too many entries", small, func(t *testing.T) []byte {
			var es []zentry
			for i := range 6 {
				es = append(es, zentry{name: "f" + string(rune('a'+i))})
			}
			return buildZip(t, es...)
		}, CodeInvalidCode},
		{"declared total too large", small, func(t *testing.T) []byte {
			return buildZip(t, zentry{name: "a", body: make([]byte, 550)}, zentry{name: "b", body: make([]byte, 550)})
		}, CodeCodeTooLarge},
		{"declared file too large", small, func(t *testing.T) []byte {
			return buildZip(t, zentry{name: "a", body: make([]byte, 700)})
		}, CodeCodeTooLarge},
		{"written total over the cap despite small declared sizes", small, func(t *testing.T) []byte {
			b := bytes.Repeat([]byte("x"), 550)
			return buildZip(t, rawEntry(t, "a", zip.Store, 0, b, 10), rawEntry(t, "b", zip.Store, 0, b, 10))
		}, CodeInvalidCode},
		{"zip too large", func(l *ZipLimits) { l.MaxZipBytes = 100 }, func(t *testing.T) []byte {
			return buildZip(t, zentry{name: "a", body: bytes.Repeat([]byte("y"), 200)})
		}, CodeCodeTooLarge},
		{"not a zip", nil, func(t *testing.T) []byte { return []byte("this is not a zip archive at all") }, CodeInvalidCode},
		{"empty upload", nil, func(t *testing.T) []byte { return nil }, CodeInvalidCode},
		{"empty zip", nil, func(t *testing.T) []byte { return buildZip(t) }, CodeInvalidCode},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := DefaultZipLimits(0)
			if c.limits != nil {
				c.limits(&l)
			}
			env := newDeployEnv(t, l)
			_, err := env.deploy(t, c.data(t))
			if CodeOf(err) != c.code {
				t.Fatalf("err = %v (code %q), want %s", err, CodeOf(err), c.code)
			}
			t.Log(err)
			env.assertClean(t)
		})
	}
}

func TestDeployOperationalGuards(t *testing.T) {
	data := buildZip(t, zentry{name: "index.js", body: []byte("x")})

	t.Run("timeout", func(t *testing.T) {
		env := newDeployEnv(t, DefaultZipLimits(0))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := env.x.Deploy(ctx, bytes.NewReader(data), env.paths, env.name, time.Now(), CodeCheck{})
		if CodeOf(err) != CodeCodeTimeout {
			t.Fatalf("err = %v", err)
		}
		env.assertClean(t)
	})
	t.Run("busy", func(t *testing.T) {
		env := newDeployEnv(t, DefaultZipLimits(0))
		env.x.sem <- struct{}{}
		env.x.sem <- struct{}{}
		if _, err := env.deploy(t, data); CodeOf(err) != CodeBusy {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no space", func(t *testing.T) {
		env := newDeployEnv(t, DefaultZipLimits(0))
		env.x.statfs = func(string) (fsSpace, error) {
			return fsSpace{bytes: 10 << 20, inodes: 1 << 30, inodesKnown: true}, nil
		}
		if _, err := env.deploy(t, data); CodeOf(err) != CodeNoSpace {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("symlinked deployments directory", func(t *testing.T) {
		env := newDeployEnv(t, DefaultZipLimits(0))
		os.Remove(env.deployments)
		os.Symlink(t.TempDir(), env.deployments)
		if _, err := env.deploy(t, data); CodeOf(err) != CodeStorage {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestValidNameBounds(t *testing.T) {
	ok := []string{"a", "index.js", ".well-known/x", strings.Repeat("a", 255), strings.Repeat("d/", 63) + "f", "café/ü.js", "a b.js"}
	for _, n := range ok {
		if err := validName(n); err != nil {
			t.Errorf("%q refused: %v", n, err)
		}
	}
}

// A count that wraps modulo 65536 is accepted by archive/zip, which then
// allocates every record: the pre-scan refuses it before.
func TestDeployRefusesWrappedEntryCount(t *testing.T) {
	const n = 65536 + 5
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for i := range n {
		h := &zip.FileHeader{Name: strconv.Itoa(i), Method: zip.Store}
		if _, err := w.CreateHeader(h); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	data := buf.Bytes()
	eocd := bytes.LastIndex(data, []byte("PK\x05\x06"))
	binary.LittleEndian.PutUint16(data[eocd+8:], 5)
	binary.LittleEndian.PutUint16(data[eocd+10:], 5)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(zr.File) != n {
		t.Fatalf("archive/zip reads %d records (%v): the test no longer exercises the wrap", len(zr.File), err)
	}
	if got, ok := countDirectoryRecords(bytes.NewReader(data), int64(len(data)), 50_000); !ok || got != 50_001 {
		t.Fatalf("count = %d, %v", got, ok)
	}
	env := newDeployEnv(t, DefaultZipLimits(0))
	if _, err := env.deploy(t, data); CodeOf(err) != CodeInvalidCode || !strings.Contains(err.Error(), "more than 50000 entries") {
		t.Fatalf("err = %v", err)
	}
	env.assertClean(t)
	// A regular archive is counted exactly.
	small := buildZip(t, zentry{name: "a"}, zentry{name: "b/"}, zentry{name: "b/c"})
	if got, ok := countDirectoryRecords(bytes.NewReader(small), int64(len(small)), 10); !ok || got != 3 {
		t.Fatalf("count = %d, %v", got, ok)
	}
}

// A zip64 locator whose disk fields make archive/zip ignore the zip64
// record must not lower the pre-count (found by the security review: the
// zip64 record declared 1 entry, archive/zip read 131,071).
func TestDeployRefusesZip64Decoy(t *testing.T) {
	const n = 65535 + 65536
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for i := range n {
		w.CreateHeader(&zip.FileHeader{Name: strconv.Itoa(i), Method: zip.Store})
	}
	w.Close()
	data := buf.Bytes()
	le := binary.LittleEndian
	eocd := bytes.LastIndex(data, []byte("PK\x05\x06"))
	z64 := bytes.LastIndex(data[:eocd-20], []byte("PK\x06\x06"))
	cdSize, cdOff := le.Uint64(data[z64+40:]), le.Uint64(data[z64+48:])
	first := data[cdOff : int(cdOff)+46+int(le.Uint16(data[cdOff+28:]))+int(le.Uint16(data[cdOff+30:]))]
	// A decoy directory of one record, then the real one.
	decoy := append(append([]byte{}, first...), 0, 0, 0, 0)
	out := append(append(append([]byte{}, data[:cdOff]...), decoy...), data[cdOff:]...)
	nz64 := z64 + len(decoy)
	le.PutUint64(out[nz64+32:], 1)
	le.PutUint64(out[nz64+40:], uint64(len(first)))
	le.PutUint64(out[nz64+48:], cdOff)
	loc := len(out) - 22 - 20
	le.PutUint64(out[loc+8:], uint64(nz64))
	le.PutUint32(out[loc+4:], 1) // disk != 0: archive/zip ignores the zip64 record
	ne := len(out) - 22
	le.PutUint16(out[ne+10:], 0xffff)
	le.PutUint32(out[ne+12:], uint32(cdSize))
	le.PutUint32(out[ne+16:], uint32(cdOff)+uint32(len(decoy)))

	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil || len(zr.File) != n {
		t.Fatalf("archive/zip reads %d records (%v): the test no longer exercises the decoy", len(zr.File), err)
	}
	if got, ok := countDirectoryRecords(bytes.NewReader(out), int64(len(out)), 50_000); !ok || got <= 50_000 {
		t.Fatalf("pre-count = %d, %v", got, ok)
	}
	env := newDeployEnv(t, DefaultZipLimits(0))
	if _, err := env.deploy(t, out); CodeOf(err) != CodeInvalidCode || !strings.Contains(err.Error(), "entries") {
		t.Fatalf("err = %v", err)
	}
	env.assertClean(t)
}

// Implied directories count against MaxDirs (review: 50,000 entries of 62
// nested components would create about 3.1 million directories).
func TestDeployRefusesDirectoryExplosion(t *testing.T) {
	var es []zentry
	for i := range 400 {
		es = append(es, zentry{name: "x" + strconv.Itoa(i) + "/" + strings.Repeat("a/", 62) + "f"})
	}
	env := newDeployEnv(t, DefaultZipLimits(0))
	_, err := env.deploy(t, buildZip(t, es...))
	if CodeOf(err) != CodeInvalidCode || !strings.Contains(err.Error(), "more than 20000 directories") {
		t.Fatalf("err = %v", err)
	}
	env.assertClean(t)

	// Shared parents are counted once.
	var shared []zentry
	for i := range 3000 {
		shared = append(shared, zentry{name: "node_modules/pkg/lib/" + strconv.Itoa(i) + ".js"})
	}
	l := DefaultZipLimits(0)
	l.MaxDirs = 3
	env = newDeployEnv(t, l)
	if _, err := env.deploy(t, buildZip(t, shared...)); err != nil {
		t.Fatal(err)
	}
	// Explicit directories count too.
	l.MaxDirs = 2
	env = newDeployEnv(t, l)
	if _, err := env.deploy(t, buildZip(t, zentry{name: "a/"}, zentry{name: "b/"}, zentry{name: "c/"})); CodeOf(err) != CodeInvalidCode {
		t.Fatalf("explicit directories: %v", err)
	}
}

func TestExtractStopsOnTimeout(t *testing.T) {
	data := buildZip(t, zentry{name: "a/b/c.js"})
	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	plan, err := validateArchive(context.Background(), zr.File, int64(len(data)), DefaultZipLimits(0))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	if _, err := plan.extract(ctx, dir, DefaultZipLimits(0)); CodeOf(err) != CodeCodeTimeout {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("directories created after the deadline: %v", entries)
	}
}

// Link resolution stays cheap with many long, hop-heavy targets.
func TestValidateManyLongLinks(t *testing.T) {
	es := []zentry{{name: "d/x"}}
	long := strings.Repeat("d/../", 100) + "d"
	for i := range 6000 {
		es = append(es, zentry{name: "l" + strconv.Itoa(i), mode: fs.ModeSymlink | 0o777, body: []byte(long)})
	}
	data := buildZip(t, es...)
	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	start := time.Now()
	if _, err := validateArchive(context.Background(), zr.File, int64(len(data)), DefaultZipLimits(0)); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("validation took %v", d)
	}
}

func TestDeploySpaceReservation(t *testing.T) {
	l := DefaultZipLimits(0)
	// Zip, extracted code, and a block per possible file and directory.
	need := uint64(l.MaxZipBytes+l.MaxTotalBytes) + uint64(l.MaxFiles+l.MaxDirs+3)*blockSize
	env := newDeployEnv(t, l)
	// Room for one upload, not two: the second must not count the same space.
	env.x.statfs = func(string) (fsSpace, error) {
		return fsSpace{dev: 7, bytes: need + need/2, inodes: 1 << 30, inodesKnown: true}, nil
	}
	release, err := env.x.reserve(env.uploads, env.deployments)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.x.reserve(env.uploads, env.deployments); CodeOf(err) != CodeNoSpace {
		t.Fatalf("second reservation: %v", err)
	}
	// Without the per-file blocks, the zip and code alone would fit.
	env.x.statfs = func(string) (fsSpace, error) {
		return fsSpace{dev: 8, bytes: uint64(l.MaxZipBytes+l.MaxTotalBytes) + 1<<20, inodes: 1 << 30, inodesKnown: true}, nil
	}
	if _, err := env.x.reserve(env.uploads, env.deployments); CodeOf(err) != CodeNoSpace {
		t.Fatalf("block overhead ignored: %v", err)
	}
	env.x.statfs = func(string) (fsSpace, error) {
		return fsSpace{dev: 7, bytes: need + need/2, inodes: 1 << 30, inodesKnown: true}, nil
	}
	release()
	if r, err := env.x.reserve(env.uploads, env.deployments); err != nil {
		t.Fatalf("after release: %v", err)
	} else {
		r()
	}
	if len(env.x.reserved) != 0 {
		t.Fatalf("reservations left: %v", env.x.reserved)
	}
	// Inodes, when the file system reports them.
	env.x.statfs = func(string) (fsSpace, error) {
		return fsSpace{dev: 7, bytes: 1 << 40, inodes: 100, inodesKnown: true}, nil
	}
	if _, err := env.deploy(t, buildZip(t, zentry{name: "a.js"})); CodeOf(err) != CodeNoSpace {
		t.Fatalf("no inodes: %v", err)
	}
	env.x.statfs = func(string) (fsSpace, error) { return fsSpace{dev: 7, bytes: 1 << 40}, nil } // btrfs: unknown
	if _, err := env.deploy(t, buildZip(t, zentry{name: "a.js"})); err != nil {
		t.Fatalf("unknown inode count: %v", err)
	}
}

func TestSweepLeftovers(t *testing.T) {
	env := newDeployEnv(t, DefaultZipLimits(0))
	d, err := env.deploy(t, buildZip(t, zentry{name: "a.js"}))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(env.uploads, "upload-123.zip"), []byte("x"), 0o600)
	staging := filepath.Join(env.deployments, ".staging-456")
	os.MkdirAll(filepath.Join(staging, "sub"), 0o700)
	os.WriteFile(filepath.Join(staging, "sub", "f"), []byte("x"), 0o444)
	os.Chmod(filepath.Join(staging, "sub"), 0o555)
	victim := filepath.Join(t.TempDir(), "victim")
	os.WriteFile(victim, []byte("keep"), 0o600)
	os.Symlink(victim, filepath.Join(env.uploads, "upload-link.zip"))
	removed, err := env.paths.SweepLeftovers()
	if err != nil || len(removed) != 3 {
		t.Fatalf("removed %v, %v", removed, err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("a link target was removed")
	}
	if err := env.paths.VerifyDeployment(env.name, d); err != nil {
		t.Fatalf("deployment touched: %v", err)
	}
	env.assertNoLeftovers(t)
}

func (e *deployEnv) assertNoLeftovers(t *testing.T) {
	t.Helper()
	for _, pattern := range []string{filepath.Join(e.uploads, "*"), filepath.Join(e.deployments, ".*")} {
		if m, _ := filepath.Glob(pattern); len(m) != 0 {
			t.Fatalf("leftovers: %v", m)
		}
	}
}

func TestPathsRefuseUnsafeDirectories(t *testing.T) {
	env := newDeployEnv(t, DefaultZipLimits(0))
	d, err := env.deploy(t, buildZip(t, zentry{name: "a.js"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{env.paths.DataDir, env.paths.FunctionData(env.name), env.deployments} {
		os.Chmod(dir, 0o777)
		if err := env.paths.VerifyDeployment(env.name, d); CodeOf(err) != CodeArtifactModified {
			t.Errorf("%s writable by others: %v", dir, err)
		}
		if _, err := env.deploy(t, buildZip(t, zentry{name: "b.js"})); err == nil {
			t.Errorf("%s writable by others: upload accepted", dir)
		}
		os.Chmod(dir, 0o700)
	}
	os.Chmod(env.paths.Private(env.name), 0o770)
	if err := env.paths.WriteSecretsEnv(env.name, []string{"A"}, map[string]string{"A": "1"}); err == nil {
		t.Error("group-writable private directory accepted")
	}
	if err := env.paths.EnsureFunctionDirs(env.name); err == nil {
		t.Error("EnsureFunctionDirs accepted a group-writable private directory")
	}
	os.Chmod(env.paths.Private(env.name), 0o700)
	// A file of the tree writable by others.
	dir := env.paths.Deployment(env.name, d.ID)
	os.Chmod(filepath.Join(dir, "a.js"), 0o446)
	if err := env.paths.VerifyDeployment(env.name, d); CodeOf(err) != CodeArtifactModified {
		t.Errorf("world-writable file: %v", err)
	}
}

// Names never reach the file system unvalidated: the zero Name is refused.
func TestZeroNameRefused(t *testing.T) {
	env := newDeployEnv(t, DefaultZipLimits(0))
	var zero Name
	if err := env.paths.EnsureFunctionDirs(zero); err == nil {
		t.Error("EnsureFunctionDirs")
	}
	if err := env.paths.WriteSecretsEnv(zero, []string{"A"}, map[string]string{"A": "1"}); err == nil {
		t.Error("WriteSecretsEnv")
	}
	if _, err := env.paths.ReadReference(zero, testConfig); err == nil {
		t.Error("ReadReference")
	}
	if err := env.paths.RemoveFunctionDirs(zero); err == nil {
		t.Error("RemoveFunctionDirs")
	}
	if err := env.paths.VerifyDeployment(zero, &Deployment{ID: "20261106T120000Z-0123abcd"}); err == nil {
		t.Error("VerifyDeployment")
	}
	if _, err := env.x.Deploy(context.Background(), bytes.NewReader(nil), env.paths, zero, time.Now(), CodeCheck{}); err == nil {
		t.Error("Deploy")
	}
	if env.paths.Deployment(env.name, "../../x") != "" {
		t.Error("Deployment accepted an invalid id")
	}
	if err := env.paths.RemoveDeployment(env.name, "../../x"); err == nil {
		t.Error("RemoveDeployment accepted an invalid id")
	}
	for _, bad := range []string{"x/../../../victim", "../x", "x/y", ""} {
		if _, err := ParseName(bad); err == nil {
			t.Errorf("ParseName(%q) accepted", bad)
		}
	}
}
