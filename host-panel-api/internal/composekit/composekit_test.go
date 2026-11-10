package composekit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Moved from internal/hosting with tailBuffer.
func TestTailBuffer(t *testing.T) {
	if got := (&tailBuffer{max: 5}).String(); got != "" {
		t.Fatalf("empty = %q", got)
	}
	short := &tailBuffer{max: 5}
	if _, err := short.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if got := short.String(); got != "abc" {
		t.Fatalf("untruncated = %q", got)
	}
	b := &tailBuffer{max: 5}
	for _, chunk := range []string{"abc", "defgh"} {
		if _, err := b.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if got := b.String(); got != "…defgh" {
		t.Fatalf("tail = %q", got)
	}
}

func TestExecRunnerArgs(t *testing.T) {
	dir := "/data/compose/hosted-site"
	got, err := ExecRunner{}.args(dir, "up", "-d")
	if err != nil {
		t.Fatal(err)
	}
	want := "compose --project-name hosted-site --project-directory /data/compose/hosted-site " +
		"-f /data/compose/hosted-site/compose.yaml --env-file " + os.DevNull +
		" --ansi never --progress plain up -d"
	if strings.Join(got, " ") != want {
		t.Fatalf("args =\n%s\nwant\n%s", strings.Join(got, " "), want)
	}

	got, _ = ExecRunner{Files: []string{"compose.yaml", "compose.override.yaml"}}.args(dir, "config")
	if j := strings.Join(got, " "); !strings.Contains(j, "-f "+dir+"/compose.yaml -f "+dir+"/compose.override.yaml --env-file") {
		t.Fatalf("override not passed in order: %s", j)
	}

	for _, bad := range []string{"../other/compose.yaml", "/etc/compose.yaml", ""} {
		if _, err := (ExecRunner{Files: []string{bad}}).args(dir); err == nil {
			t.Fatalf("file %q must be refused", bad)
		}
	}
}

func TestWriteFileAtomicPermAndSymlinkDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "d")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(dir, "secret", []byte("x"), 0o400); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "secret")); fi.Mode().Perm() != 0o400 {
		t.Fatalf("perm = %v", fi.Mode().Perm())
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(link, "f", []byte("x"), 0o640); err == nil {
		t.Fatal("writing through a symlinked directory must fail")
	}
	if err := os.Symlink(filepath.Join(dir, "secret"), filepath.Join(root, "filelink")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFileNoFollow(filepath.Join(root, "filelink")); err == nil {
		t.Fatal("reading a symlinked file must fail")
	}
	if IsRegularFile(filepath.Join(root, "filelink")) {
		t.Fatal("a symlink is not a regular file")
	}
}

func TestEnsureDir(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "x")
	if created, err := EnsureDir(p, 0o750); err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if created, err := EnsureDir(p, 0o750); err != nil || created {
		t.Fatalf("second call: created=%v err=%v", created, err)
	}
	if err := os.Symlink(p, filepath.Join(root, "l")); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureDir(filepath.Join(root, "l"), 0o750); err == nil {
		t.Fatal("a symlink must not be accepted as a directory")
	}
}

// RunStdin gives compose the exact bytes on stdin (-f -), never a path.
func TestRunStdin(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\"\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := ExecRunner{Binary: bin}.RunStdin(t.Context(), dir, []byte("services: {}\n"), "up", "-d")
	if err != nil {
		t.Fatal(err)
	}
	want := "compose --project-name " + filepath.Base(dir) + " --project-directory " + dir + " -f - --env-file /dev/null --ansi never --progress plain up -d\nservices: {}\n"
	if out != want {
		t.Fatalf("got %q", out)
	}
	if _, err := (ExecRunner{Binary: bin}).RunStdin(t.Context(), "relative", nil); err == nil {
		t.Fatal("a relative project directory must be refused")
	}
}
