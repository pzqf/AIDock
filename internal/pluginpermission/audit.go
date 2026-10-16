package pluginpermission

import (
	"context"
	"fmt"

	"github.com/pzqf/AIDock/internal/auditlog"
	"github.com/pzqf/AIDock/internal/workstore"
)

func (m *Manager) Audit(ctx context.Context) auditlog.Chain {
	return auditlog.Observe(ctx, "permission", "copy", m.Store, func(t workstore.Tree) ([]auditlog.Event, string, error) {
		if _, err := m.records(t); err != nil {
			return nil, "", err
		}
		items, head, err := m.audits(t)
		out := []auditlog.Event{}
		for _, a := range items {
			code := ""
			if a.Request.ConfirmUnisolated {
				code = "UNISOLATED_ACCEPTED"
			}
			out = append(out, auditlog.Event{Sequence: a.Sequence, ID: a.ID, At: a.CreatedAt, Action: a.Action, Code: code, Subject: a.Request.Name, Actor: a.Actor, Previous: a.Previous, SHA256: t.Entries[fmt.Sprintf("audit/%08d-%s.json", a.Sequence, a.ID)].Hash})
		}
		return out, head, err
	})
}
