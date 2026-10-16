package mcpsession

import (
	"context"
	"fmt"

	"github.com/pzqf/AIDock/internal/auditlog"
	"github.com/pzqf/AIDock/internal/workstore"
)

func (m *Manager) Audit(ctx context.Context) auditlog.Chain {
	return auditlog.Observe(ctx, "session", "copy", m.Store, func(t workstore.Tree) ([]auditlog.Event, string, error) {
		items, err := m.read(t)
		out, head := []auditlog.Event{}, ""
		for _, a := range items {
			head = t.Entries[fmt.Sprintf("audit/%08d-%s.json", a.Sequence, a.ID)].Hash
			code := a.Code
			event := auditlog.Event{Sequence: a.Sequence, ID: a.ID, At: a.CreatedAt, Action: a.Phase, Subject: a.Input.RuntimeInput.Name, Actor: a.Actor, Attempt: a.Attempt, Previous: a.Previous, SHA256: head}
			if a.Relay != nil {
				code = a.Relay.Code
				event.Method, event.Tool, event.Request = a.Relay.Method, a.Relay.Tool, a.Relay.RequestID
			}
			event.Code = code
			out = append(out, event)
		}
		return out, head, err
	})
}
