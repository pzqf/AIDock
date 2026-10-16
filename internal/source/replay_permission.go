package source

import (
	"context"
	"encoding/json"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/mcppolicy"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// ReplayPermissionReview exposes the inputs that require human review, never
// archived profile/rule bodies, previous purposes, vault values or grant files.
type ReplayPermissionEntry struct {
	Name          string                            `json:"name"`
	Enabled       bool                              `json:"enabled"`
	Installation  string                            `json:"installation"`
	Authorization string                            `json:"authorization"`
	ReplacesGrant bool                              `json:"replaces_grant"`
	Permissions   map[string]any                    `json:"permissions"`
	Tier          int                               `json:"tier"`
	Secrets       []string                          `json:"secrets"`
	ToolPolicy    *mcppolicy.Policy                 `json:"tool_policy,omitempty"`
	Unisolated    *pluginpermission.UnisolatedInput `json:"unisolated,omitempty"`
}

type ReplayPermissionReview struct {
	Candidate    string                  `json:"candidate_digest"`
	SHA256       string                  `json:"sha256"`
	ReviewSHA256 string                  `json:"review_sha256"`
	Entries      []ReplayPermissionEntry `json:"entries"`
	Checks       []inventory.Finding     `json:"checks"`
	Recovery     bool                    `json:"recovery_required"`
	Boundary     string                  `json:"boundary"`
}

// Only internal archive reconstruction supplies d. Every call holds current
// source/Local/artifact locks and rejects changed saved drafts. Candidate grants
// use the existing authoritative approval store and audit, not a parallel store.
func (m *Manager) withReplayPermission(ctx context.Context, d ReplayDraft, use func(*pluginpermission.Manager, []pluginpermission.Candidate, []inventory.Finding) error) error {
	if d.Digest == "" || d.Digest != m.replayDraftDigest(d) || use == nil {
		return result.Fail("PLAN_STALE", "回放候选绑定无效，请从原快照重新审查", 3)
	}
	return m.Store.Locked(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.Store.Pending(); err != nil {
			return err
		}
		local, err := m.localProfileStore()
		if err != nil {
			return err
		}
		return local.Locked(func() error {
			if err := local.Pending(); err != nil {
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
				return result.Fail("PLAN_STALE", "审查后项目源或本机草稿变化，未更改授权；请重新审查回放候选", 3)
			}
			idx, shared, err := m.load(&d.SourceAfter)
			if err != nil {
				return err
			}
			_, skills, err := m.resolve(&d.SourceAfter, idx, shared)
			if err != nil {
				return err
			}
			report, err := m.resolveProfile(d.SourceAfter, shared, skills, d.Layers)
			if err != nil {
				return err
			}
			if string(jsonBytes(report)) != string(jsonBytes(d.Report)) {
				return result.Fail("PLAN_STALE", "回放候选配置与审查摘要不符", 3)
			}
			return m.withArtifactLocks(d.SourceAfter, func() error {
				lock, err := m.mcpLockState(ctx, d.SourceAfter)
				if err != nil {
					return err
				}
				candidates, checks, err := m.permissionInputs(d.SourceAfter, lock, &report)
				if err != nil {
					return err
				}
				p, err := m.permissionManager()
				if err != nil {
					return err
				}
				return use(p, candidates, checks)
			})
		})
	})
}

func (m *Manager) ReplayPermissions(ctx context.Context, d ReplayDraft) (ReplayPermissionReview, error) {
	out := ReplayPermissionReview{Candidate: d.Digest, Entries: []ReplayPermissionEntry{}, Checks: []inventory.Finding{}, Boundary: "只审查冻结回放候选，项目源、本机覆盖和工具文件不变；不启动程序、不读取密钥。明确确认候选授权会替换同名 MCP 的现有授权，使当前配置的旧授权和运行时证据失效。授权仅匹配此精确候选；完整应用回放尚未开放。"}
	err := m.withReplayPermission(ctx, d, func(p *pluginpermission.Manager, candidates []pluginpermission.Candidate, checks []inventory.Finding) error {
		s, err := p.State(candidates)
		if err != nil {
			return err
		}
		out.SHA256, out.ReviewSHA256, out.Recovery = s.SHA256, s.ReviewSHA256, s.Recovery
		for _, f := range append(checks, s.Checks...) {
			out.Checks = append(out.Checks, inventory.Finding{Component: f.Component, Subject: f.Subject, Status: f.Status, Code: f.Code})
		}
		enabled := map[string]bool{}
		section, _ := d.Report.Effective["mcp"].(map[string]any)
		for _, raw := range array(section["enable"]) {
			enabled[raw.(string)] = true
		}
		for _, raw := range array(section["disable"]) {
			enabled[raw.(string)] = false
		}
		conf, err := m.mcpConfig(d.SourceAfter)
		if err != nil {
			return err
		}
		for _, raw := range array(conf["servers"]) {
			server := raw.(map[string]any)
			name := server["name"].(string)
			e := ReplayPermissionEntry{Name: name, Enabled: enabled[name], Installation: "MISSING", Authorization: "NONE", Permissions: server["permissions"].(map[string]any), Tier: server["tier"].(int), Secrets: []string{}}
			for _, c := range candidates {
				if c.Input.Name == name {
					e.Installation = c.Status
					e.Secrets = append([]string{}, c.Input.Secrets...)
					e.ToolPolicy = c.Input.ToolPolicy
					e.Unisolated = c.Input.Unisolated
				}
			}
			for _, a := range s.Authorizations {
				if a.Record.Input.Name == e.Name {
					e.Authorization = a.Status
					e.ReplacesGrant = true
				}
			}
			out.Entries = append(out.Entries, e)
		}
		return nil
	})
	return out, err
}

func (m *Manager) PlanReplayPermission(ctx context.Context, d ReplayDraft, req pluginpermission.Edit) (pluginpermission.Preview, error) {
	var out pluginpermission.Preview
	if req.Action != "grant" {
		return out, result.Fail("INVALID_INPUT", "回放候选只接受本次明确授权；撤销仍使用项目授权入口，历史授权不回放", 2)
	}
	err := m.withReplayPermission(ctx, d, func(p *pluginpermission.Manager, c []pluginpermission.Candidate, checks []inventory.Finding) (err error) {
		for _, f := range checks {
			if f.Status == "FAIL" {
				return result.Fail("REPLAY_PERMISSION_INPUT_INVALID", "候选依赖、安装或安全证据无效；未授权", 4)
			}
		}
		out, err = p.Plan(ctx, req, c, d.Digest)
		return err
	})
	return out, err
}

func (m *Manager) ApplyReplayPermission(ctx context.Context, d ReplayDraft, name, id, digest string) (workstore.Journal, error) {
	var out workstore.Journal
	err := m.withReplayPermission(ctx, d, func(p *pluginpermission.Manager, c []pluginpermission.Candidate, checks []inventory.Finding) (err error) {
		for _, f := range checks {
			if f.Status == "FAIL" {
				return result.Fail("REPLAY_PERMISSION_INPUT_INVALID", "候选依赖、安装或安全证据已变化；未授权", 4)
			}
		}
		plan, err := p.Store.LoadPlan(id)
		if err != nil {
			return err
		}
		if plan.Action != "permission-grant" {
			return result.Fail("INVALID_INPUT", "此入口只确认已审查的候选授权", 2)
		}
		var request pluginpermission.Edit
		if json.Unmarshal([]byte(plan.Inputs["request"]), &request) != nil || request.Name != name {
			return result.Fail("PLAN_STALE", "候选授权插件与原预览不符", 3)
		}
		out, err = p.Apply(ctx, id, digest, c, d.Digest)
		return err
	})
	return out, err
}
