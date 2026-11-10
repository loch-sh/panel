package hosting

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"host-panel-api/internal/composekit"
)

// The stacks directory is shared with stacks deployed by CI. Every write goes
// through these helpers, which:
//   - only touch "hosted-*" directories,
//   - refuse symlinks (a planted link must never redirect a write),
//   - write atomically (temp file + rename, which replaces a link itself and
//     never follows it).

const (
	previousDir = ".previous"
	dirPerm     = 0o750
	filePerm    = 0o640
)

// Symlink-safe helpers shared with other features (see composekit).
func realDir(p string) error                             { return composekit.RealDir(p) }
func ensureDir(p string, perm os.FileMode) (bool, error) { return composekit.EnsureDir(p, perm) }
func readFileNoFollow(p string) ([]byte, error)          { return composekit.ReadFileNoFollow(p) }
func isRegularFile(p string) bool                        { return composekit.IsRegularFile(p) }

// writeFileAtomic writes data to dir/name through a temp file and a rename.
func writeFileAtomic(dir, name string, data []byte) error {
	return composekit.WriteFileAtomic(dir, name, data, filePerm)
}

// readOptional reads a file, returning "" when it does not exist.
func readOptional(p string) (string, error) {
	data, err := readFileNoFollow(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(data), err
}

// stackFiles lists the files of a stack (relative to its directory) that are
// backed up before an update and restored on failure.
var stackFiles = []string{
	ComposeFile,
	filepath.Join(ConfigDir, PHPIniFile),
	filepath.Join(ConfigDir, VhostFile),
	filepath.Join(ConfigDir, RoutingFile),
	filepath.Join(ConfigDir, FPMPoolFile),
}

// generatedFiles are the config files the panel derives from a spec.
type generatedFiles struct {
	Routing string // routing rules ("" for the image default)
	FPMPool string // PHP-FPM pool overrides ("" when none)
}

func generatedFor(spec *Spec, snippets Snippets) generatedFiles {
	return generatedFiles{Routing: RoutingContent(spec, snippets), FPMPool: FPMPoolContent(spec)}
}

// writeStackFiles writes the compose file, the snippets and the generated
// config files of a stack.
func writeStackFiles(dir string, compose []byte, snippets Snippets, gen generatedFiles) error {
	// Check the stack directory before creating anything inside it: Mkdir
	// would otherwise follow a symlinked stack directory.
	if err := realDir(dir); err != nil {
		return err
	}
	cfgDir := filepath.Join(dir, ConfigDir)
	if _, err := ensureDir(cfgDir, dirPerm); err != nil {
		return err
	}
	// Snippet files always exist (possibly empty) so the compose mounts never
	// point at a missing file, which Docker would create as a directory.
	if err := writeFileAtomic(cfgDir, PHPIniFile, []byte(snippets.PHPIni)); err != nil {
		return err
	}
	if err := writeFileAtomic(cfgDir, VhostFile, []byte(snippets.Vhost)); err != nil {
		return err
	}
	if err := writeFileAtomic(cfgDir, RoutingFile, []byte(gen.Routing)); err != nil {
		return err
	}
	if err := writeFileAtomic(cfgDir, FPMPoolFile, []byte(gen.FPMPool)); err != nil {
		return err
	}
	// The compose file goes last: it is what marks the stack as complete.
	return writeFileAtomic(dir, ComposeFile, compose)
}

// backupStack copies the current files into .previous/.
func backupStack(dir string) error {
	return copyFiles(dir, filepath.Join(dir, previousDir))
}

// restoreStack copies .previous/ back over the stack files.
func restoreStack(dir string) error {
	return copyFiles(filepath.Join(dir, previousDir), dir)
}

func copyFiles(from, to string) error {
	if err := realDir(from); err != nil {
		return err
	}
	if _, err := ensureDir(to, dirPerm); err != nil {
		return err
	}
	for _, rel := range stackFiles {
		data, err := readFileNoFollow(filepath.Join(from, rel))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		target := filepath.Join(to, filepath.Dir(rel))
		if _, err := ensureDir(target, dirPerm); err != nil {
			return err
		}
		if err := writeFileAtomic(target, filepath.Base(rel), data); err != nil {
			return err
		}
	}
	return nil
}

// ensureSiteDirs creates <sitesDir>/<site>/<documentRoot> (each level checked
// against symlinks). Directories it creates are given to uid:gid so that SFTP
// users and the web server (webdevops "application" user) can write in them;
// existing directories keep their ownership.
func ensureSiteDirs(sitesDir, site, documentRoot string, uid, gid int) error {
	if err := realDir(sitesDir); err != nil {
		return err
	}
	current := filepath.Join(sitesDir, site)
	segments := []string{}
	if documentRoot != "" {
		segments = strings.Split(documentRoot, "/")
	}
	for i := 0; ; i++ {
		created, err := ensureDir(current, 0o755)
		if err != nil {
			return err
		}
		if created {
			if err := os.Lchown(current, uid, gid); err != nil && !errors.Is(err, os.ErrPermission) {
				return err
			}
		}
		if i == len(segments) {
			return nil
		}
		current = filepath.Join(current, segments[i])
	}
}

// listSiteDirs returns the directories directly under sitesDir.
func listSiteDirs(sitesDir string) ([]string, error) {
	entries, err := os.ReadDir(sitesDir)
	if err != nil {
		return nil, err
	}
	var sites []string
	for _, e := range entries {
		// DirEntry.Type comes from Lstat: symlinks are excluded.
		if e.IsDir() && e.Type()&os.ModeSymlink == 0 && ValidPathSegment(e.Name()) {
			sites = append(sites, e.Name())
		}
	}
	return sites, nil
}

// ManagedMarker marks a stack directory created by the panel. Directories
// without it are never managed, even if they look like hosted stacks.
const ManagedMarker = composekit.ManagedMarker

const managedMarkerContent = "Created by Loch.sh Panel (hosting). Do not remove: the panel only manages directories carrying this file.\n"

// operationMarker is present in a stack directory while an operation runs.
const operationMarker = ".loch-operation"

type operationMarkerData struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	StartedAt time.Time `json:"startedAt"`
}

// checkConfigFiles verifies that the config files mounted in the container
// are regular files (Docker follows a symlink at a bind mount source).
func checkConfigFiles(dir string) error {
	cfgDir := filepath.Join(dir, ConfigDir)
	if err := realDir(cfgDir); err != nil {
		return err
	}
	for _, rel := range stackFiles[1:] {
		p := filepath.Join(dir, rel)
		if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if !isRegularFile(p) {
			return fmt.Errorf("%s: not a regular file", p)
		}
	}
	return nil
}

func writeOperationMarker(dir string, op *Operation) error {
	data, err := json.Marshal(operationMarkerData{ID: op.ID, Kind: op.Kind, StartedAt: op.StartedAt})
	if err != nil {
		return err
	}
	return writeFileAtomic(dir, operationMarker, data)
}

func readOperationMarker(dir string) (operationMarkerData, bool) {
	var marker operationMarkerData
	data, err := readFileNoFollow(filepath.Join(dir, operationMarker))
	if err != nil || json.Unmarshal(data, &marker) != nil || marker.Kind == "" {
		return marker, false
	}
	return marker, true
}

func removeOperationMarker(dir string) {
	_ = os.Remove(filepath.Join(dir, operationMarker))
}
