package services

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"host-panel-api/internal/composekit"
)

const composekitMarker = composekit.ManagedMarker

var engineIDRe = regexp.MustCompile(`\A[a-z][a-z0-9]{1,15}\z`)

// versionRe: canonical versions only ("8.4", "17"; no leading zero), so
// that a version has one instance key.
var versionRe = regexp.MustCompile(`\A(0|[1-9][0-9]{0,2})(\.(0|[1-9][0-9]{0,2}))?\z`)

// Registry holds the enabled engines and their versions: the versions
// discovered on the registries (see catalog.go), the built-in catalogs used
// until a discovery succeeded, and the versions pinned in the configuration.
type Registry struct {
	order    []string
	engines  map[string]Engine
	releases map[string]Releases
	pinned   map[string][]string
	now      func() time.Time

	mu        sync.RWMutex
	state     catalogState
	store     string     // file the discovered catalog is kept in ("" = memory only)
	refreshMu sync.Mutex // one discovery at a time
}

// NewRegistry keeps the engines listed in enabled (all when empty) and, per
// engine, the versions pinned in the configuration (always offered, even out
// of support). Unknown engines, invalid pinned versions and invalid engine
// declarations are configuration errors: they fail at startup.
func NewRegistry(all []Engine, enabled []string, pinned map[string][]string, now func() time.Time) (*Registry, error) {
	if now == nil {
		now = time.Now
	}
	byID := map[string]Engine{}
	for _, e := range all {
		if err := checkEngine(e); err != nil {
			return nil, err
		}
		if _, dup := byID[e.ID()]; dup {
			return nil, fmt.Errorf("services: duplicate engine %q", e.ID())
		}
		byID[e.ID()] = e
	}
	ids := enabled
	if len(ids) == 0 {
		for _, e := range all {
			ids = append(ids, e.ID())
		}
	}
	for id := range pinned {
		if !slices.Contains(ids, id) {
			return nil, fmt.Errorf("services: versions pinned for engine %q, which is not enabled", id)
		}
	}
	r := &Registry{engines: map[string]Engine{}, releases: map[string]Releases{}, pinned: map[string][]string{}, now: now,
		state: catalogState{Engines: map[string]*engineCatalog{}}}
	for _, id := range ids {
		e, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("services: unknown engine %q", id)
		}
		if _, dup := r.engines[id]; dup {
			return nil, fmt.Errorf("services: engine %q listed twice", id)
		}
		rel := e.Releases()
		seen := map[string]bool{}
		for _, p := range pinned[id] {
			if !versionRe.MatchString(p) || seen[p] {
				return nil, fmt.Errorf("services: engine %q: invalid or duplicate pinned version %q", id, p)
			}
			// Exact tags (x.y.z) are only known once discovered: such a
			// version is offered when the registry publishes it.
			if CompareVersions(p, rel.Min) < 0 {
				return nil, fmt.Errorf("services: engine %q: pinned version %q is older than the oldest supported one (%s)", id, p, rel.Min)
			}
			seen[p] = true
		}
		r.order = append(r.order, id)
		r.engines[id] = e
		r.releases[id] = rel
		r.pinned[id] = slices.Clone(pinned[id])
	}
	return r, nil
}

// checkEngine validates what an engine declares.
func checkEngine(e Engine) error {
	id := e.ID()
	if !engineIDRe.MatchString(id) {
		return fmt.Errorf("services: invalid engine id %q", id)
	}
	if err := checkReleases(e); err != nil {
		return fmt.Errorf("services: engine %q: %w", id, err)
	}
	var opts Options
	switch e.Lifecycle() {
	case OnDemand:
		if e.Provisioner() == nil {
			return fmt.Errorf("services: engine %q has no provisioner", id)
		}
	case Dedicated:
		d, ok := e.(DedicatedEngine)
		if !ok {
			return fmt.Errorf("services: dedicated engine %q does not implement DedicatedEngine", id)
		}
		if err := checkOptions(d.Options()); err != nil {
			return fmt.Errorf("services: engine %q: %w", id, err)
		}
		opts = DefaultOptions(d.Options())
	default:
		return fmt.Errorf("services: engine %q: unknown lifecycle", id)
	}
	seen := map[string]bool{}
	for _, v := range e.Catalog() {
		if !versionRe.MatchString(v.Version) || v.Image == "" || !filepath.IsAbs(v.DataMount) || seen[v.Version] {
			return fmt.Errorf("services: engine %q: invalid catalog entry %q", id, v.Version)
		}
		seen[v.Version] = true
		// The built-in entry must be what the engine computes for its tag:
		// discovered versions go through VersionOf only.
		tag, ok := lineOfImage(e.Releases(), v.Version, v.Image)
		if !ok || !sameVersion(e.VersionOf(v.Version, tag), v) {
			return fmt.Errorf("services: engine %q: catalog entry %q differs from VersionOf", id, v.Version)
		}
		ref := InstanceRef{Engine: id, Version: v.Version}
		if e.Lifecycle() == Dedicated {
			ref.Name = "check"
		}
		spec := e.Service(ref, v, opts)
		if spec.State != (e.Lifecycle() == Dedicated) {
			return fmt.Errorf("services: engine %q: the state directory is for dedicated engines, and required by them", id)
		}
		if spec.MemLimit < 0 || spec.CPUs < 0 || (spec.User != "" && !userRe.MatchString(spec.User)) || (spec.Hardened && spec.User == "") {
			return fmt.Errorf("services: engine %q %s: invalid limits or user", id, v.Version)
		}
		for _, t := range spec.Tmpfs {
			p, _, _ := strings.Cut(t, ":")
			if !filepath.IsAbs(p) || p == v.DataMount || p == TLSMount || p == AdminSecretMount || p == StateMount ||
				strings.HasPrefix(v.DataMount, p+"/") || strings.HasPrefix(StateMount, p+"/") || strings.HasPrefix(TLSMount, p+"/") {
				return fmt.Errorf("services: engine %q %s: invalid tmpfs %q", id, v.Version, p)
			}
		}
		base := map[string]bool{}
		for _, k := range spec.GeneratedKeys {
			if !spec.State || !validGeneratedName(k) {
				return fmt.Errorf("services: engine %q %s: invalid generated key %q", id, v.Version, k)
			}
		}
		for _, f := range spec.Files {
			if !validGeneratedName(f.Name) || !filepath.IsAbs(f.Target) || f.Target == v.DataMount || strings.HasPrefix(f.Target, StateMount+"/") {
				return fmt.Errorf("services: engine %q %s: invalid generated file %q", id, v.Version, f.Name)
			}
			base[f.Name] = true
		}
		for _, optional := range []bool{false, true} {
			for _, f := range tlsSpec(e, ref, v, optional).Files {
				if !validGeneratedName(f.Name) || base[f.Name] || !filepath.IsAbs(f.Target) || f.Target == v.DataMount || f.Target == TLSMount {
					return fmt.Errorf("services: engine %q %s: invalid TLS file %q", id, v.Version, f.Name)
				}
			}
		}
	}
	if len(seen) == 0 {
		return fmt.Errorf("services: engine %q: empty built-in catalog", id)
	}
	return nil
}

// sameVersion compares the fields an engine computes.
func sameVersion(a, b Version) bool {
	return a.Version == b.Version && a.Image == b.Image && a.Platform == b.Platform &&
		a.DataMount == b.DataMount && a.MaxNameLength == b.MaxNameLength
}

// validGeneratedName: a single segment that does not shadow a panel file.
func validGeneratedName(name string) bool {
	switch name {
	case "", ".", "..", ComposeFile, OverrideFile, SecretsDir, CertsDir, StateDir, composekitMarker, exposureFailedMarker:
		return false
	}
	return filepath.Base(name) == name && !strings.HasPrefix(name, ".")
}

// Engines returns the enabled engines in display order.
func (r *Registry) Engines() []Engine {
	out := make([]Engine, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.engines[id])
	}
	return out
}

// Engine returns an enabled engine.
func (r *Registry) Engine(id string) (Engine, bool) {
	e, ok := r.engines[id]
	return e, ok
}

// Resolve validates an engine and a version offered for creation.
func (r *Registry) Resolve(id, version string) (Engine, Version, *Error) {
	e, ok := r.Engine(id)
	if !ok {
		return nil, Version{}, newError(CodeInvalidEngine, "engine", "unknown or disabled engine")
	}
	for _, v := range r.Versions(id) {
		if v.Version == version {
			return e, v, nil
		}
	}
	return nil, Version{}, newError(CodeInvalidVersion, "version", "unknown, unsupported or unavailable version")
}

// ByKey finds the enabled engine and version of a shared instance key
// ("postgres-16", "mariadb-11-4"). Any version the engine can run is found,
// offered for creation or not: existing instances stay managed.
func (r *Registry) ByKey(key string) (Engine, Version, bool) {
	for _, id := range r.order {
		if r.engines[id].Lifecycle() != OnDemand {
			continue
		}
		slug, ok := strings.CutPrefix(key, id+"-")
		if !ok {
			continue
		}
		version := strings.ReplaceAll(slug, "-", ".")
		if Slug(version) != slug {
			continue
		}
		if v, ok := r.Version(id, version); ok {
			return r.engines[id], v, true
		}
	}
	return nil, Version{}, false
}

// DedicatedByKey finds the enabled dedicated engine and the name of a
// dedicated instance key ("redis-cache"). The version is read from the
// instance manifest.
func (r *Registry) DedicatedByKey(key string) (Engine, string, bool) {
	id, name, ok := strings.Cut(key, "-")
	if !ok {
		return nil, "", false
	}
	e, ok := r.engines[id]
	if !ok || e.Lifecycle() != Dedicated {
		return nil, "", false
	}
	if validateDedicatedName(e, name) != nil {
		return nil, "", false
	}
	return e, name, true
}

// KnownKey reports whether key may name an instance of an enabled engine
// (shared version, or valid dedicated name).
func (r *Registry) KnownKey(key string) bool {
	if _, _, ok := r.ByKey(key); ok {
		return true
	}
	_, _, ok := r.DedicatedByKey(key)
	return ok
}

// validateDedicatedName applies the shared and the engine name rules.
func validateDedicatedName(e Engine, name string) *Error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if !dedicatedNameRe.MatchString(name) {
		// The name becomes a host name (container, DNS alias, certificate):
		// no underscore.
		return newError(CodeInvalidName, "name", "name must start with a lowercase letter and contain only a-z and 0-9 (max %d characters)", MaxDedicatedNameLength)
	}
	return e.(DedicatedEngine).ValidateName(name)
}

// MaxDedicatedNameLength bounds dedicated instance names.
const MaxDedicatedNameLength = 24

var dedicatedNameRe = regexp.MustCompile(`\A[a-z][a-z0-9]{0,23}\z`)

var userRe = regexp.MustCompile(`\A[0-9]{1,6}:[0-9]{1,6}\z`)

// checkOptions validates what a dedicated engine offers.
func checkOptions(options []Option) error {
	seen := map[string]bool{}
	for _, o := range options {
		if !optionTokenRe.MatchString(o.Key) || seen[o.Key] || len(o.Values) == 0 || !slices.Contains(o.Values, o.Default) {
			return fmt.Errorf("invalid option %q", o.Key)
		}
		seen[o.Key] = true
		for _, v := range o.Values {
			if !optionTokenRe.MatchString(v) {
				return fmt.Errorf("option %q: invalid value %q", o.Key, v)
			}
		}
		if c := o.Custom; c != nil && (c.Min <= 0 || c.Step <= 0 || c.Max < c.Min || c.Max > 1<<29 || c.Min%c.Step != 0) {
			return fmt.Errorf("option %q: invalid custom range", o.Key)
		}
	}
	return nil
}

var optionTokenRe = regexp.MustCompile(`\A[a-z0-9][a-z0-9-]{0,31}\z`)

// DefaultOptions returns the default value of every option.
func DefaultOptions(options []Option) Options {
	out := Options{}
	for _, o := range options {
		out[o.Key] = o.Default
	}
	return out
}

// ResolveOptions completes chosen options with the defaults and refuses
// unknown keys and values.
func ResolveOptions(options []Option, chosen map[string]string) (Options, *Error) {
	out := DefaultOptions(options)
	for k, v := range chosen {
		i := slices.IndexFunc(options, func(o Option) bool { return o.Key == k })
		if i < 0 {
			return nil, newError(CodeInvalidOption, "options", "unknown option %q", k)
		}
		if !options[i].Accepts(v) {
			return nil, newError(CodeInvalidOption, "options", "invalid value for option %q", k)
		}
		out[k] = v
	}
	return out, nil
}

// validOptions reports whether opts holds exactly one valid value per
// option (options read back from a manifest).
func validOptions(options []Option, opts Options) bool {
	if len(opts) != len(options) {
		return false
	}
	for _, o := range options {
		if !o.Accepts(opts[o.Key]) {
			return false
		}
	}
	return true
}
