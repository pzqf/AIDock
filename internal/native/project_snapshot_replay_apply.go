package native

import (
	"context"
	"os"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func replayApplicationTrees(p snapshotReplayApplication, after bool) (workstore.Tree, workstore.Tree) {
	var s, l workstore.Tree
	for _, t := range p.Transaction.Targets {
		tree := t.Before
		if after {
			tree = t.After
		}
		if t.Path == "source" {
			s = tree
		}
		if t.Path == "local" {
			l = tree
		}
	}
	return s, l
}

func replayAppliedTree(p snapshotReplayApplication) workstore.Tree {
	if p.AppliedSource != nil {
		return *p.AppliedSource
	}
	tree, _ := replayApplicationTrees(p, true)
	return tree
}

// Called only inside the coordinator's complete root locks. It never tries to
// reacquire source/Local/native locks and never starts tools or reads secrets.
func (m *Manager) validateReplayApplication(ctx context.Context, p snapshotReplayApplication, after bool) error {
	if err := m.Source.ValidateReplayIdentity(ctx, false); err != nil {
		return err
	}
	saved, err := m.loadReplayApplication(p.ID)
	if err != nil {
		return err
	}
	if saved.Digest != p.Digest || p.BridgeFile != m.Source.BridgeFile {
		return result.Fail("PLAN_STALE", "共同回放的原提案或外部桥绑定变化", 3)
	}
	sha, err := executableDigest(p.Executable)
	if err != nil || sha != p.ExecutableSHA {
		return result.Fail("PLAN_STALE", "共同回放使用的 AIDock 程序变化", 3)
	}
	a, err := m.projectSnapshotArchive()
	if err != nil {
		return err
	}
	original, err := a.Metadata(ctx, p.Snapshot)
	if err != nil {
		return err
	}
	if original.Commit != p.Commit {
		return result.Fail("PLAN_STALE", "原快照引用变化", 3)
	}
	if p.BridgeFile != "" {
		body, e := workstore.ReadFile(p.BridgeFile)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		digest := ""
		if e == nil {
			digest = workstore.Hash(body)
		}
		if digest != p.Native.Source.BridgeDigest {
			return result.Fail("PLAN_STALE", "外部桥保护登记变化", 3)
		}
	}
	raw, err := workstore.ReadFile(m.replayPreparedFile(p.Prepared))
	if err != nil {
		return err
	}
	var prepared snapshotReplayPrepared
	if strict(raw, &prepared) != nil || prepared.Digest != p.PreparedDigest || replayPreparedDigest(prepared) != p.PreparedDigest || string(jsonData(prepared.Native)) != string(jsonData(p.Native)) {
		return result.Fail("CHECK_CORRUPT", "共同回放的完整检查原件变化", 5)
	}
	if string(jsonData(prepared.Request.Restore)) != string(jsonData(p.Restore)) {
		return result.Fail("PLAN_STALE", "共同提案的原应用恢复目标与检查不一致", 3)
	}
	compiler, err := m.replayCompiler(p.Native.Source)
	if err != nil {
		return err
	}
	q, err := compiler.QualifyDraft(p.Native.Targets, p.Candidate, SkillsRulesScope, p.Native.ExcludedChecks, prepared.Report.Check)
	if err != nil {
		return err
	}
	if string(jsonData(q)) != string(jsonData(p.Native.Qualification)) {
		return result.Fail("CHECK_CORRUPT", "共同回放发现资格不符", 5)
	}
	for _, target := range p.Native.Targets {
		if target.Rule != nil && target.Rule.Override != nil {
			if target.Adapter.ID != "codex-cli" || target.Rule.Override.Path != "AGENTS.override.md" {
				return result.Fail("CHECK_CORRUPT", "共同回放包含未适配的规则遮盖观察路径", 5)
			}
			observed, err := compiler.Source.Store.CaptureFile(filepath.Join(m.Root, target.Rule.Override.Path))
			if err != nil {
				return err
			}
			if workstore.Digest(observed) != workstore.Digest(target.Rule.Override.Before) {
				return result.Fail("PLAN_STALE", "规则遮盖条件在共同回放前变化", 3)
			}
		}
	}
	currentSource, currentLocal := replayApplicationTrees(p, after)
	future := replayAppliedTree(p)
	return m.Source.ValidateFrozenMCPTransaction(ctx, source.FrozenMCP{Tree: future, Effective: p.Native.Source, Configuration: p.Frozen}, currentSource, currentLocal, p.Sessions)
}

func (m *Manager) applySnapshotReplayApplication(ctx context.Context, id, digest string) (workstore.BatchJournal, error) {
	var out workstore.BatchJournal
	p, err := m.loadReplayApplication(id)
	if err != nil {
		return out, err
	}
	if digest != p.Digest {
		return out, result.Fail("PLAN_CONFIRMATION_REQUIRED", "共同回放需要刚预览的完整摘要", 4)
	}
	ready, err := m.ReadSnapshotReplayPreparation(ctx, p.Prepared)
	if err != nil {
		return out, err
	}
	if ready.Digest != p.PreparedDigest || ready.Status != "CHECKED" || !ready.Fresh {
		return out, result.Fail("REPLAY_CHECK_REQUIRED", "共同回放的完整检查已过期，请重新检查", 4)
	}
	a, err := m.projectSnapshotArchive()
	if err != nil {
		return out, err
	}
	tree := replayAppliedTree(p)
	adapters := map[string]string{}
	for _, t := range p.Native.Targets {
		adapters[t.Adapter.ID] = t.Adapter.Version
	}
	restoreOf := ""
	if p.Restore != nil {
		restoreOf = p.Transaction.Reference
	}
	_, err = a.Capture(ctx, projectsnapshot.Content{Trigger: "aidock replay", Source: tree, Effective: p.Native.Source, Configuration: &p.Frozen, ActiveKey: p.ActiveKey, Adapters: adapters, Transaction: p.NativeTransaction, Coordinated: &p.Transaction, RestoredDraft: p.Restore != nil, RestoreOf: restoreOf}, m.Source.Store, m.connectionStore())
	if err != nil {
		return out, err
	}
	c := m.snapshotReplayCoordinator()
	c.Validate = func(tx workstore.BatchPlan) error {
		if string(jsonData(tx)) != string(jsonData(p.Transaction)) {
			return result.Fail("PLAN_STALE", "共同回放事务变化", 3)
		}
		return m.validateReplayApplication(ctx, p, false)
	}
	c.BeforeCommit = func(workstore.BatchPlan) error { return m.validateReplayApplication(ctx, p, true) }
	return c.Apply(ctx, p.Transaction.ID, p.Transaction.Digest)
}
