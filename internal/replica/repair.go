package replica

import (
	"context"
	"path/filepath"
	"time"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// StageRepair observes each selected target's own saved source. Comparison is
// an observation guard, not authority to adopt publisher content or a baseline.
func (m *Manager) StageRepair(ctx context.Context, req GenerationRepairRequest) (Generation, error) {
	scope, err := native.NormalizeApplicationScope(req.ApplicationScope)
	if err != nil {
		return Generation{}, err
	}
	if len(req.Tools) < 1 || len(req.Tools) > 2 {
		return Generation{}, result.Fail("TOOL_REQUIRED", "明确选择一到两个目标工具", 2)
	}
	seen := map[string]bool{}
	for _, tool := range req.Tools {
		if mirrorDirectory(tool) == "" || seen[tool] {
			return Generation{}, result.Fail("TOOL_UNSUPPORTED", "生成工具未知或重复", 2)
		}
		seen[tool] = true
	}
	var p Resolution
	err = m.Control.Locked(func() error {
		if err := m.Control.Pending(); err != nil {
			return err
		}
		control, err := m.Control.Capture(m.Control.Root)
		if err != nil {
			return err
		}
		report, err := m.compare(ctx, req.Request, control)
		if err != nil {
			return err
		}
		if req.Comparison == "" || req.Comparison != report.Digest {
			return result.Fail("PLAN_STALE", "比较结果已变化，请重新比较再修复生成物", 3)
		}
		p = Resolution{Version: 1, Kind: "replica-resolution", ID: workstore.ID(), Owner: m.Source.Owner, Scope: m.Source.Scope,
			Created: time.Now().UTC().Format(time.RFC3339Nano), Repair: true, Source: report.From,
			Request: ResolveRequest{Request: req.Request, Comparison: req.Comparison, Decisions: []Decision{}}, Targets: []ProposedTarget{},
			ControlBefore: control, ControlAfter: workstore.Clone(control), Boundary: "纯生成物修复；每个目标使用自己的源，源与共同基线保留，确认仅独立项目预演。"}
		for _, target := range report.Targets {
			if target.Status != "READY" {
				return result.Fail("REPLICA_BINDING_BLOCKED", target.Copy.Name+": "+target.Reason, 4)
			}
			s, err := m.copySource(target.Copy)
			if err != nil {
				return err
			}
			before, err := s.Store.Capture(target.Copy.Root)
			if err != nil {
				return err
			}
			if workstore.Digest(before) != target.Snapshot {
				return result.Fail("PLAN_STALE", "目标在构建修复候选前变化", 3)
			}
			bundle, err := s.ExportCandidate(before)
			if err != nil {
				return err
			}
			if err = transfer(s.Store, m.Control, before); err != nil {
				return err
			}
			p.Targets = append(p.Targets, ProposedTarget{Copy: target.Copy, Before: before, After: workstore.Clone(before), Changes: []workstore.Change{}, Preflight: bundle.Preflight})
		}
		current, err := m.compare(ctx, req.Request, control)
		if err != nil {
			return err
		}
		if current.Digest != report.Digest {
			return result.Fail("PLAN_STALE", "观察期间源、目标或绑定变化", 3)
		}
		p.Digest = resolutionDigest(p)
		return saveLocal(filepath.Join(m.resolutionDir(), "plans", p.ID+".json"), p)
	})
	if err != nil {
		return Generation{}, err
	}
	return m.StageGeneration(ctx, GenerationRequest{Plan: p.ID, Digest: p.Digest, Tools: req.Tools, ApplicationScope: scope})
}
