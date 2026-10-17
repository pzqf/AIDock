package native

import (
	"context"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
)

type SnapshotReplayRestoreTarget struct {
	Plan    string `json:"plan_id"`
	Digest  string `json:"digest"`
	Binding string `json:"applied_binding"`
}

type SnapshotReplayReviewTarget struct {
	Snapshot string                       `json:"snapshot_id"`
	Restore  *SnapshotReplayRestoreTarget `json:"restore,omitempty"`
}

func parseReplayRestoreTarget(value any) (*SnapshotReplayRestoreTarget, error) {
	bad := func() (*SnapshotReplayRestoreTarget, error) {
		return nil, result.Fail("INVALID_INPUT", "恢复目标需要明确回退提案、完整摘要和原应用绑定", 2)
	}
	fields, ok := value.(map[string]any)
	if !ok || len(fields) != 3 {
		return bad()
	}
	var out SnapshotReplayRestoreTarget
	for k, v := range fields {
		s, ok := v.(string)
		if !ok {
			return bad()
		}
		switch k {
		case "plan_id":
			out.Plan = s
		case "digest":
			out.Digest = s
		case "applied_binding":
			out.Binding = s
		default:
			return bad()
		}
	}
	if !validID.MatchString(out.Plan) || !validDigest.MatchString(out.Digest) || !validDigest.MatchString(out.Binding) {
		return bad()
	}
	return &out, nil
}

func ParseSnapshotReplayReviewRequest(raw []byte) (string, *SnapshotReplayRestoreTarget, error) {
	bad := func() (string, *SnapshotReplayRestoreTarget, error) {
		return "", nil, result.Fail("INVALID_INPUT", "需要快照身份和可选的明确恢复目标，拒绝未知字段或别名", 2)
	}
	if len(raw) > 65536 {
		return bad()
	}
	v, err := config.Parse(raw)
	if err != nil {
		return bad()
	}
	f, ok := v.(map[string]any)
	if !ok || len(f) < 1 || len(f) > 2 {
		return bad()
	}
	id, ok := f["snapshot_id"].(string)
	if !ok || !validID.MatchString(id) {
		return bad()
	}
	for key := range f {
		if key != "snapshot_id" && key != "restore" {
			return bad()
		}
	}
	var restore *SnapshotReplayRestoreTarget
	if value, exists := f["restore"]; exists {
		restore, err = parseReplayRestoreTarget(value)
		if err != nil {
			return bad()
		}
	}
	return id, restore, nil
}

func (m *Manager) snapshotReplayCandidateFor(ctx context.Context, id string, restore *SnapshotReplayRestoreTarget) (source.ReplayDraft, string, error) {
	if restore == nil {
		return m.snapshotReplayCandidate(ctx, id)
	}
	var empty source.ReplayDraft
	p, err := m.loadReplayReverse(restore.Plan, restore.Digest)
	if err != nil {
		return empty, "", err
	}
	if err = m.snapshotReplayCoordinator().ObservePlan(p.Transaction.ID, p.Transaction.Digest); err != nil {
		return empty, "", err
	}
	for _, view := range p.Views {
		if view.Binding == restore.Binding && view.Snapshot == id {
			a, err := m.loadAppliedRecord(ctx, view.Binding)
			if err != nil {
				return empty, "", err
			}
			if a.Record.ID != view.Snapshot || a.Record.Commit != view.Commit {
				return empty, "", result.Fail("PLAN_STALE", "原应用恢复归档变化", 3)
			}
			d, err := m.Source.FrozenReplayCandidate(ctx, a.Frozen)
			return d, a.Record.Commit, err
		}
	}
	return empty, "", result.Fail("INVALID_INPUT", "所选快照或绑定不属于这个回退提案的原应用", 2)
}
