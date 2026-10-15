package source

import (
	"encoding/json"
	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
)

// ParseMCPEdit rejects ambiguous maps before a decoder can discard duplicate
// env entries. Neither parse nor shape diagnostics echo possible secret values.
func ParseMCPEdit(b []byte) (MCPEdit, error) {
	bad := func() (MCPEdit, error) {
		return MCPEdit{}, result.Fail("INVALID_INPUT", "MCP 请求格式或字段无效；不接受重复键、别名与空值", 2)
	}
	v, err := config.Parse(b)
	if err != nil {
		return bad()
	}
	doc, ok := v.(map[string]any)
	if !ok {
		return bad()
	}
	for k, raw := range doc {
		switch k {
		case "expected_sha256", "expected_selection_digest":
			if _, ok := raw.(string); !ok {
				return bad()
			}
		case "config":
			if _, ok := raw.(map[string]any); !ok {
				return bad()
			}
		default:
			return bad()
		}
	}
	var out MCPEdit
	if json.Unmarshal(jsonBytes(doc), &out) != nil {
		return bad()
	}
	return out, nil
}
