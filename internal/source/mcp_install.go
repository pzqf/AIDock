package source

import (
	"context"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

func (m *Manager) installManager() (*plugininstall.Manager, error) {
	return plugininstall.New(m.Store.Local, plugininstall.Binding{Version: 1, Project: m.Owner, Scope: m.Scope, SourceRoot: m.Store.Root})
}
func installCandidates(lock MCPLockState) []plugininstall.Candidate {
	out := []plugininstall.Candidate{}
	if !lock.Present {
		return out
	}
	parsed, err := pluginlock.Parse([]byte(lock.Document))
	if err != nil {
		return out
	}
	for _, p := range parsed.Plugins {
		for _, check := range lock.Plugins {
			if check.Name != p.Name {
				continue
			}
			for _, manifest := range lock.Manifests {
				if manifest.Name == p.Name {
					out = append(out, plugininstall.Candidate{Plugin: p, Check: check, Manifest: manifest})
				}
			}
		}
	}
	return out
}

// Annotation only changes presentation fields; the review fingerprint excludes
// these fields so registering an installation cannot invalidate itself.
func (m *Manager) annotateInstallations(lock *MCPLockState) {
	i, err := m.installManager()
	var state plugininstall.State
	if err == nil {
		state, err = i.State(installCandidates(*lock))
	}
	if err != nil {
		lock.Checks = append(lock.Checks, inventory.Finding{Status: "FAIL", Code: "MCP_INSTALL_STORE_INVALID", Detail: err.Error()})
		lock.Status = "FAIL"
		return
	}
	lock.Installations = map[string]string{}
	for _, f := range state.Checks {
		lock.Checks = append(lock.Checks, f)
		if f.Status == "FAIL" {
			lock.Status = "FAIL"
		}
	}
	for _, entry := range state.Installations {
		lock.Installations[entry.Record.Plugin.Name] = entry.Status
		for idx := range lock.Manifests {
			manifest := &lock.Manifests[idx]
			if manifest.Name != entry.Record.Plugin.Name {
				continue
			}
			manifest.InstallationState = "STALE"
			manifest.SBOMStatus = "STALE"
			if entry.Status == "CURRENT" {
				manifest.InstallationState = "INSTALLED"
				manifest.SBOMStatus = "PASS"
			}
		}
	}
}

// The source lock serializes portable changes and all project-local origin and
// installation mutations. Origin reads finish before the installation lock;
// recovery can operate without parsing a damaged portable MCP definition/lock.
func (m *Manager) withInstall(ctx context.Context, checkInputs bool, fn func(*plugininstall.Manager, []plugininstall.Candidate, string, []inventory.Finding) error) error {
	return m.Store.Locked(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if checkInputs {
			if err := m.Store.Pending(); err != nil {
				return err
			}
		}
		tree, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		if _, _, err = m.load(&tree); err != nil {
			return err
		}
		if _, ok := tree.Entries[m.identityFile()]; !ok {
			return result.Fail("SOURCE_NOT_INITIALIZED", "先创建或打开项目", 4)
		}
		operation := func() error {
			i, err := m.installManager()
			if err != nil {
				return err
			}
			candidates := []plugininstall.Candidate{}
			checks := []inventory.Finding{}
			if checkInputs {
				lock, err := m.mcpLockState(ctx, tree)
				if err != nil {
					checks = append(checks, inventory.Finding{Status: "FAIL", Code: "MCP_INSTALL_INPUT_UNAVAILABLE", Detail: err.Error()})
				} else {
					candidates = installCandidates(lock)
					checks = append(checks, lock.Checks...)
				}
			}
			return fn(i, candidates, workstore.Digest(tree), checks)
		}
		if checkInputs {
			return m.withArtifactLocks(tree, operation)
		}
		return operation()
	})
}
func (m *Manager) MCPInstall(ctx context.Context) (plugininstall.State, error) {
	var out plugininstall.State
	err := m.withInstall(ctx, true, func(i *plugininstall.Manager, c []plugininstall.Candidate, _ string, checks []inventory.Finding) (err error) {
		out, err = i.State(c)
		out.Checks = append(out.Checks, checks...)
		return
	})
	return out, err
}
func (m *Manager) MCPInstallPlan(ctx context.Context, req plugininstall.Edit) (plugininstall.Preview, error) {
	var out plugininstall.Preview
	err := m.withInstall(ctx, true, func(i *plugininstall.Manager, c []plugininstall.Candidate, hash string, checks []inventory.Finding) (err error) {
		if req.Action == "install" {
			for _, f := range checks {
				if f.Status == "FAIL" {
					return result.Fail("INSTALL_CHECK_FAILED", f.Detail, 5)
				}
			}
		}
		out, err = i.Plan(ctx, req, c, hash)
		return
	})
	return out, err
}
func (m *Manager) ApplyMCPInstall(ctx context.Context, id, digest string) (workstore.Journal, error) {
	var out workstore.Journal
	err := m.withInstall(ctx, true, func(i *plugininstall.Manager, c []plugininstall.Candidate, hash string, _ []inventory.Finding) (err error) {
		out, err = i.Apply(ctx, id, digest, c, hash)
		return
	})
	return out, err
}
func (m *Manager) RecoverMCPInstall(ctx context.Context) (int, error) {
	var n int
	err := m.withInstall(ctx, false, func(i *plugininstall.Manager, _ []plugininstall.Candidate, _ string, _ []inventory.Finding) (err error) {
		n, err = i.Recover()
		return
	})
	return n, err
}
func (m *Manager) MCPInstalledSBOM(ctx context.Context, name string) (plugininstall.Entry, error) {
	state, err := m.MCPInstall(ctx)
	if err != nil {
		return plugininstall.Entry{}, err
	}
	if state.Recovery || len(state.Checks) > 0 {
		for _, f := range state.Checks {
			if f.Code == "MCP_INSTALL_STORE_INVALID" || f.Code == "MCP_INSTALL_RECOVERY_REQUIRED" {
				return plugininstall.Entry{}, result.Fail(f.Code, f.Detail, 5)
			}
		}
	}
	for _, entry := range state.Installations {
		if entry.Record.Plugin.Name == name {
			return entry, nil
		}
	}
	return plugininstall.Entry{}, result.Fail("INSTALL_NOT_FOUND", "先登记本机安装，再读取已安装插件的轻量 SBOM", 4)
}
