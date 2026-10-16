package source

import (
	"encoding/json"
	"strconv"

	"github.com/pzqf/AIDock/internal/mcppolicy"
	"github.com/pzqf/AIDock/internal/result"
)

// The browser sends edited JSON as text so duplicate keys cannot disappear in
// JSON.parse before the authoritative parser reviews the original document.
func compileMCPPolicyDrafts(req MCPEdit) (map[string]any, error) {
	bad := func() (map[string]any, error) {
		return nil, result.Fail("MCP_TOOL_POLICY_INVALID", "工具策略草稿格式或服务器索引无效；原草稿保留", 2)
	}
	if len(req.ToolPolicyDocuments) > 64 {
		return bad()
	}
	var copy map[string]any
	if json.Unmarshal(jsonBytes(req.Config), &copy) != nil || copy == nil {
		return bad()
	}
	servers, ok := copy["servers"].([]any)
	if !ok {
		return bad()
	}
	for key, raw := range req.ToolPolicyDocuments {
		i, err := strconv.Atoi(key)
		if err != nil || strconv.Itoa(i) != key || i < 0 || i >= len(servers) {
			return bad()
		}
		s, ok := servers[i].(map[string]any)
		if !ok {
			return bad()
		}
		if raw == "" {
			delete(s, "tool_policy")
			continue
		}
		p, err := mcppolicy.Parse([]byte(raw))
		if err != nil {
			return bad()
		}
		var doc any
		if json.Unmarshal(jsonBytes(p), &doc) != nil {
			return bad()
		}
		s["tool_policy"] = doc
	}
	return copy, nil
}
