package source

import (
	"context"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/pluginorigin"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

func (m *Manager) originManager() (*pluginorigin.Manager, error) {
	return pluginorigin.New(m.Store.Local, pluginorigin.Binding{Version: 1, Project: m.Owner, Scope: m.Scope, SourceRoot: m.Store.Root})
}

// All operations use source -> local-origin lock order. The complete current
// source tree is bound to confirmation; copied portable source carries no keys.
func (m *Manager) withOrigin(ctx context.Context, fn func(*pluginorigin.Manager, []pluginlock.Plugin, string) error) error {
	return m.Store.Locked(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.Store.Pending(); err != nil {
			return err
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
		plugins := []pluginlock.Plugin{}
		if e, ok := t.Entries["plugins.lock"]; ok {
			b, err := m.Store.Data(e)
			if err != nil {
				return err
			}
			lock, err := pluginlock.Parse(b)
			if err != nil {
				return result.Fail("MCP_LOCK_INVALID", err.Error(), 5)
			}
			plugins = lock.Plugins
		}
		o, err := m.originManager()
		if err != nil {
			return err
		}
		return fn(o, plugins, workstore.Digest(t))
	})
}

func (m *Manager) MCPOrigin(ctx context.Context) (pluginorigin.State, error) {
	var out pluginorigin.State
	err := m.withOrigin(ctx, func(o *pluginorigin.Manager, plugins []pluginlock.Plugin, _ string) (err error) {
		out, err = o.State(plugins)
		return
	})
	return out, err
}
func (m *Manager) MCPOriginPlan(ctx context.Context, req pluginorigin.Edit) (pluginorigin.Preview, error) {
	var out pluginorigin.Preview
	err := m.withOrigin(ctx, func(o *pluginorigin.Manager, plugins []pluginlock.Plugin, hash string) (err error) {
		out, err = o.Plan(ctx, req, plugins, hash)
		return
	})
	return out, err
}
func (m *Manager) ApplyMCPOrigin(ctx context.Context, id, digest string) (workstore.Journal, error) {
	var out workstore.Journal
	err := m.withOrigin(ctx, func(o *pluginorigin.Manager, plugins []pluginlock.Plugin, hash string) (err error) {
		out, err = o.Apply(ctx, id, digest, plugins, hash)
		return
	})
	return out, err
}
func (m *Manager) RecoverMCPOrigin(ctx context.Context) (int, error) {
	// Recovery must remain reachable even when a portable lock is malformed.
	var n int
	err := m.Store.Locked(func() error {
		if err := ctx.Err(); err != nil {
			return err
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
		o, err := m.originManager()
		if err != nil {
			return err
		}
		n, err = o.Recover()
		return err
	})
	return n, err
}
func (m *Manager) ShowMCPOrigin(ctx context.Context, subject string) (string, error) {
	var out string
	err := m.withOrigin(ctx, func(o *pluginorigin.Manager, _ []pluginlock.Plugin, _ string) (err error) {
		out, err = o.Show(subject)
		return
	})
	return out, err
}

func withOriginChecks(c pluginlock.Check, o pluginorigin.Check) pluginlock.Check {
	findings := []inventory.Finding{}
	for _, f := range c.Checks {
		if f.Code == "MCP_SOURCE_TRUST_UNVERIFIED" {
			continue
		}
		if f.Code == "MCP_RUNTIME_VERSION_UNVERIFIED" && o.Status == "PASS" && o.RuntimeVersionVerified {
			continue
		}
		findings = append(findings, f)
	}
	c.Checks = findings
	c.Status = "PASS"
	for _, f := range findings {
		if statusRank(f.Status) > statusRank(c.Status) {
			c.Status = f.Status
		}
	}
	for _, f := range o.Checks {
		c.Add(f.Status, f.Code, f.Detail)
	}
	if o.Status == "PASS" && o.RuntimeVersionVerified {
		c.Add("PASS", "MCP_RUNTIME_PUBLISHER_ATTESTED", "运行时版本由登记发布者签名声明核实；AIDock 未执行运行时或 --version")
	}
	return c
}
