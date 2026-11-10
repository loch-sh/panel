package composekit

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// The project directories live next to stacks deployed by CI. Every write
// goes through these helpers, which refuse symlinks (a planted link must never
// redirect a write) and write atomically (temp file + rename, which replaces a
// link itself and never follows it).

// ManagedMarker marks a project directory created by the panel. Directories
// without it are never managed, even if their name looks like a managed one.
const ManagedMarker = ".loch-managed"

// ErrNotDir is returned when a path is not a real directory (or is a symlink).
var ErrNotDir = errors.New("not a regular directory")

// maxReadBytes bounds ReadFileNoFollow.
const maxReadBytes = 4 << 20

// RealDir returns nil when p exists and is a directory (not a symlink).
func RealDir(p string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("%s: %w", p, ErrNotDir)
	}
	return nil
}

// EnsureDir creates p (one level) if missing and checks it is a real
// directory. It reports whether the directory was created.
func EnsureDir(p string, perm os.FileMode) (bool, error) {
	err := os.Mkdir(p, perm)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return false, err
	}
	return false, RealDir(p)
}

// WriteFileAtomic writes data to dir/name through a temp file and a rename.
func WriteFileAtomic(dir, name string, data []byte, perm os.FileMode) error {
	if err := RealDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+name+"-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, name))
}

// ReadFileNoFollow reads a regular file (at most 4 MiB), refusing symlinks.
func ReadFileNoFollow(p string) ([]byte, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", p)
	}
	return io.ReadAll(io.LimitReader(f, maxReadBytes))
}

// IsRegularFile reports whether p is a regular file (symlinks excluded).
func IsRegularFile(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode().IsRegular()
}
