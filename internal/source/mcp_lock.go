package source

import (
	"context"
	"path/filepath"
	"time"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/pluginmanifest"
	"github.com/pzqf/AIDock/internal/pluginorigin"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type MCPLockState struct {
	SHA256             string                  `json:"sha256"`
	Present            bool                    `json:"present"`
	Document           string                  `json:"document"`
	Status             string                  `json:"status"`
	ArtifactRoot       string                  `json:"artifact_root"`
	Definitions        []string                `json:"definitions"`
	Plugins            []pluginlock.Check      `json:"plugins"`
	Manifests          []pluginmanifest.Report `json:"manifests"`
	Checks             []inventory.Finding     `json:"checks"`
	VerificationSHA256 string                  `json:"verification_sha256"`
	Boundary           string                  `json:"boundary"`
	ExecutionReady     bool                    `json:"execution_ready"`
}
type MCPLockEdit struct {
	ExpectedSHA256 string `json:"expected_sha256"`
	Document       string `json:"document"`
}
type MCPLockPreview struct {
	Preview
	Candidate MCPLockState `json:"candidate"`
}

func lockDigest(t workstore.Tree) string {
	if e, ok := t.Entries["plugins.lock"]; ok {
		return e.Hash
	}
	return workstore.Hash(nil)
}
func (m *Manager) mcpLockState(ctx context.Context, t workstore.Tree) (MCPLockState, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out := MCPLockState{SHA256: lockDigest(t), Status: "PASS", Document: "{\n  \"schema_version\": 1,\n  \"plugins\": []\n}\n", ArtifactRoot: filepath.Join(m.Store.Local, "mcp-artifacts"), Plugins: []pluginlock.Check{}, Manifests: []pluginmanifest.Report{}, Checks: []inventory.Finding{}, Boundary: "只核验 Local Root 内预先备好的产物并管理 plugins.lock；不安装或运行。文件匹配与来源信任分别报告；导入的信任声明不能授予 L2 或项目授权。"}
	e, outPresent := t.Entries["plugins.lock"]
	out.Present = outPresent
	conf, err := m.mcpConfig(t)
	if err != nil {
		return out, err
	}
	out.Definitions = []string{}
	servers := map[string]map[string]any{}
	for _, raw := range array(conf["servers"]) {
		s := raw.(map[string]any)
		name := s["name"].(string)
		out.Definitions = append(out.Definitions, name)
		servers[name] = s
	}
	if !out.Present {
		out.Status = "UNKNOWN"
		out.Checks = append(out.Checks, inventory.Finding{Status: "UNKNOWN", Code: "MCP_LOCK_MISSING", Detail: "尚未保存安装锁"})
		out.VerificationSHA256 = workstore.Hash(jsonBytes(out.Plugins))
		return out, nil
	}
	b, err := m.Store.Data(e)
	if err != nil {
		return out, err
	}
	out.Document = string(b)
	lock, err := pluginlock.Parse(b)
	if err != nil {
		out.Status = "FAIL"
		out.Checks = append(out.Checks, inventory.Finding{Status: "FAIL", Code: "MCP_LOCK_INVALID", Detail: err.Error()})
		out.VerificationSHA256 = workstore.Hash(jsonBytes(out.Checks))
		return out, nil
	}
	origins := map[string]pluginorigin.Check{}
	o, originErr := m.originManager()
	if originErr == nil {
		state, err := o.State(lock.Plugins)
		originErr = err
		for _, c := range state.Results {
			origins[c.Name] = c
		}
	}
	for _, p := range lock.Plugins {
		check, observation, err := pluginlock.VerifyObserved(ctx, m.Store.Local, p, servers[p.Name])
		if err != nil {
			return out, err
		}
		manifest := pluginmanifest.Inspect(p, servers[p.Name], check, observation)
		out.Manifests = append(out.Manifests, manifest)
		seen := map[inventory.Finding]bool{}
		for _, finding := range check.Checks {
			seen[finding] = true
		}
		for _, finding := range manifest.Checks {
			if !seen[finding] {
				check.Add(finding.Status, finding.Code, finding.Detail)
			}
		}
		if originErr != nil {
			check.Add("FAIL", "MCP_ORIGIN_STORE_INVALID", originErr.Error())
		} else if origin, ok := origins[p.Name]; ok {
			check = withOriginChecks(check, origin)
		}
		out.Plugins = append(out.Plugins, check)
		if statusRank(check.Status) > statusRank(out.Status) {
			out.Status = check.Status
		}
	}
	out.VerificationSHA256 = workstore.Hash(jsonBytes(out.Plugins))
	return out, nil
}

type MCPLockInspection struct {
	ExpectedSHA256 string       `json:"expected_sha256"`
	Document       string       `json:"document"`
	Candidate      MCPLockState `json:"candidate"`
}

func (m *Manager) InspectMCPLock(ctx context.Context, req pluginlock.Preparation) (MCPLockInspection, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out MCPLockInspection
	err := m.Store.Locked(func() error {
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
		conf, err := m.mcpConfig(t)
		if err != nil {
			return err
		}
		var server map[string]any
		for _, raw := range array(conf["servers"]) {
			s := raw.(map[string]any)
			if s["name"] == req.Name {
				server = s
				break
			}
		}
		plugin, err := pluginlock.Prepare(ctx, m.Store.Local, server, req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return result.Fail("MCP_ARTIFACT_INVALID", err.Error(), 5)
		}
		lock := pluginlock.Lock{Version: 1, Plugins: []pluginlock.Plugin{}}
		if entry, ok := t.Entries["plugins.lock"]; ok {
			b, err := m.Store.Data(entry)
			if err != nil {
				return err
			}
			lock, err = pluginlock.Parse(b)
			if err != nil {
				return result.Fail("MCP_LOCK_INVALID", err.Error(), 5)
			}
		}
		plugins := []pluginlock.Plugin{}
		for _, p := range lock.Plugins {
			if p.Name != req.Name {
				plugins = append(plugins, p)
			}
		}
		lock.Plugins = append(plugins, plugin)
		out.ExpectedSHA256 = lockDigest(t)
		out.Document = string(jsonBytes(lock))
		if _, err = pluginlock.Parse([]byte(out.Document)); err != nil {
			return result.Fail("MCP_LOCK_INVALID", err.Error(), 5)
		}
		if err = m.Store.PutFile(&t, "plugins.lock", []byte(out.Document), 0644); err != nil {
			return err
		}
		out.Candidate, err = m.mcpLockState(ctx, t)
		return err
	})
	return out, err
}

func (m *Manager) MCPLock(ctx context.Context) (MCPLockState, error) {
	var out MCPLockState
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
		if _, _, err = m.load(&t); err != nil {
			return err
		}
		if _, ok := t.Entries[m.identityFile()]; !ok {
			return result.Fail("SOURCE_NOT_INITIALIZED", "先创建或打开项目", 4)
		}
		out, err = m.mcpLockState(ctx, t)
		return err
	})
	return out, err
}

func (m *Manager) MCPLockPlan(ctx context.Context, req MCPLockEdit) (MCPLockPreview, error) {
	out := MCPLockPreview{Preview: Preview{Owner: m.Owner, Scope: m.Scope, Findings: []inventory.Finding{}, PendingApply: true}}
	if req.ExpectedSHA256 == "" || req.Document == "" {
		return out, result.Fail("INVALID_INPUT", "需要已读取的锁文件摘要及完整 JSON/YAML 清单", 2)
	}
	if _, err := pluginlock.Parse([]byte(req.Document)); err != nil {
		return out, result.Fail("MCP_LOCK_INVALID", err.Error(), 5)
	}
	err := m.Store.Locked(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.Store.Pending(); err != nil {
			return err
		}
		before, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		if _, _, err = m.load(&before); err != nil {
			return err
		}
		if _, ok := before.Entries[m.identityFile()]; !ok {
			return result.Fail("SOURCE_NOT_INITIALIZED", "先创建或打开项目", 4)
		}
		if req.ExpectedSHA256 != lockDigest(before) {
			return result.Fail("PLAN_STALE", "安装锁已变化；草稿保留，请核对后重新读取", 3)
		}
		after := workstore.Clone(before)
		mode := uint32(0644)
		if e, ok := before.Entries["plugins.lock"]; ok {
			mode = e.Mode
		}
		if err = m.Store.PutFile(&after, "plugins.lock", []byte(req.Document), mode); err != nil {
			return err
		}
		out.Candidate, err = m.mcpLockState(ctx, after)
		if err != nil {
			return err
		}
		m.planInputs = map[string]string{"kind": "mcp-lock", "local_root": m.Store.Local, "artifact_state": out.Candidate.VerificationSHA256}
		defer func() { m.planInputs = nil }()
		out.Plan, err = m.preview("mcp-lock-edit", before, after)
		return err
	})
	return out, err
}

func (m *Manager) validateMCPLockPlan(ctx context.Context, p workstore.Plan) error {
	if p.Inputs["kind"] != "mcp-lock" || p.Inputs["local_root"] != m.Store.Local || p.Inputs["artifact_state"] == "" {
		return result.Fail("PLAN_STALE", "安装锁计划缺少本地核验绑定", 3)
	}
	filtered := map[string]string{}
	for k, v := range p.Inputs {
		if k != "kind" && k != "local_root" && k != "artifact_state" {
			filtered[k] = v
		}
	}
	if err := m.validateInputs(ctx, filtered); err != nil {
		return err
	}
	e, ok := p.After.Entries["plugins.lock"]
	if !ok {
		return result.Fail("MCP_LOCK_INVALID", "安装锁保存计划缺少锁文件", 5)
	}
	b, err := m.Store.Data(e)
	if err != nil {
		return err
	}
	if _, err = pluginlock.Parse(b); err != nil {
		return result.Fail("MCP_LOCK_INVALID", err.Error(), 5)
	}
	for _, change := range workstore.Changes(p.Before, p.After) {
		if change.Path != "plugins.lock" {
			return result.Fail("MCP_LOCK_INVALID", "安装锁保存不能修改其它源文件", 5)
		}
	}
	state, err := m.mcpLockState(ctx, p.After)
	if err != nil {
		return err
	}
	if state.VerificationSHA256 != p.Inputs["artifact_state"] {
		return result.Fail("PLAN_STALE", "本地产物或其核验结果在预览后发生变化；重新核验和预览", 3)
	}
	return nil
}

func (m *Manager) ApplyMCPLock(ctx context.Context, id, digest string) (workstore.Journal, error) {
	if digest == "" {
		return workstore.Journal{}, result.Fail("INVALID_INPUT", "需要确认摘要", 2)
	}
	p, err := m.Store.LoadPlan(id)
	if err != nil {
		return workstore.Journal{}, err
	}
	if p.Action != "mcp-lock-edit" {
		return workstore.Journal{}, result.Fail("INVALID_INPUT", "此入口只确认安装锁保存；回退使用源时间线", 2)
	}
	return m.Apply(ctx, id, digest)
}
