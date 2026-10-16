package native

import (
	"encoding/json"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
)

func ParseMCPConnectionRequest(raw []byte) (MCPConnectionRequest, error) {
	var req MCPConnectionRequest
	bad := func() (MCPConnectionRequest, error) {
		return MCPConnectionRequest{}, result.Fail("INVALID_INPUT", "连接请求字段或类型无效；不接受重复键、别名、空值或自定义命令", 2)
	}
	if len(raw) > 16384 {
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
		case "action", "check_id":
			if _, ok := value.(string); !ok {
				return bad()
			}
		case "tools":
			tools, ok := value.([]any)
			if !ok || len(tools) < 1 || len(tools) > 2 {
				return bad()
			}
			for _, tool := range tools {
				if _, ok := tool.(string); !ok {
					return bad()
				}
			}
		case "session":
			if _, ok := value.(map[string]any); !ok {
				return bad()
			}
			if _, err := source.ParseMCPSessionRequest(jsonData(value)); err != nil {
				return bad()
			}
		default:
			return bad()
		}
	}
	if json.Unmarshal(jsonData(fields), &req) != nil {
		return bad()
	}
	return req, nil
}
