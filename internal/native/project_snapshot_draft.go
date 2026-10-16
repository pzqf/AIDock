package native

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/pzqf/AIDock/internal/privatefs"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Draft recovery is deliberately distinct from replaying an application. It
// changes only saved source and this copy's Local profile in one recoverable
// transaction, never tools, installations, permissions, audit or credentials.
type SnapshotDraftPreview struct {
	ID            string             `json:"id"`
	Digest        string             `json:"digest"`
	Snapshot      string             `json:"snapshot"`
	Action        string             `json:"action"`
	Project       string             `json:"project"`
	Scope         string             `json:"scope"`
	Copy          string             `json:"copy"`
	SourceRoot    string             `json:"source_root"`
	LocalRoot     string             `json:"local_root"`
	SourceChanges []workstore.Change `json:"source_changes"`
	LocalChanges  []workstore.Change `json:"local_changes"`
	Boundary      string             `json:"boundary"`
}

type snapshotDraftPlan struct {
	Version     int                 `json:"schema_version"`
	Project     string              `json:"project"`
	Scope       string              `json:"scope"`
	Copy        string              `json:"copy"`
	Snapshot    string              `json:"snapshot"`
	Commit      string              `json:"commit"`
	Inputs      string              `json:"inputs"`
	Projection  string              `json:"projection"`
	Transaction workstore.BatchPlan `json:"transaction"`
	Digest      string              `json:"digest"`
}

func (m *Manager) snapshotDraftArea() string {
	return filepath.Join(m.Source.Store.Local, "snapshot-draft-transactions")
}

func (m *Manager) snapshotDraftCoordinator() *workstore.Coordinator {
	return &workstore.Coordinator{Local: m.snapshotDraftArea(), Identity: "snapshot-draft/" + m.Source.Owner + "/" + m.Source.Scope + "/" + m.Source.Copy,
		Roots: []workstore.RootBinding{{Path: "source", Root: m.Source.Store.Root}, {Path: "local", Root: source.LocalProfileRoot(m.Source.Store.Local, m.Source.Owner, m.Source.Scope, m.Source.Copy)}},
		Blobs: m.Source.Store, Hook: m.snapshotDraftHook, RecoveryCommand: "snapshot draft recover"}
}

func snapshotDraftDigest(p snapshotDraftPlan) string {
	p.Digest = ""
	return workstore.Hash(jsonData(p))
}

func (m *Manager) snapshotDraftPlanFile(id string) string {
	key := workstore.Hash(jsonData([]string{m.Source.Owner, m.Source.Scope, m.Source.Copy, m.Source.Store.Root}))
	return filepath.Join(m.snapshotDraftArea(), "review", key, id+".json")
}

func draftPreview(p snapshotDraftPlan) SnapshotDraftPreview {
	out := SnapshotDraftPreview{ID: p.Transaction.ID, Digest: p.Digest, Snapshot: p.Snapshot, Action: p.Transaction.Action, Project: p.Project, Scope: p.Scope, Copy: p.Copy, SourceChanges: []workstore.Change{}, LocalChanges: []workstore.Change{}, Boundary: "仅恢复已保存草稿：历史共享配置转为当前项目覆盖，本机值单独保存。工具文件不变，当前安装、授权和凭据不回退，不启动程序；恢复后须重新检查、授权并预览应用。撤销草稿恢复可还原此前的已保存草稿，不能代替完整项目应用回退。"}
	for _, r := range p.Transaction.Roots {
		if r.Path == "source" {
			out.SourceRoot = r.Root
		}
		if r.Path == "local" {
			out.LocalRoot = r.Root
		}
	}
	for _, t := range p.Transaction.Targets {
		if t.Path == "source" {
			out.SourceChanges = workstore.Changes(t.Before, t.After)
		}
		if t.Path == "local" {
			out.LocalChanges = workstore.Changes(t.Before, t.After)
		}
	}
	return out
}

func (m *Manager) PrepareSnapshotDraft(ctx context.Context, id string) (SnapshotDraftPreview, error) {
	var out SnapshotDraftPreview
	a, err := m.projectSnapshotArchive()
	if err != nil {
		return out, err
	}
	r, err := a.Materialize(ctx, id, m.Source.Store)
	if err != nil {
		return out, err
	}
	if r.Configuration == nil {
		return out, result.Fail("SNAPSHOT_CONFIGURATION_MISSING", "旧快照未保留原始分层输入，不能自动恢复为历史有效草稿", 4)
	}
	draft, err := m.Source.DraftReplay(ctx, r.Source, r.Effective, *r.Configuration)
	if err != nil {
		return out, err
	}
	if len(workstore.Changes(draft.SourceBefore, draft.SourceAfter)) == 0 && len(workstore.Changes(draft.LocalBefore, draft.LocalAfter)) == 0 {
		return out, result.Fail("SNAPSHOT_DRAFT_CURRENT", "已保存草稿与此快照的项目投影一致，无需重复恢复", 3)
	}
	if err = privatefs.Ensure(m.snapshotDraftArea()); err != nil {
		return out, err
	}
	tx, err := m.snapshotDraftCoordinator().PreviewObserved("apply", []workstore.BatchTarget{{Path: "source", Before: draft.SourceBefore, After: draft.SourceAfter}, {Path: "local", Before: draft.LocalBefore, After: draft.LocalAfter}})
	if err != nil {
		return out, err
	}
	p := snapshotDraftPlan{Version: 1, Project: m.Source.Owner, Scope: m.Source.Scope, Copy: m.Source.Copy, Snapshot: r.ID, Commit: r.Commit, Inputs: r.Configuration.Digest, Projection: draft.Digest, Transaction: tx}
	p.Digest = snapshotDraftDigest(p)
	if err = save(m.snapshotDraftPlanFile(tx.ID), jsonData(p)); err != nil {
		return out, err
	}
	return draftPreview(p), nil
}

func (m *Manager) loadSnapshotDraft(id, digest string) (snapshotDraftPlan, error) {
	var p snapshotDraftPlan
	if !validID.MatchString(id) || !validDigest.MatchString(digest) {
		return p, result.Fail("INVALID_INPUT", "草稿恢复需要明确计划 UUID 与完整摘要", 2)
	}
	data, err := workstore.ReadFile(m.snapshotDraftPlanFile(id))
	if err != nil {
		return p, err
	}
	if strict(data, &p) != nil || p.Version != 1 || p.Project != m.Source.Owner || p.Scope != m.Source.Scope || p.Copy != m.Source.Copy || p.Transaction.ID != id || p.Digest != digest || snapshotDraftDigest(p) != digest || !validID.MatchString(p.Snapshot) || !validDigest.MatchString(p.Inputs) || !validDigest.MatchString(p.Projection) {
		return p, result.Fail("PLAN_STALE", "草稿恢复的绑定、快照或计划摘要不匹配", 3)
	}
	tx, err := m.snapshotDraftCoordinator().LoadPlan(id)
	if err != nil {
		return p, err
	}
	if workstore.Hash(jsonData(tx)) != workstore.Hash(jsonData(p.Transaction)) {
		return p, result.Fail("PLAN_STALE", "草稿恢复计划与共同事务不一致", 3)
	}
	return p, nil
}

func (m *Manager) ApplySnapshotDraft(ctx context.Context, id, digest string) (workstore.BatchJournal, error) {
	var out workstore.BatchJournal
	p, err := m.loadSnapshotDraft(id, digest)
	if err != nil {
		return out, err
	}
	if err = privatefs.Ensure(m.snapshotDraftArea()); err != nil {
		return out, err
	}
	c := m.snapshotDraftCoordinator()
	c.Validate = func(tx workstore.BatchPlan) error {
		if workstore.Hash(jsonData(tx)) != workstore.Hash(jsonData(p.Transaction)) {
			return result.Fail("PLAN_STALE", "草稿共同事务已变化", 3)
		}
		if tx.Action != "apply" {
			return nil
		} // Undo restores drafts, never authority.
		a, err := m.projectSnapshotArchive()
		if err != nil {
			return err
		}
		r, err := a.Load(ctx, p.Snapshot)
		if err != nil {
			return err
		}
		if r.Configuration == nil || r.Commit != p.Commit || r.Configuration.Digest != p.Inputs {
			return result.Fail("PLAN_STALE", "预览后快照或冻结输入已变化", 3)
		}
		return nil
	}
	return c.Apply(ctx, p.Transaction.ID, p.Transaction.Digest)
}

type SnapshotDraftHistoryItem struct {
	ID         string `json:"id"`
	Snapshot   string `json:"snapshot"`
	Action     string `json:"action"`
	State      string `json:"state"`
	At         string `json:"at"`
	Restorable bool   `json:"restorable"`
}

func (m *Manager) SnapshotDraftHistory() ([]SnapshotDraftHistoryItem, error) {
	items := []SnapshotDraftHistoryItem{}
	js, err := m.snapshotDraftCoordinator().History()
	if err != nil {
		return nil, err
	}
	latest := false
	for _, j := range js {
		data, err := workstore.ReadFile(m.snapshotDraftPlanFile(j.Plan.ID))
		if err != nil {
			return nil, err
		}
		var p snapshotDraftPlan
		if strict(data, &p) != nil {
			return nil, result.Fail("BACKUP_INVALID", "草稿恢复关联记录损坏", 5)
		}
		p, err = m.loadSnapshotDraft(j.Plan.ID, p.Digest)
		if err != nil {
			return nil, err
		}
		if workstore.Hash(jsonData(p.Transaction)) != workstore.Hash(jsonData(j.Plan)) {
			return nil, result.Fail("BACKUP_INVALID", "草稿恢复关联与事务不一致", 5)
		}
		restorable := !latest && j.State == "committed"
		if j.State == "committed" {
			latest = true
		}
		items = append(items, SnapshotDraftHistoryItem{ID: j.Plan.ID, Snapshot: p.Snapshot, Action: j.Plan.Action, State: j.State, At: j.Updated, Restorable: restorable})
	}
	return items, nil
}
func (m *Manager) RecoverSnapshotDraft() (int, error) {
	r, err := m.InspectSnapshotDraftRecovery()
	if err != nil {
		return 0, err
	}
	if !r.Pending {
		return 0, nil
	}
	if err = privatefs.Ensure(m.snapshotDraftArea()); err != nil {
		return 0, err
	}
	return m.snapshotDraftCoordinator().Recover()
}
func (m *Manager) InspectSnapshotDraftRecovery() (workstore.RecoveryInspection, error) {
	c := m.snapshotDraftCoordinator()
	js, err := c.History()
	if err != nil {
		return workstore.RecoveryInspection{}, err
	}
	if len(js) == 0 {
		markers := false
		for _, root := range c.Roots {
			p := workstore.CoordinatedMarker(root.Root)
			if err = workstore.CheckLinks(p); err != nil {
				return workstore.RecoveryInspection{}, err
			}
			if _, err = os.Lstat(p); err == nil {
				markers = true
			} else if !os.IsNotExist(err) {
				return workstore.RecoveryInspection{}, err
			}
		}
		if !markers {
			return workstore.RecoveryInspection{Version: 1, Observed: time.Now().UTC().Format(time.RFC3339Nano), Identity: c.Identity, Status: "IDLE", Transactions: []workstore.RecoveryTransaction{}, Issues: []workstore.RecoveryIssue{}, Boundary: "当前绑定没有草稿共同事务或恢复标记；未创建缺失的本机配置目录。其它类型事务须使用原恢复入口。"}, nil
		}
	}
	return c.InspectRecovery()
}

func (m *Manager) ReverseSnapshotDraft(ctx context.Context, transaction string) (SnapshotDraftPreview, error) {
	var out SnapshotDraftPreview
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !validID.MatchString(transaction) {
		return out, result.Fail("INVALID_INPUT", "选择当前绑定最新成功的草稿恢复事务", 2)
	}
	data, err := workstore.ReadFile(m.snapshotDraftPlanFile(transaction))
	if err != nil {
		return out, err
	}
	var prior snapshotDraftPlan
	if strict(data, &prior) != nil {
		return out, result.Fail("PLAN_STALE", "原草稿恢复记录不可读取", 3)
	}
	prior, err = m.loadSnapshotDraft(transaction, prior.Digest)
	if err != nil {
		return out, err
	}
	if err = privatefs.Ensure(m.snapshotDraftArea()); err != nil {
		return out, err
	}
	tx, err := m.snapshotDraftCoordinator().ReversePreserving(transaction, map[string][]string{"source": {".revisions", ".revision-records", ".governance-records", ".external-approvals"}})
	if err != nil {
		return out, err
	}
	prior.Transaction, prior.Digest = tx, ""
	prior.Digest = snapshotDraftDigest(prior)
	if err = save(m.snapshotDraftPlanFile(tx.ID), jsonData(prior)); err != nil {
		return out, err
	}
	return draftPreview(prior), nil
}
