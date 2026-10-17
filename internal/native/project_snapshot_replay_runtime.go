package native

import (
	"context"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/runtimecheck"
	"github.com/pzqf/AIDock/internal/source"
)

type SnapshotReplayCandidateRequest struct {
	Snapshot  string                       `json:"snapshot_id"`
	Candidate string                       `json:"candidate_digest"`
	Restore   *SnapshotReplayRestoreTarget `json:"restore,omitempty"`
}

type SnapshotReplayRuntimeRequest struct {
	SnapshotReplayCandidateRequest
	Runtime source.MCPRuntimeRequest `json:"runtime"`
}

// Parse with the same strict YAML/JSON decoder as permission review. Callers
// cannot replace candidate trees, launch commands, roots or saved authorities.
func parseReplayCandidate(raw []byte, probe bool) (SnapshotReplayRuntimeRequest, error) {
	var out SnapshotReplayRuntimeRequest
	bad := func() (SnapshotReplayRuntimeRequest, error) {
		return SnapshotReplayRuntimeRequest{}, result.Fail("INVALID_INPUT", "需要明确快照、完整候选摘要及受限的运行时请求；不接受重复键、别名、空值或自定义命令", 2)
	}
	if len(raw) > 65536 {
		return bad()
	}
	v, err := config.Parse(raw)
	if err != nil {
		return bad()
	}
	m, ok := v.(map[string]any)
	n := 2
	if probe {
		n++
	}
	if _, exists := m["restore"]; exists {
		n++
	}
	if !ok || len(m) != n {
		return bad()
	}
	for key, value := range m {
		switch key {
		case "snapshot_id", "candidate_digest":
			if _, ok := value.(string); !ok {
				return bad()
			}
		case "restore":
			out.Restore, err = parseReplayRestoreTarget(value)
			if err != nil {
				return bad()
			}
		case "runtime":
			if !probe {
				return bad()
			}
			if _, ok := value.(map[string]any); !ok {
				return bad()
			}
		default:
			return bad()
		}
	}
	out.Snapshot, _ = m["snapshot_id"].(string)
	out.Candidate, _ = m["candidate_digest"].(string)
	if !validID.MatchString(out.Snapshot) || !validDigest.MatchString(out.Candidate) {
		return bad()
	}
	if probe {
		out.Runtime, err = source.ParseMCPRuntimeRequest(jsonData(m["runtime"]))
		if err != nil || !out.Runtime.Probe || out.Runtime.Name == "" || !validDigest.MatchString(out.Runtime.ExpectedSHA256) || !validDigest.MatchString(out.Runtime.ExpectedReviewSHA256) {
			return bad()
		}
	}
	return out, nil
}

func ParseSnapshotReplayCandidateRequest(raw []byte) (SnapshotReplayCandidateRequest, error) {
	v, err := parseReplayCandidate(raw, false)
	return v.SnapshotReplayCandidateRequest, err
}
func ParseSnapshotReplayRuntimeRequest(raw []byte) (SnapshotReplayRuntimeRequest, error) {
	return parseReplayCandidate(raw, true)
}

func (m *Manager) checkedReplayCandidate(ctx context.Context, req SnapshotReplayCandidateRequest) (source.ReplayDraft, error) {
	var d source.ReplayDraft
	if !validDigest.MatchString(req.Candidate) {
		return d, result.Fail("INVALID_INPUT", "需要已审查候选的完整摘要", 2)
	}
	d, _, err := m.snapshotReplayCandidateFor(ctx, req.Snapshot, req.Restore)
	if err == nil && d.Digest != req.Candidate {
		err = result.Fail("PLAN_STALE", "回放候选或当前草稿已变化，请重新审查", 3)
	}
	return d, err
}

type SnapshotReplayRuntimeReview struct {
	Snapshot string `json:"snapshot_id"`
	source.ReplayRuntimeReview
}

func (m *Manager) ReviewSnapshotReplayRuntime(ctx context.Context, req SnapshotReplayCandidateRequest) (SnapshotReplayRuntimeReview, error) {
	out := SnapshotReplayRuntimeReview{Snapshot: req.Snapshot}
	d, err := m.checkedReplayCandidate(ctx, req)
	if err != nil {
		return out, err
	}
	out.ReplayRuntimeReview, err = m.Source.ReplayRuntime(ctx, d)
	return out, err
}

func (m *Manager) CheckSnapshotReplayRuntime(ctx context.Context, req SnapshotReplayRuntimeRequest) (runtimecheck.Event, error) {
	d, err := m.checkedReplayCandidate(ctx, req.SnapshotReplayCandidateRequest)
	if err != nil {
		return runtimecheck.Event{}, err
	}
	return m.Source.CheckReplayRuntime(ctx, d, req.Runtime)
}

type SnapshotReplayClients struct {
	Snapshot  string                          `json:"snapshot_id"`
	Candidate string                          `json:"candidate_digest"`
	Clients   []source.MCPClientConfiguration `json:"clients"`
	Sessions  []source.MCPSessionRequest      `json:"sessions"`
	Boundary  string                          `json:"boundary"`
}

func (m *Manager) SnapshotReplayClientConfigs(ctx context.Context, req SnapshotReplayCandidateRequest, executable, home string) (SnapshotReplayClients, error) {
	out := SnapshotReplayClients{Snapshot: req.Snapshot, Candidate: req.Candidate, Clients: []source.MCPClientConfiguration{}, Sessions: []source.MCPSessionRequest{}, Boundary: "仅预览冻结候选全部启用 MCP 的连接参数，不写入原生工具、不启动插件或读取密钥。共同回放需使用单独提案；当前不同配置不能使用这些参数。实际应用前仍须重新核对授权、运行时和完整产物，运行时记录最多有效 24 小时。"}
	d, err := m.checkedReplayCandidate(ctx, req)
	if err != nil {
		return out, err
	}
	err = m.Source.WithReplayMCPClientConfigs(ctx, d, executable, home, func(clients []source.MCPClientConfiguration, sessions []source.MCPSessionRequest) error {
		out.Clients, out.Sessions = clients, sessions
		return nil
	})
	return out, err
}
