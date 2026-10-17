package engine

import (
	"regexp"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
)

var replayUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var replaySHA = regexp.MustCompile(`^[0-9a-f]{64}$`)

type ReplayRequest struct {
	Snapshot string   `json:"snapshot_id"`
	Kind     string   `json:"kind,omitempty"`
	Action   string   `json:"action,omitempty"`
	Copies   []string `json:"copies,omitempty"`
	Prepared string   `json:"prepared_id,omitempty"`
	Plan     string   `json:"plan_id,omitempty"`
	Digest   string   `json:"digest,omitempty"`
	Confirm  bool     `json:"confirm,omitempty"`
}

func ValidateReplayRequest(r ReplayRequest) error { return validateReplayRequest(r) }

func validateReplayRequest(r ReplayRequest) error {
	bad := func() error {
		return result.Fail("INVALID_INPUT", "需要明确恢复节点、动作及相应完整摘要；不接受空选择、混合阶段或隐式确认", 2)
	}
	if !replayUUID.MatchString(r.Snapshot) || r.Kind != "" && r.Kind != "project" && r.Kind != "replica" || r.Prepared != "" && !replayUUID.MatchString(r.Prepared) || r.Plan != "" && !replayUUID.MatchString(r.Plan) || r.Digest != "" && !replaySHA.MatchString(r.Digest) {
		return bad()
	}
	if r.Kind == "project" && r.Copies != nil || r.Kind == "replica" && (r.Prepared != "" || r.Action == "review") {
		return bad()
	}
	if r.Copies != nil {
		if len(r.Copies) == 0 || len(r.Copies) > 32 {
			return bad()
		}
		seen := map[string]bool{}
		for _, id := range r.Copies {
			if !replayUUID.MatchString(id) || seen[id] {
				return bad()
			}
			seen[id] = true
		}
	}
	switch r.Action {
	case "":
		if r.Prepared != "" || r.Plan != "" || r.Digest != "" || r.Confirm {
			return bad()
		}
	case "review":
		if r.Prepared != "" || r.Plan != "" || r.Digest != "" || r.Confirm || r.Copies != nil {
			return bad()
		}
	case "plan":
		if r.Plan != "" || r.Confirm || (r.Prepared == "") != (r.Digest == "") || r.Prepared != "" && r.Copies != nil {
			return bad()
		}
	case "planned", "apply":
		if r.Prepared != "" || r.Copies != nil || r.Plan == "" || r.Digest == "" || (r.Action == "apply") != r.Confirm {
			return bad()
		}
	default:
		return bad()
	}
	return nil
}

func ParseReplayRequest(raw []byte) (ReplayRequest, error) {
	var out ReplayRequest
	bad := func() (ReplayRequest, error) {
		return out, result.Fail("INVALID_INPUT", "恢复请求含未知字段、别名、重复键、空值或错误类型", 2)
	}
	if len(raw) > 65536 {
		return bad()
	}
	value, err := config.Parse(raw)
	if err != nil {
		return bad()
	}
	fields, ok := value.(map[string]any)
	if !ok || len(fields) == 0 {
		return bad()
	}
	for key, value := range fields {
		switch key {
		case "snapshot_id", "kind", "action", "prepared_id", "plan_id", "digest":
			s, ok := value.(string)
			if !ok || s == "" {
				return bad()
			}
			switch key {
			case "snapshot_id":
				out.Snapshot = s
			case "kind":
				out.Kind = s
			case "action":
				out.Action = s
			case "prepared_id":
				out.Prepared = s
			case "plan_id":
				out.Plan = s
			case "digest":
				out.Digest = s
			}
		case "confirm":
			b, ok := value.(bool)
			if !ok {
				return bad()
			}
			out.Confirm = b
		case "copies":
			ids, ok := value.([]any)
			if !ok {
				return bad()
			}
			out.Copies = []string{}
			for _, id := range ids {
				s, ok := id.(string)
				if !ok {
					return bad()
				}
				out.Copies = append(out.Copies, s)
			}
		default:
			return bad()
		}
	}
	return out, validateReplayRequest(out)
}
