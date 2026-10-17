package source

import (
	"context"
	"encoding/json"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/pluginartifact"
	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/profile"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

func (m *Manager) withCandidateObservation(ctx context.Context, c ConfigurationCandidate, use func() error) error {
	if err := m.validateConfigurationCandidate(c); err != nil {
		return err
	}
	if use == nil {
		return result.Fail("INVALID_INPUT", "候选操作需要明确消费者", 2)
	}
	return m.withReplayObservation(ctx, c.draft, func(profile.Report) error { return use() })
}

func (m *Manager) CandidateArtifacts(ctx context.Context, c ConfigurationCandidate) (MCPArtifactState, error) {
	out := MCPArtifactState{LockSHA256: lockDigest(c.draft.SourceAfter), Artifacts: []pluginartifact.State{}, Checks: []inventory.Finding{}, Boundary: "按目标未来配置核验自己的精确产物，不写源、工具或安装记录；缺包时可独立导入已核对 ZIP，旧版本目录保留。"}
	err := m.withCandidateObservation(ctx, c, func() error {
		conf, err := m.mcpConfig(c.draft.SourceAfter)
		if err != nil {
			return err
		}
		if _, exists := c.draft.SourceAfter.Entries["plugins.lock"]; !exists && len(array(conf["servers"])) == 0 {
			return nil
		}
		plugins, err := m.artifactInputs(c.draft.SourceAfter)
		if err != nil {
			return err
		}
		for _, p := range plugins {
			a, err := m.artifactManager(p)
			if err != nil {
				return err
			}
			s, err := a.State()
			if err != nil {
				return err
			}
			out.Artifacts = append(out.Artifacts, s)
		}
		return nil
	})
	return out, artifactRecoveryError(err)
}

func (m *Manager) withCandidateArtifact(ctx context.Context, c ConfigurationCandidate, name string, use func(*pluginartifact.Manager) error) error {
	return artifactRecoveryError(m.withCandidateObservation(ctx, c, func() error {
		plugins, err := m.artifactInputs(c.draft.SourceAfter)
		if err != nil {
			return err
		}
		conf, err := m.mcpConfig(c.draft.SourceAfter)
		if err != nil {
			return err
		}
		for _, p := range plugins {
			if p.Name != name {
				continue
			}
			matched := false
			for _, raw := range array(conf["servers"]) {
				s := raw.(map[string]any)
				if s["name"] == name && pluginlock.Definition(s) == p.DefinitionSHA256 {
					matched = true
				}
			}
			if !matched {
				return result.Fail("MCP_LOCK_DEFINITION_MISMATCH", "未来配置的 MCP 定义与锁不符，未导入产物", 5)
			}
			a, err := m.artifactManager(p)
			if err != nil {
				return err
			}
			return use(a)
		}
		return result.Fail("MCP_LOCK_ENTRY_MISSING", "未来配置未锁定此 MCP", 4)
	}))
}
func (m *Manager) PlanCandidateArtifact(ctx context.Context, c ConfigurationCandidate, req pluginartifact.Edit) (pluginartifact.Preview, error) {
	var out pluginartifact.Preview
	if req.Action != "import" {
		return out, result.Fail("INVALID_INPUT", "候选入口仅导入已锁定的完整 ZIP；回退与恢复使用原产物入口", 2)
	}
	err := m.withCandidateArtifact(ctx, c, req.Name, func(a *pluginartifact.Manager) (err error) {
		if req.ExpectedLockSHA256 == "" || req.ExpectedLockSHA256 != lockDigest(c.draft.SourceAfter) {
			return result.Fail("PLAN_STALE", "未来安装锁与刚读取的候选不符，未导入", 3)
		}
		out, err = a.Plan(ctx, req, c.Digest())
		return err
	})
	return out, err
}
func (m *Manager) ApplyCandidateArtifact(ctx context.Context, c ConfigurationCandidate, name, id, digest string) (workstore.Journal, error) {
	var out workstore.Journal
	err := m.withCandidateArtifact(ctx, c, name, func(a *pluginartifact.Manager) (err error) {
		p, err := a.Store.LoadPlan(id)
		if err != nil {
			return err
		}
		if p.Action != "artifact-import" {
			return result.Fail("INVALID_INPUT", "候选产物确认只接受导入计划", 2)
		}
		out, err = a.Apply(ctx, id, digest, c.Digest())
		return err
	})
	return out, err
}

func (m *Manager) withCandidateInstallation(ctx context.Context, c ConfigurationCandidate, use func(*plugininstall.Manager, []plugininstall.Candidate, []inventory.Finding) error) error {
	return m.withCandidateObservation(ctx, c, func() error {
		return m.withArtifactLocks(c.draft.SourceAfter, func() error {
			lock, err := m.mcpLockState(ctx, c.draft.SourceAfter)
			if err != nil {
				return err
			}
			i, err := m.installManager()
			if err != nil {
				return err
			}
			return use(i, installCandidates(lock), lock.Checks)
		})
	})
}
func (m *Manager) CandidateInstallations(ctx context.Context, c ConfigurationCandidate) (plugininstall.State, error) {
	var out plugininstall.State
	err := m.withCandidateInstallation(ctx, c, func(i *plugininstall.Manager, candidates []plugininstall.Candidate, checks []inventory.Finding) (err error) {
		out, err = i.State(candidates)
		out.Checks = append(out.Checks, checks...)
		out.Boundary = "目标自己的未来版本安装登记与审计；不修改规范源、工具或授权。替换同名安装会使旧安装/授权失效，需明确审查风险；旧产物目录保留。"
		return err
	})
	return out, err
}
func candidateInstallChecks(checks []inventory.Finding) error {
	for _, f := range checks {
		if f.Status == "FAIL" {
			return result.Fail("INSTALL_CHECK_FAILED", "候选完整产物、来源或安全检查存在阻断；未登记安装", 5)
		}
	}
	return nil
}
func (m *Manager) PlanCandidateInstallation(ctx context.Context, c ConfigurationCandidate, req plugininstall.Edit) (plugininstall.Preview, error) {
	var out plugininstall.Preview
	if req.Action != "install" {
		return out, result.Fail("INVALID_INPUT", "候选入口只登记已锁定版本；移除和恢复使用项目安装入口", 2)
	}
	err := m.withCandidateInstallation(ctx, c, func(i *plugininstall.Manager, candidates []plugininstall.Candidate, checks []inventory.Finding) (err error) {
		if err := candidateInstallChecks(checks); err != nil {
			return err
		}
		out, err = i.Plan(ctx, req, candidates, c.Digest())
		return err
	})
	return out, err
}
func (m *Manager) ApplyCandidateInstallation(ctx context.Context, c ConfigurationCandidate, name, id, digest string) (workstore.Journal, error) {
	var out workstore.Journal
	err := m.withCandidateInstallation(ctx, c, func(i *plugininstall.Manager, candidates []plugininstall.Candidate, checks []inventory.Finding) (err error) {
		if err := candidateInstallChecks(checks); err != nil {
			return err
		}
		p, err := i.Store.LoadPlan(id)
		if err != nil {
			return err
		}
		var req plugininstall.Edit
		if p.Action != "install-install" || json.Unmarshal([]byte(p.Inputs["request"]), &req) != nil || req.Name != name {
			return result.Fail("INVALID_INPUT", "候选安装确认与插件或动作不符", 2)
		}
		out, err = i.Apply(ctx, id, digest, candidates, c.Digest())
		return err
	})
	return out, err
}
