package services

import (
	"regexp"
	"strings"
)

// MaxNameLength bounds resource names: 32 fits every engine limit (MySQL
// user names) and keeps derived identifiers short.
const MaxNameLength = 32

// nameRe is the only accepted form of a resource name. It is anchored with
// \z: `$` would accept a trailing newline. Lowercase only (PostgreSQL folds
// unquoted identifiers, names must not differ by case alone); no quote,
// backtick, backslash, dash, dot, space or non-ASCII character can appear, so
// a valid name is safe in every identifier syntax.
var nameRe = regexp.MustCompile(`\A[a-z][a-z0-9_]{0,31}\z`)

// reservedNames are refused for every engine (generic admin names); each
// engine refuses its own system names in Provisioner.ValidateName.
var reservedNames = map[string]bool{
	"admin": true, "all": true, "default": true, "healthcheck": true, "loch": true,
	"none": true, "root": true, "test": true, "user": true,
}

// "loch_" holds the panel registries (loch_meta on MySQL and MariaDB).
var reservedPrefixes = []string{"loch_"}

// ReservedNames builds the engine-specific part of ValidateName.
func ReservedNames(names []string, prefixes []string) func(string) *Error {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(name string) *Error {
		if set[name] {
			return newError(CodeNameReserved, "name", "this name is reserved")
		}
		for _, p := range prefixes {
			if strings.HasPrefix(name, p) {
				return newError(CodeNameReserved, "name", "names starting with %q are reserved", p)
			}
		}
		return nil
	}
}

// NameTooLong is the error of a name longer than a version accepts.
func NameTooLong(limit int) *Error {
	return newError(CodeInvalidName, "name", "names are limited to %d characters on this version", limit)
}

// ValidateName checks a resource name against the rules shared by every
// engine. Engines may add their own rules on top.
func ValidateName(name string) *Error {
	if !nameRe.MatchString(name) {
		return newError(CodeInvalidName, "name",
			"name must start with a lowercase letter and contain only a-z, 0-9 and _ (max %d characters)", MaxNameLength)
	}
	if reservedNames[name] {
		return newError(CodeNameReserved, "name", "this name is reserved")
	}
	for _, p := range reservedPrefixes {
		if strings.HasPrefix(name, p) {
			return newError(CodeNameReserved, "name", "names starting with %q are reserved", p)
		}
	}
	return nil
}

// IsValidName reports whether name passes ValidateName.
func IsValidName(name string) bool { return ValidateName(name) == nil }
