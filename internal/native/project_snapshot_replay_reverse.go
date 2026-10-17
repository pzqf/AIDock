package native

import (
	"context"
	"path/filepath"
	"sort"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type replayRestoreView struct {
	Binding  string `json:"binding"`
	Snapshot string `json:"snapshot"`
	Commit   string `json:"commit"`
}
type snapshotReplayReverse struct {
	Version     int                 `json:"schema_version"`
	Transaction workstore.BatchPlan `json:"transaction"`
	Views       []replayRestoreView `json:"views"`
	Digest      string              `json:"digest"`
}

func replayReverseDigest(p snapshotReplayReverse) string {
	p.Digest = ""
	return workstore.Hash(jsonData(p))
}
func (m *Manager) replayReverseFile(id string) string {
	return filepath.Join(m.snapshotReplayCoordinator().Local, "reversals", id+".json")
}
func (m *Manager) loadReplayReverse(id, digest string) (snapshotReplayReverse, error) {
	var p snapshotReplayReverse
	if !validID.MatchString(id) || !validDigest.MatchString(digest) {
		return p, result.Fail("INVALID_INPUT", "回放撤销需要明确计划和完整摘要", 2)
	}
	raw, err := workstore.ReadFile(m.replayReverseFile(id))
	if err != nil {
		return p, err
	}
	if strict(raw, &p) != nil || p.Version != 1 || p.Transaction.ID != id || p.Digest != digest || replayReverseDigest(p) != digest || (p.Transaction.Action != "rollback" && p.Transaction.Action != "undo-rollback") {
		return snapshotReplayReverse{}, result.Fail("PLAN_STALE", "回放撤销提案的绑定或完整摘要不符", 3)
	}
	tx, err := m.snapshotReplayCoordinator().LoadPlan(id)
	if err != nil {
		return p, err
	}
	if string(jsonData(tx)) != string(jsonData(p.Transaction)) {
		return p, result.Fail("PLAN_STALE", "回放撤销提案与共同事务不一致", 3)
	}
	return p, nil
}

func (m *Manager) replayLedger(tx workstore.BatchPlan, after bool) (connectionLedger, error) {
	empty := connectionLedger{Tools: map[string]connectionTool{}}
	for _, t := range tx.Targets {
		if t.Path == "native/"+connectionLedgerPath {
			tree := t.Before
			if after {
				tree = t.After
			}
			if !tree.Exists {
				return empty, nil
			}
			store := &workstore.Store{Root: filepath.Join(m.Root, connectionLedgerPath), Local: m.snapshotReplayCoordinator().Local}
			body, err := store.Data(tree.Entries[workstore.FileContent])
			if err != nil {
				return empty, err
			}
			return m.decodeConnectionLedger(body)
		}
	}
	return empty, result.Fail("BACKUP_INVALID", "共同回放缺少原生字段归属根", 5)
}

func (m *Manager) replayRestoreViews(ctx context.Context, tx workstore.BatchPlan) ([]replayRestoreView, error) {
	before, err := m.replayLedger(tx, false)
	if err != nil {
		return nil, err
	}
	after, err := m.replayLedger(tx, true)
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	for tool, fields := range after.Tools {
		for name, next := range fields.Fields {
			if before.Tools[tool].Fields[name].ConfigSHA == next.ConfigSHA {
				continue
			}
			if next.AppliedKey == "" {
				return nil, result.Fail("REPLAY_LEGACY_RESTORE_REVIEW_REQUIRED", "重新引入的旧连接没有固定应用视图；须先协调旧安装的当前授权与参数", 4)
			}
			keys[next.AppliedKey] = true
		}
	}
	sorted := []string{}
	for key := range keys {
		sorted = append(sorted, key)
	}
	sort.Strings(sorted)
	out := []replayRestoreView{}
	for _, key := range sorted {
		a, err := m.loadAppliedRecord(ctx, key)
		if err != nil {
			return nil, err
		}
		for tool, fields := range after.Tools {
			for name, next := range fields.Fields {
				if next.AppliedKey == key && before.Tools[tool].Fields[name].ConfigSHA != next.ConfigSHA {
					if string(jsonData(next)) != string(jsonData(a.Ledger.Tools[tool].Fields[name])) {
						return nil, result.Fail("APPLIED_VIEW_INVALID", "撤销重新引入的字段与原成功应用不一致", 5)
					}
				}
			}
		}
		out = append(out, replayRestoreView{key, a.Record.ID, a.Record.Commit})
	}
	return out, nil
}

// Already inside the common coordinator's native project lock. This verifies
// only immutable commit proof, not current installation or current permission.
func (m *Manager) appliedCommitLocked(a appliedRecord) error {
	if a.Record.Coordinated == nil {
		j, err := m.profileApplicationBatch().CommittedJournal(a.Record.Transaction.ID, a.Record.Transaction.Digest)
		if err != nil {
			return err
		}
		if string(jsonData(j.Plan)) != string(jsonData(a.Record.Transaction)) {
			return result.Fail("APPLIED_VIEW_INVALID", "原生成功事务被替换", 5)
		}
		return nil
	}
	js, err := m.snapshotReplayCoordinator().History()
	if err != nil {
		return err
	}
	for _, j := range js {
		if j.Plan.ID == a.Record.Coordinated.ID {
			if j.State != "committed" || string(jsonData(j.Plan)) != string(jsonData(a.Record.Coordinated)) {
				return result.Fail("APPLIED_VIEW_NOT_COMMITTED", "原共同回放尚未成功提交或记录变化", 4)
			}
			return nil
		}
	}
	return result.Fail("APPLIED_VIEW_NOT_COMMITTED", "缺少原共同回放成功记录", 4)
}

func (m *Manager) reverseSnapshotReplay(ctx context.Context, id string) (snapshotReplayReverse, error) {
	var out snapshotReplayReverse
	if !validID.MatchString(id) {
		return out, result.Fail("INVALID_INPUT", "选择最新成功的共同回放事务", 2)
	}
	tx, err := m.snapshotReplayCoordinator().ReversePreserving(id, map[string][]string{"source": {".revisions", ".revision-records", ".governance-records", ".external-approvals"}})
	if err != nil {
		return out, err
	}
	views, err := m.replayRestoreViews(ctx, tx)
	if err != nil {
		return out, err
	}
	out = snapshotReplayReverse{Version: 1, Transaction: tx, Views: views}
	out.Digest = replayReverseDigest(out)
	if err = save(m.replayReverseFile(tx.ID), jsonData(out)); err != nil {
		return snapshotReplayReverse{}, err
	}
	return out, nil
}

func (m *Manager) applySnapshotReplayReverse(ctx context.Context, id, digest string) (workstore.BatchJournal, error) {
	var out workstore.BatchJournal
	p, err := m.loadReplayReverse(id, digest)
	if err != nil {
		return out, err
	}
	records := []appliedRecord{}
	for _, view := range p.Views {
		a, err := m.loadAppliedRecord(ctx, view.Binding)
		if err != nil {
			return out, err
		}
		if a.Record.ID != view.Snapshot || a.Record.Commit != view.Commit {
			return out, result.Fail("PLAN_STALE", "撤销所需的原应用归档变化", 3)
		}
		records = append(records, a)
	}
	validate := func(after bool) error {
		if err := m.Source.ValidateReplayIdentity(ctx, false); err != nil {
			return err
		}
		currentSource, currentLocal := replayApplicationTrees(snapshotReplayApplication{Transaction: p.Transaction}, after)
		for _, a := range records {
			if err := m.appliedCommitLocked(a); err != nil {
				return err
			}
			if err := m.appliedArchiveCurrent(ctx, a); err != nil {
				return err
			}
			if err := m.Source.ValidateFrozenMCPTransaction(ctx, a.Frozen, currentSource, currentLocal, a.Plan.Request.Sessions); err != nil {
				return err
			}
		}
		return nil
	}
	c := m.snapshotReplayCoordinator()
	c.Validate = func(tx workstore.BatchPlan) error {
		if string(jsonData(tx)) != string(jsonData(p.Transaction)) {
			return result.Fail("PLAN_STALE", "撤销共同事务变化", 3)
		}
		return validate(false)
	}
	c.BeforeCommit = func(workstore.BatchPlan) error { return validate(true) }
	return c.Apply(ctx, p.Transaction.ID, p.Transaction.Digest)
}
