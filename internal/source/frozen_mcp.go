package source

import (
	"context"
	"github.com/pzqf/AIDock/internal/config"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/mcprelay"
	"github.com/pzqf/AIDock/internal/mcpsession"
	"github.com/pzqf/AIDock/internal/profile"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// FrozenMCP is internal archive material. No CLI/API accepts these trees or
// config values; the native owner supplies only a checked committed archive.
type FrozenMCP struct {
	Tree          workstore.Tree        `json:"-"`
	Effective     Export                `json:"-"`
	Configuration SnapshotConfiguration `json:"-"`
}

func (m *Manager) frozenMCPReport(f FrozenMCP) (profile.Report, error) {
	var out profile.Report
	if f.Effective.Root != m.Store.Root || f.Effective.Owner != m.Owner || f.Effective.Scope != m.Scope || f.Effective.Copy != m.Copy || f.Effective.Snapshot != workstore.Digest(f.Tree) {
		return out, result.Fail("SNAPSHOT_BINDING_MISMATCH", "已应用视图不属于当前项目副本", 3)
	}
	if err := ValidateSnapshotConfiguration(f.Configuration, f.Effective, m.Store.Local); err != nil {
		return out, err
	}
	if err := ValidateSnapshotConfigurationBytes(f.Configuration, f.Effective, m.Store.Data); err != nil {
		return out, err
	}
	idx, shared, err := m.load(&f.Tree)
	if err != nil {
		return out, err
	}
	_, skills, err := m.resolve(&f.Tree, idx, shared)
	if err != nil {
		return out, err
	}
	out, err = m.resolveProfile(f.Tree, shared, skills, f.Configuration.Layers)
	if err != nil {
		return out, err
	}
	if string(jsonBytes(out)) != string(jsonBytes(f.Effective.ProfileReport)) {
		return out, result.Fail("SNAPSHOT_CONFIGURATION_INVALID", "已应用视图无法重建原始配置", 5)
	}
	return out, nil
}

func (m *Manager) frozenCurrentIdentity() error {
	live, err := m.Store.Capture(m.Store.Root)
	if err != nil {
		return err
	}
	if _, err = m.replayProject(live); err != nil {
		return err
	}
	entry, ok := live.Entries["skills.yaml"]
	if !ok || entry.Dir {
		return result.Fail("SOURCE_IDENTITY_MISMATCH", "当前技能作用域身份缺失", 3)
	}
	body, err := m.Store.Data(entry)
	if err != nil {
		return err
	}
	value, parseErr := config.Parse(body)
	fields, _ := value.(map[string]any)
	if parseErr != nil || fields["schema_version"] != 1 || fields["owner"] != m.Owner || fields["scope_id"] != m.Scope {
		return result.Fail("SOURCE_IDENTITY_MISMATCH", "当前技能作用域身份损坏或变化", 3)
	}
	return nil
}

// The source and Local roots are locked only for a bounded consumer. Frozen
// layers are reconstructed without today's shared defaults or Local draft.
// Independent current install/permission/runtime authorities remain mandatory.
func (m *Manager) withFrozenMCPSessions(ctx context.Context, f FrozenMCP, requests []MCPSessionRequest, use func([]lockedMCPSession) error) error {
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
			if err := m.frozenCurrentIdentity(); err != nil {
				return err
			}
			return m.withArtifactLocks(f.Tree, func() error {
				report, err := m.frozenMCPReport(f)
				if err != nil {
					return err
				}
				section, _ := report.Effective["mcp"].(map[string]any)
				enabled := map[string]bool{}
				for _, raw := range array(section["enable"]) {
					enabled[raw.(string)] = true
				}
				for _, raw := range array(section["disable"]) {
					delete(enabled, raw.(string))
				}
				for _, req := range requests {
					if !enabled[req.Name] {
						return result.Fail("MCP_NOT_ENABLED", "插件未在这个成功应用视图中启用", 4)
					}
				}
				lock, err := m.mcpLockState(ctx, f.Tree)
				if err != nil {
					return err
				}
				candidates, checks, err := m.permissionInputs(f.Tree, lock, &report)
				if err != nil {
					return err
				}
				for _, check := range checks {
					if check.Status == "FAIL" {
						return result.Fail("MCP_SESSION_INPUT_INVALID", "已应用视图的当前安装、产物或安全证据无效", 4)
					}
				}
				p, err := m.permissionManager()
				if err != nil {
					return err
				}
				recheck := func(c context.Context) error {
					if err := c.Err(); err != nil {
						return err
					}
					return m.frozenCurrentIdentity()
				}
				return m.withMCPSessionAuthorization(ctx, f.Tree, p, candidates, requests, recheck, use)
			})
		})
	})
}

func (m *Manager) WithFrozenMCPClientConfigs(ctx context.Context, f FrozenMCP, requests []MCPSessionRequest, executable, home string, use func([]MCPClientConfiguration) error) error {
	if use == nil || !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || !filepath.IsAbs(home) || filepath.Join(home, "local") != m.Store.Local {
		return result.Fail("MCP_CLIENT_CONFIG_BINDING_INVALID", "已应用连接需要当前 home 和稳定程序路径", 2)
	}
	selected := append([]MCPSessionRequest{}, requests...)
	for i := range selected {
		selected[i].Probe = true
	}
	return m.withFrozenMCPSessions(ctx, f, selected, func(s []lockedMCPSession) error {
		future := *m
		future.Library = nil
		future.ProfileLibrary = false
		out := []MCPClientConfiguration{}
		for i, req := range selected {
			if err := s[i].Check(ctx); err != nil {
				return err
			}
			c, err := future.clientConfiguration(req, executable, home)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		return use(out)
	})
}

// guard proves the immutable application is still installed and committed.
// It spans startup and each bounded protocol transfer, never response waits.
func (m *Manager) ServeFrozenMCPClient(ctx context.Context, f FrozenMCP, req MCPSessionRequest, guard func(context.Context, func() error) error, client mcprelay.Endpoint) (mcpsession.Event, error) {
	if guard == nil {
		return mcpsession.Event{}, result.Fail("INVALID_INPUT", "已应用会话需要原生提交证明", 2)
	}
	reader := func(c context.Context, r MCPSessionRequest, use func(lockedMCPSession) error) error {
		return retryMCPClientInput(c, func(fn func(lockedMCPSession) error) error {
			return m.withFrozenMCPSessions(c, f, []MCPSessionRequest{r}, func(s []lockedMCPSession) error { return guard(c, func() error { return fn(s[0]) }) })
		}, use)
	}
	return m.serveMCPClient(ctx, req, client, reader)
}
