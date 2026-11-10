// Package env reads and lints the environment variables of the API.
//
// Every variable is read through a Reader, which records every problem
// instead of stopping at the first one: the API refuses to start and lists
// them all at once. The Reader also knows every variable it read, so that
// an unknown variable with a known prefix (a typo, or a variable removed in
// a later version) is reported instead of being silently ignored.
//
// Values are never written in the problems: a variable may hold a secret.
package env

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Problem is an invalid, missing, unknown or removed variable.
type Problem struct {
	Var     string
	Message string
}

func (p Problem) String() string { return p.Var + ": " + p.Message }

// Problems is the error returned by Reader.Err.
type Problems []Problem

func (ps Problems) Error() string {
	lines := make([]string, len(ps))
	for i, p := range ps {
		lines[i] = "  - " + p.String()
	}
	return fmt.Sprintf("%d invalid environment variable(s):\n%s", len(ps), strings.Join(lines, "\n"))
}

// AsProblems returns the problems of an error returned by Reader.Err.
func AsProblems(err error) (Problems, bool) {
	var ps Problems
	ok := errors.As(err, &ps)
	return ps, ok
}

// Reader reads variables and records their problems.
type Reader struct {
	lookup   func(string) (string, bool)
	known    map[string]bool
	removed  map[string]string
	problems []Problem
}

// New returns a Reader over lookup (os.LookupEnv when nil).
func New(lookup func(string) (string, bool)) *Reader {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	return &Reader{lookup: lookup, known: map[string]bool{}, removed: map[string]string{}}
}

// raw returns the trimmed value of a variable; an empty value is unset.
func (r *Reader) raw(name string) (string, bool) {
	r.known[name] = true
	v, ok := r.lookup(name)
	v = strings.TrimSpace(v)
	return v, ok && v != ""
}

// IsSet reports whether a variable has a non-empty value.
func (r *Reader) IsSet(name string) bool {
	_, ok := r.raw(name)
	return ok
}

// Fail records a problem on a variable.
func (r *Reader) Fail(name, format string, args ...any) {
	r.problems = append(r.problems, Problem{Var: name, Message: fmt.Sprintf(format, args...)})
}

// String returns a variable, or def when unset.
func (r *Reader) String(name, def string) string {
	if v, ok := r.raw(name); ok {
		return v
	}
	return def
}

// Required returns a variable that must be set.
func (r *Reader) Required(name string) string {
	v, ok := r.raw(name)
	if !ok {
		r.Fail(name, "required")
	}
	return v
}

// Secret returns a required secret of at least minLen characters.
func (r *Reader) Secret(name string, minLen int) string {
	v := r.Required(name)
	if v != "" && len(v) < minLen {
		r.Fail(name, "must be at least %d characters", minLen)
	}
	return v
}

// Bool returns a boolean (true/false, 1/0, yes/no, on/off).
func (r *Reader) Bool(name string, def bool) bool {
	v, ok := r.raw(name)
	if !ok {
		return def
	}
	switch strings.ToLower(v) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	}
	r.Fail(name, "must be true or false")
	return def
}

// Int returns an integer within [min, max].
func (r *Reader) Int(name string, def, min, max int) int {
	v, ok := r.raw(name)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		r.Fail(name, "must be an integer between %d and %d", min, max)
		return def
	}
	return n
}

// Float returns a number within [min, max].
func (r *Reader) Float(name string, def, min, max float64) float64 {
	v, ok := r.raw(name)
	if !ok {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < min || f > max {
		r.Fail(name, "must be a number between %g and %g", min, max)
		return def
	}
	return f
}

// Enum returns one of the allowed values.
func (r *Reader) Enum(name, def string, allowed ...string) string {
	v, ok := r.raw(name)
	if !ok {
		return def
	}
	if !slices.Contains(allowed, v) {
		r.Fail(name, "must be one of %s", strings.Join(allowed, ", "))
		return def
	}
	return v
}

// List returns a comma-separated list (trimmed, empty items dropped).
func (r *Reader) List(name, def string) []string {
	v, ok := r.raw(name)
	if !ok {
		v = def
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// AbsPath returns an absolute, clean path (a trailing slash is dropped).
func (r *Reader) AbsPath(name, def string) string {
	v := r.String(name, def)
	if v == "" {
		return ""
	}
	if trimmed := strings.TrimRight(v, "/"); trimmed != "" {
		v = trimmed
	}
	if !filepath.IsAbs(v) || filepath.Clean(v) != v {
		r.Fail(name, "must be an absolute, clean path")
	}
	return v
}

// Prefixes returns a list of CIDRs (a single address is a /32 or /128).
func (r *Reader) Prefixes(name, def string) []netip.Prefix {
	var out []netip.Prefix
	for _, item := range r.List(name, def) {
		var p netip.Prefix
		var err error
		if strings.Contains(item, "/") {
			p, err = netip.ParsePrefix(item)
		} else {
			var a netip.Addr
			if a, err = netip.ParseAddr(item); err == nil {
				p = netip.PrefixFrom(a, a.BitLen())
			}
		}
		if err != nil {
			r.Fail(name, "%q is not an IP address or a CIDR", item)
			continue
		}
		out = append(out, p.Masked())
	}
	return out
}

// Removed records variables that no longer exist: setting one is an error
// (its setting would otherwise be silently ignored), hint tells what
// replaces it.
func (r *Reader) Removed(hint string, names ...string) {
	for _, n := range names {
		r.removed[n] = hint
	}
}

// Declare marks variables as known without reading them (read elsewhere,
// e.g. by a library).
func (r *Reader) Declare(names ...string) {
	for _, n := range names {
		r.known[n] = true
	}
}

// CheckUnknown reports the variables of environ (KEY=value) that start with
// one of prefixes but were never read: typos are caught at startup.
func (r *Reader) CheckUnknown(environ []string, prefixes ...string) {
	for _, kv := range environ {
		name, value, _ := strings.Cut(kv, "=")
		if hint, ok := r.removed[name]; ok {
			if strings.TrimSpace(value) != "" {
				r.Fail(name, "no longer supported: %s", hint)
			}
			continue
		}
		if r.known[name] || !hasAnyPrefix(name, prefixes) {
			continue
		}
		if s := r.suggest(name); s != "" {
			r.Fail(name, "unknown variable (did you mean %s?)", s)
		} else {
			r.Fail(name, "unknown variable")
		}
	}
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// suggest returns the known variable closest to name (edit distance <= 3).
func (r *Reader) suggest(name string) string {
	best, bestD := "", 4
	for k := range r.known {
		if d := distance(name, k); d < bestD || d == bestD && k < best {
			best, bestD = k, d
		}
	}
	return best
}

// distance is the Levenshtein distance between a and b.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// Err returns every problem recorded, sorted by variable (nil if none).
func (r *Reader) Err() error {
	if len(r.problems) == 0 {
		return nil
	}
	ps := slices.Clone(r.problems)
	slices.SortStableFunc(ps, func(a, b Problem) int { return strings.Compare(a.Var, b.Var) })
	return Problems(ps)
}
