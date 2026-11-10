package composekit

import "strings"

// EscapeInterpolation escapes "$" so Docker Compose does not interpolate a
// value written in a compose file (environment values chosen by users).
func EscapeInterpolation(v string) string {
	return strings.ReplaceAll(v, "$", "$$")
}
