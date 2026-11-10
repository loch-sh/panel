package networks

import (
	"context"
	"strings"
)

// Resolver is what the compose renderers (hosting, services) need from the
// registry: the Docker network behind a registry name, and a check that the
// live Docker network is still the registered one.
type Resolver interface {
	// Lookup returns a registered network by its registry name.
	Lookup(name string) (Ref, bool)
	// Verify reports whether the live Docker network of a registry name is
	// the registered one, with its Docker ID.
	Verify(ctx context.Context, name string) (id string, ok bool)
}

// BootComments returns the compose header lines that recreate the given
// networks on a new host, so a stack boots without the panel.
func BootComments(refs []Ref) string {
	var b strings.Builder
	for _, n := range refs {
		b.WriteString("#   " + n.CreateCommand() + "   (once, if missing)\n")
	}
	return b.String()
}
