package pluginpermission

import (
	"encoding/json"
	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
)

// ParseEdit rejects ambiguous confirmations and never reflects input values.
func ParseEdit(b []byte) (Edit, error) {
	var out Edit
	bad := func() (Edit, error) {
		return Edit{}, result.Fail("INVALID_INPUT", "授权请求字段、类型或格式无效；不接受重复键、别名与空值", 2)
	}
	v, err := config.Parse(b)
	if err != nil {
		return bad()
	}
	m, ok := v.(map[string]any)
	if !ok {
		return bad()
	}
	for key, value := range m {
		switch key {
		case "action", "expected_sha256", "expected_review_sha256", "name", "snapshot", "purpose", "data_level", "destination":
			if _, ok := value.(string); !ok {
				return bad()
			}
		case "confirm_permissions", "confirm_secrets", "confirm_unisolated":
			if _, ok := value.(bool); !ok {
				return bad()
			}
		default:
			return bad()
		}
	}
	if json.Unmarshal(bytesOf(m), &out) != nil {
		return bad()
	}
	return out, nil
}
