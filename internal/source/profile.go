package source

import (
	"context"
	"strings"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/profile"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type ProfileState struct {
	Profile         map[string]any      `json:"profile"`
	SHA256          string              `json:"sha256"`
	Skills          []EffectiveSkill    `json:"skills"`
	EffectiveSkills []EffectiveSkill    `json:"effective_skills,omitempty"`
	MCP             []MCPCheck          `json:"mcp"`
	MCPSHA256       string              `json:"mcp_sha256"`
	Report          *profile.Report     `json:"report,omitempty"`
	Checks          []inventory.Finding `json:"checks"`
	Boundary        string              `json:"boundary"`
	RuleFiles       []RuleFile          `json:"rule_files"`
	RulesSHA256     string              `json:"rules_sha256"`
	UnmanagedRules  []string            `json:"unmanaged_rules"`
	Layers          ProfileLayers       `json:"layers"`
	NamedProfiles   []map[string]any    `json:"named_profiles"`
	NamedSHA256     string              `json:"named_sha256,omitempty"`
}
type ProfileEdit struct {
	Target              string            `json:"target,omitempty"`
	Clear               bool              `json:"clear,omitempty"`
	Snapshot            string            `json:"snapshot,omitempty"`
	ExpectedLayers      string            `json:"expected_layers_digest,omitempty"`
	NamedProfiles       *[]map[string]any `json:"named_profiles,omitempty"`
	ExpectedNamed       string            `json:"expected_named_sha256,omitempty"`
	LibraryRoot         *string           `json:"library_root,omitempty"`
	ExpectedSHA256      string            `json:"expected_sha256"`
	ExpectedMCP         string            `json:"expected_mcp_sha256,omitempty"`
	Profile             map[string]any    `json:"profile"`
	NativeRule          *NativeRuleEdit   `json:"native_rule,omitempty"`
	RuleFiles           *[]RuleFile       `json:"rule_files,omitempty"`
	ExpectedRulesSHA256 string            `json:"expected_rules_sha256,omitempty"`
}

func profileChecks(v map[string]any) []inventory.Finding {
	checks := []inventory.Finding{}
	if len(array(v["rules"])) > 0 {
		checks = append(checks, inventory.Finding{Status: "UNKNOWN", Code: "PROFILE_RULES_PENDING", Detail: "共享规则需要编译为原生托管区块；文件预演与实际会话加载分别验证"})
	}
	return checks
}

// Profile reads only the current saved layer. Named/global/local/session layers
// are never inferred from neighboring folders or silently flattened into it.
func (m *Manager) Profile() (ProfileState, error) {
	out := ProfileState{Checks: []inventory.Finding{}, Boundary: "按显式关联合并全局默认、命名 Profile 继承链、项目共享和本机覆盖，并报告有效值来源。共享 Profile 和文件规则共用源回退点；本机覆盖独立保存、恢复和回退，不参与副本同步。保存后需单独预览工具应用和检查会话加载。"}
	err := m.Store.Locked(func() error {
		if err := m.Store.Pending(); err != nil {
			return err
		}
		t, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		idx, v, err := m.load(&t)
		if err != nil {
			return err
		}
		entry, ok := t.Entries["profile.yaml"]
		if !ok {
			return result.Fail("SOURCE_NOT_INITIALIZED", "先创建项目或保存技能，再管理 Profile", 4)
		}
		_, effective, err := m.resolve(&t, idx, v)
		if err != nil {
			return err
		}
		out.RuleFiles, out.UnmanagedRules, err = m.ruleLibrary(t)
		if err != nil {
			return err
		}
		out.RulesSHA256 = ruleLibraryDigest(t)
		out.Profile, out.SHA256, out.Skills = v, entry.Hash, effective
		out.Layers, err = m.profileLayers()
		if err != nil {
			return err
		}
		if m.Owner == "global" {
			out.NamedProfiles, err = m.namedProfiles(t)
			if err != nil {
				return err
			}
			out.NamedSHA256 = workstore.Digest(subtree(t, "profiles"))
		}
		report, err := m.resolveProfile(t, v, effective, out.Layers)
		if err != nil {
			out.Checks = append(out.Checks, inventory.Finding{Status: "FAIL", Code: "PROFILE_RESOLUTION_FAILED", Detail: err.Error()})
		} else {
			out.Report = &report
			out.Checks = profileChecks(report.Effective)
			out.EffectiveSkills, err = m.selectProfileSkills(t, effective, report.Effective)
			if err != nil {
				out.Checks = append(out.Checks, inventory.Finding{Status: "FAIL", Code: "PROFILE_PIN_MISMATCH", Detail: err.Error()})
			}
		}
		mcp, err := m.mcpState(t, out.Report)
		if err != nil {
			return err
		}
		out.MCP, out.MCPSHA256 = mcp.Servers, mcp.SHA256
		out.Checks = append(out.Checks, selectedMCPFindings(mcp)...)
		if len(out.UnmanagedRules) > 0 {
			out.Checks = append(out.Checks, inventory.Finding{Status: "WARN", Code: "RULE_FILES_UNMANAGED", Detail: "未登记规则文件保留，不会自动编译：" + strings.Join(out.UnmanagedRules, "、")})
		}
		out.Boundary = "全局默认 → extends 链 → 项目共享 → 本机副本覆盖。来源报告解释已保存有效值；保存不自动应用工具或重启会话。本机覆盖只存 Local Root，独立回退，不参与共享同步。"
		return nil
	})
	return out, err
}

func (m *Manager) ProfilePlan(ctx context.Context, req ProfileEdit) (Preview, error) {
	if req.Target == "local" {
		return m.localProfilePlan(ctx, req)
	}
	out := Preview{Owner: m.Owner, Scope: m.Scope, Findings: []inventory.Finding{}, PendingApply: true}
	if req.Target != "" && req.Target != "shared" || req.Clear || req.Snapshot != "" || req.LibraryRoot != nil {
		return out, result.Fail("INVALID_INPUT", "共享编辑不能使用本机清除/恢复动作", 2)
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if req.ExpectedSHA256 == "" || req.Profile == nil {
		return out, result.Fail("INVALID_INPUT", "需要加载时 Profile 摘要及完整 Profile 对象", 2)
	}
	err := m.Store.Locked(func() error {
		if err := m.Store.Pending(); err != nil {
			return err
		}
		before, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		inputs, err := m.ruleImportInputs(req.NativeRule)
		if err != nil {
			return err
		}
		entry, ok := before.Entries["profile.yaml"]
		if !ok || entry.Hash != req.ExpectedSHA256 {
			return result.Fail("PLAN_STALE", "Profile 在编辑后变化；草稿保留，请核对后重新加载", 3)
		}
		if req.ExpectedMCP != "" && req.ExpectedMCP != mcpDigest(before) {
			return result.Fail("PLAN_STALE", "MCP 定义在编辑后变化；Profile 草稿保留，请重新核对", 3)
		}
		idx, old, err := m.load(&before)
		if err != nil {
			return err
		}
		if old["schema_version"] != 2 {
			return result.Fail("MIGRATION_REQUIRED", "先确认迁移旧 Profile，再编辑", 3)
		}
		value, err := config.Validate("profile", jsonBytes(req.Profile))
		if err != nil {
			return result.Fail("PROFILE_INVALID", err.Error(), 5)
		}
		v := value.(map[string]any)
		if req.NativeRule != nil {
			library, _, err := m.ruleLibrary(before)
			if err != nil {
				return err
			}
			if len(library) > 0 && (req.RuleFiles == nil || req.NativeRule.MergedRules == nil) {
				return result.Fail("RULE_LIBRARY_DECISION_REQUIRED", "规则库已有文件；导入须提供 merged_rules、rule_files 及规则库观察摘要，明确确认完整合并结果", 3)
			}
			imported, replace := importedRules(req.NativeRule.Body, req.NativeRule.Mode, old["rules"])
			if req.NativeRule.MergedRules != nil {
				imported = []any{}
				for _, rule := range *req.NativeRule.MergedRules {
					imported = append(imported, rule)
				}
			}
			v["rules"] = imported
			if replace {
				v["rules_override"] = true
			}
		}
		_, _, err = m.resolve(&before, idx, old)
		if err != nil {
			return err
		}
		life, err := m.loadLifecycle(&before, idx)
		if err != nil {
			return err
		}
		if err = validateRetiredProfile(life, v); err != nil {
			return err
		}
		after := workstore.Clone(before)
		if req.NamedProfiles != nil {
			if req.ExpectedNamed == "" || req.ExpectedNamed != workstore.Digest(subtree(before, "profiles")) {
				return result.Fail("PLAN_STALE", "命名 Profile 库已变化或缺少观察摘要", 3)
			}
			if err = m.draftNamedProfiles(&after, *req.NamedProfiles); err != nil {
				return err
			}
		}
		if req.RuleFiles != nil {
			if err = m.draftRuleLibrary(before, &after, req.ExpectedRulesSHA256, *req.RuleFiles); err != nil {
				return err
			}
		}
		if err = m.Store.PutFile(&after, "profile.yaml", jsonBytes(v), entry.Mode); err != nil {
			return err
		}
		layers, err := m.profileLayers()
		if err != nil {
			return err
		}
		_, effective, err := m.resolve(&after, idx, v)
		if err != nil {
			return err
		}
		resolved, err := m.resolveProfile(after, v, effective, layers)
		if err != nil {
			return err
		}
		report, err := m.preflight(after, idx, v, layers)
		if err != nil {
			return err
		}
		if err = blockEnabled(report); err != nil {
			return err
		}
		report.Candidate = true
		out.Preflight = &report
		if req.ExpectedLayers != "" && req.ExpectedLayers != layers.Digest {
			return result.Fail("PLAN_STALE", "全局或本机配置层已变化；草稿保留，请重新核对", 3)
		}
		out.ProfileReport = &resolved
		action := "profile-edit"
		if req.NativeRule != nil {
			action = "profile-import-rules"
		}
		m.planInputs = inputs
		if layers.Digest != "" {
			if m.planInputs == nil {
				m.planInputs = map[string]string{}
			}
			m.planInputs["profile_layers"] = layers.Digest
		}
		defer func() { m.planInputs = nil }()
		out.Plan, err = m.preview(action, before, after)
		return err
	})
	return out, err
}
