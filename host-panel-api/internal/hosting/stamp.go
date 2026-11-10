package hosting

import (
	"bytes"
	"strings"
	"time"
	"unicode"
)

// deployStampPrefix starts the comment line recording who last deployed a
// stack and when. It is ignored when detecting manual edits.
const deployStampPrefix = "# Last deployed by "

// stampCompose returns data with a single deployment comment, placed right
// after the leading comment block of the file:
//
//	# Last deployed by admin@example.com at 2026-11-04T10:12:00Z (update)
func stampCompose(data []byte, actor, kind string, at time.Time) []byte {
	lines := strings.SplitAfter(string(stripStamp(data)), "\n")
	i := 0
	for i < len(lines) && strings.HasPrefix(lines[i], "#") {
		i++
	}
	stamp := deployStampPrefix + sanitizeActor(actor) + " at " + at.UTC().Format(time.RFC3339) + " (" + kind + ")\n"
	out := append(append(append([]string{}, lines[:i]...), stamp), lines[i:]...)
	return []byte(strings.Join(out, ""))
}

// stripStamp removes the deployment comment lines.
func stripStamp(data []byte) []byte {
	if !bytes.Contains(data, []byte(deployStampPrefix)) {
		return data
	}
	var out []string
	for _, line := range strings.SplitAfter(string(data), "\n") {
		if !strings.HasPrefix(line, deployStampPrefix) {
			out = append(out, line)
		}
	}
	return []byte(strings.Join(out, ""))
}

const maxActorRunes = 128

// sanitizeActor keeps the actor on one printable line: control and format
// characters (bidi overrides) and Unicode line/paragraph separators, which
// YAML treats as line breaks, are dropped.
func sanitizeActor(actor string) string {
	actor = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return -1
		}
		return r
	}, strings.TrimSpace(actor))
	if runes := []rune(actor); len(runes) > maxActorRunes {
		actor = string(runes[:maxActorRunes])
	}
	if actor == "" {
		return "unknown"
	}
	return actor
}
