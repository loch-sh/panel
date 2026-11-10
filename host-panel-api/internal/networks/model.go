// Package networks manages the private networks of the panel: internal
// Docker bridge networks that hosted sites and service instances join to
// reach each other.
//
// Only native Docker is used: a network is a plain Docker network, created by
// the panel and referenced as "external" by the compose files of its members,
// so every stack still boots with `docker compose up -d` when the panel is
// down. The registry (DATA_DIR/networks.json) records what the panel
// created (name, Docker ID, subnet), so that a network recreated or replaced
// outside the panel is detected; it is never needed to run the stacks.
//
// A network is not an access control against whoever can run compose on the
// host (CI included): any container can join a Docker network by name. It
// separates the sites and services managed by the panel from each other.
package networks

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"time"
)

// Labels set on the Docker networks created by the panel.
const (
	LabelManaged = "sh.loch.managed"
	LabelNetwork = "sh.loch.network"
)

// DockerPrefix prefixes the Docker name of the private networks.
const DockerPrefix = "loch-net-"

// SharedName is the registry name of the network shared with the services
// module (SERVICES_NETWORK, "loch-services" by default).
const SharedName = "services"

// Member kinds.
const (
	KindSite      = "site"
	KindInstance  = "instance"
	KindFunction  = "function"
	KindPanel     = "panel"
	KindUnmanaged = "unmanaged"
)

// Network states.
const (
	StateOK          = "ok"
	StateMissing     = "missing"
	StateCompromised = "compromised"
)

// Network is a registry entry.
type Network struct {
	Name        string    `json:"name"`
	DockerName  string    `json:"dockerName"`
	ID          string    `json:"id"`
	Subnet      string    `json:"subnet"`
	Gateway     string    `json:"gateway,omitempty"`
	Description string    `json:"description,omitempty"`
	System      bool      `json:"system,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	CreatedBy   string    `json:"createdBy,omitempty"`
}

// Ref is what the compose renderers need about a network.
type Ref struct {
	Name       string
	DockerName string
	ID         string
	Subnet     string
	System     bool
}

// Ref returns the rendering reference of a network.
func (n Network) Ref() Ref {
	return Ref{Name: n.Name, DockerName: n.DockerName, ID: n.ID, Subnet: n.Subnet, System: n.System}
}

// CreateCommand is the native command that recreates the network on a new
// host (written in the compose headers: the stacks boot without the panel).
func (r Ref) CreateCommand() string {
	cmd := "docker network create --internal"
	if r.Subnet != "" {
		cmd += " --subnet " + r.Subnet
	}
	cmd += " --label " + LabelManaged + "=true"
	if !r.System {
		cmd += " --label " + LabelNetwork + "=" + r.Name
	}
	return cmd + " " + r.DockerName
}

// Problems of a network that is not the registered one (Summary.Problem):
// stable codes, translated by the clients.
const (
	ProblemReplaced         = "replaced"
	ProblemNotBridge        = "not_bridge"
	ProblemNotInternal      = "not_internal"
	ProblemOptions          = "unexpected_options"
	ProblemMissingLabel     = "missing_label"
	ProblemUnexpectedLabel  = "unexpected_label"
	ProblemComposeProject   = "compose_project"
	ProblemUnexpectedSubnet = "unexpected_subnet"
	ProblemInspectFailed    = "inspect_failed"
)

// Error codes returned to clients.
const (
	CodeNotFound        = "NETWORK_NOT_FOUND"
	CodeInvalidName     = "INVALID_NETWORK_NAME"
	CodeNameTaken       = "NETWORK_NAME_TAKEN"
	CodeInvalidSubnet   = "INVALID_SUBNET"
	CodeInUse           = "NETWORK_IN_USE"
	CodeLimit           = "NETWORK_LIMIT_REACHED"
	CodeSystem          = "NETWORK_SYSTEM"
	CodeCompromised     = "NETWORK_COMPROMISED"
	CodeMemberNotFound  = "NETWORK_MEMBER_NOT_FOUND"
	CodeMemberManaged   = "NETWORK_MEMBER_MANAGED"
	CodeInvalidKind     = "INVALID_NETWORK_MEMBER_KIND"
	CodeDockerError     = "NETWORK_DOCKER_ERROR"
	CodeStorage         = "NETWORK_STORAGE_ERROR"
	CodeAlreadyAttached = "NETWORK_ALREADY_ATTACHED"
	CodeNotAttached     = "NETWORK_NOT_ATTACHED"
	CodeBusy            = "NETWORK_OPERATION_IN_PROGRESS"
)

// AllCodes lists every client-facing code (tests check they are mapped).
var AllCodes = []string{
	CodeNotFound, CodeInvalidName, CodeNameTaken, CodeInvalidSubnet, CodeInUse, CodeLimit,
	CodeSystem, CodeCompromised, CodeMemberNotFound, CodeMemberManaged, CodeInvalidKind,
	CodeDockerError, CodeStorage, CodeAlreadyAttached, CodeNotAttached, CodeBusy,
}

// Error is a typed, client-facing error.
type Error struct {
	Code    string
	Field   string
	Message string
}

func (e *Error) Error() string { return e.Message }

func newError(code, field, format string, args ...any) *Error {
	return &Error{Code: code, Field: field, Message: fmt.Sprintf(format, args...)}
}

// AsError returns err as a networks error, if it is one.
func AsError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

var nameRe = regexp.MustCompile(`\A[a-z](?:[a-z0-9-]{0,22}[a-z0-9])?\z`)

// reservedNames are compose or Docker network names with a meaning.
var reservedNames = map[string]bool{
	SharedName: true, "pub": true, "default": true, "host": true, "none": true, "bridge": true, "traefik": true,
	// Paths of the API (/networks/config, /networks/docker).
	"config": true, "docker": true,
}

// ValidateName checks a network name (also its compose key).
func ValidateName(name string) *Error {
	if !nameRe.MatchString(name) {
		return newError(CodeInvalidName, "name", "a network name is 1 to 24 lowercase letters, digits or dashes, starting with a letter and not ending with a dash")
	}
	if reservedNames[name] {
		return newError(CodeInvalidName, "name", "this network name is reserved")
	}
	return nil
}

// privateRanges are the ranges a panel subnet may come from.
var privateRanges = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"),
}

const (
	minPrefix = 16
	maxPrefix = 28
)

// ParseSubnet validates a subnet chosen by the user: an IPv4 private range,
// written as its network address, between /16 and /28. Overlaps with other
// Docker networks are refused by Docker itself.
func ParseSubnet(s string) (netip.Prefix, *Error) {
	p, err := netip.ParsePrefix(s)
	if err != nil || !p.Addr().Is4() {
		return netip.Prefix{}, newError(CodeInvalidSubnet, "subnet", "the subnet must be an IPv4 CIDR (e.g. 10.20.0.0/24)")
	}
	if p.Masked() != p {
		return netip.Prefix{}, newError(CodeInvalidSubnet, "subnet", "the subnet must start at its network address (%s)", p.Masked())
	}
	if p.Bits() < minPrefix || p.Bits() > maxPrefix {
		return netip.Prefix{}, newError(CodeInvalidSubnet, "subnet", "the prefix length must be between /%d and /%d", minPrefix, maxPrefix)
	}
	for _, r := range privateRanges {
		if r.Bits() <= p.Bits() && r.Contains(p.Addr()) {
			return p, nil
		}
	}
	return netip.Prefix{}, newError(CodeInvalidSubnet, "subnet", "the subnet must be in a private range (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10)")
}

// reservedOverlap returns the reserved range a subnet overlaps ("" if none
// or if the subnet cannot be parsed).
func reservedOverlap(reserved []netip.Prefix, subnet string) string {
	p, err := netip.ParsePrefix(subnet)
	if err != nil {
		return ""
	}
	for _, r := range reserved {
		if r.Overlaps(p) {
			return r.String()
		}
	}
	return ""
}

// Limits are the creation rules, for clients.
type Limits struct {
	Max            int      `json:"max"`
	Count          int      `json:"count"`
	ReservedRanges []string `json:"reservedRanges"`
	PrivateRanges  []string `json:"privateRanges"`
	MinPrefix      int      `json:"minPrefix"`
	MaxPrefix      int      `json:"maxPrefix"`
}

// maxDescription bounds the description of a network.
const maxDescription = 200
