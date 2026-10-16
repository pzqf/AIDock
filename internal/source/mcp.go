package source

import (
	"context"
	"regexp"
	"strings"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/credentials"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/mcppolicy"
	"github.com/pzqf/AIDock/internal/profile"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/runtimecheck"
	"github.com/pzqf/AIDock/internal/secref"
	"github.com/pzqf/AIDock/internal/workstore"
)

// MCP definitions are declarations, never direct client launch commands.
// Saving enabled=true requests inclusion; it grants no execution permission.
type MCPState struct {
	Config          map[string]any      `json:"config"`
	SHA256          string              `json:"sha256"`
	Present         bool                `json:"present"`
	Status          string              `json:"status"`
	Servers         []MCPCheck          `json:"servers"`
	Checks          []inventory.Finding `json:"checks"`
	Boundary        string              `json:"boundary"`
	ExecutionReady  bool                `json:"execution_ready"`
	SelectionDigest string              `json:"selection_digest"`
	ProfileReport   *profile.Report     `json:"profile_report,omitempty"`
}

type MCPCheck struct {
	Name              string              `json:"name"`
	Enabled           bool                `json:"enabled"`
	EffectiveEnabled  bool                `json:"effective_enabled"`
	SelectionResolved bool                `json:"selection_resolved"`
	EnabledSource     string              `json:"enabled_source,omitempty"`
	SelectionTrace    []profile.Event     `json:"selection_trace"`
	Status            string              `json:"status"`
	Checks            []inventory.Finding `json:"checks"`
	ExecutionReady    bool                `json:"execution_ready"`
}

type MCPEdit struct {
	ExpectedSHA256      string            `json:"expected_sha256"`
	Config              map[string]any    `json:"config"`
	ExpectedSelection   string            `json:"expected_selection_digest,omitempty"`
	ToolPolicyDocuments map[string]string `json:"tool_policy_documents,omitempty"`
}

type MCPPreview struct {
	Preview
	Candidate MCPState `json:"candidate"`
}

var mcpFixedVersion = regexp.MustCompile(`^(v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?|[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)

func mcpDigest(t workstore.Tree) string {
	if e, ok := t.Entries["mcp.yaml"]; ok {
		return e.Hash
	}
	return workstore.Hash(nil)
}

func (m *Manager) mcpConfig(t workstore.Tree) (map[string]any, error) {
	if e, ok := t.Entries["mcp.yaml"]; ok {
		b, err := m.Store.Data(e)
		if err != nil {
			return nil, err
		}
		v, err := config.Validate("mcp", b)
		if err != nil {
			return nil, result.Fail("MCP_DEFINITION_INVALID", err.Error(), 5)
		}
		return v.(map[string]any), nil
	}
	return map[string]any{"schema_version": 1, "servers": []any{}}, nil
}

func (m *Manager) mcpState(t workstore.Tree, selection ...*profile.Report) (MCPState, error) {
	out := MCPState{SHA256: mcpDigest(t), Status: "PASS", Servers: []MCPCheck{}, Checks: []inventory.Finding{}, Boundary: "定义仅保存在当前项目 .aidock/mcp.yaml；启用是声明，不代表已安装、已授权或正在运行。工具配置、安装锁验证、隔离和会话生命周期分别检查。"}
	_, out.Present = t.Entries["mcp.yaml"]
	v, err := m.mcpConfig(t)
	if err != nil {
		return out, err
	}
	out.Config = v
	if len(selection) > 0 {
		out.ProfileReport = selection[0]
	} else {
		idx, shared, err := m.load(&t)
		if err != nil {
			return out, err
		}
		_, skills, err := m.resolve(&t, idx, shared)
		if err != nil {
			return out, err
		}
		layers, err := m.profileLayers()
		if err != nil {
			return out, err
		}
		report, err := m.resolveProfile(t, shared, skills, layers)
		if err != nil {
			out.Checks = append(out.Checks, inventory.Finding{Status: "FAIL", Code: "PROFILE_RESOLUTION_FAILED", Detail: err.Error()})
			out.Status = "FAIL"
		} else {
			out.ProfileReport = &report
		}
	}
	if out.ProfileReport != nil {
		out.SelectionDigest = out.ProfileReport.Digest
	}
	lock, err := m.mcpLockState(context.Background(), t)
	if err != nil {
		return out, err
	}
	if lock.Present {
		out.Checks = append(out.Checks, lock.Checks...)
		if statusRank(lock.Status) > statusRank(out.Status) {
			out.Status = lock.Status
		}
	}
	permission, permissionErr := m.permissionState(t, lock, out.ProfileReport)
	var runtimeState runtimecheck.State
	var runtimeErr error
	if len(lock.Definitions) > 0 {
		r, err := m.runtimeManager()
		runtimeErr = err
		if err == nil {
			runtimeState, runtimeErr = r.State(currentRuntimeInputs(permission))
		}
	}
	var vaultState credentials.State
	var vaultErr error
	vaultLoaded := false
	for _, raw := range array(v["servers"]) {
		s := raw.(map[string]any)
		item := MCPCheck{Name: s["name"].(string), Enabled: s["enabled"].(bool), Status: "UNKNOWN", Checks: []inventory.Finding{}, SelectionTrace: []profile.Event{}}
		if out.ProfileReport != nil {
			item.SelectionResolved = true
			section, _ := out.ProfileReport.Effective["mcp"].(map[string]any)
			for _, name := range array(section["enable"]) {
				if name == item.Name {
					item.EffectiveEnabled = true
				}
			}
			for _, event := range out.ProfileReport.Trace {
				if event.Path == "/mcp/"+item.Name {
					item.EnabledSource = event.Layer
					item.SelectionTrace = append(item.SelectionTrace, event)
				}
			}
		}
		add := func(status, code, detail string) {
			item.Checks = append(item.Checks, inventory.Finding{Status: status, Code: code, Detail: detail})
			if status == "FAIL" {
				item.Status = "FAIL"
			}
		}
		if s["transport"] != "stdio" {
			add("FAIL", "UNSUPPORTED_TRANSPORT", "受管接入目前只支持 stdio；不会生成 SSE/HTTP 直连配置")
		}
		src := s["source"].(map[string]any)
		if src["type"] == "local" {
			if p, _ := src["path"].(string); p == "" || workstore.SafeRelative(p) != nil {
				add("FAIL", "MCP_SOURCE_NOT_PORTABLE", "本地来源需要明确的项目相对路径；此处不读取或运行来源程序")
			}
		} else {
			version, _ := src["version"].(string)
			if !mcpFixedVersion.MatchString(version) {
				add("FAIL", "MCP_VERSION_NOT_PINNED", "来源需要精确版本或提交；浮动版本不能作为安装锁输入")
			}
			if src["type"] == "npm" || src["type"] == "registry" {
				if p, _ := src["package"].(string); strings.TrimSpace(p) == "" {
					add("FAIL", "MCP_SOURCE_INCOMPLETE", "需要明确来源包名")
				}
			} else if u, _ := src["url"].(string); strings.TrimSpace(u) == "" {
				add("FAIL", "MCP_SOURCE_INCOMPLETE", "需要明确来源 URL")
			}
		}
		policy, err := mcppolicy.FromDefinition(s)
		if err != nil {
			return out, err
		}
		if policy == nil {
			add("UNKNOWN", "MCP_TOOL_POLICY_MISSING", "尚无已审查的可信工具策略；未知工具不能获得中继调用权限")
		} else {
			add("PASS", "MCP_TOOL_POLICY_DECLARED", "已登记固定项目/Public 查询合同的工具策略；描述与读写分类须单独确认，未知工具和写调用继续拒绝")
		}
		permissions := s["permissions"].(map[string]any)
		net := permissions["net"].(map[string]any)
		if net["default"] != "deny" {
			add("WARN", "MCP_NETWORK_BROAD", "默认允许网络；此声明需要单独授权和 OS 能力核对")
		}
		if s["tier"] == 0 {
			add("WARN", "MCP_UNISOLATED_REQUEST", "Tier 0 为非隔离声明，仍需显式信任选择与授权")
		}
		if lock.Present {
			found := false
			for _, check := range lock.Checks {
				add(check.Status, check.Code, check.Detail)
				found = true
			}
			for _, plugin := range lock.Plugins {
				if plugin.Name == item.Name {
					for _, check := range plugin.Checks {
						add(check.Status, check.Code, check.Detail)
					}
					found = true
				}
			}
			if !found {
				add("UNKNOWN", "MCP_LOCK_ENTRY_MISSING", "安装锁中没有此 MCP 的锁定清单")
			}
		} else {
			add("UNKNOWN", "MCP_LOCK_MISSING", "尚无经过验证的安装锁")
		}
		if lock.Installations[item.Name] == "CURRENT" {
			add("PASS", "MCP_INSTALL_REGISTERED", "本机已登记匹配当前检查的安装与轻量直接声明 SBOM；尚未授权或运行")
		} else if lock.Installations[item.Name] != "" {
			add("UNKNOWN", "MCP_INSTALL_REVIEW_STALE", "已有本机安装登记，但当前产物、定义或安全证据已变化；重新审查后登记")
		} else {
			add("UNKNOWN", "MCP_INSTALL_NOT_REGISTERED", "产物尚未登记为本机安装，先检查并明确确认安装登记")
		}
		if permissionErr != nil {
			add("FAIL", "MCP_AUTHORIZATION_STORE_INVALID", permissionErr.Error())
		} else {
			for _, f := range permission.Checks {
				if f.Code == "MCP_AUTHORIZATION_STORE_INVALID" || f.Code == "MCP_AUTHORIZATION_RECOVERY_REQUIRED" {
					add(f.Status, f.Code, f.Detail)
				}
			}
			approved := "MISSING"
			for _, e := range permission.Authorizations {
				if e.Record.Input.Name == item.Name {
					approved = e.Status
				}
			}
			if approved == "CURRENT" {
				add("PASS", "MCP_PROJECT_AUTHORIZED", "当前副本的权限、安装和有效配置授权记录匹配；仅 Public / 未知外部客户端，运行另行检查")
			} else if approved == "MISSING" {
				add("UNKNOWN", "MCP_PROJECT_AUTHORIZATION_REQUIRED", "尚未确认此项目副本的权限与用途")
			} else {
				add("UNKNOWN", "MCP_PROJECT_AUTHORIZATION_STALE", "已有授权与当前安装、锁、权限或有效配置不一致；重新审查")
			}
		}
		refs, _ := secref.Definition(s)
		if s["tier"] == 0 {
			if runtimeErr != nil {
				add("FAIL", "MCP_RUNTIME_AUDIT_INVALID", "运行时核验记录或事务锚点无效；先检查运行时审计")
			} else if runtimeState.Recovery {
				add("FAIL", "MCP_RUNTIME_RECOVERY_REQUIRED", "运行时核验存在未完成审计事务；先执行 mcp runtime-recover，不重放进程")
			} else {
				for _, entry := range runtimeState.Entries {
					if entry.Event.Input.Name != item.Name {
						continue
					}
					if !entry.Current {
						add("UNKNOWN", "MCP_RUNTIME_CHECK_STALE", "已有运行时核验记录，但当前授权、输入或有效期已变化；需重新显式核验")
					} else if entry.Status == "PASS" {
						add("PASS", "MCP_RUNTIME_VERSION_OBSERVED", "已显式核验所选运行时返回的版本 "+entry.Event.ObservedVersion+"；不证明发行者身份、插件启动或 OS 隔离")
					} else if entry.Status == "INCOMPLETE" {
						add("UNKNOWN", "MCP_RUNTIME_CHECK_INCOMPLETE", "版本核验只有执行意图，尚无完成回执；不重放、不记为通过")
					} else {
						add("FAIL", "MCP_RUNTIME_CHECK_FAILED", "最近一次当前运行时核验未通过："+entry.Event.Code)
					}
					break
				}
			}
		}
		if len(refs) > 0 {
			if !vaultLoaded {
				vaultLoaded = true
				vault, e := credentials.New(m.Store.Local)
				vaultErr = e
				if e == nil {
					vaultState, vaultErr = vault.State(context.Background())
				}
			}
			if vaultErr != nil {
				add("FAIL", "MCP_CREDENTIAL_STATE_INVALID", "系统凭据审计或读取失败；先修复密钥管理")
			} else if !vaultState.Available {
				add("UNKNOWN", "MCP_CREDENTIAL_VAULT_UNAVAILABLE", "此平台无法核验系统凭据")
			} else if len(vaultState.Pending) > 0 {
				add("FAIL", "MCP_CREDENTIAL_RECOVERY_REQUIRED", "密钥存在未核对的中断操作；交付已阻断")
			} else {
				present := map[string]bool{}
				for _, e := range vaultState.Entries {
					present[e.Name] = true
				}
				missing := []string{}
				for _, n := range secref.Names(refs) {
					if !present[n] {
						missing = append(missing, n)
					}
				}
				if len(missing) > 0 {
					add("FAIL", "MCP_CREDENTIAL_MISSING", "缺少映射所需密钥："+strings.Join(missing, "、"))
				} else {
					add("PASS", "MCP_CREDENTIAL_REFERENCES_PRESENT", "映射所需名称在当前系统凭据库中存在；交付时仍须重查授权、修订和恢复状态")
				}
			}
		}
		add("UNKNOWN", "MCP_EXECUTION_PENDING", "隔离、逐次调用授权和受管 stdio 启动尚未开放；当前未启动程序")
		out.Servers = append(out.Servers, item)
		if statusRank(item.Status) > statusRank(out.Status) {
			out.Status = item.Status
		}
	}
	return out, nil
}

func (m *Manager) MCP() (MCPState, error) {
	var out MCPState
	err := m.Store.Locked(func() error {
		if err := m.Store.Pending(); err != nil {
			return err
		}
		t, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		if _, _, err = m.load(&t); err != nil {
			return err
		}
		if _, ok := t.Entries[m.identityFile()]; !ok {
			return result.Fail("SOURCE_NOT_INITIALIZED", "先创建或打开项目，再管理 MCP 定义", 4)
		}
		out, err = m.mcpState(t)
		return err
	})
	return out, err
}

func (m *Manager) mcpFindings(t workstore.Tree, selection *profile.Report) []inventory.Finding {
	if _, ok := t.Entries["mcp.yaml"]; !ok {
		return nil
	}
	state, err := m.mcpState(t, selection)
	if err != nil {
		return []inventory.Finding{{Status: "FAIL", Code: "MCP_DEFINITION_INVALID", Detail: err.Error()}}
	}
	return selectedMCPFindings(state)
}

func selectedMCPFindings(state MCPState) []inventory.Finding {
	out := []inventory.Finding{}
	selected := false
	for _, server := range state.Servers {
		if !server.SelectionResolved || !server.EffectiveEnabled {
			continue
		}
		selected = true
		for _, finding := range server.Checks {
			finding.Detail = server.Name + "：" + finding.Detail
			out = append(out, finding)
		}
	}
	if selected {
		out = append(out, state.Checks...)
	}
	return out
}

func (m *Manager) MCPPlan(ctx context.Context, req MCPEdit) (MCPPreview, error) {
	out := MCPPreview{Preview: Preview{Owner: m.Owner, Scope: m.Scope, Findings: []inventory.Finding{}, PendingApply: true}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if req.ExpectedSHA256 == "" || req.Config == nil {
		return out, result.Fail("INVALID_INPUT", "需要已读取的 MCP 摘要及完整定义", 2)
	}
	err := m.Store.Locked(func() error {
		if err := m.Store.Pending(); err != nil {
			return err
		}
		before, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		if _, _, err = m.load(&before); err != nil {
			return err
		}
		if _, ok := before.Entries[m.identityFile()]; !ok {
			return result.Fail("SOURCE_NOT_INITIALIZED", "先创建或打开项目", 4)
		}
		if req.ExpectedSHA256 != mcpDigest(before) {
			return result.Fail("PLAN_STALE", "MCP 定义已变化；草稿保留，请核对后重新读取", 3)
		}
		if req.ExpectedSelection != "" {
			state, err := m.mcpState(before)
			if err != nil {
				return err
			}
			if state.SelectionDigest != req.ExpectedSelection {
				return result.Fail("PLAN_STALE", "Profile 启停来源已变化；定义草稿保留，请重新核对", 3)
			}
		}
		compiled, err := compileMCPPolicyDrafts(req)
		if err != nil {
			return err
		}
		v, err := config.Validate("mcp", jsonBytes(compiled))
		if err != nil {
			return result.Fail("MCP_DEFINITION_INVALID", err.Error(), 5)
		}
		if err = m.validateMCPRemoval(before, v.(map[string]any)); err != nil {
			return err
		}
		after := workstore.Clone(before)
		mode := uint32(0644)
		if e, ok := before.Entries["mcp.yaml"]; ok {
			mode = e.Mode
		}
		if err = m.Store.PutFile(&after, "mcp.yaml", jsonBytes(v), mode); err != nil {
			return err
		}
		out.Candidate, err = m.mcpState(after)
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		out.Plan, err = m.preview("mcp-edit", before, after)
		return err
	})
	return out, err
}

func (m *Manager) ApplyMCP(ctx context.Context, id, digest string) (workstore.Journal, error) {
	if digest == "" {
		return workstore.Journal{}, result.Fail("INVALID_INPUT", "需要确认摘要", 2)
	}
	p, err := m.Store.LoadPlan(id)
	if err != nil {
		return workstore.Journal{}, err
	}
	if p.Action != "mcp-edit" {
		return workstore.Journal{}, result.Fail("INVALID_INPUT", "此入口仅确认 MCP 定义保存；回退使用源时间线", 2)
	}
	return m.Apply(ctx, id, digest)
}
