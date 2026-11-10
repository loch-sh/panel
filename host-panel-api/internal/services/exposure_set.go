package services

// Exposure chosen in the panel: validated against the ExposurePolicy of the
// API configuration, recorded in the instance reference, then applied by
// the same fail-closed path as at startup (applyExposureLocked).

import (
	"context"
	"os"
	"path/filepath"
	"slices"
)

// validateExposure checks an exposure against the policy and the engine.
func (m *Manager) validateExposure(inst *instance, exp Exposure) *Error {
	p := m.cfg.Exposure
	if !p.Allowed(exp.Address) {
		return newError(CodeInvalidExposure, "address", "this address is not allowed by the server configuration (SERVICES_EXPOSE_ALLOWED_CIDRS)")
	}
	if slices.Contains(alwaysReservedPorts, exp.Port) || slices.Contains(p.ReservedPorts, exp.Port) {
		return newError(CodeInvalidExposure, "port", "port %d is reserved", exp.Port)
	}
	if exp.Public() {
		if inst.version.Deprecated {
			return newError(CodeInvalidExposure, "address", "this version is out of support: it can only be published on a loopback address (use loch proxy or an SSH tunnel)")
		}
		if IsUnauthenticated(inst.engine) {
			return newError(CodeInvalidExposure, "address", "this engine has no authentication: it can only be published on a loopback address")
		}
		if m.cfg.TLS == TLSAuto && !supportsTLS(inst.engine, inst.ref, inst.version) {
			return newError(CodeInvalidExposure, "address", "this engine cannot require TLS: it can only be published on a loopback address")
		}
	}
	if exp.TLSOptional {
		if !p.AllowTLSOptional {
			return newError(CodeInvalidExposure, "tlsOptional", "optional TLS is not allowed by the server configuration (SERVICES_ALLOW_TLS_OPTIONAL)")
		}
		if !m.tlsRequired(inst.engine, inst.ref, inst.version, &exp) {
			return newError(CodeInvalidExposure, "tlsOptional", "TLS is not required on this exposure: optional TLS does not apply")
		}
		if !supportsOptionalTLS(inst.engine, inst.ref, inst.version) {
			return newError(CodeInvalidExposure, "tlsOptional", "this engine cannot offer TLS without requiring it")
		}
	}
	return nil
}

// portTaken returns the instance already recorded with an overlapping
// exposure on the same port ("" when free).
func (m *Manager) portTaken(inst *instance, exp Exposure) (string, error) {
	all, err := m.loadAll()
	if err != nil {
		return "", err
	}
	for _, o := range all {
		if o.ref.Key() == inst.ref.Key() {
			continue
		}
		for _, other := range []*Exposure{o.desired, appliedOf(o)} {
			if other != nil && other.Port == exp.Port && addressesOverlap(other.Address, exp.Address) {
				return o.ref.Key(), nil
			}
		}
	}
	return "", nil
}

func appliedOf(inst *instance) *Exposure {
	exp, _ := inst.applied.exposure()
	return exp
}

// SetExposure publishes an instance (nil closes its port) and applies it
// right away. A failure keeps the previous exposure, or stops the
// instance when the change closed or restricted it.
func (m *Manager) SetExposure(ctx context.Context, key string, exp *Exposure, actor string) (*InstanceSummary, error) {
	if err := m.knownKey(key); err != nil {
		return nil, err
	}
	ctx, cancel := detach(ctx)
	defer cancel()
	unlock, err := m.lock(ctx, key)
	if err != nil {
		return nil, err
	}
	defer unlock()

	inst, lerr := m.load(key)
	if lerr != nil {
		return nil, lerr
	}
	// Closing is always possible (a modified instance is then stopped);
	// opening never runs files edited by hand.
	if inst.modified && exp != nil {
		return nil, errModifiedByHand()
	}
	if exp != nil {
		if verr := m.validateExposure(inst, *exp); verr != nil {
			return nil, verr
		}
		owner, err := m.portTaken(inst, *exp)
		if err != nil {
			return nil, m.storageError(err)
		}
		if owner != "" {
			return nil, newError(CodePortInUse, "port", "this port is already used by %s", owner)
		}
	}
	before := "none"
	if inst.desired != nil {
		before = inst.desired.String()
	}
	after := "none"
	if exp != nil {
		after = exp.String()
	}
	next := inst.reference()
	next.Exposure = recordOf(exp)
	if err := m.writeReference(inst.ref, next); err != nil {
		return nil, m.storageError(err)
	}
	// An explicit change is always tried, even if the same one failed
	// before (the marker only stops the automatic retries).
	_ = os.Remove(filepath.Join(inst.dir, exposureFailedMarker))
	m.audit.Info("service exposure changed", "user", actor, "instance", key, "before", before, "after", after,
		"tlsOptional", exp != nil && exp.TLSOptional)
	closing := narrows(inst.applied, exp, m.tlsRequired(inst.engine, inst.ref, inst.version, exp))
	if err := m.applyExposureLocked(ctx, key); err != nil {
		m.logger.Error("services: the new exposure was not applied", "instance", key, "error", err)
		if !closing {
			// The previous exposure is still in effect: record it again.
			if werr := m.writeReference(inst.ref, inst.reference()); werr != nil {
				m.logger.Error("services: restoring the previous exposure record failed", "instance", key, "error", werr)
			}
		}
		return nil, newError(CodeExposureFailed, "", "the new exposure could not be applied (see the API logs): the instance keeps its previous port, or was stopped if the port had to be closed")
	}
	fresh, lerr := m.load(key)
	if lerr != nil {
		return nil, lerr
	}
	s := m.summary(ctx, fresh, m.inspectOne(ctx, fresh))
	return &s, nil
}

// inspectOne returns the Docker state of one instance.
func (m *Manager) inspectOne(ctx context.Context, inst *instance) runtime {
	all, err := m.docker.ListContainers(ctx)
	if err != nil {
		return runtime{state: "missing", problem: "list failed"}
	}
	return m.inspect(ctx, inst, all)
}

// ExposureLimits is the exposure policy, for clients.
type ExposureLimits struct {
	AllowedCIDRs     []string `json:"allowedCidrs"`
	MinPort          int      `json:"minPort"`
	MaxPort          int      `json:"maxPort"`
	ReservedPorts    []int    `json:"reservedPorts"`
	AllowTLSOptional bool     `json:"allowTlsOptional"`
	TLSMode          TLSMode  `json:"tlsMode"`
}

// ExposureLimits returns the exposure policy of the API configuration.
func (m *Manager) ExposureLimits() ExposureLimits {
	p := m.cfg.Exposure
	l := ExposureLimits{MinPort: minExposedPort, MaxPort: maxExposedPort, AllowTLSOptional: p.AllowTLSOptional, TLSMode: m.cfg.TLS,
		AllowedCIDRs: []string{}, ReservedPorts: append(append([]int{}, alwaysReservedPorts...), p.ReservedPorts...)}
	for _, c := range p.AllowedCIDRs {
		l.AllowedCIDRs = append(l.AllowedCIDRs, c.String())
	}
	return l
}
