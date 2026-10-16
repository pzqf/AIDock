package source

import (
	"context"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/result"
)

type MCPClientConfiguration struct {
	Command  string                   `json:"command"`
	Args     []string                 `json:"args"`
	Binding  pluginpermission.Binding `json:"binding"`
	Boundary string                   `json:"boundary"`
}

// MCPClientConfig only prepares argv. It neither starts a plugin nor writes
// native client configuration. The startup command repeats all current gates.
func (m *Manager) MCPClientConfig(ctx context.Context, req MCPSessionRequest, executable, home string) (MCPClientConfiguration, error) {
	var out MCPClientConfiguration
	err := m.WithMCPClientConfig(ctx, req, executable, home, func(value MCPClientConfiguration) error {
		out = value
		return nil
	})
	return out, err
}

// WithMCPClientConfig keeps the existing authority locks through the caller's
// bounded file transaction. The callback must not reenter a source manager.
func (m *Manager) WithMCPClientConfig(ctx context.Context, req MCPSessionRequest, executable, home string, use func(MCPClientConfiguration) error) error {
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || !filepath.IsAbs(home) || filepath.Join(home, "local") != m.Store.Local {
		return result.Fail("MCP_CLIENT_CONFIG_BINDING_INVALID", "客户端配置需要稳定的绝对程序路径和与当前 Local Root 一致的显式 home", 2)
	}
	if use == nil {
		return result.Fail("INVALID_INPUT", "客户端配置需要明确的事务消费者", 2)
	}
	req.Probe = true // Reuse the read-only gate; this method never launches.
	return m.withMCPClientInput(ctx, req, func(s lockedMCPSession) error {
		out, err := m.clientConfiguration(req, executable, home)
		if err != nil {
			return err
		}
		return use(out)
	})
}

// WithProfileMCPClientConfigs requires exactly the effective enabled MCP set.
// All source/layer/artifact/authorization locks span the joint file consumer;
// it cannot reenter a source manager or start a plugin.
func (m *Manager) WithProfileMCPClientConfigs(ctx context.Context, requests []MCPSessionRequest, executable, home string, use func(Export, []MCPClientConfiguration) error) error {
	if use == nil {
		return result.Fail("INVALID_INPUT", "需要明确的联合应用消费者", 2)
	}
	return m.withMCPClientConfigs(ctx, requests, executable, home, true, func(bundle *Export, out []MCPClientConfiguration) error { return use(*bundle, out) })
}

// WithMCPClientConfigs validates a selected subset when restoring launchers.
// It does not require every enabled plugin to be restored by this transaction.
func (m *Manager) WithMCPClientConfigs(ctx context.Context, requests []MCPSessionRequest, executable, home string, use func([]MCPClientConfiguration) error) error {
	if use == nil || len(requests) == 0 {
		return result.Fail("INVALID_INPUT", "需要明确的连接恢复消费者", 2)
	}
	return m.withMCPClientConfigs(ctx, requests, executable, home, false, func(_ *Export, out []MCPClientConfiguration) error { return use(out) })
}

func (m *Manager) withMCPClientConfigs(ctx context.Context, requests []MCPSessionRequest, executable, home string, exact bool, use func(*Export, []MCPClientConfiguration) error) error {
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || !filepath.IsAbs(home) || filepath.Join(home, "local") != m.Store.Local || use == nil {
		return result.Fail("MCP_CLIENT_CONFIG_BINDING_INVALID", "联合应用需要当前显式 home、稳定程序路径与事务消费者", 2)
	}
	selected := append([]MCPSessionRequest{}, requests...)
	for i := range selected {
		selected[i].Probe = true
	}
	return m.withMCPSessionInputs(ctx, selected, exact, func(s []lockedMCPSession, bundle *Export) error {
		out := []MCPClientConfiguration{}
		for _, req := range selected {
			c, err := m.clientConfiguration(req, executable, home)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		for _, session := range s {
			if err := session.Check(ctx); err != nil {
				return err
			}
		}
		return use(bundle, out)
	})
}

func (m *Manager) clientConfiguration(req MCPSessionRequest, executable, home string) (MCPClientConfiguration, error) {
	out := MCPClientConfiguration{Command: executable}
	out.Args = []string{"mcp", "exec", "--allow-unisolated", "--plugin", req.Name, "--expected-permission", req.ExpectedSHA256, "--expected-review", req.ExpectedReviewSHA256, "--expected-runtime-attempt", req.ExpectedRuntimeAttempt, "--home", home, "--managed-root", m.Store.Root, "--project", m.Owner, "--scope", m.Scope, "--copy", m.Copy}
	if m.Library != nil {
		out.Args = append(out.Args, "--library-root", m.Library.Store.Root)
	}
	if m.BridgeFile != "" {
		p, err := filepath.Abs(m.BridgeFile)
		if err != nil {
			return out, err
		}
		out.Args = append(out.Args, "--bridge-file", p)
	}
	out.Binding = pluginpermission.Binding{Version: 1, Project: m.Owner, Scope: m.Scope, Copy: m.Copy, SourceRoot: m.Store.Root}
	out.Boundary = "只读配置预览，未写入客户端或启动插件。客户端会启动已授权自有本地 Node 插件的非隔离实例；仅固定项目/Public 合同内已审查读取，写调用拒绝。授权或输入变化需重新审查并生成配置。请保持 AIDock 程序在此绝对路径；当前同一项目副本只允许一个受管实例。"
	return out, nil
}
