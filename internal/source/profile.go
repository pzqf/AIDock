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
	Profile        map[string]any      `json:"profile"`
	SHA256         string              `json:"sha256"`
	Skills         []EffectiveSkill    `json:"skills"`
	Report         *profile.Report     `json:"report,omitempty"`
	Checks         []inventory.Finding `json:"checks"`
	Boundary       string              `json:"boundary"`
	RuleFiles      []RuleFile          `json:"rule_files"`
	RulesSHA256    string              `json:"rules_sha256"`
	UnmanagedRules []string            `json:"unmanaged_rules"`
}
type ProfileEdit struct {
	ExpectedSHA256      string          `json:"expected_sha256"`
	Profile             map[string]any  `json:"profile"`
	NativeRule          *NativeRuleEdit `json:"native_rule,omitempty"`
	RuleFiles           *[]RuleFile     `json:"rule_files,omitempty"`
	ExpectedRulesSHA256 string          `json:"expected_rules_sha256,omitempty"`
}

func projectProfile(v map[string]any, effective []EffectiveSkill) (profile.Report, error) {
	catalog := profile.Catalog{Skills: map[string][]string{}}
	for _, item := range effective {
		catalog.Skills[item.Skill.ID] = []string{item.Skill.Revision}
	}
	return profile.Resolve(profile.Input{Project: v, Catalog: catalog})
}

func profileChecks(v map[string]any, effective []EffectiveSkill) []inventory.Finding {
	checks := []inventory.Finding{}
	if _, err := projectProfile(v, effective); err != nil {
		checks = append(checks, inventory.Finding{Status: "FAIL", Code: "PROFILE_RESOLUTION_FAILED", Detail: err.Error()})
		return checks
	}
	unsupported := []string{}
	for _, key := range []string{"plugins", "mcp"} {
		section, _ := v[key].(map[string]any)
		if len(array(section["enable"])) > 0 || len(array(section["disable"])) > 0 {
			unsupported = append(unsupported, key)
		}
	}
	if len(array(v["rules"])) > 0 {
		checks = append(checks, inventory.Finding{Status: "UNKNOWN", Code: "PROFILE_RULES_PENDING", Detail: "共享规则需要编译为原生托管区块；文件预演与实际会话加载分别验证"})
	}
	if values, _ := v["config_overrides"].(map[string]any); len(values) > 0 {
		unsupported = append(unsupported, "config_overrides")
	}
	if len(unsupported) > 0 {
		checks = append(checks, inventory.Finding{Status: "UNKNOWN", Code: "PROFILE_NATIVE_UNSUPPORTED", Detail: "当前技能文件适配器不能应用这些 Profile 字段：" + strings.Join(unsupported, ", ") + "；必须完成对应编译后单独应用，不能忽略后声称全部生效"})
	}
	return checks
}

// Profile reads only the current saved layer. Named/global/local/session layers
// are never inferred from neighboring folders or silently flattened into it.
func (m *Manager) Profile() (ProfileState, error) {
	out := ProfileState{Checks: []inventory.Finding{}, Boundary: "编辑项目共享配置和文件化规则库。内联规则在前，已启用规则文件按索引顺序追加。保存共用源回退点；应用到工具和会话生效检查需单独执行。全局 Profile 和本机覆盖持久关联继续开发。"}
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
		out.Checks = profileChecks(v, effective)
		texts, err := m.sourceRules(t, v)
		if err != nil {
			return err
		}
		resolved := profileWithRules(v, texts)
		if len(out.UnmanagedRules) > 0 {
			out.Checks = append(out.Checks, inventory.Finding{Status: "WARN", Code: "RULE_FILES_UNMANAGED", Detail: "未登记规则文件保留，不会自动编译：" + strings.Join(out.UnmanagedRules, "、")})
		}
		if len(texts) > 0 && len(array(v["rules"])) == 0 {
			out.Checks = append(out.Checks, inventory.Finding{Status: "UNKNOWN", Code: "PROFILE_RULES_PENDING", Detail: "规则库已进入编译输入，仍需单独应用和会话加载验证"})
		}
		if report, e := projectProfile(resolved, effective); e == nil {
			out.Report = &report
		}
		return nil
	})
	return out, err
}

func (m *Manager) ProfilePlan(ctx context.Context, req ProfileEdit) (Preview, error) {
	out := Preview{Owner: m.Owner, Scope: m.Scope, Findings: []inventory.Finding{}, PendingApply: true}
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
		_, effective, err := m.resolve(&before, idx, old)
		if err != nil {
			return err
		}
		if _, err = projectProfile(v, effective); err != nil {
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
		if req.RuleFiles != nil {
			if err = m.draftRuleLibrary(before, &after, req.ExpectedRulesSHA256, *req.RuleFiles); err != nil {
				return err
			}
		}
		if err = m.Store.PutFile(&after, "profile.yaml", jsonBytes(v), entry.Mode); err != nil {
			return err
		}
		report, err := m.preflight(after, idx, v)
		if err != nil {
			return err
		}
		if err = blockEnabled(report); err != nil {
			return err
		}
		report.Candidate = true
		out.Preflight = &report
		action := "profile-edit"
		if req.NativeRule != nil {
			action = "profile-import-rules"
		}
		m.planInputs = inputs
		defer func() { m.planInputs = nil }()
		out.Plan, err = m.preview(action, before, after)
		return err
	})
	return out, err
}
