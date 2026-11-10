package functions

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"host-panel-api/internal/composekit"
)

// NewDeploymentID returns a new, sortable deployment id.
func NewDeploymentID(now time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:])
}

// Normalized modes of an extracted tree, before and after it is made
// read-only. World-readable is mandatory: the function runs as uid 10001,
// the files belong to the panel.
const (
	dirMode      = 0o755
	fileMode     = 0o644
	execMode     = 0o755
	roDirMode    = 0o555
	roFileMode   = 0o444
	roExecMode   = 0o555
	maxTreeFiles = 200_000 // bound of a walk (the zip limit is lower)
)

// treeEntry is one line of a tree hash.
type treeEntry struct {
	path    string // slash-separated, relative
	kind    byte   // 'd', 'f', 'l'
	mode    os.FileMode
	content string // sha256 (files) or target (links)
}

// TreeHash returns the hash of a deployment directory: for every entry
// sorted by path, its path, type, normalized mode (0755 or 0644, whatever
// the read-only state) and sha256 or link target. Only directories, regular
// files and symlinks are accepted. When readOnly is set, the modes must be
// the read-only ones (0555 directories, 0444 / 0555 files).
func TreeHash(dir string, readOnly bool) (string, error) {
	entries, err := walkTree(dir, readOnly)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s\x00%c\x00%04o\x00%s\x00", e.path, e.kind, e.mode, e.content)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func walkTree(dir string, readOnly bool) ([]treeEntry, error) {
	if err := composekit.RealDir(dir); err != nil {
		return nil, err
	}
	var entries []treeEntry
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if len(entries) > maxTreeFiles {
			return errors.New("too many files")
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		perm := fi.Mode().Perm()
		if fi.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return fmt.Errorf("%s: special mode bits", rel)
		}
		if err := checkPanelOwned(rel, fi); err != nil {
			return err
		}
		e := treeEntry{path: filepath.ToSlash(rel)}
		switch {
		case fi.IsDir():
			e.kind, e.mode = 'd', dirMode
			if readOnly && perm != roDirMode {
				return fmt.Errorf("%s: directory is not read-only", rel)
			}
		case fi.Mode().IsRegular():
			e.kind, e.mode = 'f', fileMode
			if perm&0o100 != 0 {
				e.mode = execMode
			}
			if readOnly && perm != roFileMode && perm != roExecMode {
				return fmt.Errorf("%s: file is not read-only", rel)
			}
			sum, err := hashFileNoFollow(p)
			if err != nil {
				return err
			}
			e.content = sum
		case fi.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			e.kind, e.mode, e.content = 'l', 0o777, target
		default:
			return fmt.Errorf("%s: unexpected file type", rel)
		}
		if rel == "." {
			return nil // the root itself: its mode is checked, it has no path
		}
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(entries, func(a, b treeEntry) int { return strings.Compare(a.path, b.path) })
	return entries, nil
}

// hashFileNoFollow returns the sha256 of a regular file, read from the
// opened descriptor (a symlink is refused).
func hashFileNoFollow(p string) (string, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s: not a regular file", p)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyDeployment checks that a deployment directory is the recorded one:
// read-only, owned by the panel and with the recorded tree hash. Called
// before every activate, rollback and start.
func (p Paths) VerifyDeployment(n Name, d *Deployment) error {
	if d == nil || !ValidDeploymentID(d.ID) {
		return newError(CodeInvalidDeployment, "", "no valid deployment")
	}
	if err := p.dataDirs(n); err != nil {
		return newError(CodeArtifactModified, "", "the deployments directory is not the panel's: %v", err)
	}
	got, err := TreeHash(p.Deployment(n, d.ID), true)
	if err != nil {
		return newError(CodeArtifactModified, "", "deployment %s cannot be verified: %v", d.ID, err)
	}
	if got != d.TreeHash {
		return newError(CodeArtifactModified, "", "deployment %s was modified outside the panel", d.ID)
	}
	return nil
}

// removeTree deletes a directory tree that may contain read-only
// directories (deployments), never following a symlink. A missing path is
// not an error; a symlink in place of the directory is refused.
func removeTree(dir string) error {
	if err := composekit.RealDir(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	// Make the directories writable (owner), top-down: WalkDir does not
	// follow symlinks and needs to read each directory before entering it.
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return os.Chmod(p, 0o700)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// RemoveDeployment deletes a kept deployment.
func (p Paths) RemoveDeployment(n Name, id string) error {
	if !ValidDeploymentID(id) {
		return newError(CodeInvalidDeployment, "id", "invalid deployment id")
	}
	if err := p.dataDirs(n); err != nil {
		return err
	}
	return removeTree(p.Deployment(n, id))
}
