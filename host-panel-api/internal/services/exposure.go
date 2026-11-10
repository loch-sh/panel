package services

import (
	"fmt"
	"net/netip"
	"strings"
)

// Exposure publishes the port of an instance on the host. It is chosen in
// the panel within the ExposurePolicy of the API configuration, and recorded
// in the instance reference.
type Exposure struct {
	Address netip.Addr
	Port    int
	// TLSOptional: TLS is offered but not required on the instance
	// (engines implementing OptionalTLSCapable).
	TLSOptional bool
}

// HostIP is the address as Docker expects it.
func (e Exposure) HostIP() string { return e.Address.String() }

// Public reports whether the port is reachable from other machines.
func (e Exposure) Public() bool { return !e.Address.IsLoopback() }

// String renders the exposure as configured ("127.0.0.1:5417", "[::1]:5417").
func (e Exposure) String() string {
	return netip.AddrPortFrom(e.Address, uint16(e.Port)).String()
}

// ExposurePolicy bounds the exposures chosen in the panel. It comes from
// the API configuration only: a stolen session or API key can publish an
// instance only where the server administrator allows it.
type ExposurePolicy struct {
	// AllowedCIDRs are the host addresses an instance may be published on
	// (SERVICES_EXPOSE_ALLOWED_CIDRS). They are bind addresses, not client
	// sources: Docker cannot filter the clients of a published port.
	AllowedCIDRs []netip.Prefix
	// AllowTLSOptional lets a public exposure offer TLS without requiring
	// it (SERVICES_ALLOW_TLS_OPTIONAL).
	AllowTLSOptional bool
	ReservedPorts    []int // API, SFTP...
}

const (
	minExposedPort = 1024
	maxExposedPort = 32767 // below the Linux ephemeral range
)

// alwaysReservedPorts are never published by an instance.
var alwaysReservedPorts = []int{2375, 2376, 2538, 8080}

// DefaultAllowedCIDRs is used when SERVICES_EXPOSE_ALLOWED_CIDRS is empty.
const DefaultAllowedCIDRs = "127.0.0.1/32"

// ParseAllowedCIDRs parses SERVICES_EXPOSE_ALLOWED_CIDRS: comma-separated
// CIDRs (or single addresses). "All interfaces" is never implied (a
// range containing the unspecified address: "0.0.0.0" or "::" must be listed
// alone), and any non-loopback entry needs SERVICES_ALLOW_PUBLIC_BIND
// (Docker bypasses the host firewall).
func ParseAllowedCIDRs(spec string, allowPublic bool) ([]netip.Prefix, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		spec = DefaultAllowedCIDRs
	}
	var out []netip.Prefix
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		var p netip.Prefix
		if strings.Contains(entry, "/") {
			pp, err := netip.ParsePrefix(entry)
			if err != nil {
				return nil, fmt.Errorf("SERVICES_EXPOSE_ALLOWED_CIDRS: %q is not a CIDR", entry)
			}
			p = pp
		} else {
			a, err := netip.ParseAddr(entry)
			if err != nil {
				return nil, fmt.Errorf("SERVICES_EXPOSE_ALLOWED_CIDRS: %q is not an address or a CIDR", entry)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		if p.Masked() != p {
			return nil, fmt.Errorf("SERVICES_EXPOSE_ALLOWED_CIDRS: %q must start at its network address (%s)", entry, p.Masked())
		}
		if _, err := parseAddress(p.Addr().String()); err != nil {
			return nil, fmt.Errorf("SERVICES_EXPOSE_ALLOWED_CIDRS: %w", err)
		}
		// Every interface is allowed only when listed alone ("0.0.0.0" or
		// "::"), never through a range that happens to contain it.
		if (p.Contains(netip.IPv4Unspecified()) || p.Contains(netip.IPv6Unspecified())) && !p.IsSingleIP() {
			return nil, fmt.Errorf("SERVICES_EXPOSE_ALLOWED_CIDRS: %q includes the unspecified address: list the host addresses, or 0.0.0.0 alone for every interface", entry)
		}
		if !allLoopback(p) && !allowPublic {
			return nil, fmt.Errorf("SERVICES_EXPOSE_ALLOWED_CIDRS: %q is not a loopback range: set SERVICES_ALLOW_PUBLIC_BIND=true to allow it (Docker bypasses the host firewall)", entry)
		}
		out = append(out, p)
	}
	return out, nil
}

// allLoopback reports whether every address of p is a loopback address.
func allLoopback(p netip.Prefix) bool {
	if p.Addr().Is4() {
		return p.Bits() >= 8 && netip.MustParsePrefix("127.0.0.0/8").Contains(p.Addr())
	}
	return p.Bits() == 128 && p.Addr().IsLoopback()
}

// Allowed reports whether an address may be published on (the unspecified
// address only when listed itself: ranges containing it are refused).
func (p ExposurePolicy) Allowed(a netip.Addr) bool {
	for _, c := range p.AllowedCIDRs {
		if c.Contains(a) {
			return true
		}
	}
	return false
}

// ParseExposure parses an exposure entered by a client.
func ParseExposure(address string, port int) (Exposure, *Error) {
	a, err := parseAddress(address)
	if err != nil {
		return Exposure{}, newError(CodeInvalidExposure, "address", "%s", err.Error())
	}
	if port < minExposedPort || port > maxExposedPort {
		return Exposure{}, newError(CodeInvalidExposure, "port", "the port must be between %d and %d", minExposedPort, maxExposedPort)
	}
	return Exposure{Address: a, Port: port}, nil
}

// parseAddress accepts a literal unicast (or unspecified) IP address.
func parseAddress(s string) (netip.Addr, error) {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%q is not an IP address", s)
	}
	switch {
	case a.Zone() != "":
		return netip.Addr{}, fmt.Errorf("%q: zones are not accepted", s)
	case a.Is4In6():
		return netip.Addr{}, fmt.Errorf("%q: write the IPv4 address itself", s)
	case a.IsMulticast() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsInterfaceLocalMulticast():
		return netip.Addr{}, fmt.Errorf("%q is not a unicast address", s)
	}
	return a, nil
}

// addressesOverlap reports whether binding both addresses on a port would
// conflict (an unspecified address covers its whole family, and "::" may
// also cover IPv4).
func addressesOverlap(a, b netip.Addr) bool {
	if a == b || a.IsUnspecified() && b.IsUnspecified() {
		return true
	}
	if a.IsUnspecified() {
		return a.Is6() || b.Is4()
	}
	if b.IsUnspecified() {
		return b.Is6() || a.Is4()
	}
	return false
}
