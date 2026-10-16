package source

import (
	"context"
	"path/filepath"
	"sort"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/runtimecheck"
	"github.com/pzqf/AIDock/internal/workstore"
)

type ReplayRuntimeReview struct {
	Candidate        string              `json:"candidate_digest"`
	PermissionSHA256 string              `json:"permission_sha256"`
	ReviewSHA256     string              `json:"review_sha256"`
	Runtime          runtimecheck.State  `json:"runtime"`
	Checks           []inventory.Finding `json:"checks"`
	Boundary         string              `json:"boundary"`
}

func (m *Manager) ReplayRuntime(ctx context.Context, d ReplayDraft) (ReplayRuntimeReview, error) {
	out := ReplayRuntimeReview{Candidate: d.Digest, Checks: []inventory.Finding{}, Boundary: "只读当前本机运行时审计；current 仅表示与这个冻结候选的现有授权匹配，不代表当前不同配置或插件已运行。未启动进程、未读取密钥、未应用源和工具。"}
	err := m.withReplayPermission(ctx, d, func(p *pluginpermission.Manager, c []pluginpermission.Candidate, checks []inventory.Finding) error {
		s, err := p.State(c)
		if err != nil {
			return err
		}
		out.PermissionSHA256, out.ReviewSHA256 = s.SHA256, s.ReviewSHA256
		for _, f := range append(checks, s.Checks...) {
			out.Checks = append(out.Checks, inventory.Finding{Component: f.Component, Subject: f.Subject, Code: f.Code, Status: f.Status})
		}
		r, err := m.runtimeManager()
		if err != nil {
			return err
		}
		out.Runtime, err = r.State(currentRuntimeInputs(s))
		return err
	})
	return out, err
}

func (m *Manager) CheckReplayRuntime(ctx context.Context, d ReplayDraft, req MCPRuntimeRequest) (runtimecheck.Event, error) {
	var out runtimecheck.Event
	if !req.Probe || req.Name == "" || len(req.ExpectedSHA256) != 64 || len(req.ExpectedReviewSHA256) != 64 {
		return out, result.Fail("INVALID_INPUT", "候选版本核验需要明确 probe、插件及刚审查的授权摘要", 2)
	}
	err := m.withReplayPermission(ctx, d, func(p *pluginpermission.Manager, c []pluginpermission.Candidate, checks []inventory.Finding) error {
		for _, f := range checks {
			if f.Status == "FAIL" {
				return result.Fail("RUNTIME_CHECK_INPUT_INVALID", "候选安装、完整产物或安全证据无效；未执行", 4)
			}
		}
		return m.checkMCPRuntimeLocked(ctx, req, d.SourceAfter, p, c, &out)
	})
	return out, err
}

// The projection freezes shared input into the project and removes the live
// global-library association. Future argv must use that future binding rather
// than the library attached to today's unmodified saved draft.
func (m *Manager) WithReplayMCPClientConfigs(ctx context.Context, d ReplayDraft, executable, home string, use func([]MCPClientConfiguration, []MCPSessionRequest) error) error {
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || !filepath.IsAbs(home) || filepath.Join(home, "local") != m.Store.Local || use == nil {
		return result.Fail("MCP_CLIENT_CONFIG_BINDING_INVALID", "候选连接需要当前显式 home、稳定绝对程序路径与内部消费者", 2)
	}
	return m.withReplayPermission(ctx, d, func(p *pluginpermission.Manager, c []pluginpermission.Candidate, checks []inventory.Finding) error {
		section, _ := d.Report.Effective["mcp"].(map[string]any)
		enabled := map[string]bool{}
		for _, raw := range array(section["enable"]) {
			enabled[raw.(string)] = true
		}
		for _, raw := range array(section["disable"]) {
			delete(enabled, raw.(string))
		}
		names := []string{}
		for name := range enabled {
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) == 0 {
			return use([]MCPClientConfiguration{}, []MCPSessionRequest{})
		}
		for _, f := range checks {
			if f.Status == "FAIL" {
				return result.Fail("MCP_SESSION_INPUT_INVALID", "候选依赖、安装或安全证据无效；未生成连接", 4)
			}
		}
		s, err := p.State(c)
		if err != nil {
			return err
		}
		r, err := m.runtimeManager()
		if err != nil {
			return err
		}
		versions, err := r.State(currentRuntimeInputs(s))
		if err != nil {
			return err
		}
		requests := []MCPSessionRequest{}
		for _, name := range names {
			req := MCPSessionRequest{Name: name, Probe: true, ExpectedSHA256: s.SHA256, ExpectedReviewSHA256: s.ReviewSHA256}
			for _, v := range versions.Entries {
				if v.Event.Input.Name == name {
					req.ExpectedRuntimeAttempt = v.Event.Attempt
					break
				}
			}
			if req.ExpectedRuntimeAttempt == "" {
				return result.Fail("REPLAY_RUNTIME_CHECK_REQUIRED", "候选启用的 MCP 缺少本次授权对应的运行时记录；请明确核验全部启用项", 4)
			}
			requests = append(requests, req)
		}
		local, err := m.localProfileStore()
		if err != nil {
			return err
		}
		recheck := func(checkCtx context.Context) error {
			if err := checkCtx.Err(); err != nil {
				return err
			}
			current, err := m.Store.Capture(m.Store.Root)
			if err != nil {
				return err
			}
			currentLocal, err := local.Capture(local.Root)
			if err != nil {
				return err
			}
			if workstore.Digest(current) != workstore.Digest(d.SourceBefore) || workstore.Digest(currentLocal) != workstore.Digest(d.LocalBefore) {
				return result.Fail("PLAN_STALE", "生成候选连接期间当前源或本机草稿变化", 3)
			}
			return nil
		}
		return m.withMCPSessionAuthorization(ctx, d.SourceAfter, p, c, requests, recheck, func(sessions []lockedMCPSession) error {
			future := *m
			future.Library = nil
			future.ProfileLibrary = false
			out := []MCPClientConfiguration{}
			for i, req := range requests {
				if err := sessions[i].Check(ctx); err != nil {
					return err
				}
				config, err := future.clientConfiguration(req, executable, home)
				if err != nil {
					return err
				}
				config.Boundary = "只读的冻结候选连接参数；仅在该候选实际应用且授权及运行时证据仍匹配时可启动。当前不同配置无法使用。未写入工具、启动插件或读取密钥；保留原受管 stdio、Public 合同与 Tier 0 明确非隔离门槛。"
				out = append(out, config)
				requests[i].Probe = false
			}
			return use(out, requests)
		})
	})
}
