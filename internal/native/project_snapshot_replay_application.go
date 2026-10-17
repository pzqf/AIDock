package native

import (
	"context"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Private write proposal. All raw compiler content remains inside the native
// private area. The public preview will expose safe differences and bindings.
type snapshotReplayApplication struct {
	Version           int                          `json:"schema_version"`
	ID                string                       `json:"id"`
	Snapshot          string                       `json:"snapshot"`
	Commit            string                       `json:"commit"`
	Candidate         string                       `json:"candidate"`
	Prepared          string                       `json:"prepared"`
	PreparedDigest    string                       `json:"prepared_digest"`
	Executable        string                       `json:"executable"`
	ExecutableSHA     string                       `json:"executable_sha256"`
	Home              string                       `json:"home"`
	BridgeFile        string                       `json:"bridge_file,omitempty"`
	ActiveKey         string                       `json:"applied_binding"`
	Sessions          []source.MCPSessionRequest   `json:"sessions"`
	Native            Plan                         `json:"native"`
	Transaction       workstore.BatchPlan          `json:"transaction"`
	NativeTransaction workstore.BatchPlan          `json:"native_transaction"`
	Frozen            source.SnapshotConfiguration `json:"configuration"`
	AppliedSource     *workstore.Tree              `json:"applied_source,omitempty"`
	Restore           *SnapshotReplayRestoreTarget `json:"restore,omitempty"`
	Digest            string                       `json:"digest"`
}

func replayApplicationDigest(p snapshotReplayApplication) string {
	p.Digest = ""
	return workstore.Hash(jsonData(p))
}
func (m *Manager) replayApplicationFile(id string) string {
	return filepath.Join(m.snapshotReplayCoordinator().Local, "applications", id+".json")
}
func (m *Manager) loadReplayApplication(id string) (snapshotReplayApplication, error) {
	var p snapshotReplayApplication
	if !validID.MatchString(id) {
		return p, result.Fail("INVALID_INPUT", "共同回放需要明确计划 UUID", 2)
	}
	raw, err := workstore.ReadFile(m.replayApplicationFile(id))
	if err != nil {
		return p, err
	}
	if strict(raw, &p) != nil || p.Version != 1 || p.ID != id || p.Transaction.ID != id || p.NativeTransaction.ID != id || p.Digest != replayApplicationDigest(p) || !validDigest.MatchString(p.Digest) || !validDigest.MatchString(p.ActiveKey) || p.Native.Root != m.Root || p.Native.Source.Root != m.Source.Store.Root || p.Native.Source.Owner != m.Source.Owner || p.Native.Source.Scope != m.Source.Scope || p.Native.Source.Copy != m.Source.Copy || !validID.MatchString(p.Snapshot) || !validID.MatchString(p.Prepared) || !validDigest.MatchString(p.PreparedDigest) || !validDigest.MatchString(p.Candidate) || !validDigest.MatchString(p.ExecutableSHA) || !filepath.IsAbs(p.Executable) || filepath.Clean(p.Executable) != p.Executable || filepath.Join(p.Home, "local") != m.Source.Store.Local {
		return snapshotReplayApplication{}, result.Fail("PLAN_STALE", "共同回放计划的身份、材料或完整摘要不符", 3)
	}
	tx, err := m.snapshotReplayCoordinator().LoadPlan(id)
	if err != nil {
		return p, err
	}
	if string(jsonData(tx)) != string(jsonData(p.Transaction)) {
		return p, result.Fail("PLAN_STALE", "共同回放提案与原共同事务不一致", 3)
	}
	if workstore.Digest(replayAppliedTree(p)) != p.Native.Source.Snapshot || (tx.Action == "apply" && (p.Restore != nil || p.AppliedSource != nil)) || (tx.Action != "apply" && (p.Restore == nil || p.AppliedSource == nil)) {
		return p, result.Fail("PLAN_STALE", "共同提案的应用视图与已保存草稿角色不一致", 3)
	}
	if p.Restore != nil {
		seed, err := m.loadReplayReverse(p.Restore.Plan, p.Restore.Digest)
		if err != nil {
			return p, err
		}
		if len(seed.Views) != 1 || seed.Views[0].Binding != p.Restore.Binding || seed.Views[0].Snapshot != p.Snapshot || seed.Views[0].Commit != p.Commit || seed.Transaction.Action != tx.Action || seed.Transaction.Reference != tx.Reference {
			return p, result.Fail("PLAN_STALE", "重新生成提案的原应用或共同引用不一致", 3)
		}
		for i, target := range tx.Targets {
			original := seed.Transaction.Targets[i]
			if target.Path != original.Path || workstore.Digest(target.Before) != workstore.Digest(original.Before) || ((target.Path == "source" || target.Path == "local") && workstore.Digest(target.After) != workstore.Digest(original.After)) {
				return p, result.Fail("PLAN_STALE", "重新生成提案改变了原前镜像或已保存草稿", 3)
			}
		}
	}
	return p, nil
}

func (m *Manager) loadReplayAppliedRecord(ctx context.Context, a *projectsnapshot.Archive, r projectsnapshot.Record) (appliedRecord, error) {
	var out appliedRecord
	p, err := m.loadReplayApplication(r.Coordinated.ID)
	if err != nil {
		return out, err
	}
	if p.ActiveKey != r.ActiveKey || r.RestoredDraft != (p.Restore != nil) || workstore.Digest(replayAppliedTree(p)) != workstore.Digest(r.Source) || string(jsonData(p.Transaction)) != string(jsonData(r.Coordinated)) || string(jsonData(p.NativeTransaction)) != string(jsonData(r.Transaction)) || string(jsonData(p.Frozen)) != string(jsonData(r.Configuration)) || string(jsonData(p.Native.Source)) != string(jsonData(r.Effective)) || m.appliedKey(r.Effective, p.Sessions, r.Adapters, p.ExecutableSHA) != r.ActiveKey {
		return out, result.Fail("APPLIED_VIEW_INVALID", "共同回放视图与成功事务或归档不一致", 5)
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
		return out, result.Fail("APPLIED_VIEW_INVALID", "共同回放归档在读取期间变化", 5)
	}
	ledger, err := m.appliedRecordLedger(r)
	if err != nil {
		return out, err
	}
	// Runtime reuses the same frozen/current-authority reader. Only commit proof
	// comes from the owning common journal instead of an independent native one.
	plan := ProfileApplicationPlan{ID: p.ID, Root: m.Root, Executable: p.Executable, ExecutableSHA: p.ExecutableSHA, Home: p.Home, ActiveKey: p.ActiveKey, Request: ProfileApplicationRequest{Sessions: p.Sessions}}
	return appliedRecord{Archive: a, Record: r, Plan: plan, Frozen: source.FrozenMCP{Tree: r.Source, Effective: r.Effective, Configuration: *r.Configuration}, Ledger: ledger}, nil
}

func (m *Manager) withAppliedCommit(ctx context.Context, a appliedRecord, use func() error) error {
	if a.Record.Coordinated == nil {
		return m.profileApplicationBatch().WithCommitted(a.Record.Transaction.ID, a.Record.Transaction.Digest, func(j workstore.BatchJournal) error {
			if string(jsonData(j.Plan)) != string(jsonData(a.Record.Transaction)) {
				return result.Fail("APPLIED_VIEW_INVALID", "原始成功事务被替换", 5)
			}
			return use()
		})
	}
	// The outer frozen reader owns source/Local; WithCurrent owns the actual
	// native project lock. Every common writer takes those same locks. History
	// reads the original atomically-written immutable journal without reentry.
	return m.profileApplicationBatch().WithCurrent(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		js, err := m.snapshotReplayCoordinator().History()
		if err != nil {
			return err
		}
		for _, j := range js {
			if j.Plan.ID == a.Record.Coordinated.ID {
				if j.State != "committed" || string(jsonData(j.Plan)) != string(jsonData(a.Record.Coordinated)) {
					return result.Fail("APPLIED_VIEW_NOT_COMMITTED", "共同回放事务尚未提交或已变化", 4)
				}
				return use()
			}
		}
		return result.Fail("APPLIED_VIEW_NOT_COMMITTED", "缺少共同回放原始成功事务", 4)
	})
}
