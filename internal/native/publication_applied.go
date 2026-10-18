package native

import (
	"context"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
)

func (m *Manager) loadPublicationAppliedRecord(ctx context.Context, a *projectsnapshot.Archive, r projectsnapshot.Record) (appliedRecord, error) {
	var out appliedRecord
	p := r.Publication
	if p == nil || p.View == nil || m.appliedKey(r.Effective, p.View.Sessions, r.Adapters, p.View.ExecutableSHA) != r.ActiveKey {
		return out, result.Fail("APPLIED_VIEW_INVALID", "副本快照的应用绑定、程序或完整会话集合不符", 5)
	}
	state, err := p.State(m.Source.Store.Local)
	if err != nil {
		return out, err
	}
	if state != "committed" {
		return out, result.Fail("APPLIED_VIEW_NOT_COMMITTED", "缺少真实副本全批成功提交；单目标记录不构成应用证明", 4)
	}
	material, err := a.Materialize(ctx, r.ID, m.Source.Store)
	if err != nil {
		return out, err
	}
	native, err := a.MaterializeNative(ctx, r.ID, m.connectionStore())
	if err != nil {
		return out, err
	}
	if material.Commit != r.Commit || native.Commit != r.Commit || r.Configuration == nil {
		return out, result.Fail("APPLIED_VIEW_INVALID", "全批应用归档在读取期间变化", 5)
	}
	ledger, err := m.appliedRecordLedger(r)
	if err != nil {
		return out, err
	}
	// No local transaction/journal is fabricated. Runtime authorization and
	// ownership checks remain in the existing frozen/current-authority reader.
	plan := ProfileApplicationPlan{ID: p.Transaction.ID, Root: m.Root, Executable: p.View.Executable, ExecutableSHA: p.View.ExecutableSHA, Home: filepath.Dir(m.Source.Store.Local), ActiveKey: r.ActiveKey, Request: ProfileApplicationRequest{Sessions: p.View.Sessions}}
	return appliedRecord{Archive: a, Record: r, Plan: plan, Frozen: source.FrozenMCP{Tree: r.Source, Effective: r.Effective, Configuration: *r.Configuration}, Ledger: ledger}, nil
}
