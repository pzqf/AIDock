package native

import (
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
)

// Empty preserves the original full-profile gate and legacy persisted digests.
const SkillsRulesScope = "skills-rules"

// NormalizeApplicationScope validates a shared compiler scope. Empty is the
// legacy full-profile gate, including in persisted candidate descriptors.
func NormalizeApplicationScope(value string) (string, error) {
	switch value {
	case "", "full":
		return "", nil
	case SkillsRulesScope:
		return value, nil
	default:
		return "", result.Fail("INVALID_INPUT", "application_scope 只支持 full 或 skills-rules", 2)
	}
}

func applicationBoundary(scope string) string {
	if scope == SkillsRulesScope {
		return "应用范围：仅技能与规则。MCP 配置由独立连接流程管理，本次保留其原值；范围外诊断不代表已修复或已生效。"
	}
	return "应用范围：完整 Profile 检查；未实现或未验证的配置条件仍阻断安装。"
}

// Only diagnostics marked by the source's MCP compiler are outside this scope.
// General Profile failures and skill dependencies remain gates, regardless of code.
// The Plan retains the original full Export for freshness and transaction checks.
func applicationBundle(bundle source.Export, scope string) (source.Export, []inventory.Finding) {
	if scope != SkillsRulesScope {
		return bundle, nil
	}
	included := make([]inventory.Finding, 0, len(bundle.Preflight.ProfileChecks))
	var excluded []inventory.Finding
	for _, finding := range bundle.Preflight.ProfileChecks {
		if finding.Component == "mcp" {
			excluded = append(excluded, finding)
		} else {
			included = append(included, finding)
		}
	}
	bundle.Preflight.ProfileChecks = included
	return bundle, excluded
}
