package replica

import (
	"context"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type MemberPublicationEvidence struct {
	Publication string                      `json:"publication_id"`
	Transaction string                      `json:"transaction"`
	Initiator   Binding                     `json:"initiator"`
	Entry       string                      `json:"initiator_binding,omitempty"`
	Member      Binding                     `json:"member"`
	History     native.ConfigurationHistory `json:"history"`
}

// MemberPublicationChecks is a read of a member's own original references.
// It never impersonates the initiator or grants access to another member's
// private preparation. Callers supply publication IDs from checked snapshots.
func (m *Manager) MemberPublicationChecks(ctx context.Context, ids []string) ([]MemberPublicationEvidence, error) {
	out := []MemberPublicationEvidence{}
	if len(ids) > 64 {
		return nil, result.Fail("INVALID_INPUT", "一次最多读取 64 个完整批次的当前成员证据", 2)
	}
	if len(ids) == 0 {
		return out, nil
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !uuid.MatchString(id) || seen[id] {
			return nil, result.Fail("INVALID_INPUT", "需要互不重复的原完整批次身份", 2)
		}
		seen[id] = true
	}
	current, err := m.List()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p, err := m.readFullPublicationDescriptor(id, "")
		if err != nil {
			return nil, err
		}
		state, err := p.Proof.State(m.Source.Store.Local)
		if err != nil {
			return nil, err
		}
		if state != "committed" {
			return nil, result.Fail("PUBLICATION_COMMIT_REQUIRED", "历史证据需要原完整批次的实际成功提交", 4)
		}
		found := false
		for i, target := range p.Resolution.Targets {
			if target.Copy.ID != m.Source.Copy || target.Copy.Root != m.Source.Store.Root {
				continue
			}
			found = true
			n, err := native.New(m.Source, p.Proof.Members[i].NativeRoot)
			if err != nil {
				return nil, err
			}
			history, err := n.ConfigurationCheckHistory(ctx, p.Checks[i].ID, p.Checks[i].Digest)
			if err != nil {
				return nil, err
			}
			initiator := Binding{}
			registry, err := m.registry(p.Resolution.ControlBefore)
			if err != nil {
				return nil, err
			}
			for _, binding := range registry.Copies {
				if binding.ID == p.InitiatorCopy && binding.Root == p.InitiatorRoot {
					initiator = binding
				}
			}
			entry := ""
			for _, active := range current.Copies {
				if active.ID == initiator.ID && active.Root == initiator.Root {
					entry = workstore.Hash([]byte(m.Source.Owner + "/" + m.Source.Scope + "/" + active.ID))
				}
			}
			out = append(out, MemberPublicationEvidence{Publication: id, Transaction: p.Proof.Transaction.ID, Initiator: initiator, Entry: entry, Member: target.Copy, History: history})
		}
		if !found {
			return nil, result.Fail("PUBLICATION_MEMBER_MISMATCH", "完整批次没有当前项目根与副本的成员检查", 4)
		}
	}
	return out, nil
}
