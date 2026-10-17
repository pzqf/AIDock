package engine

import (
	"context"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
)

type ReplayOutcome struct {
	Kind        string                                 `json:"kind"`
	Snapshot    string                                 `json:"snapshot_id"`
	Action      string                                 `json:"action"`
	CanApply    bool                                   `json:"can_apply"`
	Code        string                                 `json:"code,omitempty"`
	Review      *native.SnapshotReplayPermissionReview `json:"review,omitempty"`
	Project     *native.SnapshotReplayPreview          `json:"project_plan,omitempty"`
	Publication *replica.PublicationReplayPreview      `json:"publication_plan,omitempty"`
	Result      any                                    `json:"result,omitempty"`
	Boundary    string                                 `json:"boundary"`
}

func (e *Engine) replicaRecoveryReader() (*replica.Manager, error) {
	if e.SourceRoot == "" {
		return nil, result.Fail("SOURCE_BINDING_REQUIRED", "副本回放需要原发起项目的明确源、项目及作用域绑定", 4)
	}
	if e.GlobalRoot != "" {
		return nil, result.Fail("INVALID_SOURCE_BINDING", "副本恢复不能使用全局技能管理绑定", 2)
	}
	s, err := source.New(e.SourceRoot, e.Roots.Local, e.SourceOwner, e.SourceScope)
	if err != nil {
		return nil, err
	}
	if err = s.BindBridgeObservation(e.BridgeFile, e.SourceCopy); err != nil {
		return nil, err
	}
	return replica.OpenRecovery(s)
}

func (e *Engine) resolveReplayKind(ctx context.Context, kind, id string) (string, error) {
	project, publication := false, false
	if kind != "replica" && e.NativeRoot != "" {
		m, err := e.SnapshotReader()
		if err != nil {
			return "", err
		}
		rows, err := m.ProjectSnapshots(ctx)
		if err != nil {
			return "", err
		}
		for _, row := range rows.Items {
			project = project || row.ID == id
		}
	}
	if kind != "project" {
		m, err := e.replicaRecoveryReader()
		if err != nil {
			return "", err
		}
		_, publication, err = m.FindPublicationSnapshot(id)
		if err != nil {
			return "", err
		}
	}
	if project && publication {
		return "", result.Fail("SNAPSHOT_AMBIGUOUS", "节点同时匹配项目与副本批次，请明确 --kind project 或 replica", 4)
	}
	if project {
		return "project", nil
	}
	if publication {
		return "replica", nil
	}
	if kind == "project" && e.NativeRoot == "" {
		return "", result.Fail("NATIVE_BINDING_REQUIRED", "项目回放需要明确的原生工具项目根", 4)
	}
	return "", result.Fail("SNAPSHOT_NOT_FOUND", "当前绑定中没有匹配的项目快照或成功副本恢复节点", 2)
}

// Replay unifies target selection and explicit confirmation. Current grants,
// candidate probes and dependency verification remain with their owning
// managers; dispatch does not manufacture approvals or run a tool implicitly.
func (e *Engine) Replay(ctx context.Context, req ReplayRequest, executable string) (ReplayOutcome, error) {
	var out ReplayOutcome
	if err := validateReplayRequest(req); err != nil {
		return out, err
	}
	kind, err := e.resolveReplayKind(ctx, req.Kind, req.Snapshot)
	if err != nil {
		return out, err
	}
	action := req.Action
	if action == "" {
		if kind == "project" {
			action = "review"
		} else {
			action = "plan"
		}
	}
	out = ReplayOutcome{Kind: kind, Snapshot: req.Snapshot, Action: action, Boundary: "项目快照与副本批次采用各自原事务；默认只读审查或生成预览。确认写入需要本目标的完整提案摘要，不回放旧授权、凭据或会话，不自动执行探测、安装依赖或传播局部回退。"}
	if kind == "project" {
		if req.Copies != nil {
			return out, result.Fail("INVALID_INPUT", "项目快照不能通过 copies 扩展为副本批次", 2)
		}
		m, err := e.SnapshotReader()
		if err != nil {
			return out, err
		}
		switch action {
		case "review":
			r, err := m.ReviewSnapshotReplayPermissions(ctx, req.Snapshot)
			if err != nil {
				return out, err
			}
			out.Review, out.Code = &r, "REPLAY_CHECK_REQUIRED"
		case "plan":
			if req.Prepared == "" {
				return out, result.Fail("REPLAY_CHECK_REQUIRED", "先审查精确候选并完成完整检查，使用 --prepared 与检查摘要生成提案", 4)
			}
			check, err := m.ReadSnapshotReplayPreparation(ctx, req.Prepared)
			if err != nil {
				return out, err
			}
			if check.Snapshot != req.Snapshot {
				return out, result.Fail("REPLAY_TARGET_MISMATCH", "完整候选检查属于另一个快照，不能替换本次回退目标", 2)
			}
			r, err := m.PlanSnapshotReplay(ctx, req.Prepared, req.Digest, executable, e.Roots.Home)
			if err != nil {
				return out, err
			}
			out.Project, out.CanApply, out.Code = &r, r.CanApply, r.Code
		case "planned", "apply":
			r, err := m.ReadSnapshotReplayPlan(ctx, req.Plan, req.Digest)
			if err != nil {
				return out, err
			}
			if r.Snapshot != req.Snapshot {
				return out, result.Fail("REPLAY_TARGET_MISMATCH", "回放提案不是本次明确选择的项目快照", 2)
			}
			out.Project, out.CanApply, out.Code = &r, r.CanApply, r.Code
			if action == "apply" && r.CanApply {
				out.Result, err = m.ApplySnapshotReplay(ctx, req.Plan, req.Digest)
				if err != nil {
					return out, err
				}
				out.Project, out.CanApply, out.Action = nil, false, "committed"
			}
		}
		return out, nil
	}
	if req.Prepared != "" || action == "review" {
		return out, result.Fail("INVALID_INPUT", "副本恢复从原批次备份生成提案，不接收项目候选检查或授权审查参数", 2)
	}
	m, err := e.replicaRecoveryReader()
	if err != nil {
		return out, err
	}
	if action == "plan" {
		r, err := m.PlanPublicationReplay(ctx, req.Snapshot, req.Copies)
		if err != nil {
			return out, err
		}
		out.Publication, out.CanApply, out.Code = &r, r.CanApply, r.Code
		return out, nil
	}
	ref, found, err := m.FindPublicationSnapshot(req.Snapshot)
	if err != nil {
		return out, err
	}
	if !found {
		return out, result.Fail("SNAPSHOT_NOT_FOUND", "副本恢复节点已不可用", 2)
	}
	r, err := m.ReadPublicationReplay(ctx, ref.Publication, req.Plan, req.Digest)
	if err != nil {
		return out, err
	}
	if r.Snapshot != req.Snapshot {
		return out, result.Fail("REPLAY_TARGET_MISMATCH", "提案属于另一个明确恢复节点，不能替换目标", 2)
	}
	out.Publication, out.CanApply, out.Code = &r, r.CanApply, r.Code
	if action == "apply" && r.CanApply {
		out.Result, err = m.ApplyPublicationReplay(ctx, ref.Publication, req.Plan, req.Digest)
		if err != nil {
			return out, err
		}
		out.Publication, out.CanApply, out.Action = nil, false, "committed"
	}
	return out, nil
}
