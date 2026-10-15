package project

import (
	"context"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

// RemoveCopy is the replica-facing entry point to the same canonical operation.
func (m *Manager) RemoveCopy(ctx context.Context, owner, scope, copy string) (Plan, error) {
	return m.Remove(ctx, workstore.Hash([]byte(owner+"/"+scope+"/"+copy)))
}

func (m *Manager) Remove(ctx context.Context, id string) (Plan, error) {
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	b, err := m.Resolve(id)
	if err != nil {
		return Plan{}, err
	}
	s, err := source.New(b.Root, m.Local, b.Project, b.Scope)
	if err != nil {
		return Plan{}, err
	}
	r, err := replica.New(s)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Version: 1, Kind: "project-removal", ID: workstore.ID(), Local: m.Local, Binding: b,
		Request: Request{Root: filepath.Dir(b.Root), Name: b.Name, Project: b.Project, Scope: b.Scope, Copy: b.Copy}}
	_, before, after, err := r.RemovalCandidate(b.Copy, p.ID)
	if err != nil {
		return Plan{}, err
	}
	current, err := s.Store.Capture(s.Store.Root)
	if err != nil {
		return Plan{}, err
	}
	if err = transfer(r.Control, s.Store, before, after); err != nil {
		return Plan{}, err
	}
	p.Batch.Roots = []workstore.RootBinding{{Path: "registry", Root: r.Control.Root}, {Path: "source", Root: b.Root}}
	p.Batch, err = m.coordinator(p, s.Store).PreviewObserved("apply", []workstore.BatchTarget{{Path: "source", Before: current, After: workstore.Clone(current)}, {Path: "registry", Before: before, After: after}})
	if err != nil {
		return Plan{}, err
	}
	p.Digest = digest(p)
	return p, save(m.file(p.ID), p)
}

func (m *Manager) validateRemoval(p Plan) error {
	s, err := source.New(p.Binding.Root, m.Local, p.Binding.Project, p.Binding.Scope)
	if err != nil {
		return err
	}
	r, err := replica.New(s)
	if err != nil {
		return err
	}
	b, before, after, err := r.RemovalCandidate(p.Binding.Copy, p.ID)
	if err != nil {
		return err
	}
	if binding(replica.Registry{Owner: p.Binding.Project, Scope: p.Binding.Scope}, b) != p.Binding ||
		workstore.Digest(before) != workstore.Digest(p.Batch.Targets[1].Before) || workstore.Digest(after) != workstore.Digest(p.Batch.Targets[1].After) ||
		p.Request.Create || workstore.Digest(p.Batch.Targets[0].Before) != workstore.Digest(p.Batch.Targets[0].After) {
		return result.Fail("PLAN_STALE", "移除范围或登记在预览后变化，请重新预览", 3)
	}
	return nil
}
