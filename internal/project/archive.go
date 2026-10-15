package project

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"

	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

type ArchivedItem struct {
	Binding    Binding             `json:"binding"`
	Operation  string              `json:"operation"`
	References []replica.Reference `json:"references"`
}

func (m *Manager) Archived() ([]ArchivedItem, error) {
	registries, err := replica.Catalog(m.Local)
	if err != nil {
		return nil, err
	}
	out := []ArchivedItem{}
	for _, r := range registries {
		for _, a := range r.Archived {
			b := binding(r, a.Binding)
			b.Status = "ARCHIVED"
			out = append(out, ArchivedItem{Binding: b, Operation: a.Operation, References: a.References})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Binding.Name == out[j].Binding.Name {
			return out[i].Binding.ID < out[j].Binding.ID
		}
		return out[i].Binding.Name < out[j].Binding.Name
	})
	return out, nil
}

func (m *Manager) Membership(ctx context.Context, id string, restore bool) (Plan, error) {
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	var b Binding
	var err error
	if restore {
		items, e := m.Archived()
		if e != nil {
			return Plan{}, e
		}
		for _, a := range items {
			if a.Binding.ID == id {
				b = a.Binding
				b.Status = "READY"
				break
			}
		}
		if b.ID == "" {
			return Plan{}, result.Fail("PROJECT_NOT_ARCHIVED", "请选择已归档项目", 4)
		}
	} else {
		b, err = m.Resolve(id)
		if err != nil {
			return Plan{}, err
		}
	}
	s, err := source.New(b.Root, m.Local, b.Project, b.Scope)
	if err != nil {
		return Plan{}, err
	}
	r, err := replica.New(s)
	if err != nil {
		return Plan{}, err
	}
	kind := "project-archive"
	if restore {
		kind = "project-restore"
	}
	p := Plan{Version: 1, Kind: kind, ID: workstore.ID(), Local: m.Local, Binding: b, Request: Request{Root: filepath.Dir(b.Root), Name: b.Name, Project: b.Project, Scope: b.Scope, Copy: b.Copy}}
	_, before, after, err := r.MembershipCandidate(b.Copy, p.ID, restore)
	if err != nil {
		return Plan{}, err
	}
	referenceTree := after
	if restore {
		referenceTree = before
	}
	p.References, err = membershipReferences(r, referenceTree, b.Copy)
	if err != nil {
		return Plan{}, err
	}
	if err = transfer(r.Control, s.Store, before, after); err != nil {
		return Plan{}, err
	}
	p.Batch.Roots = []workstore.RootBinding{{Path: "registry", Root: r.Control.Root}}
	targets := []workstore.BatchTarget{{Path: "registry", Before: before, After: after}}
	if !restore {
		current, e := s.Store.Capture(b.Root)
		if e != nil {
			return Plan{}, e
		}
		p.Batch.Roots = append(p.Batch.Roots, workstore.RootBinding{Path: "source", Root: b.Root})
		targets = append([]workstore.BatchTarget{{Path: "source", Before: current, After: workstore.Clone(current)}}, targets...)
	}
	p.Batch, err = m.coordinator(p, s.Store).PreviewObserved("apply", targets)
	if err != nil {
		return Plan{}, err
	}
	p.Digest = digest(p)
	return p, save(m.file(p.ID), p)
}

func (m *Manager) validateMembership(p Plan) error {
	s, err := source.New(p.Binding.Root, m.Local, p.Binding.Project, p.Binding.Scope)
	if err != nil {
		return err
	}
	r, err := replica.New(s)
	if err != nil {
		return err
	}
	restore := p.Kind == "project-restore"
	b, before, after, err := r.MembershipCandidate(p.Binding.Copy, p.ID, restore)
	if err != nil {
		return err
	}
	referenceTree := after
	if restore {
		referenceTree = before
	}
	refs, err := membershipReferences(r, referenceTree, b.ID)
	if err != nil {
		return err
	}
	refsMatch := len(refs) == len(p.References)
	for i := range refs {
		if !refsMatch || refs[i] != p.References[i] {
			refsMatch = false
			break
		}
	}
	target := p.Batch.Targets[len(p.Batch.Targets)-1]
	if !refsMatch || binding(replica.Registry{Owner: p.Binding.Project, Scope: p.Binding.Scope}, b) != p.Binding || p.Request.Create ||
		workstore.Digest(before) != workstore.Digest(target.Before) || workstore.Digest(after) != workstore.Digest(target.After) ||
		!restore && workstore.Digest(p.Batch.Targets[0].Before) != workstore.Digest(p.Batch.Targets[0].After) {
		return result.Fail("PLAN_STALE", "归档成员或恢复引用在预览后变化，请重新预览", 3)
	}
	return nil
}

func membershipReferences(r *replica.Manager, tree workstore.Tree, id string) ([]replica.Reference, error) {
	bytes, err := r.Control.Data(tree.Entries["replicas.json"])
	if err != nil {
		return nil, err
	}
	var registry replica.Registry
	if err = json.Unmarshal(bytes, &registry); err != nil {
		return nil, err
	}
	for _, a := range registry.Archived {
		if a.Binding.ID == id {
			return a.References, nil
		}
	}
	return nil, result.Fail("PROJECT_PLAN_INVALID", "归档引用缺少原成员", 5)
}
