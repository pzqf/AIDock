package source

import (
	"context"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/pluginsecurity"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

func (m *Manager) securityManager() (*pluginsecurity.Manager, error) {
	return pluginsecurity.New(m.Store.Local, pluginsecurity.Binding{Version: 1, Project: m.Owner, Scope: m.Scope, SourceRoot: m.Store.Root})
}
func securityCandidates(lock MCPLockState) []pluginsecurity.Candidate {
	out := []pluginsecurity.Candidate{}
	parsed, err := pluginlock.Parse([]byte(lock.Document))
	if err != nil {
		return out
	}
	for _, p := range parsed.Plugins {
		for _, manifest := range lock.Manifests {
			if p.Name == manifest.Name {
				candidate := pluginsecurity.Candidate{Plugin: p, Manifest: manifest}
				for _, check := range lock.Plugins {
					if check.Name != p.Name {
						continue
					}
					candidate.DependenciesVerified = check.DependencyStatus == "PASS" && manifest.DependencyStatus == "PASS"
					for _, f := range check.Checks {
						if f.Status == "PASS" && f.Code == "MCP_ORIGIN_SIGNATURE_VERIFIED" {
							candidate.SourceVerified = true
						}
						if f.Status == "PASS" && f.Code == "MCP_RUNTIME_PUBLISHER_ATTESTED" {
							candidate.RuntimeVerified = true
						}
					}
				}
				out = append(out, candidate)
			}
		}
	}
	return out
}
func (m *Manager) annotateSecurity(lock *MCPLockState) {
	i, err := m.securityManager()
	var state pluginsecurity.State
	if err == nil {
		state, err = i.State(securityCandidates(*lock))
	}
	if err != nil {
		finding := inventory.Finding{Status: "FAIL", Code: "MCP_SECURITY_STORE_INVALID", Detail: err.Error()}
		lock.Checks = append(lock.Checks, finding)
		for idx := range lock.Plugins {
			lock.Plugins[idx].Add(finding.Status, finding.Code, finding.Detail)
		}
		lock.Status = "FAIL"
		return
	}
	for _, f := range state.Checks {
		lock.Checks = append(lock.Checks, f)
		for idx := range lock.Plugins {
			lock.Plugins[idx].Add(f.Status, f.Code, f.Detail)
		}
		if f.Status == "FAIL" {
			lock.Status = "FAIL"
		}
	}
	for _, security := range state.Results {
		for idx := range lock.Plugins {
			c := &lock.Plugins[idx]
			if c.Name != security.Name {
				continue
			}
			for _, f := range security.Checks {
				c.Add(f.Status, f.Code, f.Detail)
			}
			c.TrustLevel = security.TrustLevel
			c.SecuritySHA256 = workstore.Hash(jsonBytes(struct {
				Cache  string
				Result pluginsecurity.Check
			}{state.SHA256, security}))
			if statusRank(c.Status) > statusRank(lock.Status) {
				lock.Status = c.Status
			}
		}
	}
}

// Keep source -> security lock order. Reading current lock state finishes all
// subordinate reads before the security mutation lock is acquired.
func (m *Manager) withSecurity(ctx context.Context, inputs bool, fn func(*pluginsecurity.Manager, []pluginsecurity.Candidate, string, []inventory.Finding) error) error {
	return m.Store.Locked(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if inputs {
			if err := m.Store.Pending(); err != nil {
				return err
			}
		}
		t, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		if _, _, err = m.load(&t); err != nil {
			return err
		}
		if _, ok := t.Entries[m.identityFile()]; !ok {
			return result.Fail("SOURCE_NOT_INITIALIZED", "先创建或打开项目", 4)
		}
		i, err := m.securityManager()
		if err != nil {
			return err
		}
		c := []pluginsecurity.Candidate{}
		checks := []inventory.Finding{}
		if inputs {
			lock, err := m.mcpLockState(ctx, t)
			if err != nil {
				return err
			}
			c = securityCandidates(lock)
			checks = append(checks, lock.Checks...)
			if len(c) == 0 && len(lock.Definitions) > 0 {
				checks = append(checks, inventory.Finding{Status: "UNKNOWN", Code: "MCP_SECURITY_NO_LOCKED_ARTIFACTS", Detail: "已有 MCP 定义但没有可核验的锁定产物，尚不能进行安全参考检查"})
			}
		}
		return fn(i, c, workstore.Digest(t), checks)
	})
}
func (m *Manager) MCPSecurity(ctx context.Context) (pluginsecurity.State, error) {
	var out pluginsecurity.State
	err := m.withSecurity(ctx, true, func(i *pluginsecurity.Manager, c []pluginsecurity.Candidate, _ string, checks []inventory.Finding) (err error) {
		out, err = i.State(c)
		out.Checks = append(out.Checks, checks...)
		return
	})
	return out, err
}
func (m *Manager) MCPSecurityPlan(ctx context.Context, r pluginsecurity.Edit) (pluginsecurity.Preview, error) {
	var out pluginsecurity.Preview
	err := m.withSecurity(ctx, true, func(i *pluginsecurity.Manager, c []pluginsecurity.Candidate, h string, _ []inventory.Finding) (err error) {
		out, err = i.Plan(ctx, r, c, h)
		return
	})
	return out, err
}
func (m *Manager) ApplyMCPSecurity(ctx context.Context, id, digest string) (workstore.Journal, error) {
	var out workstore.Journal
	err := m.withSecurity(ctx, true, func(i *pluginsecurity.Manager, c []pluginsecurity.Candidate, h string, _ []inventory.Finding) (err error) {
		out, err = i.Apply(ctx, id, digest, c, h)
		return
	})
	return out, err
}
func (m *Manager) RecoverMCPSecurity(ctx context.Context) (int, error) {
	n := 0
	err := m.withSecurity(ctx, false, func(i *pluginsecurity.Manager, _ []pluginsecurity.Candidate, _ string, _ []inventory.Finding) (err error) {
		n, err = i.Recover()
		return
	})
	return n, err
}
