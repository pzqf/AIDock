// Package native compiles saved skills into native previews. Explicit discovery
// probes initialize tool processes in disposable projects; no prompts, skill
// scripts, generators or user/project tool configuration are executed.
package native

import (
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/pzqf/AIDock/internal/nativeconfig"
	"github.com/pzqf/AIDock/internal/result"
)

// Descriptors are an independently versioned data contract, not executable
// plugins. Discovery acceptance must precede adding an exact verified version.
//
//go:embed adapters.json
var descriptors []byte

type Adapter struct {
	ID            string                 `json:"id"`
	Version       string                 `json:"version"`
	Directory     string                 `json:"project_directory"`
	Required      []string               `json:"required_frontmatter"`
	Reserved      []string               `json:"reserved_names"`
	Reference     string                 `json:"reference"`
	Checked       string                 `json:"contract_checked"`
	Observed      string                 `json:"observed_cli_version"`
	Verified      []string               `json:"verified_versions"`
	ProbeVersions []string               `json:"discovery_probe_versions"`
	RuleVersions  []string               `json:"rule_probe_versions,omitempty"`
	Limitations   string                 `json:"limitations"`
	MCPConfig     *nativeconfig.Contract `json:"mcp_config,omitempty"`
}

func Adapters() []Adapter {
	var out []Adapter
	if err := json.Unmarshal(descriptors, &out); err != nil {
		panic(err)
	}
	return out
}
func selectAdapters(ids []string) ([]Adapter, error) {
	if len(ids) == 0 {
		return nil, result.Fail("TOOL_REQUIRED", "请明确选择 claude-code 或 codex-cli", 2)
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		if wanted[id] {
			return nil, result.Fail("INVALID_INPUT", "工具选择重复", 2)
		}
		if id != "claude-code" && id != "codex-cli" {
			return nil, result.Fail("TOOL_UNSUPPORTED", "未知工具："+id, 2)
		}
		wanted[id] = true
	}
	out := []Adapter{}
	for _, a := range Adapters() {
		if wanted[a.ID] {
			out = append(out, a)
		}
	}
	return out, nil
}
func reserved(a Adapter, name string) bool {
	for _, n := range a.Reserved {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}
