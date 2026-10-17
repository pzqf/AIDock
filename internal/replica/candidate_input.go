package replica

import (
	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
)

// These requests contain identity, scalar confirmations and nested typed
// operations only. Reject duplicate/alias/null ambiguity before JSON decoding,
// and never echo risk reasons, purposes, file paths or unknown input values.
func ParseCandidateInput(raw []byte, target any) error {
	bad := func() error {
		return result.Fail("INVALID_INPUT", "候选请求需要唯一明确的字段和值；拒绝重复键、别名、空值、未知字段或错误类型", 2)
	}
	if len(raw) > 65536 {
		return bad()
	}
	value, err := config.Parse(raw)
	if err != nil {
		return bad()
	}
	fields, ok := value.(map[string]any)
	if !ok || len(fields) < 3 {
		return bad()
	}
	var scalarObjects func(any) bool
	scalarObjects = func(v any) bool {
		switch v := v.(type) {
		case string, bool:
			return true
		case map[string]any:
			for _, value := range v {
				if !scalarObjects(value) {
					return false
				}
			}
			return true
		}
		return false
	}
	if !scalarObjects(fields) || decode(data(fields), target) != nil {
		return bad()
	}
	return nil
}
