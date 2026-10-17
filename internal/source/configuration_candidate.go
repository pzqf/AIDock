package source

import (
	"context"

	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/runtimecheck"
	"github.com/pzqf/AIDock/internal/workstore"
)

const replicaCandidateIntent = "replica-candidate"

// ConfigurationCandidate is immutable compiler material supplied by an internal
// owner of a source proposal. No HTTP/CLI decoder can supply trees or grants.
// It retains the destination's live library and Local association; historical
// replay has a different intent and deliberately projects frozen layers.
type ConfigurationCandidate struct {
	draft ReplayDraft
}

func (c ConfigurationCandidate) Digest() string { return c.draft.Digest }

func (m *Manager) DraftConfigurationCandidate(ctx context.Context, before, after workstore.Tree, expectedConfiguration string) (ConfigurationCandidate, error) {
	var out ConfigurationCandidate
	if m.Owner == "global" || m.Copy == "" {
		return out, result.Fail("INVALID_SOURCE_BINDING", "候选配置需要明确的项目、作用域与目标副本", 2)
	}
	err := m.Store.Locked(func() error {
		return WithConfigurationLocks([]*Manager{m}, func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := m.Store.Pending(); err != nil {
				return err
			}
			current, err := m.Store.Capture(m.Store.Root)
			if err != nil {
				return err
			}
			if workstore.Digest(current) != workstore.Digest(before) {
				return result.Fail("PLAN_STALE", "目标项目源已偏离候选提案；未授权或写入", 3)
			}
			if err := m.ValidateReplayIdentity(ctx, false); err != nil {
				return err
			}
			// Reuse current export for external-authority protection only. The
			// future effective configuration is independently resolved below.
			original, err := m.exportLocked()
			if err != nil {
				return err
			}
			local, err := m.localProfileStore()
			if err != nil {
				return err
			}
			if err := local.Pending(); err != nil {
				return err
			}
			localTree, err := local.Capture(local.Root)
			if err != nil {
				return err
			}
			for _, e := range localTree.Entries {
				if e.Dir {
					continue
				}
				body, err := local.Data(e)
				if err != nil {
					return err
				}
				scratch := workstore.Tree{Entries: map[string]workstore.Entry{}}
				if err := m.Store.PutFile(&scratch, e.Hash, body, e.Mode); err != nil {
					return err
				}
			}
			idx, shared, err := m.load(&after)
			if err != nil {
				return err
			}
			if _, err := m.replayProject(after); err != nil {
				return err
			}
			_, skills, err := m.resolve(&after, idx, shared)
			if err != nil {
				return err
			}
			layers, err := m.profileLayers()
			if err != nil {
				return err
			}
			if expectedConfiguration == "" || layers.Digest != expectedConfiguration {
				return result.Fail("PLAN_STALE", "目标配置层不再匹配原候选提案；重新比较", 3)
			}
			report, err := m.resolveProfile(after, shared, skills, layers)
			if err != nil {
				return err
			}
			preflight, err := m.preflight(after, idx, shared, layers)
			if err != nil {
				return err
			}
			if err := blockConfigurationCandidate(preflight); err != nil {
				return err
			}
			d := ReplayDraft{Intent: replicaCandidateIntent, SourceBefore: workstore.Clone(before), SourceAfter: workstore.Clone(after), LocalBefore: localTree, LocalAfter: workstore.Clone(localTree), Layers: layers, Report: report, Preflight: preflight, Authorities: original.Authorities, BridgeDigest: original.BridgeDigest}
			d.Digest = m.replayDraftDigest(d)
			out = ConfigurationCandidate{draft: d}
			return nil
		})
	})
	return out, err
}

// A source proposal must exist before the target can import its new package.
// Retain MCP failures in the report, but defer those live dependency gates to
// installation, permission, execution and complete native application. This
// function never saves the source or generates a connection.
func blockConfigurationCandidate(r Preflight) error {
	checks := r.ProfileChecks
	r.ProfileChecks = nil
	for _, f := range checks {
		if f.Component != "mcp" {
			r.ProfileChecks = append(r.ProfileChecks, f)
		}
	}
	return blockEnabled(r)
}

func (m *Manager) validateConfigurationCandidate(c ConfigurationCandidate) error {
	if c.draft.Intent != replicaCandidateIntent || c.Digest() == "" || c.Digest() != m.replayDraftDigest(c.draft) {
		return result.Fail("PLAN_STALE", "候选配置不属于当前目标绑定；重新读取提案", 3)
	}
	return nil
}

func (m *Manager) CandidatePermissions(ctx context.Context, c ConfigurationCandidate) (ReplayPermissionReview, error) {
	if err := m.validateConfigurationCandidate(c); err != nil {
		return ReplayPermissionReview{}, err
	}
	out, err := m.ReplayPermissions(ctx, c.draft)
	out.Boundary = "只审查此目标的未来配置，不修改源、本机覆盖或工具文件，不启动进程或读取密钥。候选授权使用目标自己的审批与审计，明确确认后会替换同名 MCP 的旧授权；安装、运行时检查及整批发布分别处理。"
	return out, err
}
func (m *Manager) PlanCandidatePermission(ctx context.Context, c ConfigurationCandidate, req pluginpermission.Edit) (pluginpermission.Preview, error) {
	if err := m.validateConfigurationCandidate(c); err != nil {
		return pluginpermission.Preview{}, err
	}
	return m.PlanReplayPermission(ctx, c.draft, req)
}
func (m *Manager) ApplyCandidatePermission(ctx context.Context, c ConfigurationCandidate, name, id, digest string) (workstore.Journal, error) {
	if err := m.validateConfigurationCandidate(c); err != nil {
		return workstore.Journal{}, err
	}
	return m.ApplyReplayPermission(ctx, c.draft, name, id, digest)
}
func (m *Manager) CandidateRuntime(ctx context.Context, c ConfigurationCandidate) (ReplayRuntimeReview, error) {
	if err := m.validateConfigurationCandidate(c); err != nil {
		return ReplayRuntimeReview{}, err
	}
	out, err := m.ReplayRuntime(ctx, c.draft)
	out.Boundary = "只读取目标本机运行时证据，current 仅匹配此精确候选与目标授权，不代表不同配置或实际插件运行。"
	return out, err
}
func (m *Manager) CheckCandidateRuntime(ctx context.Context, c ConfigurationCandidate, req MCPRuntimeRequest) (runtimecheck.Event, error) {
	if err := m.validateConfigurationCandidate(c); err != nil {
		return runtimecheck.Event{}, err
	}
	return m.CheckReplayRuntime(ctx, c.draft, req)
}
func (m *Manager) WithCandidateMCPClientConfigs(ctx context.Context, c ConfigurationCandidate, executable, home string, use func([]MCPClientConfiguration, []MCPSessionRequest) error) error {
	if err := m.validateConfigurationCandidate(c); err != nil {
		return err
	}
	return m.WithReplayMCPClientConfigs(ctx, c.draft, executable, home, use)
}
func (m *Manager) WithCandidateExport(ctx context.Context, c ConfigurationCandidate, use func(Export) error) error {
	if err := m.validateConfigurationCandidate(c); err != nil {
		return err
	}
	return m.WithReplayExport(ctx, c.draft, use)
}
func (m *Manager) WithCandidateSnapshotInputs(ctx context.Context, c ConfigurationCandidate, use func(Export, workstore.Tree, SnapshotConfiguration) error) error {
	if err := m.validateConfigurationCandidate(c); err != nil {
		return err
	}
	return m.WithReplaySnapshotInputs(ctx, c.draft, use)
}
