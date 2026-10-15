package replica

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

type Reference struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}

// RemovalReferences reports snapshots which can still restore the real bound
// source/tool roots. Completed project-registration records and independent
// rehearsals cannot reverse real roots, so they do not pin active membership.
// Baselines and their deletion tombstones remain in Control without changes.
func (m *Manager) RemovalReferences(b Binding) ([]Reference, error) {
	s, err := source.New(b.Root, m.Source.Store.Local, m.Source.Owner, m.Source.Scope)
	if err != nil {
		return nil, err
	}
	if err = s.Store.Pending(); err != nil {
		return nil, err
	}
	js, err := s.Store.Journals()
	if err != nil {
		return nil, err
	}
	refs := []Reference{}
	for _, j := range js {
		if j.State == "committed" || j.State == "prepared" {
			refs = append(refs, Reference{Kind: "source", ID: j.Plan.ID, State: j.State, Reason: "技能源恢复/回退快照仍绑定此目录"})
		}
	}
	native, err := workstore.NativeHistoryForRoot(m.Source.Store.Local, filepath.Dir(b.Root))
	if err != nil {
		return nil, err
	}
	for _, j := range native {
		if j.State == "committed" || j.State == "prepared" {
			refs = append(refs, Reference{Kind: "native", ID: j.Plan.ID, State: j.State, Reason: "原生工具恢复/回退快照仍绑定此项目"})
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Kind+refs[i].ID < refs[j].Kind+refs[j].ID })
	return refs, nil
}

// RemovalCandidate prepares only the canonical registry change. Project and
// replica entry points share this gate and the coordinated confirmation flow.
// Confirmation must call it again while holding registry and source locks.
func (m *Manager) RemovalCandidate(id, operation string) (Binding, workstore.Tree, workstore.Tree, error) {
	var b Binding
	var before, after workstore.Tree
	if !uuid.MatchString(id) || !uuid.MatchString(operation) {
		return b, before, after, result.Fail("INVALID_INPUT", "移除需要明确的副本身份", 2)
	}
	if err := m.Control.Pending(); err != nil {
		return b, before, after, err
	}
	before, err := m.Control.Capture(m.Control.Root)
	if err != nil {
		return b, before, after, err
	}
	r, err := m.registry(before)
	if err != nil {
		return b, before, after, err
	}
	index := -1
	for i, copy := range r.Copies {
		if copy.ID == id {
			b, index = copy, i
			break
		}
	}
	if index < 0 {
		return b, before, after, result.Fail("PROJECT_NOT_REGISTERED", "此项目副本未登记或已移除", 4)
	}
	refs, err := m.RemovalReferences(b)
	if err != nil {
		return b, before, after, err
	}
	if len(refs) != 0 {
		return b, before, after, &result.Error{Code: "BINDING_REFERENCED", Message: fmt.Sprintf("不能移除“%s”：仍有 %d 个源或工具恢复快照引用；保留登记和恢复入口", b.Name, len(refs)), Exit: 4, Recovery: refs}
	}
	r.Copies = append(r.Copies[:index], r.Copies[index+1:]...)
	r.Removed = append(r.Removed, RemovedBinding{Binding: b, Operation: operation})
	after = workstore.Clone(before)
	if err = m.Control.PutFile(&after, "replicas.json", data(r), 0600); err != nil {
		return b, before, after, err
	}
	return b, before, after, nil
}
