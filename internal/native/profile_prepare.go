package native

import (
	"context"
	"os/exec"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
)

func ParseProfilePreparationRequest(raw []byte) (CheckRequest, error) {
	var out CheckRequest
	bad := func() (CheckRequest, error) {
		return CheckRequest{}, result.Fail("INVALID_INPUT", "需要字段与类型明确的联合准备请求；不接受重复键、别名、空值或自定义命令", 2)
	}
	if len(raw) > 65536 {
		return bad()
	}
	v, err := config.Parse(raw)
	if err != nil {
		return bad()
	}
	fields, ok := v.(map[string]any)
	if !ok {
		return bad()
	}
	for key, value := range fields {
		switch key {
		case "probe", "rule_probe":
			if _, ok := value.(bool); !ok {
				return bad()
			}
		case "application_scope":
			if _, ok := value.(string); !ok {
				return bad()
			}
		case "tools":
			items, ok := value.([]any)
			if !ok || len(items) > 2 {
				return bad()
			}
			for _, item := range items {
				if _, ok := item.(string); !ok {
					return bad()
				}
			}
		case "rule_resolutions":
			items, ok := value.([]any)
			if !ok || len(items) > 2 {
				return bad()
			}
			for _, item := range items {
				obj, ok := item.(map[string]any)
				if !ok {
					return bad()
				}
				for k, v := range obj {
					if k != "tool" && k != "action" && k != "observed_digest" {
						return bad()
					}
					if _, ok := v.(string); !ok {
						return bad()
					}
				}
			}
		default:
			return bad()
		}
	}
	if strict(jsonData(fields), &out) != nil {
		return bad()
	}
	return out, nil
}

type ProfilePreparation struct {
	Status     string                   `json:"status"`
	Check      CheckReport              `json:"check"`
	NativePlan Plan                     `json:"native_plan"`
	MCP        ProfileApplicationReview `json:"mcp"`
	Plan       *ProfileApplicationPlan  `json:"plan,omitempty"`
	Gates      []inventory.Finding      `json:"gates"`
	Boundary   string                   `json:"boundary"`
}

// PrepareProfile joins the existing component gates without installing or
// granting access. Static requests cannot start version/discovery probes.
func (m *Manager) PrepareProfile(ctx context.Context, req CheckRequest, executable, home string) (ProfilePreparation, error) {
	out := ProfilePreparation{Status: "NEEDS_CHECK", Gates: []inventory.Finding{}, Boundary: "准备完整已支持组件的联合应用：检查技能与规则，并核对全部有效 MCP 的现有独立授权。仅显式 probe 启动空配置工具发现；不启动 MCP、不授予权限或写入目标，未适配 Profile 字段继续阻断。安装与实际技能会话 L3/L4 分别验收。"}
	scope, err := NormalizeApplicationScope(req.ApplicationScope)
	if err != nil {
		return out, err
	}
	if scope != "" {
		return out, result.Fail("INVALID_INPUT", "Profile 联合准备使用完整范围；仅技能与规则仍使用原独立入口", 2)
	}
	req.ApplicationScope = SkillsRulesScope
	native, err := m.Prepare(ctx, req)
	if err != nil {
		return out, err
	}
	out.Status, out.Check, out.NativePlan = native.Status, native.Check, native.Plan
	out.Gates = append(out.Gates, native.Gates...)
	out.MCP, err = m.ReviewProfileApplication(ctx, executable, home)
	if err != nil {
		return out, err
	}
	if out.MCP.Status != "READY" {
		if out.Status != "BLOCKED" {
			out.Status = "NEEDS_ATTENTION"
		}
		out.Gates = append(out.Gates, inventory.Finding{Status: "UNKNOWN", Code: "PROFILE_MCP_REVIEW_REQUIRED", Detail: "部分有效 MCP 的安装、独立授权或运行时记录仍需处理；技能规则预览保留，不允许遗漏后安装部分 Profile"})
	}
	var snapshotGate *inventory.Finding
	if _, err := m.projectSnapshotArchive(); err != nil {
		snapshotGate = &inventory.Finding{Status: "UNKNOWN", Code: "SNAPSHOT_BINDING_REQUIRED", Detail: "联合应用快照需要稳定的 project/scope/copy 与安全本机路径；请使用已登记项目绑定或补齐明确 copy UUID"}
	} else if _, err := exec.LookPath("git"); err != nil {
		snapshotGate = &inventory.Finding{Status: "UNKNOWN", Code: "SNAPSHOT_GIT_UNAVAILABLE", Detail: "联合应用前需要本机 Git 保存完整快照；当前未找到 Git 程序"}
	}
	if snapshotGate != nil {
		if out.Status != "BLOCKED" {
			out.Status = "NEEDS_ATTENTION"
		}
		out.Gates = append(out.Gates, *snapshotGate)
	}
	if out.Status != "READY" {
		return out, nil
	}
	requests := []source.MCPSessionRequest{}
	for _, entry := range out.MCP.Entries {
		if entry.Session == nil {
			return out, result.Fail("PROFILE_MCP_REVIEW_REQUIRED", "MCP 审查缺少明确启动绑定，请重新读取", 4)
		}
		requests = append(requests, *entry.Session)
	}
	p, err := m.ProfileApplicationPlan(ctx, ProfileApplicationRequest{NativePlan: native.Plan.ID, NativeDigest: native.Plan.Digest, Sessions: requests}, executable, home)
	if err != nil {
		return out, err
	}
	out.Plan = &p
	return out, nil
}
