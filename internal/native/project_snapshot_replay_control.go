package native

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

type SnapshotReplayPreview struct {
	RestoreTargets []SnapshotReplayReviewTarget `json:"restore_targets,omitempty"`
	ID             string                       `json:"id"`
	Digest         string                       `json:"digest"`
	Action         string                       `json:"action"`
	Snapshot       string                       `json:"snapshot_id,omitempty"`
	Project        string                       `json:"project"`
	Scope          string                       `json:"scope"`
	Copy           string                       `json:"copy"`
	Root           string                       `json:"root"`
	SourceRoot     string                       `json:"source_root"`
	LocalRoot      string                       `json:"local_root"`
	Regenerated    bool                         `json:"regenerated_restore,omitempty"`
	CanApply       bool                         `json:"can_apply"`
	Code           string                       `json:"code,omitempty"`
	SourceChanges  []workstore.Change           `json:"source_changes"`
	LocalChanges   []workstore.Change           `json:"local_changes"`
	NativeChanges  []ReplayNativeChange         `json:"native_changes"`
	Boundary       string                       `json:"boundary"`
}
type SnapshotReplayResult struct {
	ID    string `json:"id"`
	State string `json:"state"`
}
type SnapshotReplayHistoryItem struct {
	ID         string `json:"id"`
	Action     string `json:"action"`
	State      string `json:"state"`
	At         string `json:"at"`
	CanReverse bool   `json:"can_reverse"`
}

func (m *Manager) replayPreview(tx workstore.BatchPlan, digest, snapshot string) SnapshotReplayPreview {
	out := SnapshotReplayPreview{ID: tx.ID, Digest: digest, Action: tx.Action, Snapshot: snapshot, Project: m.Source.Owner, Scope: m.Source.Scope, Copy: m.Source.Copy, Root: m.Root, CanApply: true, SourceChanges: []workstore.Change{}, LocalChanges: []workstore.Change{}, NativeChanges: []ReplayNativeChange{}, Boundary: "一次确认共同处理规范源、本机覆盖与全部工具目标，保留完整前镜像和原草稿；提交前后重新核验当前权限和输入，失败整批恢复。安装不运行插件或授予新权限。回退与撤销分别预览，原权限已变化的连接仍须独立重新审查；历史授权不回放。记忆、会话、审计、凭据和 Git 不作为回退对象。"}
	out.SourceRoot, out.LocalRoot = m.Source.Store.Root, source.LocalProfileRoot(m.Source.Store.Local, m.Source.Owner, m.Source.Scope, m.Source.Copy)
	for _, t := range tx.Targets {
		switch t.Path {
		case "source":
			out.SourceChanges = workstore.Changes(t.Before, t.After)
		case "local":
			out.LocalChanges = workstore.Changes(t.Before, t.After)
		default:
			out.NativeChanges = append(out.NativeChanges, ReplayNativeChange{Path: t.Path, Changes: workstore.Changes(t.Before, t.After), Before: workstore.Digest(t.Before), After: workstore.Digest(t.After)})
		}
	}
	return out
}

func replayBlocked(out SnapshotReplayPreview, err error) SnapshotReplayPreview {
	out.CanApply = false
	out.Code = "REPLAY_PROPOSAL_STALE"
	var e *result.Error
	if errors.As(err, &e) {
		out.Code = e.Code
	}
	out.SourceChanges, out.LocalChanges, out.NativeChanges = []workstore.Change{}, []workstore.Change{}, []ReplayNativeChange{}
	return out
}

func (m *Manager) PlanSnapshotReplay(ctx context.Context, prepared, digest, executable, home string) (SnapshotReplayPreview, error) {
	p, err := m.planSnapshotReplayApplication(ctx, prepared, digest, executable, home)
	if err != nil {
		return SnapshotReplayPreview{}, err
	}
	out := m.replayPreview(p.Transaction, p.Digest, p.Snapshot)
	out.Regenerated = p.Restore != nil
	return out, nil
}

// A regenerated restore is an application with a rollback action. Dispatch by
// its unique associated record, never by action alone or a generic Apply call.
func (m *Manager) replayProposalApplication(id string) (bool, error) {
	application, reversal := false, false
	for i, path := range []string{m.replayApplicationFile(id), m.replayReverseFile(id)} {
		if err := workstore.CheckLinks(path); err != nil {
			return false, err
		}
		_, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return false, err
		}
		if err == nil {
			if i == 0 {
				application = true
			} else {
				reversal = true
			}
		}
	}
	if application == reversal {
		return false, result.Fail("BACKUP_INVALID", "共同回放缺少唯一关联提案，不能按动作推断或执行", 5)
	}
	return application, nil
}

func (m *Manager) ReadSnapshotReplayPlan(ctx context.Context, id, digest string) (SnapshotReplayPreview, error) {
	c := m.snapshotReplayCoordinator()
	c.Validate = func(workstore.BatchPlan) error { return m.Source.ValidateReplayIdentity(ctx, false) }
	tx, err := c.LoadPlan(id)
	if err != nil {
		return SnapshotReplayPreview{}, err
	}
	application, err := m.replayProposalApplication(id)
	if err != nil {
		return SnapshotReplayPreview{}, err
	}
	if application {
		p, err := m.loadReplayApplication(id)
		if err != nil {
			return SnapshotReplayPreview{}, err
		}
		if p.Digest != digest {
			return SnapshotReplayPreview{}, result.Fail("PLAN_CONFIRMATION_REQUIRED", "需要完整回放提案摘要", 4)
		}
		out := m.replayPreview(tx, p.Digest, p.Snapshot)
		out.Regenerated = p.Restore != nil
		if err = c.ObservePlan(tx.ID, tx.Digest); err != nil {
			return replayBlocked(out, err), nil
		}
		ready, err := m.ReadSnapshotReplayPreparation(ctx, p.Prepared)
		if err != nil {
			return replayBlocked(out, err), nil
		}
		if !ready.Fresh || ready.Status != "CHECKED" || ready.Digest != p.PreparedDigest {
			return replayBlocked(out, result.Fail("REPLAY_CHECK_REQUIRED", "候选检查已过期", 4)), nil
		}
		return out, nil
	}
	p, err := m.loadReplayReverse(id, digest)
	if err != nil {
		return SnapshotReplayPreview{}, err
	}
	out := m.replayPreview(tx, p.Digest, "")
	for _, view := range p.Views {
		out.RestoreTargets = append(out.RestoreTargets, SnapshotReplayReviewTarget{Snapshot: view.Snapshot, Restore: &SnapshotReplayRestoreTarget{Plan: p.Transaction.ID, Digest: p.Digest, Binding: view.Binding}})
	}
	if err = c.ObservePlan(tx.ID, tx.Digest); err != nil {
		return replayBlocked(out, err), nil
	}
	for _, v := range p.Views {
		a, err := m.loadAppliedRecord(ctx, v.Binding)
		if err != nil {
			return replayBlocked(out, err), nil
		}
		if a.Record.ID != v.Snapshot || a.Record.Commit != v.Commit {
			return replayBlocked(out, result.Fail("PLAN_STALE", "原应用归档变化", 3)), nil
		}
		err = m.Source.WithFrozenMCPClientConfigs(ctx, a.Frozen, a.Plan.Request.Sessions, a.Plan.Executable, a.Plan.Home, func([]source.MCPClientConfiguration) error { return m.appliedArchiveCurrent(ctx, a) })
		if err != nil {
			return replayBlocked(out, err), nil
		}
	}
	return out, nil
}

func (m *Manager) ApplySnapshotReplay(ctx context.Context, id, digest string) (SnapshotReplayResult, error) {
	_, err := m.snapshotReplayCoordinator().LoadPlan(id)
	if err != nil {
		return SnapshotReplayResult{}, err
	}
	var j workstore.BatchJournal
	application, err := m.replayProposalApplication(id)
	if err != nil {
		return SnapshotReplayResult{}, err
	}
	if application {
		j, err = m.applySnapshotReplayApplication(ctx, id, digest)
	} else {
		j, err = m.applySnapshotReplayReverse(ctx, id, digest)
	}
	if err != nil {
		return SnapshotReplayResult{}, err
	}
	return SnapshotReplayResult{ID: j.Plan.ID, State: j.State}, nil
}

func (m *Manager) ReverseSnapshotReplay(ctx context.Context, id string) (SnapshotReplayPreview, error) {
	p, err := m.reverseSnapshotReplay(ctx, id)
	if err != nil {
		return SnapshotReplayPreview{}, err
	}
	return m.ReadSnapshotReplayPlan(ctx, p.Transaction.ID, p.Digest)
}
func (m *Manager) SnapshotReplayHistory() ([]SnapshotReplayHistoryItem, error) {
	js, err := m.snapshotReplayHistoryRecords()
	if err != nil {
		return nil, err
	}
	out := []SnapshotReplayHistoryItem{}
	latest := false
	for _, j := range js {
		application, e := m.replayProposalApplication(j.Plan.ID)
		if e != nil {
			return nil, e
		}
		if application {
			if _, err = m.loadReplayApplication(j.Plan.ID); err != nil {
				return nil, err
			}
		} else {
			raw, e := workstore.ReadFile(m.replayReverseFile(j.Plan.ID))
			if e != nil {
				return nil, e
			}
			var p snapshotReplayReverse
			if strict(raw, &p) != nil {
				return nil, result.Fail("BACKUP_INVALID", "回放撤销关联记录损坏", 5)
			}
			if _, err = m.loadReplayReverse(j.Plan.ID, p.Digest); err != nil {
				return nil, err
			}
		}
		can := !latest && j.State == "committed"
		latest = latest || j.State == "committed"
		out = append(out, SnapshotReplayHistoryItem{j.Plan.ID, j.Plan.Action, j.State, j.Updated, can})
	}
	return out, nil
}

func (m *Manager) InspectSnapshotReplayRecovery() (workstore.RecoveryInspection, error) {
	c := m.snapshotReplayCoordinator()
	js, err := m.snapshotReplayHistoryRecords()
	if err != nil {
		return workstore.RecoveryInspection{}, err
	}
	if len(js) == 0 {
		markers := false
		for _, r := range c.Roots {
			p := workstore.CoordinatedMarker(r.Root)
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
			return workstore.RecoveryInspection{Version: 1, Observed: time.Now().UTC().Format(time.RFC3339Nano), Identity: c.Identity, Status: "IDLE", Transactions: []workstore.RecoveryTransaction{}, Issues: []workstore.RecoveryIssue{}, Boundary: "当前绑定没有共同回放恢复记录或标记；未创建缺失目录。其它事务使用原入口。"}, nil
		}
	}
	return c.InspectRecovery()
}
func (m *Manager) RecoverSnapshotReplay() (int, error) {
	r, err := m.InspectSnapshotReplayRecovery()
	if err != nil {
		return 0, err
	}
	if !r.Pending {
		return 0, nil
	}
	return m.snapshotReplayCoordinator().Recover()
}
