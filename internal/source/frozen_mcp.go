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
			return m.withFrozenMCPAuthorities(ctx, f, requests, false, m.frozenCurrentIdentity, use)
		})
	})
}

// All callers hold source and Local locks, either through the ordinary reader
// above or through the complete-root transaction coordinator. Authority locks
// remain owned here; no archived permission is accepted as current authority.
func (m *Manager) withFrozenMCPAuthorities(ctx context.Context, f FrozenMCP, requests []MCPSessionRequest, exact bool, recheck func() error, use func([]lockedMCPSession) error) error {
	if recheck == nil || use == nil {
		return result.Fail("INVALID_INPUT", "需要明确冻结配置消费者", 2)
	}
	if err := recheck(); err != nil {
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
		seen := map[string]bool{}
		for _, req := range requests {
			if !enabled[req.Name] || seen[req.Name] {
				return result.Fail("MCP_NOT_ENABLED", "插件未在这个成功应用视图中启用或重复选择", 4)
			}
			seen[req.Name] = true
		}
		if exact && len(seen) != len(enabled) {
			return result.Fail("PROFILE_MCP_SELECTION_MISMATCH", "共同回放必须重新验证全部有效 MCP，不能遗漏", 4)
		}
		if len(requests) == 0 {
			return use([]lockedMCPSession{})
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
		check := func(c context.Context) error {
			if err := c.Err(); err != nil {
				return err
			}
			return recheck()
		}
		conf, err := m.mcpConfig(f.Tree)
		if err != nil {
			return err
		}
		configurations, _ := m.compilePluginConfigurations(conf, lock, &report)
		return m.withMCPSessionAuthorization(ctx, f.Tree, p, candidates, requests, configurations, check, use)
	})
}

// ValidateFrozenMCPTransaction is a read-only commit gate, never a write or
// runtime entrypoint. The native coordinator must already own ALL source,
// Local and native locks. It supplies the exact current side of its checked
// transaction; this also works after it has installed its own pending markers.
// The ordinary runtime continues to reject pending markers via its reader.
func (m *Manager) ValidateFrozenMCPTransaction(ctx context.Context, f FrozenMCP, expectedSource, expectedLocal workstore.Tree, requests []MCPSessionRequest) error {
	local, err := m.localProfileStore()
	if err != nil {
		return err
	}
	recheck := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		s, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		l, err := local.Capture(local.Root)
		if err != nil {
			return err
		}
		if workstore.Digest(s) != workstore.Digest(expectedSource) || workstore.Digest(l) != workstore.Digest(expectedLocal) {
			return result.Fail("PLAN_STALE", "共同回放的源或本机原件与已锁定事务不一致", 3)
		}
		return nil
	}
	selected := append([]MCPSessionRequest{}, requests...)
	for i := range selected {
		selected[i].Probe = true
	}
	return m.withFrozenMCPAuthorities(ctx, f, selected, true, recheck, func(sessions []lockedMCPSession) error {
		for _, s := range sessions {
			if err := s.Check(ctx); err != nil {
				return err
			}
		}
		return nil
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
