package functions

import (
	"context"
	"io"
	"slices"
	"strings"
)

// DeploymentInfo describes a kept deployment.
type DeploymentInfo struct {
	Deployment
	Active bool `json:"active"`
}

// clone returns a deep copy of a reference (every operation builds the
// next reference from a copy: the previous one is kept for the rollback).
func (r *Reference) clone() *Reference {
	c := *r
	c.Entrypoint = slices.Clone(r.Entrypoint)
	c.Env = slices.Clone(r.Env)
	c.Secrets = slices.Clone(r.Secrets)
	c.Networks = slices.Clone(r.Networks)
	c.Kept = slices.Clone(r.Kept)
	if r.Deployment != nil {
		d := *r.Deployment
		c.Deployment = &d
	}
	return &c
}

// keep returns the kept deployments after `previous` stops being active,
// oldest first, within FUNCTIONS_DEPLOYMENTS_KEPT (the active one
// included), and the ones pruned: the oldest go first, but the one that
// was active until now is always kept (it is the rollback target).
func (m *Manager) keep(kept []Deployment, previous *Deployment, active string) (out, pruned []Deployment) {
	others := slices.DeleteFunc(slices.Clone(kept), func(d Deployment) bool {
		return d.ID == active || (previous != nil && d.ID == previous.ID)
	})
	slices.SortFunc(others, func(a, b Deployment) int { return strings.Compare(a.ID, b.ID) })
	room := m.cfg.DeploymentsKept - 1 // the active one
	if previous != nil && previous.ID != active && room > 0 {
		room--
	}
	for len(others) > max(room, 0) {
		pruned = append(pruned, others[0])
		others = others[1:]
	}
	out = others
	if previous != nil && previous.ID != active {
		if m.cfg.DeploymentsKept > 1 {
			out = append(out, *previous)
		} else {
			pruned = append(pruned, *previous)
		}
	}
	slices.SortFunc(out, func(a, b Deployment) int { return strings.Compare(a.ID, b.ID) })
	return out, pruned
}

// prune removes the directories of deployments no longer kept (after the
// reference that drops them was written).
func (m *Manager) prune(n Name, pruned []Deployment) {
	for _, d := range pruned {
		if err := m.paths.RemoveDeployment(n, d.ID); err != nil {
			m.logger.Warn("functions: removing a pruned deployment", "function", n.String(), "deployment", d.ID, "error", err)
		}
	}
}

// Deploy validates and extracts an uploaded zip into a new deployment and
// activates it. The previous deployment is kept for a rollback; the
// oldest beyond FUNCTIONS_DEPLOYMENTS_KEPT are removed. The caller bounds
// the body (http.MaxBytesReader); the zip is streamed to disk.
func (m *Manager) Deploy(ctx context.Context, name string, upload io.Reader, actor string) (*Deployment, error) {
	n, nerr := parse(name)
	if nerr != nil {
		return nil, nerr
	}
	// The upload is read from the request: no detached context until the
	// zip is on disk and validated.
	unlock, err := m.lock(ctx, n)
	if err != nil {
		return nil, err
	}
	defer unlock()
	prev, err := m.load(n)
	if err != nil {
		return nil, err
	}
	if m.modified(prev) {
		return nil, m.refused(n, actor, "deploy", "compose project modified outside the panel", CodeModified,
			"the function was modified outside the panel: it is never run by the panel (purge it)")
	}
	d, err := m.extractor.Deploy(ctx, upload, m.paths, n, m.now(), CodeCheckFor(prev))
	if err != nil {
		return nil, err
	}
	ctx, cancel := detach(ctx)
	defer cancel()
	next := prev.clone()
	next.Deployment = d
	var pruned []Deployment
	next.Kept, pruned = m.keep(prev.Kept, prev.Deployment, d.ID)
	if err := m.apply(ctx, prev, next, change{}, "deploy", actor); err != nil {
		if rerr := m.paths.RemoveDeployment(n, d.ID); rerr != nil {
			m.logger.Warn("functions: removing a failed deployment", "function", n.String(), "deployment", d.ID, "error", rerr)
		}
		m.audit.Warn("function code deployment failed", "user", actor, "function", n.String(), "deployment", d.ID,
			"zip_sha256", d.ZipSHA256, "code", CodeOf(err))
		return nil, err
	}
	m.prune(n, pruned)
	attrs := []any{"user", actor, "function", n.String(), "deployment", d.ID,
		"zip_sha256", d.ZipSHA256, "zip_size", d.ZipSize, "files", d.Files, "bytes", d.Bytes}
	if len(d.Warnings) > 0 {
		attrs = append(attrs, "warnings", strings.Join(d.Warnings, ","))
	}
	m.audit.Info("function code deployed", attrs...)
	return d, nil
}

// Deployments lists the kept deployments, newest first.
func (m *Manager) Deployments(_ context.Context, name string) ([]DeploymentInfo, error) {
	n, nerr := parse(name)
	if nerr != nil {
		return nil, nerr
	}
	ref, err := m.load(n)
	if err != nil {
		return nil, err
	}
	return deploymentInfos(ref), nil
}

func deploymentInfos(ref *Reference) []DeploymentInfo {
	out := make([]DeploymentInfo, 0, len(ref.Kept)+1)
	if ref.Deployment != nil {
		out = append(out, DeploymentInfo{Deployment: *ref.Deployment, Active: true})
	}
	for _, d := range ref.Kept {
		out = append(out, DeploymentInfo{Deployment: d})
	}
	slices.SortFunc(out, func(a, b DeploymentInfo) int { return strings.Compare(b.ID, a.ID) })
	return out
}

// Activate makes a kept deployment the active one (a rollback when it is
// older than the current one). Its tree is verified before.
func (m *Manager) Activate(ctx context.Context, name, id, actor string) error {
	n, nerr := parse(name)
	if nerr != nil {
		return nerr
	}
	if !ValidDeploymentID(id) {
		return newError(CodeInvalidDeployment, "id", "invalid deployment id")
	}
	ctx, cancel := detach(ctx)
	defer cancel()
	unlock, err := m.lock(ctx, n)
	if err != nil {
		return err
	}
	defer unlock()
	prev, err := m.load(n)
	if err != nil {
		return err
	}
	if prev.Deployment != nil && prev.Deployment.ID == id {
		return newError(CodeDeploymentActive, "id", "this deployment is already active")
	}
	i := slices.IndexFunc(prev.Kept, func(d Deployment) bool { return d.ID == id })
	if i < 0 {
		return newError(CodeDeploymentNotFound, "id", "deployment not found")
	}
	target := prev.Kept[i]
	if err := m.paths.VerifyDeployment(n, &target); err != nil {
		m.logger.Error("functions: deployment refused", "function", n.String(), "deployment", id, "error", err)
		return m.refused(n, actor, "activate", "deployment tree modified", CodeArtifactModified,
			"the deployment was modified outside the panel")
	}
	next := prev.clone()
	next.Deployment = &target
	var pruned []Deployment
	next.Kept, pruned = m.keep(prev.Kept, prev.Deployment, id)
	if err := m.apply(ctx, prev, next, change{}, "activate", actor); err != nil {
		return err
	}
	m.prune(n, pruned)
	event := "function deployment activated"
	if prev.Deployment != nil && id < prev.Deployment.ID {
		event = "function deployment rolled back"
	}
	from := ""
	if prev.Deployment != nil {
		from = prev.Deployment.ID
	}
	m.audit.Info(event, "user", actor, "function", n.String(), "deployment", id, "previous", from)
	return nil
}
