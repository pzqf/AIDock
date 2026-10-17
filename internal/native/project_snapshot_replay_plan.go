package native

import (
	"context"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func (m *Manager) planSnapshotReplayApplication(ctx context.Context, id, digest, executable, home string) (snapshotReplayApplication, error) {
	var out snapshotReplayApplication
	checked, err := m.ReadSnapshotReplayPreparation(ctx, id)
	if err != nil {
		return out, err
	}
	if checked.Digest != digest || checked.Status != "CHECKED" || !checked.Fresh {
		return out, result.Fail("REPLAY_CHECK_REQUIRED", "共同回放需要当前完整候选检查及其完整摘要", 4)
	}
	raw, err := workstore.ReadFile(m.replayPreparedFile(id))
	if err != nil {
		return out, err
	}
	var prepared snapshotReplayPrepared
	if strict(raw, &prepared) != nil || prepared.Digest != digest || replayPreparedDigest(prepared) != digest || prepared.Native.Qualification == nil || prepared.Executable != executable || prepared.Home != home {
		return out, result.Fail("CHECK_CORRUPT", "候选检查材料、程序或数据根与共同回放不一致", 5)
	}
	d, commit, err := m.snapshotReplayCandidateFor(ctx, prepared.Request.Snapshot, prepared.Request.Restore)
	if err != nil {
		return out, err
	}
	if d.Digest != prepared.Request.Candidate || commit != prepared.Commit {
		return out, result.Fail("PLAN_STALE", "共同回放候选或归档已变化", 3)
	}
	out = snapshotReplayApplication{Version: 1, Snapshot: prepared.Request.Snapshot, Commit: commit, Candidate: d.Digest, Prepared: id, PreparedDigest: digest, Executable: executable, ExecutableSHA: prepared.ExecutableSHA, Home: home, BridgeFile: m.Source.BridgeFile, Native: prepared.Native, Sessions: []source.MCPSessionRequest{}}
	out.Restore = prepared.Request.Restore
	var seed snapshotReplayReverse
	var images map[string]workstore.Tree
	if out.Restore != nil {
		seed, images, err = m.replayRestoreImages(out.Restore, checked.Tools)
		if err != nil {
			return out, err
		}
		tree := workstore.Clone(d.SourceAfter)
		out.AppliedSource = &tree
	}
	err = m.Source.WithReplaySnapshotInputs(ctx, d, func(bundle source.Export, _ workstore.Tree, cfg source.SnapshotConfiguration) error {
		if string(jsonData(bundle)) != string(jsonData(prepared.Native.Source)) {
			return result.Fail("PLAN_STALE", "候选源导出与已检查材料不同", 3)
		}
		out.Frozen = cfg
		return nil
	})
	if err != nil {
		return out, err
	}
	adapters := map[string]string{}
	for _, t := range prepared.Native.Targets {
		adapters[t.Adapter.ID] = t.Adapter.Version
	}
	native := []workstore.BatchTarget{}
	err = m.Source.WithReplayMCPClientConfigs(ctx, d, executable, home, func(configs []source.MCPClientConfiguration, sessions []source.MCPSessionRequest) error {
		requests, pointers := []MCPConnectionRequest{}, []*source.MCPClientConfiguration{}
		for i, s := range sessions {
			requests = append(requests, MCPConnectionRequest{Action: "install", Tools: checked.Tools, Session: s})
			pointers = append(pointers, &configs[i])
		}
		observed, err := m.compileConnectionSet(checked.Tools, requests, pointers, executable, home, true)
		if err != nil {
			return err
		}
		if string(jsonData(observed)) != string(jsonData(prepared.Connections)) {
			return result.Fail("PLAN_STALE", "共同回放连接与已检查候选不同", 3)
		}
		out.Sessions = append(out.Sessions, sessions...)
		out.ActiveKey = m.appliedKey(prepared.Native.Source, out.Sessions, adapters, prepared.ExecutableSHA)
		pointers = []*source.MCPClientConfiguration{}
		for i := range configs {
			configs[i] = m.appliedConfiguration(configs[i], out.ActiveKey)
			pointers = append(pointers, &configs[i])
		}
		connections, err := m.compileConnectionImages(images, checked.Tools, requests, pointers, executable, home, true)
		if err != nil {
			return err
		}
		native = append(batchTargets(prepared.Native), connections...)
		return nil
	})
	if err != nil {
		return out, err
	}
	var tx workstore.BatchPlan
	if out.Restore != nil {
		tx, native, err = m.reviseReplayRestore(seed, native)
	} else {
		var targets []workstore.BatchTarget
		targets, err = m.snapshotReplayTargets(d, native)
		if err == nil {
			tx, err = m.snapshotReplayCoordinator().PreviewObserved("apply", targets)
		}
	}
	if err != nil {
		return out, err
	}
	out.ID, out.Transaction = tx.ID, tx
	out.NativeTransaction = workstore.BatchPlan{Version: 1, Kind: "native-targets", ID: tx.ID, Root: m.Root, Identity: "profile/" + m.Source.Owner + "/" + m.Source.Scope + "/" + m.Source.Copy, Action: "apply", Targets: native}
	out.NativeTransaction.Digest = workstore.BatchPlanDigest(out.NativeTransaction)
	out.Digest = replayApplicationDigest(out)
	if err = save(m.replayApplicationFile(out.ID), jsonData(out)); err != nil {
		return snapshotReplayApplication{}, err
	}
	return out, nil
}
