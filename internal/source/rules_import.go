package source

import (
	"path/filepath"
	"strings"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/rules"
	"github.com/pzqf/AIDock/internal/workstore"
)

// NativeRuleEdit observes only a fixed sibling of this project's .aidock root.
// It never grants write ownership or accepts an arbitrary filesystem path.
type NativeRuleEdit struct {
	Tool        string    `json:"tool"`
	SHA256      string    `json:"sha256"`
	Body        string    `json:"body"`
	Mode        string    `json:"mode"`
	MergedRules *[]string `json:"merged_rules,omitempty"`
}

func (m *Manager) ruleImportInputs(req *NativeRuleEdit) (map[string]string, error) {
	if req == nil {
		return nil, nil
	}
	inputs := map[string]string{"kind": "native-rule-import", "tool": req.Tool, "sha256": req.SHA256, "body": req.Body, "mode": req.Mode}
	if err := m.validateRuleImportInputs(inputs); err != nil {
		return nil, err
	}
	return inputs, nil
}
func (m *Manager) validateRuleImportInputs(inputs map[string]string) error {
	name := ""
	switch inputs["tool"] {
	case "codex-cli":
		name = "AGENTS.md"
	case "claude-code":
		name = "CLAUDE.md"
	}
	if m.Owner == "global" || name == "" || len(inputs) != 5 || !revisionPattern.MatchString("sha256:"+inputs["sha256"]) || (inputs["mode"] != "replace" && inputs["mode"] != "append") || inputs["body"] == "" {
		return result.Fail("INVALID_INPUT", "规则导入需要项目绑定、工具、完整文件摘要、托管正文和明确合并方式", 2)
	}
	data, err := workstore.ReadFile(filepath.Join(filepath.Dir(m.Store.Root), name))
	if err != nil || workstore.Hash(data) != inputs["sha256"] {
		return result.Fail("PLAN_STALE", "原生规则文件已变化或不可读取，请重新核对导入草稿", 3)
	}
	block, err := rules.Inspect(data)
	if err != nil {
		return err
	}
	if block.Body != inputs["body"] {
		return result.Fail("PLAN_STALE", "导入正文与观察的原生区块不符，请重新读取；合并编辑应放在规则草稿中", 3)
	}
	return nil
}

func importedRules(body, mode string, existing any) ([]any, bool) {
	parts := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n")
	values := []any{}
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			values = append(values, part)
		}
	}
	if mode == "append" {
		if old, ok := existing.([]any); ok {
			return append(append([]any{}, old...), values...), false
		}
	}
	return values, mode == "replace"
}
