package native

import (
	"context"
	"sort"
	"time"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
)

// Public projections intentionally omit source/configuration bodies, permission
// values, purpose, raw parser diagnostics and historical runtime evidence.
type SnapshotAssessmentCheck struct {
	Component string `json:"component"`
	Subject   string `json:"subject,omitempty"`
	Status    string `json:"status"`
	Code      string `json:"code"`
}

type ProjectSnapshotAssessment struct {
	Snapshot         ProjectSnapshot               `json:"snapshot"`
	At               string                        `json:"at"`
	Reconstruction   string                        `json:"reconstruction"`
	DependencyStatus string                        `json:"dependency_status"`
	Checks           []SnapshotAssessmentCheck     `json:"checks"`
	SkillMaterial    *source.SnapshotSkillMaterial `json:"skill_revision_material"`
	Boundary         string                        `json:"boundary"`
}

// AssessProjectSnapshot restores only immutable cache blobs, reconstructs the
// original layered configuration and recomputes current static dependency /
// permission evidence. It neither writes live files nor executes a process.
func (m *Manager) AssessProjectSnapshot(ctx context.Context, id string) (ProjectSnapshotAssessment, error) {
	out := ProjectSnapshotAssessment{Checks: []SnapshotAssessmentCheck{}, Boundary: "已重新解析历史配置并核对完整包，按当前安装、授权和已有运行时证据检查依赖；这是本次静态观察。未启动工具、插件或凭据库，未核验原生目标、实际新会话、恢复后的授权摘要或恢复写入可行性；完整项目回退尚未开放。本机只补齐不可变缓存，不修改项目、全局库或本机覆盖。"}
	a, err := m.projectSnapshotArchive()
	if err != nil {
		return out, err
	}
	r, err := a.Load(ctx, id)
	if err != nil {
		return out, err
	}
	if r.Configuration == nil {
		return out, result.Fail("SNAPSHOT_CONFIGURATION_MISSING", "此旧快照未保存原始分层输入；可核验归档字节，不能从当前配置推测历史生效值", 4)
	}
	r, err = a.Materialize(ctx, id, m.Source.Store)
	if err != nil {
		return out, err
	}
	bundle, err := m.Source.ExportHistorical(ctx, r.Source, r.Effective, *r.Configuration)
	if err != nil {
		return out, err
	}
	js, err := m.ProfileApplicationHistory()
	if err != nil {
		return out, err
	}
	state, err := snapshotState(r, js)
	if err != nil {
		return out, err
	}
	out.Snapshot, out.At = snapshotSummary(r, state, "PASS_ARCHIVED_BYTES"), time.Now().UTC().Format(time.RFC3339Nano)
	out.Reconstruction, out.DependencyStatus = "PASS_FROZEN_INPUTS", bundle.Preflight.Status
	out.SkillMaterial = bundle.SkillMaterial
	for _, check := range bundle.Preflight.ProfileChecks {
		component := "profile"
		if check.Component == "mcp" {
			component = "mcp"
		}
		out.Checks = append(out.Checks, SnapshotAssessmentCheck{Component: component, Subject: check.Subject, Status: check.Status, Code: check.Code})
	}
	for _, skill := range bundle.Preflight.Skills {
		if !skill.Enabled {
			continue
		}
		for _, check := range skill.Checks {
			out.Checks = append(out.Checks, SnapshotAssessmentCheck{Component: "skill", Subject: skill.ID, Status: check.Status, Code: check.Code})
		}
	}
	sort.Slice(out.Checks, func(i, j int) bool {
		a, b := out.Checks[i], out.Checks[j]
		if a.Component != b.Component {
			return a.Component < b.Component
		}
		if a.Subject != b.Subject {
			return a.Subject < b.Subject
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Status < b.Status
	})
	return out, nil
}
