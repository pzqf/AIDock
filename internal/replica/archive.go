package replica

import (
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// MembershipCandidate keeps referenced bindings intact in the canonical
// registry while removing them from active navigation/synchronization. Restore
// changes only registry membership and remains possible when the source needs
// recovery; it never clears or repairs a source/native transaction itself.
func (m *Manager) MembershipCandidate(id, operation string, restore bool) (Binding, workstore.Tree, workstore.Tree, error) {
	var b Binding
	var before, after workstore.Tree
	if !uuid.MatchString(id) || !uuid.MatchString(operation) {
		return b, before, after, result.Fail("INVALID_INPUT", "需要明确的归档成员身份", 2)
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
	if restore {
		for i, old := range r.Archived {
			if old.Binding.ID == id {
				b, index = old.Binding, i
				break
			}
		}
		if index < 0 {
			return b, before, after, result.Fail("PROJECT_NOT_ARCHIVED", "此项目不在归档列表", 4)
		}
		r.Archived = append(r.Archived[:index], r.Archived[index+1:]...)
		r.Copies = append(r.Copies, b)
	} else {
		for i, copy := range r.Copies {
			if copy.ID == id {
				b, index = copy, i
				break
			}
		}
		if index < 0 {
			return b, before, after, result.Fail("PROJECT_NOT_REGISTERED", "此项目副本未登记或已归档", 4)
		}
		refs, e := m.RemovalReferences(b)
		if e != nil {
			return b, before, after, e
		}
		for _, ref := range refs {
			if ref.State == "prepared" {
				return b, before, after, result.Fail("PROJECT_RECOVERY_REQUIRED", "项目有未完成事务，先恢复后归档", 6)
			}
		}
		r.Copies = append(r.Copies[:index], r.Copies[index+1:]...)
		r.Archived = append(r.Archived, ArchivedBinding{Binding: b, Operation: operation, References: refs})
	}
	if err = m.validateRegistry(r); err != nil {
		return b, before, after, err
	}
	after = workstore.Clone(before)
	err = m.Control.PutFile(&after, "replicas.json", data(r), 0600)
	return b, before, after, err
}
