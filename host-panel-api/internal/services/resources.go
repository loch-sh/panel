package services

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/moby/moby/api/types/container"

	"host-panel-api/internal/composekit"
)

// InstanceSummary describes an instance for clients.
type InstanceSummary struct {
	Key     string `json:"key"`
	Engine  string `json:"engine"`
	Version string `json:"version"`
	// Name and Options: dedicated instances only.
	Name       string  `json:"name,omitempty"`
	Options    Options `json:"options,omitempty"`
	Image      string  `json:"image"`
	Project    string  `json:"project"`
	Deprecated bool    `json:"deprecated"`
	// SupportUntil (end of the security fixes) and ActiveSupportUntil (end
	// of the bug fixes) of the version: YYYY-MM-DD, "" when unknown.
	SupportUntil       string `json:"supportUntil,omitempty"`
	ActiveSupportUntil string `json:"activeSupportUntil,omitempty"`
	// Update: what a dedicated instance can be updated to (nil for shared
	// and modified instances).
	Update   *UpdateInfo `json:"update,omitempty"`
	State    string      `json:"state"`  // container state, "missing" when absent
	Health   string      `json:"health"` // healthcheck status, "" when unknown
	Ready    bool        `json:"ready"`
	Modified bool        `json:"modified"`
	// Resources is the number of resources, nil when the instance is not
	// ready (its catalog cannot be read: nothing is assumed).
	Resources *int `json:"resources"`
	// Exposure is the published port in effect, nil when not published.
	Exposure *ExposureInfo `json:"exposure,omitempty"`
	// ExposureError: the last change of the published port failed (the
	// previous one is kept, see the API logs).
	ExposureError bool `json:"exposureError,omitempty"`
	// Networks the instance joins (registry names).
	Networks []string `json:"networks"`
	// Unauthenticated: the engine has no authentication (never on the
	// shared network, never published beyond the loopback).
	Unauthenticated bool `json:"unauthenticated,omitempty"`
}

// ExposureInfo describes a published port.
type ExposureInfo struct {
	Address     string `json:"address"`
	Port        int    `json:"port"`
	TLS         bool   `json:"tls"`
	TLSOptional bool   `json:"tlsOptional,omitempty"` // TLS offered, not required
	Fingerprint string `json:"fingerprint,omitempty"` // SHA-256 of the certificate
}

// ResourceSummary describes a resource for clients.
type ResourceSummary struct {
	Instance string `json:"instance"`
	Engine   string `json:"engine"`
	Version  string `json:"version"`
	Name     string `json:"name"`
	Managed  bool   `json:"managed"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
}

// CreatedResource is returned once, with the generated password.
type CreatedResource struct {
	Resource        ResourceSummary `json:"resource"`
	Credentials     []Credential    `json:"credentials"`
	InstanceCreated bool            `json:"instanceCreated"`
}

// DeleteResult tells what happened to the instance after a deletion.
type DeleteResult struct {
	InstanceRetired bool `json:"instanceRetired"`
	// RetireBlocked explains why an empty instance was kept:
	// "modified" (files edited by hand) or "error" (see the logs).
	RetireBlocked string `json:"retireBlocked,omitempty"`
}

func (m *Manager) summary(ctx context.Context, inst *instance, rt runtime) InstanceSummary {
	s := InstanceSummary{
		Key: inst.ref.Key(), Engine: inst.ref.Engine, Version: inst.ref.Version, Name: inst.ref.Name, Options: inst.opts,
		Image: inst.version.Image, Project: inst.ref.Project(), Deprecated: inst.version.Deprecated,
		SupportUntil: inst.version.SupportUntil, ActiveSupportUntil: inst.version.ActiveSupportUntil,
		Update: m.updateInfo(inst, rt),
		State:  rt.state, Health: rt.health, Ready: rt.ready, Modified: inst.modified,
		Exposure: m.exposureInfo(inst), ExposureError: exposureFailed(inst.dir),
		Networks: append([]string{}, inst.networks...), Unauthenticated: IsUnauthenticated(inst.engine),
	}
	if inst.engine.Lifecycle() == Dedicated {
		// The instance is the resource (nil, like the others, when it is
		// not ready).
		if rt.ready {
			one := 1
			s.Resources = &one
		}
		return s
	}
	if rt.ready {
		lctx, cancel := context.WithTimeout(ctx, listTimeout)
		defer cancel()
		if list, err := inst.engine.Provisioner().List(lctx, m.target(inst, rt)); err == nil {
			n := len(list)
			s.Resources = &n
		} else {
			m.logger.Warn("services: listing resources", "instance", s.Key, "error", err)
		}
	}
	return s
}

func (m *Manager) resourceSummary(inst *instance, r Resource) ResourceSummary {
	return ResourceSummary{
		Instance: inst.ref.Key(), Engine: inst.ref.Engine, Version: inst.ref.Version,
		Name: r.Name, Managed: r.Managed, Host: inst.ref.Project(), Port: inst.engine.DefaultPort(),
	}
}

// credentials returns the connection information of a resource: the
// internal address, plus the published one when the instance is exposed.
func (m *Manager) credentials(inst *instance, name, password string) []Credential {
	// The instance requires TLS from every client once it is published
	// with TLS: the internal address too.
	creds := inst.engine.ConnectionInfo(ConnParams{
		Host: inst.ref.Project(), Port: inst.engine.DefaultPort(), Name: name, Password: password, TLS: inst.applied.TLS,
	})
	if info := m.exposureInfo(inst); info != nil {
		host := info.Address
		if m.cfg.PublicHost != "" && !strings.HasPrefix(info.Address, "127.") && info.Address != "::1" {
			host = m.cfg.PublicHost
		}
		creds = append(creds, Credential{Key: "external", Value: net.JoinHostPort(host, strconv.Itoa(info.Port))})
		if info.TLS {
			creds = append(creds, Credential{Key: "tlsFingerprint", Value: info.Fingerprint})
		}
	}
	return creds
}

// exposureInfo returns the exposure in effect on an instance.
func (m *Manager) exposureInfo(inst *instance) *ExposureInfo {
	exp, _ := inst.applied.exposure()
	if exp == nil {
		return nil
	}
	info := &ExposureInfo{Address: exp.HostIP(), Port: exp.Port, TLS: inst.applied.TLS, TLSOptional: inst.applied.TLS && inst.applied.TLSOptional}
	if info.TLS {
		if cert, err := composekit.ReadFileNoFollow(filepath.Join(inst.dir, CertsDir, CertFile)); err == nil {
			info.Fingerprint = Fingerprint(cert)
		}
	}
	return info
}

// Instances lists the managed instances with their state.
func (m *Manager) Instances(ctx context.Context) ([]InstanceSummary, error) {
	insts, err := m.loadAll()
	if err != nil {
		return nil, m.storageError(err)
	}
	all, err := m.docker.ListContainers(ctx)
	if err != nil {
		return nil, m.storageError(err)
	}
	// One catalog read per ready instance: run them concurrently (bounded).
	out := make([]InstanceSummary, len(insts))
	sem := make(chan struct{}, listConcurrency)
	var wg sync.WaitGroup
	for i, inst := range insts {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			out[i] = m.summary(ctx, inst, m.inspect(ctx, inst, all))
		}()
	}
	wg.Wait()
	return out, nil
}

// Resources lists the resources of an instance, read from the engine.
func (m *Manager) Resources(ctx context.Context, key string) ([]ResourceSummary, error) {
	inst, lerr := m.load(key)
	if lerr != nil {
		return nil, lerr
	}
	if inst.engine.Lifecycle() == Dedicated {
		return []ResourceSummary{m.resourceSummary(inst, Resource{Name: inst.ref.Name, Managed: true})}, nil
	}
	rt, rerr := m.ready(ctx, inst)
	if rerr != nil {
		return nil, rerr
	}
	list, err := inst.engine.Provisioner().List(ctx, m.target(inst, rt))
	if err != nil {
		return nil, m.engineError(inst, "list", err)
	}
	out := make([]ResourceSummary, 0, len(list))
	for _, r := range list {
		out = append(out, m.resourceSummary(inst, r))
	}
	return out, nil
}

// validateName applies the shared and the engine rules.
func validateName(p Provisioner, v Version, name string) *Error {
	if err := ValidateName(name); err != nil {
		return err
	}
	return p.ValidateName(v, name)
}

// knownKey refuses keys that are not an enabled instance before anything
// else (no lock entry is created for arbitrary input).
func (m *Manager) knownKey(key string) *Error {
	if !m.reg.KnownKey(key) {
		return instanceNotFound()
	}
	return nil
}

// CreateRequest describes a resource to create. Options are for dedicated
// engines (defaults apply to the missing ones).
type CreateRequest struct {
	Engine  string
	Version string
	Name    string
	Options map[string]string
	// LegacyAuth: the older password mechanism (LegacyAuthProvisioner).
	LegacyAuth bool
	// Networks the instance joins when this request creates it (none: the
	// shared network). Ignored for an existing shared instance: its
	// networks are changed with UpdateNetworks.
	Networks []string
}

// Create provisions a resource: in a shared instance (created first if
// needed), or as a new dedicated instance. The operation is detached from
// ctx: it completes even if the client goes away.
func (m *Manager) Create(ctx context.Context, req CreateRequest, actor string) (*CreatedResource, error) {
	e, v, verr := m.reg.Resolve(req.Engine, req.Version)
	if verr != nil && verr.Code == CodeInvalidVersion {
		// A version no longer offered keeps serving its existing shared
		// instance (a new instance needs an offered version).
		if se, sv, ok := m.reg.ByKey(InstanceRef{Engine: req.Engine, Version: req.Version}.Key()); ok && se.ID() == req.Engine && sv.Version == req.Version {
			if _, lerr := m.load(InstanceRef{Engine: req.Engine, Version: req.Version}.Key()); lerr == nil {
				e, v, verr = se, sv, nil
			}
		}
	}
	if verr != nil {
		return nil, verr
	}
	if req.LegacyAuth && !SupportsLegacyAuth(e, v) {
		return nil, newError(CodeInvalidOption, "legacyAuth", "this engine version has no legacy authentication")
	}
	if e.Lifecycle() == Dedicated {
		return m.createDedicated(ctx, e, v, req, actor)
	}
	if len(req.Options) > 0 {
		return nil, newError(CodeInvalidOption, "options", "this engine has no options")
	}
	name := req.Name
	prov := e.Provisioner()
	if err := validateName(prov, v, name); err != nil {
		return nil, err
	}
	ref := InstanceRef{Engine: e.ID(), Version: v.Version}
	ctx, cancel := detach(ctx)
	defer cancel()
	unlock, err := m.lock(ctx, ref.Key())
	if err != nil {
		return nil, err
	}
	defer unlock()

	inst, lerr := m.load(ref.Key())
	created := false
	if lerr != nil {
		nets, nerr := m.checkNetworks(ctx, e, req.Networks)
		if nerr != nil {
			return nil, nerr
		}
		if inst, err = m.createInstance(ctx, newInstance{e: e, v: v, ref: ref, networks: nets}, actor); err != nil {
			return nil, err
		}
		created = true
	}
	// A fresh instance must not stay without resources: undo it on any
	// failure below. An existing instance is never touched.
	done := false
	defer func() {
		if created && !done {
			m.undoNewInstance(ctx, inst, actor)
		}
	}()

	rt, rerr := m.ready(ctx, inst)
	if rerr != nil {
		return nil, rerr
	}
	t := m.target(inst, rt)
	if err := prov.Init(ctx, t); err != nil {
		return nil, m.engineError(inst, "init", err)
	}
	list, err := prov.List(ctx, t)
	if err != nil {
		return nil, m.engineError(inst, "list", err)
	}
	if slices.ContainsFunc(list, func(r Resource) bool { return r.Name == name }) {
		return nil, newError(CodeResourceExists, "name", "a resource with this name already exists")
	}
	if len(list) >= m.cfg.MaxResourcesPerInstance {
		return nil, newError(CodeResourceLimit, "", "this instance holds the maximum number of resources")
	}
	password, err := GeneratePassword()
	if err != nil {
		return nil, m.storageError(err)
	}
	if err := m.provision(ctx, inst, t, name, password, req.LegacyAuth); err != nil {
		return nil, err
	}
	done = true
	m.audit.Info("service resource created", "user", actor, "instance", ref.Key(), "resource", name, "legacy_auth", req.LegacyAuth)
	m.onChange()
	return &CreatedResource{
		Resource:        m.resourceSummary(inst, Resource{Name: name, Managed: true}),
		Credentials:     m.credentials(inst, name, password),
		InstanceCreated: created,
	}, nil
}

// provision runs the provisioner. An unknown outcome is never taken for a
// success: a resource half created (a user without its database...) would
// be handed out. Provisioners resolve what they can themselves; what
// remains is listed by List, so it can be deleted and created again.
func (m *Manager) provision(ctx context.Context, inst *instance, t Target, name, password string, legacy bool) error {
	var err error
	if legacy {
		err = inst.engine.Provisioner().(LegacyAuthProvisioner).ProvisionLegacy(ctx, t, name, password)
	} else {
		err = inst.engine.Provisioner().Provision(ctx, t, name, password)
	}
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrResourceExists) {
		return newError(CodeResourceExists, "name", "a resource or user with this name already exists")
	}
	return m.engineError(inst, "provision", err)
}

// undoNewInstance retires an instance created by a failed Create. It runs
// on its own deadline: the operation may have failed by timing out.
func (m *Manager) undoNewInstance(ctx context.Context, inst *instance, actor string) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()
	if err := m.retireWith(rctx, inst, actor, false); err != nil {
		m.logger.Error("services: removing the new instance after a failure", "instance", inst.ref.Key(), "error", err)
	}
	m.onChange()
}

// findResource looks a resource up in the engine catalog.
func (m *Manager) findResource(ctx context.Context, inst *instance, t Target, name string) (Resource, bool, error) {
	list, err := inst.engine.Provisioner().List(ctx, t)
	if err != nil {
		return Resource{}, false, err
	}
	i := slices.IndexFunc(list, func(r Resource) bool { return r.Name == name })
	if i < 0 {
		return Resource{}, false, nil
	}
	return list[i], true, nil
}

// managedResource checks that name is a managed resource of a ready instance.
func (m *Manager) managedResource(ctx context.Context, key, name string) (*instance, Target, *Error) {
	inst, lerr := m.load(key)
	if lerr != nil {
		return nil, Target{}, lerr
	}
	rt, rerr := m.ready(ctx, inst)
	if rerr != nil {
		return nil, Target{}, rerr
	}
	t := m.target(inst, rt)
	r, ok, err := m.findResource(ctx, inst, t, name)
	if err != nil {
		return nil, Target{}, m.engineError(inst, "list", err)
	}
	if !ok {
		return nil, Target{}, newError(CodeResourceNotFound, "name", "resource not found")
	}
	if !r.Managed || validateName(inst.engine.Provisioner(), inst.version, name) != nil {
		return nil, Target{}, newError(CodeResourceNotManaged, "name", "this resource was not created by the panel")
	}
	return inst, t, nil
}

// Delete removes a resource and its user. When it was the last resource,
// the instance is retired (containers stopped, data deleted or archived).
func (m *Manager) Delete(ctx context.Context, key, name, actor string) (*DeleteResult, error) {
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

	if inst, lerr := m.load(key); lerr == nil && inst.engine.Lifecycle() == Dedicated {
		return m.deleteDedicated(ctx, inst, name, actor)
	}
	inst, t, merr := m.managedResource(ctx, key, name)
	if merr != nil {
		return nil, merr
	}
	prov := inst.engine.Provisioner()
	if err := prov.Deprovision(ctx, t, name); err != nil {
		if !errors.Is(err, ErrOutcomeUnknown) {
			return nil, m.engineError(inst, "deprovision", err)
		}
		// Only a catalog that no longer lists the resource proves it is gone.
		if _, ok, verr := m.findResource(ctx, inst, t, name); verr != nil || ok {
			return nil, m.engineError(inst, "deprovision", err)
		}
	}
	m.audit.Info("service resource deleted", "user", actor, "instance", key, "resource", name)
	m.onChange()

	res := &DeleteResult{}
	list, err := prov.List(ctx, t)
	if err != nil {
		// Unknown catalog: never retire blindly.
		m.logger.Warn("services: cannot list resources after a deletion, the instance is kept", "instance", key, "error", err)
		return res, nil
	}
	if len(list) > 0 {
		return res, nil
	}
	if inst.modified {
		res.RetireBlocked = "modified"
		m.logger.Warn("services: empty instance kept: its files were edited by hand", "instance", key)
		return res, nil
	}
	if err := m.retire(ctx, inst, actor); err != nil {
		res.RetireBlocked = "error"
		return res, nil
	}
	res.InstanceRetired = true
	m.onChange()
	return res, nil
}

// RotatePassword sets a new generated password on the user of a resource.
func (m *Manager) RotatePassword(ctx context.Context, key, name, actor string) ([]Credential, error) {
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

	if inst, lerr := m.load(key); lerr == nil && inst.engine.Lifecycle() == Dedicated {
		return m.rotateDedicated(ctx, inst, name, actor)
	}
	inst, t, merr := m.managedResource(ctx, key, name)
	if merr != nil {
		return nil, merr
	}
	password, err := GeneratePassword()
	if err != nil {
		return nil, m.storageError(err)
	}
	if err := inst.engine.Provisioner().SetPassword(ctx, t, name, password); err != nil {
		return nil, m.engineError(inst, "set password", err)
	}
	m.audit.Info("service resource password reset", "user", actor, "instance", key, "resource", name)
	return m.credentials(inst, name, password), nil
}

// Retire removes an empty instance explicitly, e.g. after a retirement that
// failed half-way (the container may then be stopped or gone: it is started
// again so that its catalog can be read). Instances whose files were edited
// by hand are refused: they must be restored or removed by an operator.
func (m *Manager) Retire(ctx context.Context, key, actor string) error {
	if err := m.knownKey(key); err != nil {
		return err
	}
	ctx, cancel := detach(ctx)
	defer cancel()
	unlock, err := m.lock(ctx, key)
	if err != nil {
		return err
	}
	defer unlock()

	inst, lerr := m.load(key)
	if lerr != nil {
		return lerr
	}
	if inst.modified {
		return errModifiedByHand()
	}
	if inst.engine.Lifecycle() == Dedicated {
		// The instance is the resource: retiring it is deleting it.
		_, err := m.deleteDedicated(ctx, inst, inst.ref.Name, actor)
		return err
	}
	rt, rerr := m.ready(ctx, inst)
	if rerr != nil && rt.problem == "" && (rt.state == "missing" || rt.state == string(container.StateExited) || rt.state == string(container.StateCreated)) {
		// Stopped or removed by an interrupted retirement: start it again
		// (the files are exactly what the panel writes).
		if out, err := m.compose(ctx, inst.dir, upArgs...); err != nil {
			m.logger.Error("services: restarting an instance to retire it", "instance", key, "error", err, "output", out)
			return newError(CodeStartFailed, "", "the instance failed to start")
		}
		rt, rerr = m.ready(ctx, inst)
	}
	if rerr != nil {
		return rerr
	}
	list, err := inst.engine.Provisioner().List(ctx, m.target(inst, rt))
	if err != nil {
		return m.engineError(inst, "list", err)
	}
	if len(list) > 0 {
		return newError(CodeInstanceNotEmpty, "", "the instance still holds resources")
	}
	if err := m.retire(ctx, inst, actor); err != nil {
		return m.storageError(err)
	}
	m.onChange()
	return nil
}

// engineError logs a provisioner failure and maps it to a client error.
// Provisioner errors never hold engine output.
func (m *Manager) engineError(inst *instance, op string, err error) *Error {
	switch {
	case errors.Is(err, ErrResourceNotFound):
		return newError(CodeResourceNotFound, "name", "resource not found")
	case errors.Is(err, ErrResourceExists):
		return newError(CodeResourceExists, "name", "a resource with this name already exists")
	case errors.Is(err, ErrOutcomeUnknown):
		m.logger.Error("services: engine command did not report its end", "instance", inst.ref.Key(), "op", op, "error", err)
		return newError(CodeOutcomeUnknown, "", "the engine did not answer in time; check the state and retry")
	}
	m.logger.Error("services: engine command failed", "instance", inst.ref.Key(), "op", op, "error", err)
	return newError(CodeEngineError, "", "the engine refused the operation")
}

// Purge deletes an instance whose files were edited by hand, with its data
// (archived with ArchiveOnRetire): the only way out for such an instance,
// which the panel never starts nor stops through compose. Its compose file
// is never run: the containers of the project (by the panel's project name,
// or started from the instance directory under another name) are removed
// through the Docker API, then the data, the private directory and the
// instance directory, all at the paths the panel computes, never at paths
// read from the edited files. The resources a shared instance held are lost.
func (m *Manager) Purge(ctx context.Context, key, actor string) error {
	if err := m.knownKey(key); err != nil {
		return err
	}
	ctx, cancel := detach(ctx)
	defer cancel()
	unlock, err := m.lock(ctx, key)
	if err != nil {
		return err
	}
	defer unlock()

	inst, lerr := m.load(key)
	if lerr != nil {
		return lerr
	}
	if !inst.modified {
		return newError(CodeInstanceNotModified, "", "the instance was not edited by hand: delete its resources or retire it")
	}
	project := inst.ref.Project()
	containers, err := m.docker.ListContainers(ctx)
	if err != nil {
		return m.storageError(err)
	}
	removed := 0
	for _, c := range containers {
		if c.Labels[composeProjectLabel] != project && c.Labels[composeWorkingDirLabel] != inst.dir {
			continue
		}
		if err := m.docker.RemoveContainer(ctx, c.ID); err != nil {
			m.logger.Error("services: purge: removing a container", "instance", key, "container", c.ID, "error", err)
			return m.storageError(err)
		}
		removed++
	}
	// From here on the instance is partly gone: a failure is audited too.
	partial := func(step string) {
		m.audit.Warn("service instance purge incomplete", "user", actor, "instance", key, "containers", removed, "failed_step", step)
	}
	if err := m.docker.RemoveNetwork(ctx, project+"_"+pubNetworkKey); err != nil {
		m.logger.Warn("services: purge: removing the publication network", "instance", key, "error", err)
	}
	archive := m.cfg.ArchiveOnRetire
	if err := m.removeDataDir(inst.ref, archive); err != nil {
		m.logger.Error("services: purge: removing the data directory", "instance", key, "error", err)
		partial("data")
		return m.storageError(err)
	}
	if inst.ref.Name != "" {
		if err := m.removePrivate(inst.ref); err != nil {
			m.logger.Error("services: purge: removing the private directory", "instance", key, "error", err)
			partial("private")
			return m.storageError(err)
		}
	}
	// Last: until then, the instance is still listed and the purge can be
	// retried.
	if err := os.RemoveAll(inst.dir); err != nil {
		m.logger.Error("services: purge: removing the instance directory", "instance", key, "error", err)
		partial("instance directory")
		return m.storageError(err)
	}
	m.audit.Warn("service instance purged", "user", actor, "instance", key, "containers", removed, "archived", archive)
	m.onChange()
	return nil
}
