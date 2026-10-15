package source

import (
	"context"
	"strings"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/pluginartifact"
	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type MCPArtifactState struct {
	LockSHA256     string                 `json:"lock_sha256"`
	Artifacts      []pluginartifact.State `json:"artifacts"`
	Checks         []inventory.Finding    `json:"checks"`
	Boundary       string                 `json:"boundary"`
	ExecutionReady bool                   `json:"execution_ready"`
}

func (m *Manager) artifactManager(p pluginlock.Plugin) (*pluginartifact.Manager, error) {
	return pluginartifact.New(m.Store.Local, string(jsonBytes(struct{ Project, Scope, Root string }{m.Owner, m.Scope, m.Store.Root})), p)
}
func (m *Manager) artifactInputs(t workstore.Tree) ([]pluginlock.Plugin, error) {
	if _, _, err := m.load(&t); err != nil {
		return nil, err
	}
	if _, ok := t.Entries[m.identityFile()]; !ok {
		return nil, result.Fail("SOURCE_NOT_INITIALIZED", "先创建或打开项目", 4)
	}
	e, ok := t.Entries["plugins.lock"]
	if !ok {
		return nil, result.Fail("MCP_LOCK_MISSING", "先导入并保存完整安装锁，再获取对应产物", 4)
	}
	b, err := m.Store.Data(e)
	if err != nil {
		return nil, err
	}
	lock, err := pluginlock.Parse(b)
	if err != nil {
		return nil, result.Fail("MCP_LOCK_INVALID", err.Error(), 5)
	}
	return lock.Plugins, nil
}
func (m *Manager) MCPArtifacts(ctx context.Context) (MCPArtifactState, error) {
	out := MCPArtifactState{Artifacts: []pluginartifact.State{}, Checks: []inventory.Finding{}, Boundary: "按已保存锁核验完整本地 ZIP，再预览确认导入。尚不联网取包、不执行安装脚本；导入不等于已登记安装或获得运行授权。"}
	err := m.Store.Locked(func() error {
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
		plugins, err := m.artifactInputs(t)
		out.LockSHA256 = lockDigest(t)
		if err != nil {
			out.Checks = append(out.Checks, inventory.Finding{Status: "FAIL", Code: "MCP_ARTIFACT_LOCK_REQUIRED", Detail: err.Error()})
			return nil
		}
		for _, p := range plugins {
			a, err := m.artifactManager(p)
			if err == nil {
				var state pluginartifact.State
				state, err = a.State()
				if err == nil {
					out.Artifacts = append(out.Artifacts, state)
				}
			}
			if err != nil {
				out.Checks = append(out.Checks, inventory.Finding{Status: "FAIL", Code: "MCP_ARTIFACT_STATE_INVALID", Detail: p.Name + "：" + err.Error()})
			}
		}
		return nil
	})
	return out, artifactRecoveryError(err)
}
func (m *Manager) withArtifact(ctx context.Context, name string, fn func(*pluginartifact.Manager, string) error) error {
	return artifactRecoveryError(m.Store.Locked(func() error {
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
		plugins, err := m.artifactInputs(t)
		if err != nil {
			return err
		}
		config, err := m.mcpConfig(t)
		if err != nil {
			return err
		}
		for _, p := range plugins {
			if p.Name != name {
				continue
			}
			matched := false
			for _, raw := range array(config["servers"]) {
				server := raw.(map[string]any)
				if server["name"] == name && pluginlock.Definition(server) == p.DefinitionSHA256 {
					matched = true
				}
			}
			if !matched {
				return result.Fail("MCP_LOCK_DEFINITION_MISMATCH", "已保存锁与当前 MCP 定义不符；先恢复对应定义", 5)
			}
			a, err := m.artifactManager(p)
			if err != nil {
				return err
			}
			return fn(a, workstore.Digest(t))
		}
		return result.Fail("MCP_LOCK_ENTRY_MISSING", "安装锁没有此 MCP", 4)
	}))
}
func (m *Manager) artifactRollbackGate(path string) error {
	users, err := plugininstall.ArtifactUsers(m.Store.Local, path)
	if err != nil {
		return err
	}
	if len(users) > 0 {
		return result.Fail("ARTIFACT_IN_USE", "此产物仍被本机项目安装登记引用；先移除相关登记，再回退完整包", 4)
	}
	return nil
}

func (m *Manager) MCPArtifactPlan(ctx context.Context, req pluginartifact.Edit) (pluginartifact.Preview, error) {
	var out pluginartifact.Preview
	err := m.withArtifact(ctx, req.Name, func(a *pluginartifact.Manager, hash string) (err error) {
		source, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		if req.ExpectedLockSHA256 == "" || req.ExpectedLockSHA256 != lockDigest(source) {
			return result.Fail("PLAN_STALE", "安装锁在读取后变化；保留完整包路径草稿并重读", 3)
		}
		if req.Action == "rollback" {
			if err = m.artifactRollbackGate(a.Plugin.Artifact.Path); err != nil {
				return err
			}
		}
		out, err = a.Plan(ctx, req, hash)
		return
	})
	return out, err
}
func (m *Manager) ApplyMCPArtifact(ctx context.Context, name, id, digest string) (workstore.Journal, error) {
	var out workstore.Journal
	err := m.withArtifact(ctx, name, func(a *pluginartifact.Manager, hash string) (err error) {
		p, err := a.Store.LoadPlan(id)
		if err != nil {
			return err
		}
		if p.Action == "artifact-rollback" {
			a.BeforeApply = func() error { return m.artifactRollbackGate(a.Plugin.Artifact.Path) }
			if err = m.artifactRollbackGate(a.Plugin.Artifact.Path); err != nil {
				return err
			}
		}
		out, err = a.Apply(ctx, id, digest, hash)
		return
	})
	return out, err
}
func (m *Manager) RecoverMCPArtifact(ctx context.Context, path string) (int, error) {
	var count int
	err := m.Store.Locked(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		s, err := pluginartifact.RecoveryStore(m.Store.Local, path)
		if err != nil {
			return err
		}
		binding := string(jsonBytes(struct{ Project, Scope, Root string }{m.Owner, m.Scope, m.Store.Root}))
		count, err = s.RecoverGuarded(func(p workstore.Plan) error {
			if p.Inputs["binding"] != binding {
				return result.Fail("ARTIFACT_BINDING_MISMATCH", "此完整包中断事务属于其它项目或作用域；使用原绑定恢复", 3)
			}
			return nil
		})
		return err
	})
	return count, artifactRecoveryError(err)
}
func artifactRecoveryError(err error) error {
	if e, ok := err.(*result.Error); ok && e.Code == "RECOVERY_CONFLICT" {
		return &result.Error{Code: e.Code, Message: "完整安装包事务需恢复或核对：" + strings.ReplaceAll(e.Message, "skill recover", "mcp artifact recover --path <产物相对路径>"), Exit: e.Exit, Recovery: map[string]string{"command": "mcp artifact recover --path <产物相对路径>"}}
	}
	return err
}
