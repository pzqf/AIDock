package source

import (
	"context"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/pluginmanifest"
	"github.com/pzqf/AIDock/internal/result"
)

type MCPManifestState struct {
	Status         string                  `json:"status"`
	Plugins        []pluginmanifest.Report `json:"plugins"`
	Checks         []inventory.Finding     `json:"checks"`
	Boundary       string                  `json:"boundary"`
	ExecutionReady bool                    `json:"execution_ready"`
}

func (m *Manager) MCPManifest(ctx context.Context, name string) (MCPManifestState, error) {
	out := MCPManifestState{Status: "PASS", Plugins: []pluginmanifest.Report{}, Checks: []inventory.Finding{}, Boundary: "当前为预备产物的只读安装前检查。直接依赖来自 plugin.yaml，并与已有 npm 解析锁核对；未登记为已安装，不生成正式 SBOM。恶意代码/CVE、静态分析、身份、授权与隔离仍需独立证据，检查不提升信任等级。"}
	lock, err := m.MCPLock(ctx)
	if err != nil {
		return out, err
	}
	out.Checks = lock.Checks
	if len(lock.Manifests) == 0 && len(lock.Definitions) > 0 && lock.Present {
		out.Checks = append(out.Checks, inventory.Finding{Status: "UNKNOWN", Code: "MCP_MANIFEST_NO_LOCKED_ARTIFACTS", Detail: "已有 MCP 定义，但安装锁尚未登记产物；先读取本地产物并保存锁，再检查清单"})
	}
	for _, r := range lock.Manifests {
		if name == "" || r.Name == name {
			out.Plugins = append(out.Plugins, r)
			if statusRank(r.Status) > statusRank(out.Status) {
				out.Status = r.Status
			}
		}
	}
	if name != "" && len(out.Plugins) == 0 {
		return out, result.Fail("MCP_LOCK_ENTRY_MISSING", "所选 MCP 尚无有效的安装锁条目；先保存定义并生成安装锁", 4)
	}
	for _, f := range out.Checks {
		if statusRank(f.Status) > statusRank(out.Status) {
			out.Status = f.Status
		}
	}
	return out, nil
}
