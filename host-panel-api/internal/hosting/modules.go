package hosting

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
)

// PHP modules are disabled through PHP_DISMOD, the standard variable of the
// webdevops images (so the compose file keeps working without the panel).
// At container start, the image runs `rm -f <conf.d>/*<name>*` for each name:
// it is a substring match on the ini file names, so the panel only accepts a
// list whose matches are exactly the modules the user asked to disable.

// phpConfDir is the PHP scan directory of the webdevops images (CLI and FPM).
const phpConfDir = "/usr/local/etc/php/conf.d"

// DefaultDisabledModules are disabled on new stacks: the ionCube loader runs
// encoded PHP and is a common vector of malware.
var DefaultDisabledModules = []string{"ioncube"}

// PHPModule is a module that can be disabled, and the ini file loading it.
type PHPModule struct {
	Name string `json:"name"`
	File string `json:"file"`
}

// ModuleInspector lists the ini files of the PHP scan directory of an image.
type ModuleInspector interface {
	ConfFiles(ctx context.Context, image string) ([]string, error)
}

// nonModuleFiles are configuration files of the images that are not modules.
var nonModuleFiles = []string{"webdevops", "docker", "docker-fpm"}

// moduleName derives a module name from an ini file name:
// "docker-php-ext-gd.ini" → "gd", "00-ioncube.ini" → "ioncube".
// It returns "" for files that do not load a module.
func moduleName(file string) string {
	name, ok := strings.CutSuffix(file, ".ini")
	if !ok || strings.HasPrefix(name, "zz-") {
		return ""
	}
	name = strings.TrimPrefix(name, "docker-php-ext-")
	if i := strings.IndexByte(name, '-'); i > 0 && strings.Trim(name[:i], "0123456789") == "" {
		name = name[i+1:]
	}
	if slices.Contains(nonModuleFiles, name) {
		return ""
	}
	return name
}

// ModulesFromFiles returns the modules loaded by a list of ini files, sorted.
func ModulesFromFiles(files []string) []PHPModule {
	var out []PHPModule
	for _, f := range files {
		if name := moduleName(f); name != "" {
			out = append(out, PHPModule{Name: name, File: f})
		}
	}
	slices.SortFunc(out, func(a, b PHPModule) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// checkDisabledModules verifies that PHP_DISMOD would remove exactly the
// requested modules. A name absent from the image is accepted when it matches
// no ini file at all (e.g. ioncube, disabled by default, on an image without
// it): it is then a no-op, and stays disabled if the image changes later.
func checkDisabledModules(files, disabled []string) *Error {
	for _, d := range disabled {
		for _, f := range files {
			if !strings.Contains(f, d) {
				continue
			}
			other := moduleName(f)
			if other == "" {
				return newError(CodeInvalidPHPModule, "php.disabledModules",
					"disabling %q would also remove the image configuration file %q", d, f)
			}
			if !slices.Contains(disabled, other) {
				return &Error{Code: CodeInvalidPHPModule, Field: "php.disabledModules", Reason: other,
					Message: fmt.Sprintf("disabling %q also disables %q: disable both", d, other)}
			}
		}
	}
	return nil
}

// DockerModuleInspector lists conf.d with a throwaway container (no network,
// read-only, no capabilities), pulling the image if needed. Results are
// cached per image reference, and concurrent requests for the same image
// share a single inspection.
type DockerModuleInspector struct {
	Binary string        // default "docker"
	TTL    time.Duration // default 12h

	mu       sync.Mutex
	cache    map[string]cachedFiles
	inflight map[string]*sync.Mutex
}

type cachedFiles struct {
	files []string
	at    time.Time
}

func (d *DockerModuleInspector) ConfFiles(ctx context.Context, image string) ([]string, error) {
	ttl := d.TTL
	if ttl == 0 {
		ttl = 12 * time.Hour
	}
	cached := func() ([]string, bool) {
		d.mu.Lock()
		defer d.mu.Unlock()
		c, ok := d.cache[image]
		return c.files, ok && time.Since(c.at) < ttl
	}
	if files, ok := cached(); ok {
		return files, nil
	}

	// One inspection (and pull) per image at a time.
	d.mu.Lock()
	if d.inflight == nil {
		d.inflight = make(map[string]*sync.Mutex)
	}
	lock, ok := d.inflight[image]
	if !ok {
		lock = &sync.Mutex{}
		d.inflight[image] = lock
	}
	d.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()
	if files, ok := cached(); ok {
		return files, nil
	}

	bin := d.Binary
	if bin == "" {
		bin = "docker"
	}
	cmd := exec.CommandContext(ctx, bin, "run", "--rm", "--pull", "missing", "--network", "none",
		"--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--entrypoint", "ls", image, "-1", phpConfDir)
	cmd.Env = dockerEnv()
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing PHP modules of %s: %w", image, err)
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); strings.HasSuffix(line, ".ini") {
			files = append(files, line)
		}
	}

	d.mu.Lock()
	if d.cache == nil {
		d.cache = make(map[string]cachedFiles)
	}
	d.cache[image] = cachedFiles{files: files, at: time.Now()}
	d.mu.Unlock()
	return files, nil
}
