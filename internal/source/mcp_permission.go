package source

import (
	"context"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/profile"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

func (m *Manager) permissionManager() (*pluginpermission.Manager, error) {
	return pluginpermission.New(m.Store.Local, pluginpermission.Binding{Version: 1, Project: m.Owner, Scope: m.Scope, SourceRoot: m.Store.Root, Copy: m.Copy})
}
func (m *Manager) permissionProfile(t workstore.Tree) (*profile.Report, error) {
	idx, shared, err := m.load(&t)
	if err != nil {
		return nil, err
	}
	if _, ok := t.Entries[m.identityFile()]; !ok {
		return nil, result.Fail("SOURCE_NOT_INITIALIZED", "先创建或打开项目", 4)
	}
	_, skills, err := m.resolve(&t, idx, shared)
	if err != nil {
		return nil, err
	}
	layers, err := m.profileLayers()
	if err != nil {
		return nil, err
	}
	r, err := m.resolveProfile(t, shared, skills, layers)
	return &r, err
}

// Physical source root is part of the binding: a second copy never inherits an
// approval. The input also includes effective layered configuration and every
// byte of the current portable source, not just the MCP inclusion switch.
func (m *Manager) permissionInputs(t workstore.Tree, lock MCPLockState, report *profile.Report) ([]pluginpermission.Candidate, []inventory.Finding, error) {
	out := []pluginpermission.Candidate{}
	checks := append([]inventory.Finding{}, lock.Checks...)
	if report == nil {
		return out, checks, result.Fail("PROFILE_RESOLUTION_FAILED", "有效配置未解析，不能建立授权绑定", 5)
	}
	i, err := m.installManager()
	if err != nil {
		return out, checks, err
	}
	installed, err := i.State(installCandidates(lock))
	if err != nil {
		return out, checks, err
	}
	checks = append(checks, installed.Checks...)
	if installed.Recovery {
		return out, checks, result.Fail("MCP_INSTALL_RECOVERY_REQUIRED", "先恢复安装登记，再审查项目授权", 4)
	}
	for _, f := range checks {
		if f.Status == "FAIL" {
			return out, checks, nil
		}
	}
	conf, err := m.mcpConfig(t)
	if err != nil {
		return out, checks, err
	}
	for _, entry := range installed.Installations {
		for _, raw := range array(conf["servers"]) {
			server := raw.(map[string]any)
			if server["name"] != entry.Record.Plugin.Name {
				continue
			}
			input := pluginpermission.Input{Name: entry.Record.Plugin.Name, InstallationID: entry.Record.ID, InstallationSHA256: workstore.Hash(jsonBytes(entry.Record)), LockSHA256: lock.SHA256, ConfigurationSHA256: workstore.Hash(jsonBytes(struct{ Source, Profile string }{workstore.Digest(t), report.Digest})), ArtifactSHA256: entry.Record.Plugin.Artifact.SHA256, Permissions: server["permissions"].(map[string]any), Tier: server["tier"].(int)}
			out = append(out, pluginpermission.Candidate{Input: input, Status: entry.Status})
		}
	}
	return out, checks, nil
}
func (m *Manager) permissionState(t workstore.Tree, lock MCPLockState, report *profile.Report) (pluginpermission.State, error) {
	// Projects without MCP definitions or a lock have no authorization input.
	// Avoid opening local approval stores during unrelated skill/replica reads.
	if len(lock.Definitions) == 0 && !lock.Present {
		return pluginpermission.State{}, nil
	}
	p, err := m.permissionManager()
	if err != nil {
		return pluginpermission.State{}, err
	}
	c, checks, inputErr := m.permissionInputs(t, lock, report)
	if inputErr != nil {
		checks = append(checks, inventory.Finding{Status: "FAIL", Code: "MCP_AUTHORIZATION_INPUT_UNAVAILABLE", Detail: inputErr.Error()})
	}
	out, err := p.State(c)
	out.Checks = append(out.Checks, checks...)
	return out, err
}
func (m *Manager) withPermission(ctx context.Context, inputs bool, fn func(*pluginpermission.Manager, []pluginpermission.Candidate, string, []inventory.Finding) error) error {
	return m.Store.Locked(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		p, err := m.permissionManager()
		if err != nil {
			return err
		}
		if !inputs {
			return fn(p, nil, workstore.Hash(nil), nil)
		}
		if err = m.Store.Pending(); err != nil {
			return err
		}
		t, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		return m.withArtifactLocks(t, func() error {
			c := []pluginpermission.Candidate{}
			checks := []inventory.Finding{}
			report, inputErr := m.permissionProfile(t)
			var lock MCPLockState
			if inputErr == nil {
				lock, inputErr = m.mcpLockState(ctx, t)
			}
			if inputErr == nil {
				c, checks, inputErr = m.permissionInputs(t, lock, report)
			}
			if inputErr != nil {
				checks = append(checks, inventory.Finding{Status: "FAIL", Code: "MCP_AUTHORIZATION_INPUT_UNAVAILABLE", Detail: inputErr.Error()})
			}
			return fn(p, c, workstore.Digest(t), checks)
		})
	})
}
func (m *Manager) MCPPermission(ctx context.Context) (pluginpermission.State, error) {
	var out pluginpermission.State
	err := m.withPermission(ctx, true, func(p *pluginpermission.Manager, c []pluginpermission.Candidate, _ string, checks []inventory.Finding) (err error) {
		out, err = p.State(c)
		out.Checks = append(out.Checks, checks...)
		return
	})
	return out, err
}
func (m *Manager) MCPPermissionPlan(ctx context.Context, req pluginpermission.Edit) (pluginpermission.Preview, error) {
	var out pluginpermission.Preview
	err := m.withPermission(ctx, req.Action != "revoke", func(p *pluginpermission.Manager, c []pluginpermission.Candidate, hash string, _ []inventory.Finding) (err error) {
		out, err = p.Plan(ctx, req, c, hash)
		return
	})
	return out, err
}
func (m *Manager) ApplyMCPPermission(ctx context.Context, id, digest string) (workstore.Journal, error) {
	p, err := m.permissionManager()
	if err != nil {
		return workstore.Journal{}, err
	}
	plan, err := p.Store.LoadPlan(id)
	if err != nil {
		return workstore.Journal{}, err
	}
	var out workstore.Journal
	err = m.withPermission(ctx, plan.Action != "permission-revoke", func(p *pluginpermission.Manager, c []pluginpermission.Candidate, hash string, _ []inventory.Finding) (err error) {
		out, err = p.Apply(ctx, id, digest, c, hash)
		return
	})
	return out, err
}
func (m *Manager) RecoverMCPPermission(ctx context.Context) (int, error) {
	var n int
	err := m.withPermission(ctx, false, func(p *pluginpermission.Manager, _ []pluginpermission.Candidate, _ string, _ []inventory.Finding) (err error) {
		n, err = p.Recover()
		return
	})
	return n, err
}
