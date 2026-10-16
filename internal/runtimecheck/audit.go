package runtimecheck

import (
	"context"
	"fmt"

	"github.com/pzqf/AIDock/internal/auditlog"
	"github.com/pzqf/AIDock/internal/workstore"
)

func (m *Manager) Audit(ctx context.Context) auditlog.Chain {
	return auditlog.Observe(ctx, "runtime", "copy", m.Store, func(t workstore.Tree) ([]auditlog.Event, string, error) {
		items, err := m.read(t)
		out, head := []auditlog.Event{}, ""
		for _, a := range items {
			head = t.Entries[fmt.Sprintf("audit/%08d-%s.json", a.Sequence, a.ID)].Hash
			out = append(out, auditlog.Event{Sequence: a.Sequence, ID: a.ID, At: a.CreatedAt, Action: a.Phase, Code: a.Code, Subject: a.Input.Name, Actor: a.Actor, Attempt: a.Attempt, Previous: a.Previous, SHA256: head})
		}
		return out, head, err
	})
}
