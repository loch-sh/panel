package functions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/image"
)

// fakeELF returns a minimal 64-bit little-endian ELF executable header for
// a machine, followed by a marker (different binaries, different hashes).
func fakeELF(machine elf.Machine, marker string) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT), 0})
	b.Write(make([]byte, 8)) // padding of e_ident
	le := binary.LittleEndian
	put := func(v any) {
		if err := binary.Write(&b, le, v); err != nil {
			panic(err) // writes to a bytes.Buffer never fail
		}
	}
	put(uint16(elf.ET_EXEC))
	put(uint16(machine))
	put(uint32(elf.EV_CURRENT))
	put(uint64(0x401000)) // entry
	put(uint64(0))        // phoff
	put(uint64(0))        // shoff
	put(uint32(0))        // flags
	put(uint16(64))       // ehsize
	put(uint16(56))       // phentsize
	put(uint16(0))        // phnum
	put(uint16(64))       // shentsize
	put(uint16(0))        // shnum
	put(uint16(0))        // shstrndx
	b.WriteString(marker)
	return b.Bytes()
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func testStore(t *testing.T, releases ...TrustedRelease) (*RuntimeStore, Paths) {
	t.Helper()
	p := Paths{DataDir: t.TempDir()}
	return NewRuntimeStore(p, TrustTable{Releases: releases}), p
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("must not be read") }

func TestRuntimeRecordParse(t *testing.T) {
	h := strings.Repeat("a", 64)
	if _, err := ParseTrustTable([]byte(`{"releases":[{"version":"v1.0.2","arch":"arm64","sha256":"` + h + `","size":10}]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseTrustTable([]byte(`{"releases":[]}`)); err != nil {
		t.Fatal(err)
	}
	for name, doc := range map[string]string{
		"unknown field": `{"releases":[],"extra":1}`,
		"old signing":   `{"signing":{"identity":"https://x.y/ci.yml","issuer":"https://x.y"},"releases":[]}`,
		"negative size": `{"releases":[{"version":"v1.0.2","arch":"arm64","sha256":"` + h + `","size":-1}]}`,
		"huge size":     `{"releases":[{"version":"v1.0.2","arch":"arm64","sha256":"` + h + `","size":1000000000}]}`,
		"bad version":   `{"releases":[{"version":"1.0","arch":"arm64","sha256":"` + h + `"}]}`,
		"bad arch":      `{"releases":[{"version":"v1.0.2","arch":"riscv64","sha256":"` + h + `"}]}`,
		"bad sha":       `{"releases":[{"version":"v1.0.2","arch":"arm64","sha256":"abc"}]}`,
		"duplicate":     `{"releases":[{"version":"v1.0.2","arch":"arm64","sha256":"` + h + `"},{"version":"v1.0.2","arch":"arm64","sha256":"` + h + `"}]}`,
		"not json":      `releases`,
	} {
		if _, err := ParseTrustTable([]byte(doc)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestRuntimeRecordAccept(t *testing.T) {
	store, p := testStore(t)
	h1, h2 := strings.Repeat("a", 64), strings.Repeat("b", 64)
	rel := TrustedRelease{Version: "v1.0.2", Arch: "arm64", SHA256: h1}
	// Nothing recorded yet: the table of the builds before the discovery
	// (v1.0.1), no file needed.
	if got, err := LoadRuntimeRecord(p); err != nil || !slices.Equal(got.Releases, legacyRuntimeReleases) {
		t.Fatalf("initial record = %+v, %v", got, err)
	}
	if err := store.Accept([]TrustedRelease{rel, {Version: "v1.0.2", Arch: "amd64", SHA256: h2}}); err != nil {
		t.Fatal(err)
	}
	if r, ok := store.Release("v1.0.2", "arm64"); !ok || r.SHA256 != h1 {
		t.Fatalf("release = %+v", r)
	}
	if r, ok := store.Newest("amd64"); !ok || r.SHA256 != h2 {
		t.Fatalf("newest = %+v", r)
	}
	// Persisted 0600 in a 0700 directory, read back as written.
	fi, err := os.Stat(filepath.Join(p.Runtime(), runtimeRecordName))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("record file: %v %v", fi, err)
	}
	if got, err := LoadRuntimeRecord(p); err != nil || len(got.Releases) != 2 {
		t.Fatalf("reloaded record = %+v, %v", got, err)
	}
	// The same release again: nothing changes. Another sha256: refused, the
	// record keeps the first one.
	if err := store.Accept([]TrustedRelease{rel}); err != nil {
		t.Fatal(err)
	}
	var conflict errRecordConflict
	if err := store.Accept([]TrustedRelease{{Version: "v1.0.2", Arch: "arm64", SHA256: h2}}); !errors.As(err, &conflict) {
		t.Fatalf("conflicting sha256: %v", err)
	}
	// A conflict in a batch records nothing of it.
	if err := store.Accept([]TrustedRelease{{Version: "v1.0.3", Arch: "arm64", SHA256: h2}, {Version: "v1.0.2", Arch: "arm64", SHA256: h2}}); err == nil {
		t.Fatal("batch with a conflict accepted")
	}
	if _, ok := store.Release("v1.0.3", "arm64"); ok {
		t.Fatal("part of a refused batch recorded")
	}
	if got, _ := LoadRuntimeRecord(p); got.Releases[0].SHA256 != h1 && got.Releases[1].SHA256 != h1 {
		t.Fatalf("record changed: %+v", got)
	}
	// Older than MinRuntimeVersion, or invalid: refused.
	for _, r := range []TrustedRelease{{Version: "v1.0.0", Arch: "arm64", SHA256: h1}, {Version: "v1.0.4", Arch: "riscv64", SHA256: h1}, {Version: "v1.0.4", Arch: "arm64", SHA256: "x"}} {
		if err := store.Accept([]TrustedRelease{r}); err == nil {
			t.Errorf("%+v accepted", r)
		}
	}
	// A record modified outside the panel is not loaded.
	if err := os.Chmod(filepath.Join(p.Runtime(), runtimeRecordName), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRuntimeRecord(p); err == nil {
		t.Fatal("group-readable record loaded")
	}
	if err := os.Chmod(filepath.Join(p.Runtime(), runtimeRecordName), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.Runtime(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRuntimeRecord(p); err == nil {
		t.Fatal("record in a 0755 directory loaded")
	}
}

func TestRuntimeStoreInstallAndVerify(t *testing.T) {
	arm := fakeELF(elf.EM_AARCH64, "arm v1")
	store, p := testStore(t, TrustedRelease{Version: "v1.0.0", Arch: "arm64", SHA256: sha(arm)})
	ctx := context.Background()

	if _, err := store.Install(ctx, "v9.9.9", "arm64", bytes.NewReader(arm)); CodeOf(err) != CodeRuntimeUntrusted {
		t.Fatalf("untrusted version: %v", err)
	}
	if _, err := store.Install(ctx, "v1.0.0", "arm64", bytes.NewReader(append(arm, 'x'))); CodeOf(err) != CodeRuntimeUntrusted {
		t.Fatalf("wrong content: %v", err)
	}
	b, err := store.Install(ctx, "v1.0.0", "arm64", bytes.NewReader(arm))
	if err != nil {
		t.Fatal(err)
	}
	want := RuntimeBinary{Version: "v1.0.0", Arch: "arm64", SHA256: sha(arm)}
	if b != want {
		t.Fatalf("binary = %+v", b)
	}
	path, err := store.Verify(b)
	if err != nil || path != filepath.Join(p.DataDir, ".runtime", "v1.0.0", "arm64", RuntimeBinaryName) {
		t.Fatalf("verify = %q, %v", path, err)
	}
	if m := modeOf(t, path).Perm(); m != 0o555 {
		t.Fatalf("binary mode %v", m)
	}
	for dir, perm := range map[string]os.FileMode{p.Runtime(): 0o700, filepath.Dir(filepath.Dir(path)): 0o755, filepath.Dir(path): 0o755} {
		if m := modeOf(t, dir).Perm(); m != perm {
			t.Fatalf("%s mode %v, want %v", dir, m, perm)
		}
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Fatalf("temporary files left: %v", entries)
	}
	// Installed once: never rewritten, the source is not even read.
	if _, err := store.Install(ctx, "v1.0.0", "arm64", failingReader{}); err != nil {
		t.Fatal(err)
	}
	// A pin that the build no longer trusts.
	if _, err := store.Verify(RuntimeBinary{Version: "v1.0.0", Arch: "arm64", SHA256: strings.Repeat("0", 64)}); CodeOf(err) != CodeRuntimeUntrusted {
		t.Fatalf("other pin: %v", err)
	}
	if _, err := NewRuntimeStore(p, TrustTable{}).Verify(b); CodeOf(err) != CodeRuntimeUntrusted {
		t.Fatalf("no longer trusted: %v", err)
	}
}

func TestRuntimeStoreDetectsTampering(t *testing.T) {
	arm := fakeELF(elf.EM_AARCH64, "arm v1")
	rel := TrustedRelease{Version: "v1.0.0", Arch: "arm64", SHA256: sha(arm)}
	b := RuntimeBinary{Version: rel.Version, Arch: rel.Arch, SHA256: rel.SHA256}
	cases := map[string]func(t *testing.T, path string){
		"content": func(t *testing.T, path string) {
			mustChmod(t, path, 0o755)
			mustWriteFile(t, path, fakeELF(elf.EM_AARCH64, "evil"), 0o755)
			mustChmod(t, path, 0o555)
		},
		"mode":     func(t *testing.T, path string) { mustChmod(t, path, 0o755) },
		"setuid":   func(t *testing.T, path string) { mustChmod(t, path, os.ModeSetuid|0o555) },
		"dir mode": func(t *testing.T, path string) { mustChmod(t, filepath.Dir(path), 0o777) },
		"symlink": func(t *testing.T, path string) {
			other := filepath.Join(t.TempDir(), "bin")
			mustWriteFile(t, other, arm, 0o555)
			os.Remove(path)
			mustSymlink(t, other, path)
		},
		"symlinked dir": func(t *testing.T, path string) {
			dir := filepath.Dir(path)
			moved := filepath.Join(t.TempDir(), "arm64")
			if err := os.Rename(dir, moved); err != nil {
				t.Fatal(err)
			}
			mustSymlink(t, moved, dir)
		},
		"hard link": func(t *testing.T, path string) {
			if err := os.Link(path, filepath.Join(t.TempDir(), "extra")); err != nil {
				t.Fatal(err)
			}
		},
		"removed": func(t *testing.T, path string) { os.Remove(path) },
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			store, _ := testStore(t, rel)
			path := store.Path(b.Version, b.Arch)
			if _, err := store.Install(context.Background(), b.Version, b.Arch, bytes.NewReader(arm)); err != nil {
				t.Fatal(err)
			}
			tamper(t, path)
			if _, err := store.Verify(b); err == nil {
				t.Fatal("tampering not detected")
			}
		})
	}
}

func TestRuntimeStoreArchitecture(t *testing.T) {
	amd := fakeELF(elf.EM_X86_64, "amd v1")
	// Trusted hash, but an amd64 binary listed as arm64.
	store, _ := testStore(t, TrustedRelease{Version: "v1.0.0", Arch: "arm64", SHA256: sha(amd)})
	if _, err := store.Install(context.Background(), "v1.0.0", "arm64", bytes.NewReader(amd)); CodeOf(err) != CodeRuntimeUntrusted {
		t.Fatalf("wrong architecture: %v", err)
	}
	for data, want := range map[string]string{string(amd): "amd64", string(fakeELF(elf.EM_AARCH64, "")): "arm64"} {
		if got, err := BinaryArch(strings.NewReader(data)); err != nil || got != want {
			t.Fatalf("BinaryArch = %q, %v; want %s", got, err, want)
		}
	}
	for _, bad := range [][]byte{[]byte("#!/bin/sh\n"), fakeELF(elf.EM_RISCV, ""), nil} {
		if _, err := BinaryArch(bytes.NewReader(bad)); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	if err := CheckImageArch(image.InspectResponse{Os: "linux", Architecture: "arm64"}, "arm64"); err != nil {
		t.Fatal(err)
	}
	if err := CheckImageArch(image.InspectResponse{Os: "linux", Architecture: "amd64"}, "arm64"); err == nil {
		t.Fatal("architecture mismatch accepted")
	}
}

func TestRuntimeStoreSizeCap(t *testing.T) {
	store, _ := testStore(t, TrustedRelease{Version: "v1.0.0", Arch: "arm64", SHA256: strings.Repeat("a", 64)})
	big := io.LimitReader(zeroReader{}, maxRuntimeBinary+1)
	if _, err := store.Install(context.Background(), "v1.0.0", "arm64", big); CodeOf(err) != CodeRuntimeUntrusted {
		t.Fatalf("oversized binary: %v", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

// Verify never observes the transient second link of a concurrent Install.
func TestRuntimeStoreConcurrentInstallVerify(t *testing.T) {
	arm := fakeELF(elf.EM_AARCH64, "arm v1")
	store, _ := testStore(t, TrustedRelease{Version: "v1.0.0", Arch: "arm64", SHA256: sha(arm)})
	b := RuntimeBinary{Version: "v1.0.0", Arch: "arm64", SHA256: sha(arm)}
	if _, err := store.Install(context.Background(), b.Version, b.Arch, bytes.NewReader(arm)); err != nil {
		t.Fatal(err)
	}
	// Re-installing goes through the "already stored" path; a fresh
	// directory exercises the link path in parallel with Verify.
	store2, _ := testStore(t, TrustedRelease{Version: "v1.0.0", Arch: "arm64", SHA256: sha(arm)})
	errs := make(chan error, 40)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 20 {
			_, err := store2.Install(context.Background(), b.Version, b.Arch, bytes.NewReader(arm))
			errs <- err
		}
	}()
	for range 20 {
		if _, err := store2.Verify(b); err != nil && CodeOf(err) != CodeRuntimeUntrusted {
			// Not installed yet is fine; a modified binary (two links) is not.
			t.Errorf("verify during install: %v", err)
		}
	}
	<-done
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Verify(b); err != nil {
		t.Fatal(err)
	}
}
