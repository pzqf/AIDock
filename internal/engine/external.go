package engine

import (
	"encoding/json"

	"github.com/pzqf/AIDock/internal/bridge"
)

// Compare project-selected package baselines without treating a whole-source
// registration baseline as evidence that every package was adopted.
func (e *Engine) annotateExternalPins(r *bridge.Report) error {
	if e.SourceRoot == "" {
		return nil
	}
	m, err := e.Sources()
	if err != nil {
		return err
	}
	state, err := m.State()
	if err != nil {
		return err
	}
	if state.Pending {
		return nil
	}
	for _, a := range state.External {
		pin := bridge.ProjectPin{SkillID: a.Skill.ID, ApprovalID: a.ApprovalID, PackagePath: a.PackagePath, ApprovedRevision: a.CandidateRevision, Status: "FAIL", CacheAvailable: true}
		at := -1
		for i, b := range r.Bridges {
			if b.ID == a.Binding.ID {
				at = i
			}
		}
		if at < 0 {
			b := a.Binding
			r.Bridges = append(r.Bridges, bridge.BridgeReport{ID: b.ID, Project: b.Project, Scope: b.Scope, Copy: b.Copy, Root: b.Root, SourcePath: b.SourcePath, SourceRole: b.SourceRole, Format: b.Format, Generator: b.Generator, ReadOnly: true, RevisionFormat: "external-v1", CheckedAt: r.CheckedAt, Status: "FAIL", Candidates: []bridge.Candidate{}, Findings: []bridge.Finding{{Status: "FAIL", Code: "EXTERNAL_REGISTRATION_REMOVED", Detail: "已批准资产的外部登记已移除；只读批准缓存仍可用"}}, ProjectPins: []bridge.ProjectPin{pin}})
			continue
		}
		b := &r.Bridges[at]
		// The identity of an authority includes generator and source path.
		current := bridge.Registration{ID: b.ID, Project: b.Project, Scope: b.Scope, Copy: b.Copy, Root: b.Root, SourcePath: b.SourcePath, SourceRole: b.SourceRole, Format: b.Format, Generator: b.Generator}
		old := a.Binding
		old.Packages = nil
		want, _ := json.Marshal(old)
		got, _ := json.Marshal(current)
		if string(want) != string(got) {
			b.Status = "FAIL"
			b.Findings = append(b.Findings, bridge.Finding{Status: "FAIL", Code: "EXTERNAL_BINDING_CHANGED", Detail: "当前外部登记与项目批准的原源身份不同；批准缓存保持原值"})
		} else {
			for _, c := range b.Candidates {
				if c.Skill.Path == a.PackagePath {
					pin.CurrentRevision = c.Revision
				}
			}
			if pin.CurrentRevision != "" {
				pin.Status = "PASS"
				if pin.CurrentRevision != pin.ApprovedRevision {
					pin.Status = "STALE"
					if b.Status != "FAIL" {
						b.Status = "STALE"
					}
					b.Findings = append(b.Findings, bridge.Finding{Status: "STALE", Code: "EXTERNAL_PROJECT_PIN_DRIFT", Detail: a.PackagePath + " 与项目已选批准包不同；不会自动更新 pin，旧缓存仍可读"})
				}
			} else {
				b.Status = "FAIL"
				b.Findings = append(b.Findings, bridge.Finding{Status: "FAIL", Code: "EXTERNAL_APPROVED_PACKAGE_UNAVAILABLE", Detail: a.PackagePath + " 当前候选不可用或已移出扫描清单；批准缓存仍可读"})
			}
		}
		b.ProjectPins = append(b.ProjectPins, pin)
	}
	r.Status = "PASS"
	r.Counts = map[string]int{}
	rank := map[string]int{"PASS": 1, "UNKNOWN": 2, "WARN": 3, "STALE": 4, "FAIL": 5}
	for _, b := range r.Bridges {
		r.Counts[b.Status]++
		if rank[b.Status] > rank[r.Status] {
			r.Status = b.Status
		}
	}
	return nil
}
