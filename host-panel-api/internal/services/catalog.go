package services

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"host-panel-api/internal/composekit"
	"host-panel-api/internal/releases"
)

// Version catalog: the versions of every engine are discovered on the
// registries (tags of the origin repository, image checked on the mirror it
// is pulled from) with their end of support (endoflife.date). Creation offers
// the latest supported version of the OfferedMajors latest majors, plus the
// versions pinned in the configuration; updates of a dedicated instance offer
// at most UpdateTargets newer versions. Until a discovery succeeded, the
// built-in catalog of the engine (Engine.Catalog) is used.

const (
	// OfferedMajors is the number of majors offered at creation.
	OfferedMajors = 3
	// UpdateTargets is the number of newer versions offered to an instance.
	UpdateTargets = 3
	// maxDigestChecks bounds the images checked on the mirror per engine
	// and discovery.
	maxDigestChecks = 24
	// catalogFile keeps the last discovery in the panel data directory.
	catalogFile = "services-catalog.json"
	// catalogFormat is the version of the catalog file.
	catalogFormat = 1
)

// Releases describes where the versions of an engine are published.
type Releases struct {
	// Source is the repository whose tags are listed, on the origin registry
	// (the mirror cannot list tags): "docker.io/library/redis",
	// "ghcr.io/loch-sh/s3".
	Source string
	// Tag matches the tags of a version: group 1 is the version ("8.4"); an
	// optional group 2 is a patch number, the version then runs its highest
	// patch tag ("2.0.22").
	Tag *regexp.Regexp
	// LineTag formats the floating tag of a version ("%s-alpine"); empty
	// when images use exact patch tags.
	LineTag string
	// EOL is the endoflife.date product ("" when it is not listed: a
	// published version is then considered supported).
	EOL string
	// Min is the oldest version the engine code supports.
	Min string
}

// ReleaseSource reads the registries and endoflife.date (releases.Client).
type ReleaseSource interface {
	Tags(ctx context.Context, source string) ([]string, error)
	Digest(ctx context.Context, image string) (string, error)
	Support(ctx context.Context, product string) (map[string]releases.Support, error)
}

// checkReleases validates what an engine declares about its releases.
func checkReleases(e Engine) error {
	rel := e.Releases()
	if rel.Source == "" || rel.Tag == nil || rel.Tag.NumSubexp() < 1 || rel.Tag.NumSubexp() > 2 || !versionRe.MatchString(rel.Min) {
		return errors.New("invalid releases")
	}
	if (rel.LineTag == "") != (rel.Tag.NumSubexp() == 2) {
		return errors.New("releases: a line tag format, or a patch group, not both")
	}
	if rel.LineTag != "" && strings.Count(rel.LineTag, "%s") != 1 {
		return errors.New("releases: invalid line tag format")
	}
	return nil
}

// CompareVersions compares "x" or "x.y" versions numerically ("8.10" >
// "8.4"). Invalid versions sort first.
func CompareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := range 2 {
		if c := cmp.Compare(pa[i], pb[i]); c != 0 {
			return c
		}
	}
	return 0
}

func versionParts(v string) [2]int {
	if !versionRe.MatchString(v) {
		return [2]int{-1, -1}
	}
	major, minor, _ := strings.Cut(v, ".")
	a, _ := strconv.Atoi(major)
	b, _ := strconv.Atoi(minor)
	return [2]int{a, b}
}

// Major is the major of a version (its first number).
func Major(v string) int { return versionParts(v)[0] }

// parseTag returns the version of a tag and its patch (-1 for line tags).
func parseTag(rel Releases, tag string) (version string, patch int, ok bool) {
	m := rel.Tag.FindStringSubmatch(tag)
	if m == nil || m[0] != tag || !versionRe.MatchString(m[1]) {
		return "", 0, false
	}
	if len(m) == 2 {
		return m[1], -1, true
	}
	p, err := strconv.Atoi(m[2])
	if err != nil || p < 0 || p > 99999 || strconv.Itoa(p) != m[2] {
		return "", 0, false
	}
	return m[1], p, true
}

// lineOfImage returns the tag of an image if it is a tag of version.
func lineOfImage(rel Releases, version, image string) (string, bool) {
	i := strings.LastIndex(image, ":")
	if i < 0 || strings.Contains(image[i:], "/") {
		return "", false
	}
	tag := image[i+1:]
	v, _, ok := parseTag(rel, tag)
	return tag, ok && v == version
}

// --- discovered state ---

type catalogState struct {
	Format    int                       `json:"format"`
	CheckedAt time.Time                 `json:"checkedAt"`
	Engines   map[string]*engineCatalog `json:"engines"`
}

type engineCatalog struct {
	CheckedAt time.Time `json:"checkedAt"`
	// Failed: the last discovery of this engine failed (the previous lines
	// are kept).
	Failed bool                 `json:"failed,omitempty"`
	Lines  map[string]lineState `json:"lines"`
}

type lineState struct {
	Tag      string `json:"tag"`
	Active   string `json:"activeSupportUntil,omitempty"`
	Security string `json:"supportUntil,omitempty"`
	// Digest of the image on the registry it is pulled from; "" when not
	// checked. Missing: checked, the mirror does not have it.
	Digest  string `json:"digest,omitempty"`
	Missing bool   `json:"missing,omitempty"`
}

func (l lineState) available() bool { return l.Digest != "" && !l.Missing }

// supported: security support not over (unknown counts as supported).
func (l lineState) supported(today string) bool { return l.Security == "" || l.Security >= today }

func (r *Registry) today() string { return r.now().UTC().Format(time.DateOnly) }

// withSupport sets the support dates of a version from the discovery when
// the version was discovered (known); otherwise the dates of v (built-in
// catalog, or none) are kept.
func (r *Registry) withSupport(v Version, l lineState, known bool) Version {
	if known {
		v.SupportUntil, v.ActiveSupportUntil = l.Security, l.Active
	}
	v.Deprecated = v.deprecatedAt(r.now())
	return v
}

// line returns the discovered state of a version.
func (r *Registry) line(id, version string) (lineState, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ec := r.state.Engines[id]
	if ec == nil {
		return lineState{}, false
	}
	l, ok := ec.Lines[version]
	return l, ok
}

// discovered reports whether a discovery of the engine succeeded once.
func (r *Registry) discovered(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ec := r.state.Engines[id]
	return ec != nil && len(ec.Lines) > 0
}

// Version returns a version an instance may run: any version the engine
// supports, offered for creation or not (existing instances stay managed).
// The image is the one the version runs today (its line tag, or its latest
// patch tag).
func (r *Registry) Version(id, version string) (Version, bool) {
	e, ok := r.engines[id]
	if !ok || !versionRe.MatchString(version) {
		return Version{}, false
	}
	rel := r.releases[id]
	if CompareVersions(version, rel.Min) < 0 {
		return Version{}, false
	}
	l, known := r.line(id, version)
	if known && !l.Missing {
		return r.withSupport(e.VersionOf(version, l.Tag), l, true), true
	}
	if v, ok := r.fallbackVersion(id, version); ok {
		return v, true
	}
	if rel.LineTag != "" {
		return r.withSupport(e.VersionOf(version, fmt.Sprintf(rel.LineTag, version)), l, known), true
	}
	return Version{}, false
}

// VersionImage returns a version running a given image (recorded for a
// dedicated instance): the image must be one the engine computes for a tag of
// that version.
func (r *Registry) VersionImage(id, version, image string) (Version, bool) {
	e, ok := r.engines[id]
	if !ok || !versionRe.MatchString(version) {
		return Version{}, false
	}
	tag, ok := lineOfImage(r.releases[id], version, image)
	if !ok || CompareVersions(version, r.releases[id].Min) < 0 {
		return Version{}, false
	}
	v := e.VersionOf(version, tag)
	if v.Image != image {
		return Version{}, false
	}
	l, known := r.line(id, version)
	return r.withSupport(v, l, known), true
}

// fallbackVersion returns a version of the built-in catalog.
func (r *Registry) fallbackVersion(id, version string) (Version, bool) {
	e, ok := r.engines[id]
	if !ok {
		return Version{}, false
	}
	for _, v := range e.Catalog() {
		if v.Version == version {
			// Discovered dates when known; the image stays the built-in one.
			l, known := r.line(id, version)
			return r.withSupport(v, l, known), true
		}
	}
	return Version{}, false
}

// Versions returns the versions offered for creation, newest first: the
// latest supported and available version of the OfferedMajors latest
// majors, plus the pinned versions (even out of support).
func (r *Registry) Versions(id string) []Version {
	e, ok := r.engines[id]
	if !ok {
		return nil
	}
	var out []Version
	if r.discovered(id) {
		for _, version := range r.pickMajors(id) {
			if v, ok := r.Version(id, version); ok {
				out = append(out, v)
			}
		}
	}
	if len(out) == 0 {
		for _, v := range e.Catalog() {
			if fv, ok := r.fallbackVersion(id, v.Version); ok {
				out = append(out, fv)
			}
		}
	}
	for _, p := range r.pinned[id] {
		if i := slices.IndexFunc(out, func(v Version) bool { return v.Version == p }); i >= 0 {
			out[i].Pinned = true
			continue
		}
		if l, known := r.line(id, p); (known && l.Missing) || (!known && r.discovered(id)) {
			continue // not published, or not on the mirror: it could not be pulled
		}
		if v, ok := r.Version(id, p); ok {
			v.Pinned = true
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b Version) int { return CompareVersions(b.Version, a.Version) })
	return out
}

// eligible returns the discovered versions that may be offered, newest
// first: supported by the engine code and by upstream.
func (r *Registry) eligible(id string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ec := r.state.Engines[id]
	if ec == nil {
		return nil
	}
	return eligibleLines(ec.Lines, r.releases[id].Min, r.today())
}

// eligibleLines returns the versions supported by the engine code (min) and
// by upstream (today), newest first.
func eligibleLines(lines map[string]lineState, minVersion, today string) []string {
	var out []string
	for v, l := range lines {
		if CompareVersions(v, minVersion) >= 0 && l.supported(today) {
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return CompareVersions(b, a) })
	return out
}

// pickMajors returns, for the OfferedMajors latest majors, their latest
// available version.
func (r *Registry) pickMajors(id string) []string {
	var out []string
	seen := map[int]bool{}
	for _, v := range r.eligible(id) {
		if len(out) == OfferedMajors {
			break
		}
		if seen[Major(v)] {
			continue
		}
		if l, _ := r.line(id, v); l.available() {
			seen[Major(v)] = true
			out = append(out, v)
		}
	}
	return out
}

// UpdateVersions returns the versions a dedicated instance can move to: at
// most UpdateTargets newer supported and available versions (pinned newer
// versions included), newest first.
func (r *Registry) UpdateVersions(id, current string) []Version {
	var out []Version
	for _, v := range r.eligible(id) {
		if len(out) == UpdateTargets || CompareVersions(v, current) <= 0 {
			break
		}
		if l, _ := r.line(id, v); l.available() {
			if ver, ok := r.Version(id, v); ok {
				out = append(out, ver)
			}
		}
	}
	for _, p := range r.pinned[id] {
		if CompareVersions(p, current) <= 0 || slices.ContainsFunc(out, func(v Version) bool { return v.Version == p }) {
			continue
		}
		if l, known := r.line(id, p); known && l.available() {
			if v, ok := r.Version(id, p); ok {
				v.Pinned = true
				out = append(out, v)
			}
		}
	}
	slices.SortFunc(out, func(a, b Version) int { return CompareVersions(b.Version, a.Version) })
	if len(out) > UpdateTargets {
		out = out[:UpdateTargets]
	}
	return out
}

// LatestImage returns the image a version runs today and, when it was
// checked, its digest on the mirror.
// Only a discovered version available on the mirror has one (never a
// built-in image: the instance may already run a newer one).
func (r *Registry) LatestImage(id, version string) (image, digest string, ok bool) {
	l, known := r.line(id, version)
	if !known || !l.available() {
		return "", "", false
	}
	v, ok := r.Version(id, version)
	if !ok {
		return "", "", false
	}
	return v.Image, l.Digest, true
}

// CatalogStatus describes the last discovery.
type CatalogStatus struct {
	// CheckedAt is the last discovery (nil: never, the built-in catalogs
	// are used).
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	// Failed lists the engines whose last discovery failed (entirely, or
	// for some dates or images: the previous values are kept).
	Failed []string `json:"failed,omitempty"`
	// Throttled: a check ran less than 30 seconds ago, none was started.
	Throttled bool `json:"throttled,omitempty"`
}

// Status returns the state of the discovery.
func (r *Registry) Status() CatalogStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var s CatalogStatus
	if !r.state.CheckedAt.IsZero() {
		t := r.state.CheckedAt
		s.CheckedAt = &t
	}
	for _, id := range r.order {
		if ec := r.state.Engines[id]; ec != nil && ec.Failed {
			s.Failed = append(s.Failed, id)
		}
	}
	return s
}

// --- discovery ---

// Refresh discovers the versions of every enabled engine. inUse lists, per
// engine, the versions run by instances: their images are checked too (an
// update of the same version). A failed engine keeps its previous state.
// The errors (per engine, then saving) are returned for the logs.
func (r *Registry) Refresh(ctx context.Context, src ReleaseSource, inUse map[string][]string) (CatalogStatus, []error) {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	var errs []error
	for _, id := range r.order {
		ec, err := r.discover(ctx, src, id, inUse[id])
		r.mu.Lock()
		if err != nil {
			errs = append(errs, err)
			prev := r.state.Engines[id]
			if prev == nil {
				prev = &engineCatalog{Lines: map[string]lineState{}}
			}
			failed := *prev
			failed.Failed = true
			r.state.Engines[id] = &failed
		} else {
			r.state.Engines[id] = ec
		}
		r.mu.Unlock()
	}
	r.mu.Lock()
	r.state.CheckedAt = r.now().UTC()
	r.state.Format = catalogFormat
	r.mu.Unlock()
	if err := r.save(); err != nil {
		errs = append(errs, err)
	}
	return r.Status(), errs
}

// discover reads the tags and support dates of one engine and checks the
// images that may be offered or run.
func (r *Registry) discover(ctx context.Context, src ReleaseSource, id string, inUse []string) (*engineCatalog, error) {
	e, rel := r.engines[id], r.releases[id]
	tags, err := src.Tags(ctx, rel.Source)
	if err != nil {
		return nil, fmt.Errorf("%s: tags: %w", id, err)
	}
	ec := &engineCatalog{CheckedAt: r.now().UTC(), Lines: map[string]lineState{}}
	patches := map[string]int{}
	for _, tag := range tags {
		v, patch, ok := parseTag(rel, tag)
		if !ok {
			continue
		}
		if p, seen := patches[v]; seen && p >= patch {
			continue
		}
		patches[v] = patch
		ec.Lines[v] = lineState{Tag: tag}
	}
	if len(ec.Lines) == 0 {
		return nil, fmt.Errorf("%s: no tag matches", id)
	}
	r.mu.RLock()
	prev := r.state.Engines[id]
	r.mu.RUnlock()
	if rel.EOL != "" {
		support, serr := src.Support(ctx, rel.EOL)
		if serr != nil {
			// Keep the previous dates rather than offering everything; with
			// none, the discovery fails (the built-in catalog stays).
			if prev == nil || len(prev.Lines) == 0 {
				return nil, fmt.Errorf("%s: support dates: %w", id, serr)
			}
			ec.Failed = true
			support = map[string]releases.Support{}
			for v, l := range prev.Lines {
				support[v] = releases.Support{Active: l.Active, Security: l.Security}
			}
		}
		for v, l := range ec.Lines {
			if s, ok := support[v]; ok {
				l.Active, l.Security = s.Active, s.Security
				ec.Lines[v] = l
			}
		}
	}

	checks := 0
	check := func(v string) bool {
		l, ok := ec.Lines[v]
		if !ok {
			return false
		}
		if l.Digest != "" || l.Missing {
			return l.available()
		}
		if checks >= maxDigestChecks {
			return false
		}
		checks++
		d, derr := src.Digest(ctx, e.VersionOf(v, l.Tag).Image)
		switch {
		case derr == nil && releases.ValidDigest(d):
			l.Digest = d
		case errors.Is(derr, releases.ErrNotFound):
			l.Missing = true
		default:
			// Transient (rate limit, 5xx): keep what the previous discovery
			// knew of the same tag, and report the engine.
			ec.Failed = true
			if prev != nil {
				if p, ok := prev.Lines[v]; ok && p.Tag == l.Tag {
					l.Digest, l.Missing = p.Digest, p.Missing
				}
			}
		}
		ec.Lines[v] = l
		return l.available()
	}
	eligible := eligibleLines(ec.Lines, rel.Min, r.today())
	// The latest available version of each of the latest majors.
	majors := map[int]bool{}
	for _, v := range eligible {
		if len(majors) == OfferedMajors {
			break
		}
		if !majors[Major(v)] && check(v) {
			majors[Major(v)] = true
		}
	}
	// The newest versions (update targets).
	found := 0
	for _, v := range eligible {
		if found == UpdateTargets {
			break
		}
		if check(v) {
			found++
		}
	}
	for _, v := range append(slices.Clone(r.pinned[id]), inUse...) {
		check(v)
	}
	return ec, nil
}

// --- persistence ---

// SetStore keeps the discovered catalog in dir (the panel data directory)
// and loads the previous one. An unreadable or invalid file is ignored (the
// next discovery rewrites it).
func (r *Registry) SetStore(dir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store = dir
	data, err := composekit.ReadFileNoFollow(filepath.Join(dir, catalogFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var st catalogState
	if err := json.Unmarshal(data, &st); err != nil || st.Format != catalogFormat {
		return fmt.Errorf("services: ignoring %s: invalid content", catalogFile)
	}
	r.state = r.sanitize(st)
	return nil
}

// sanitize keeps the valid entries of a loaded catalog (enabled engines,
// tags of the version, valid dates and digests).
func (r *Registry) sanitize(st catalogState) catalogState {
	out := catalogState{Format: catalogFormat, CheckedAt: st.CheckedAt, Engines: map[string]*engineCatalog{}}
	for id, ec := range st.Engines {
		rel, ok := r.releases[id]
		if !ok || ec == nil {
			continue
		}
		clean := &engineCatalog{CheckedAt: ec.CheckedAt, Failed: ec.Failed, Lines: map[string]lineState{}}
		for v, l := range ec.Lines {
			tv, _, ok := parseTag(rel, l.Tag)
			if !ok || tv != v || !validDate(l.Active) || !validDate(l.Security) ||
				(l.Digest != "" && !releases.ValidDigest(l.Digest)) {
				continue
			}
			clean.Lines[v] = l
		}
		out.Engines[id] = clean
	}
	return out
}

var dateOnlyRe = regexp.MustCompile(`\A[0-9]{4}-[0-9]{2}-[0-9]{2}\z`)

func validDate(d string) bool { return d == "" || dateOnlyRe.MatchString(d) }

// save writes the catalog (a cache: a failure is only logged).
func (r *Registry) save() error {
	r.mu.RLock()
	dir := r.store
	data, err := json.Marshal(r.state)
	r.mu.RUnlock()
	if dir == "" || err != nil {
		return err
	}
	return composekit.WriteFileAtomic(dir, catalogFile, data, 0o640)
}
