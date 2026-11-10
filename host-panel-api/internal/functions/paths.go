package functions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"host-panel-api/internal/composekit"
)

// Layout of FUNCTIONS_DATA_DIRECTORY:
//
//	.loch/                      0700  private directories (shared parent)
//	  fn-<name>/                0700  private directory of a function
//	    function.json           0600  the reference
//	    secrets.env             0400  env_file (format: raw) of the secrets
//	    runtime.env             0400  env_file (format: raw) of the invoke credentials
//	.runtime/                   0700  loch function runtime binaries
//	  <version>/<arch>/loch-lambda-runtime  0555
//	fn-<name>/                  0700
//	  deployments/              0700
//	    <id>/                   0555  extracted code, mounted read-only at /var/task
//	  uploads/                  0700  zips being validated (temporary)
//
// Compose files live in HOSTING_STACKS_DIRECTORY/fn-<name>/ (writable by
// CI: never trusted, re-rendered and compared).
const (
	PrivateDir     = ".loch"
	ReferenceFile  = "function.json"
	SecretsEnvFile = "secrets.env"
	// CredentialsEnvFile holds what the runtime checks invoke credentials
	// with: LOCH_RUNTIME_TOKEN_SHA256 (never the token) and, when SigV4 is
	// enabled, LOCH_RUNTIME_ACCESS_KEY; an env_file like the secrets.
	CredentialsEnvFile = "runtime.env"
	DeploymentsDir     = "deployments"
	UploadsDir         = "uploads"
	RuntimeDir         = ".runtime"
	ComposeFile        = composekit.DefaultComposeFile
)

// Container paths.
const (
	RuntimeMount = "/loch/lambda-runtime"
	TaskMount    = "/var/task"
)

// Permissions.
const (
	privatePerm   = 0o700
	referencePerm = 0o600
	secretPerm    = 0o400
)

// FunctionUID and FunctionGID run every function (spike: an arbitrary
// non-root uid works with the AWS images).
const (
	FunctionUID = 10001
	FunctionGID = 10001
)

// Paths computes every path of the module (never read back from a file).
// Every method takes a validated Name.
type Paths struct {
	DataDir   string
	StacksDir string
}

// ProjectDir is the compose project directory of a function.
func (p Paths) ProjectDir(n Name) string { return filepath.Join(p.StacksDir, n.Project()) }

// PrivateRoot is the shared parent of the private directories.
func (p Paths) PrivateRoot() string { return filepath.Join(p.DataDir, PrivateDir) }

// Private is the private directory of a function.
func (p Paths) Private(n Name) string { return filepath.Join(p.PrivateRoot(), n.Project()) }

// CredentialsEnv is the env_file of the invoke credentials.
func (p Paths) CredentialsEnv(n Name) string {
	return filepath.Join(p.Private(n), CredentialsEnvFile)
}

// SecretsEnv is the env_file of the secrets.
func (p Paths) SecretsEnv(n Name) string { return filepath.Join(p.Private(n), SecretsEnvFile) }

// FunctionData is the data directory of a function (deployments, uploads).
func (p Paths) FunctionData(n Name) string { return filepath.Join(p.DataDir, n.Project()) }

// Deployments is the directory of the kept deployments of a function.
func (p Paths) Deployments(n Name) string { return filepath.Join(p.FunctionData(n), DeploymentsDir) }

// Deployment is the directory of one deployment, "" for an invalid id.
func (p Paths) Deployment(n Name, id string) string {
	if !ValidDeploymentID(id) {
		return ""
	}
	return filepath.Join(p.Deployments(n), id)
}

// Uploads is the temporary directory of the zips being validated.
func (p Paths) Uploads(n Name) string { return filepath.Join(p.FunctionData(n), UploadsDir) }

// Runtime is the directory of the runtime binaries.
func (p Paths) Runtime() string { return filepath.Join(p.DataDir, RuntimeDir) }

func invalidName() *Error { return newError(CodeInvalidName, "name", "invalid function name") }

// EnsureFunctionDirs creates the private and data directories of a
// function, never through a symlink, and checks the existing ones.
func (p Paths) EnsureFunctionDirs(n Name) error {
	if !n.valid() {
		return invalidName()
	}
	if err := checkPanelDir(p.DataDir); err != nil {
		return err
	}
	for _, d := range []struct {
		path string
		perm os.FileMode
	}{
		{p.PrivateRoot(), privatePerm},
		{p.Private(n), privatePerm},
		{p.FunctionData(n), privatePerm},
		{p.Deployments(n), privatePerm},
		{p.Uploads(n), privatePerm},
	} {
		created, err := composekit.EnsureDir(d.path, d.perm)
		if err != nil {
			return err
		}
		if created {
			// Exact modes whatever the umask.
			if err := os.Chmod(d.path, d.perm); err != nil {
				return err
			}
		}
		if err := checkPanelDir(d.path); err != nil {
			return err
		}
	}
	return nil
}

// checkPanelDir checks that p is a real directory (not a symlink) owned by
// the panel and writable by nobody else.
func checkPanelDir(p string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("%s: %w", p, composekit.ErrNotDir)
	}
	return checkPanelOwned(p, fi)
}

// checkPanelOwned refuses an entry not owned by the panel, or writable by
// its group or by others.
func checkPanelOwned(p string, fi os.FileInfo) error {
	if fi.Mode()&os.ModeSymlink == 0 && fi.Mode().Perm()&0o022 != 0 {
		return newError(CodeArtifactModified, "", "%s is writable by other users", p)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Geteuid() {
		return newError(CodeArtifactModified, "", "%s is not owned by the panel", p)
	}
	return nil
}

// privateDirs checks the private directories of a function (a planted
// symlink must not redirect a read or a write).
func (p Paths) privateDirs(n Name) error {
	if !n.valid() {
		return invalidName()
	}
	for _, d := range []string{p.DataDir, p.PrivateRoot(), p.Private(n)} {
		if err := checkPanelDir(d); err != nil {
			return err
		}
	}
	return nil
}

// dataDirs checks the data directories of a function.
func (p Paths) dataDirs(n Name) error {
	if !n.valid() {
		return invalidName()
	}
	for _, d := range []string{p.DataDir, p.FunctionData(n), p.Deployments(n)} {
		if err := checkPanelDir(d); err != nil {
			return err
		}
	}
	return nil
}

// WriteReference records a reference (validated, canonical order).
func (p Paths) WriteReference(ref Reference, cfg Config) error {
	ref.Sort()
	if err := ref.Validate(cfg); err != nil {
		return err
	}
	n, _ := ref.FunctionName() // validated above
	if err := p.privateDirs(n); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ref, "", "  ")
	if err != nil {
		return err
	}
	return composekit.WriteFileAtomic(p.Private(n), ReferenceFile, append(data, '\n'), referencePerm)
}

// ReadReference returns the validated reference of a function.
func (p Paths) ReadReference(n Name, cfg Config) (*Reference, error) {
	if err := p.privateDirs(n); err != nil {
		return nil, err
	}
	data, err := composekit.ReadFileNoFollow(filepath.Join(p.Private(n), ReferenceFile))
	if err != nil {
		return nil, err
	}
	var ref Reference
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ref); err != nil {
		return nil, newError(CodeInvalidReference, "", "function reference: %v", err)
	}
	if ref.Name != n.String() {
		return nil, newError(CodeInvalidReference, "", "function reference: name mismatch")
	}
	ref.droppedWarnings = dropUnknownWarnings(&ref)
	if err := ref.Validate(cfg); err != nil {
		return nil, err
	}
	return &ref, nil
}

// RenderSecretsEnv renders the env_file of the secrets (format: raw: one
// NAME=value per line, no quoting nor interpolation). Every name of the
// reference must have a value and every value a name.
func RenderSecretsEnv(names []string, values map[string]string) ([]byte, error) {
	if len(names) != len(values) {
		return nil, newError(CodeInvalidSecret, "secrets", "the secret values do not match the secret names")
	}
	sorted := slices.Sorted(slices.Values(names))
	var b strings.Builder
	for _, name := range sorted {
		v, ok := values[name]
		if !ok {
			return nil, newError(CodeInvalidSecret, "secrets", "missing value for secret %s", name)
		}
		if err := ValidateEnvName(name, "secrets"); err != nil {
			return nil, err
		}
		if err := ValidateEnvValue(v, "secrets"); err != nil {
			return nil, &Error{Code: CodeInvalidSecret, Field: "secrets", Message: "secret " + name + ": " + err.Message}
		}
		b.WriteString(name + "=" + v + "\n")
	}
	return []byte(b.String()), nil
}

// WriteSecretsEnv writes the env_file of the secrets (0400, private
// directory). The values are never logged nor returned.
func (p Paths) WriteSecretsEnv(n Name, names []string, values map[string]string) error {
	data, err := RenderSecretsEnv(names, values)
	if err != nil {
		return err
	}
	if err := p.privateDirs(n); err != nil {
		return err
	}
	return composekit.WriteFileAtomic(p.Private(n), SecretsEnvFile, data, secretPerm)
}

// RemoveFunctionDirs deletes the private and data directories of a function
// (deployments included, made writable first). Missing directories are not
// an error.
func (p Paths) RemoveFunctionDirs(n Name) error {
	if !n.valid() {
		return invalidName()
	}
	if err := checkPanelDir(p.DataDir); err != nil {
		return err
	}
	var errs []error
	if err := checkPanelDir(p.PrivateRoot()); err == nil {
		errs = append(errs, removeTree(p.Private(n)))
	} else if !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	}
	errs = append(errs, removeTree(p.FunctionData(n)))
	return errors.Join(errs...)
}

// Prefixes of the temporary files and directories of an upload.
const (
	uploadPattern  = "upload-*.zip"
	stagingPattern = ".staging-*"
)

// SweepLeftovers removes what interrupted uploads left behind (temporary
// zips, staging directories) in every function data directory. It must run
// at startup, before any upload is accepted. It returns the paths removed.
func (p Paths) SweepLeftovers() ([]string, error) {
	if err := checkPanelDir(p.DataDir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p.DataDir)
	if err != nil {
		return nil, err
	}
	var removed []string
	var errs []error
	for _, e := range entries {
		n, perr := ParseName(strings.TrimPrefix(e.Name(), ProjectPrefix))
		if perr != nil || n.Project() != e.Name() || p.dataDirs(n) != nil {
			continue
		}
		for _, sweep := range []struct{ dir, pattern string }{
			{p.Uploads(n), uploadPattern},
			{p.Deployments(n), stagingPattern},
		} {
			if checkPanelDir(sweep.dir) != nil {
				continue
			}
			matches, _ := filepath.Glob(filepath.Join(sweep.dir, sweep.pattern))
			for _, m := range matches {
				fi, err := os.Lstat(m)
				if err != nil {
					continue
				}
				if fi.IsDir() {
					err = removeTree(m)
				} else {
					err = os.Remove(m) // a file, or a planted link itself (never its target)
				}
				if err != nil {
					errs = append(errs, err)
					continue
				}
				removed = append(removed, m)
			}
		}
	}
	return removed, errors.Join(errs...)
}
