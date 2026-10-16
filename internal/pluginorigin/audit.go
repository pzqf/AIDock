package pluginorigin

import (
	"context"
	"fmt"

	"github.com/pzqf/AIDock/internal/auditlog"
	"github.com/pzqf/AIDock/internal/workstore"
)

func (m *Manager) Audit(ctx context.Context) auditlog.Chain {
	return auditlog.Observe(ctx, "origin", "source", m.Store, func(t workstore.Tree) ([]auditlog.Event, string, error) {
		if _, _, err := m.read(t); err != nil {
			return nil, "", err
		}
		items, head, err := m.audits(t)
		out := []auditlog.Event{}
		for _, a := range items {
			out = append(out, auditlog.Event{Sequence: a.Sequence, ID: a.ID, At: a.CreatedAt, Action: a.Action, Previous: a.Previous, SHA256: t.Entries[fmt.Sprintf("audit/%08d-%s.json", a.Sequence, a.ID)].Hash})
		}
		return out, head, err
	})
}
