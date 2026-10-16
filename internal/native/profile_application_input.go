package native

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
)

func ParseProfileApplicationRequest(raw []byte) (ProfileApplicationRequest, error) {
	var out ProfileApplicationRequest
	bad := func() (ProfileApplicationRequest, error) {
		return ProfileApplicationRequest{}, result.Fail("INVALID_INPUT", "联合请求须明确技能规则计划与全部 MCP 审查绑定；不接受重复键、未知字段、空值或自定义命令", 2)
	}
	if len(raw) > 65536 {
		return bad()
	}
	v, err := config.Parse(raw)
	if err != nil {
		return bad()
	}
	fields, ok := v.(map[string]any)
	if !ok || len(fields) != 3 {
		return bad()
	}
	for name, value := range fields {
		switch name {
		case "native_plan", "native_digest":
			if _, ok := value.(string); !ok {
				return bad()
			}
		case "sessions":
			items, ok := value.([]any)
			if !ok || len(items) > 64 {
				return bad()
			}
			seen := map[string]bool{}
			for _, item := range items {
				s, e := source.ParseMCPSessionRequest(jsonData(item))
				if e != nil || s.Probe || !validName.MatchString(s.Name) || seen[s.Name] || !validDigest.MatchString(s.ExpectedSHA256) || !validDigest.MatchString(s.ExpectedReviewSHA256) || !validID.MatchString(s.ExpectedRuntimeAttempt) {
					return bad()
				}
				seen[s.Name] = true
			}
		default:
			return bad()
		}
	}
	if json.Unmarshal(jsonData(fields), &out) != nil || !validID.MatchString(out.NativePlan) || !validDigest.MatchString(out.NativeDigest) {
		return bad()
	}
	return out, nil
}

type ProfileApplicationEntry struct {
	Name    string                    `json:"name"`
	Status  string                    `json:"status"`
	Code    string                    `json:"code,omitempty"`
	Session *source.MCPSessionRequest `json:"session,omitempty"`
}

type ProfileApplicationReview struct {
	Status   string                    `json:"status"`
	Entries  []ProfileApplicationEntry `json:"entries"`
	Boundary string                    `json:"boundary"`
}

// Review only projects current receipts into a read-only checklist. The plan
// revalidates the exact enabled set and holds all authority locks on commit.
func (m *Manager) ReviewProfileApplication(ctx context.Context, executable, home string) (ProfileApplicationReview, error) {
	out := ProfileApplicationReview{Status: "READY", Entries: []ProfileApplicationEntry{}, Boundary: "核对当前有效 MCP 的已有安装、独立授权及运行时记录，不启动程序、不自动授权。仅这些明确绑定可进入联合预览；保存源、撤销或程序变化后须重新审查。没有启用项时，联合预览仍会列出所选工具中旧托管连接的移除。"}
	state, err := m.Source.MCP()
	if err != nil {
		return out, err
	}
	permission, err := m.Source.MCPPermission(ctx)
	if err != nil {
		return out, err
	}
	runtime, err := m.Source.MCPRuntime(ctx)
	if err != nil {
		return out, err
	}
	for _, server := range state.Servers {
		if !server.SelectionResolved {
			return out, result.Fail("PROFILE_RESOLUTION_FAILED", "先修复有效 Profile，再审查联合应用", 4)
		}
		if !server.EffectiveEnabled {
			continue
		}
		req := source.MCPSessionRequest{Name: server.Name, ExpectedSHA256: permission.SHA256, ExpectedReviewSHA256: permission.ReviewSHA256}
		for _, entry := range runtime.Entries {
			if entry.Event.Input.Name == server.Name {
				req.ExpectedRuntimeAttempt = entry.Event.Attempt
				break
			}
		}
		e := ProfileApplicationEntry{Name: server.Name, Status: "READY"}
		if _, err := m.Source.MCPClientConfig(ctx, req, executable, home); err != nil {
			e.Status, e.Code, out.Status = "NEEDS_ATTENTION", "MCP_CONNECTION_REVIEW_REQUIRED", "NEEDS_ATTENTION"
			var classified *result.Error
			if errors.As(err, &classified) {
				e.Code = classified.Code
			}
		} else {
			e.Session = &req
		}
		out.Entries = append(out.Entries, e)
	}
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Name < out.Entries[j].Name })
	return out, nil
}
