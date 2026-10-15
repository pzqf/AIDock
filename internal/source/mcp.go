package source

import (
	"context"
	"regexp"
	"strings"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// MCP definitions are declarations, never direct client launch commands.
// Saving enabled=true requests inclusion; it grants no execution permission.
type MCPState struct {
	Config         map[string]any `json:"config"`
	SHA256         string         `json:"sha256"`
	Present        bool           `json:"present"`
	Status         string         `json:"status"`
	Servers        []MCPCheck     `json:"servers"`
	Boundary       string         `json:"boundary"`
	ExecutionReady bool           `json:"execution_ready"`
}

type MCPCheck struct {
	Name           string              `json:"name"`
	Enabled        bool                `json:"enabled"`
	Status         string              `json:"status"`
	Checks         []inventory.Finding `json:"checks"`
	ExecutionReady bool                `json:"execution_ready"`
}

type MCPEdit struct {
	ExpectedSHA256 string         `json:"expected_sha256"`
	Config         map[string]any `json:"config"`
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

func (m *Manager) mcpState(t workstore.Tree) (MCPState, error) {
	out := MCPState{SHA256: mcpDigest(t), Status: "PASS", Servers: []MCPCheck{}, Boundary: "定义仅保存在当前项目 .aidock/mcp.yaml；启用是声明，不代表已安装、已授权或正在运行。工具配置、安装锁验证、隔离和会话生命周期分别检查。"}
	_, out.Present = t.Entries["mcp.yaml"]
	v, err := m.mcpConfig(t)
	if err != nil {
		return out, err
	}
	out.Config = v
	for _, raw := range array(v["servers"]) {
		s := raw.(map[string]any)
		item := MCPCheck{Name: s["name"].(string), Enabled: s["enabled"].(bool), Status: "UNKNOWN", Checks: []inventory.Finding{}}
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
		permissions := s["permissions"].(map[string]any)
		net := permissions["net"].(map[string]any)
		if net["default"] != "deny" {
			add("WARN", "MCP_NETWORK_BROAD", "默认允许网络；此声明需要单独授权和 OS 能力核对")
		}
		if s["tier"] == 0 {
			add("WARN", "MCP_UNISOLATED_REQUEST", "Tier 0 为非隔离声明，仍需显式信任选择与授权")
		}
		if _, exists := t.Entries["plugins.lock"]; exists {
			add("UNKNOWN", "MCP_LOCK_NOT_VERIFIED", "检测到 plugins.lock；尚未验证来源、完整依赖、本地产物、平台和运行时，不能据此启动")
		} else {
			add("UNKNOWN", "MCP_LOCK_MISSING", "尚无经过验证的安装锁")
		}
		add("UNKNOWN", "MCP_EXECUTION_PENDING", "授权、隔离和受管 stdio 启动尚未开放；当前未启动程序")
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

func (m *Manager) mcpFindings(t workstore.Tree) []inventory.Finding {
	if _, ok := t.Entries["mcp.yaml"]; !ok {
		return nil
	}
	state, err := m.mcpState(t)
	if err != nil {
		return []inventory.Finding{{Status: "FAIL", Code: "MCP_DEFINITION_INVALID", Detail: err.Error()}}
	}
	out := []inventory.Finding{}
	for _, server := range state.Servers {
		if !server.Enabled {
			continue
		}
		for _, finding := range server.Checks {
			finding.Detail = server.Name + "：" + finding.Detail
			out = append(out, finding)
		}
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
		v, err := config.Validate("mcp", jsonBytes(req.Config))
		if err != nil {
			return result.Fail("MCP_DEFINITION_INVALID", err.Error(), 5)
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
