package source

import (
	"context"
	"github.com/pzqf/AIDock/internal/credentials"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/secref"
	"github.com/pzqf/AIDock/internal/workstore"
)

// WithMCPEnvironment is an internal prerequisite for a future managed launcher,
// never a plaintext resolver endpoint or an execution authorization. Locks cover
// source -> profile layers -> artifacts -> permission -> vault. Callers must
// also establish runtime/isolation gates, use a minimal fixed base environment,
// avoid logging values and keep the callback limited to the launch handoff.
func (m *Manager) WithMCPEnvironment(ctx context.Context, name string, consume func([]string) error) error {
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
		return m.lockProfileLayers(func() error {
			return m.withArtifactLocks(t, func() error {
				report, err := m.permissionProfile(t)
				if err != nil {
					return err
				}
				section, _ := report.Effective["mcp"].(map[string]any)
				enabled := false
				for _, n := range array(section["enable"]) {
					if n == name {
						enabled = true
					}
				}
				if !enabled {
					return result.Fail("MCP_NOT_ENABLED", "插件在当前有效 Profile 中未启用；未交付", 4)
				}
				conf, err := m.mcpConfig(t)
				if err != nil {
					return err
				}
				var server map[string]any
				for _, raw := range array(conf["servers"]) {
					s := raw.(map[string]any)
					if s["name"] == name {
						server = s
					}
				}
				if server == nil || server["transport"] != "stdio" {
					return result.Fail("MCP_DELIVERY_UNSUPPORTED", "需要已定义的 stdio 插件；未交付", 4)
				}
				refs, err := secref.Definition(server)
				if err != nil {
					return err
				}
				lock, err := m.mcpLockState(ctx, t)
				if err != nil {
					return err
				}
				c, checks, err := m.permissionInputs(t, lock, report)
				if err != nil {
					return err
				}
				for _, f := range checks {
					if f.Status == "FAIL" {
						return result.Fail("MCP_DELIVERY_INPUT_INVALID", "安装、产物、清单或安全证据检查失败；未交付", 4)
					}
				}
				p, err := m.permissionManager()
				if err != nil {
					return err
				}
				return p.WithCurrent(ctx, c, name, func(r pluginpermission.Record) error {
					vault, err := credentials.New(m.Store.Local)
					if err != nil {
						return err
					}
					return vault.WithEnvironment(ctx, credentials.Delivery{BindingSHA256: workstore.Hash(jsonBytes(p.Binding)), AuthorizationSHA256: workstore.Hash(jsonBytes(r)), InputSHA256: workstore.Hash(jsonBytes(r.Input))}, refs, consume)
				})
			})
		})
	})
}
