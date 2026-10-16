package source

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/credentials"
	"github.com/pzqf/AIDock/internal/mcprelay"
	"github.com/pzqf/AIDock/internal/mcpsession"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/runtimecheck"
	"github.com/pzqf/AIDock/internal/secref"
	"github.com/pzqf/AIDock/internal/workstore"
)

type MCPSessionRequest struct {
	Name                   string `json:"name"`
	Probe                  bool   `json:"probe"`
	ExpectedSHA256         string `json:"expected_sha256"`
	ExpectedReviewSHA256   string `json:"expected_review_sha256"`
	ExpectedRuntimeAttempt string `json:"expected_runtime_attempt"`
}

func ParseMCPSessionRequest(raw []byte) (MCPSessionRequest, error) {
	var req MCPSessionRequest
	bad := func() (MCPSessionRequest, error) {
		return MCPSessionRequest{}, result.Fail("INVALID_INPUT", "会话请求字段或类型无效；不接受重复键、别名、空值或自定义命令", 2)
	}
	v, err := config.Parse(raw)
	if err != nil {
		return bad()
	}
	fields, ok := v.(map[string]any)
	if !ok {
		return bad()
	}
	for k, v := range fields {
		switch k {
		case "name", "expected_sha256", "expected_review_sha256", "expected_runtime_attempt":
			if _, ok := v.(string); !ok {
				return bad()
			}
		case "probe":
			if _, ok := v.(bool); !ok {
				return bad()
			}
		default:
			return bad()
		}
	}
	if json.Unmarshal(jsonBytes(fields), &req) != nil {
		return bad()
	}
	return req, nil
}
func (m *Manager) sessionManager() (*mcpsession.Manager, error) {
	return mcpsession.New(m.Store.Local, pluginpermission.Binding{Version: 1, Project: m.Owner, Scope: m.Scope, Copy: m.Copy, SourceRoot: m.Store.Root})
}
func (m *Manager) MCPSessions(ctx context.Context) (mcpsession.State, error) {
	permission, err := m.MCPPermission(ctx)
	if err != nil {
		return mcpsession.State{}, err
	}
	manager, err := m.sessionManager()
	if err != nil {
		return mcpsession.State{}, err
	}
	// Session evidence is current only while the exact approval and runtime proof remain current.
	versions, err := m.MCPRuntime(ctx)
	if err != nil {
		return mcpsession.State{}, err
	}

	inputs := []mcpsession.Input{}
	err = m.Store.Locked(func() error {
		if err := m.Store.Pending(); err != nil {
			return err
		}
		tree, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		lockEntry, exists := tree.Entries["plugins.lock"]
		if !exists {
			return nil
		}
		raw, err := m.Store.Data(lockEntry)
		if err != nil {
			return err
		}
		locks, err := pluginlock.Parse(raw)
		if err != nil {
			return err
		}
		report, err := m.permissionProfile(tree)
		if err != nil {
			return err
		}
		section, _ := report.Effective["mcp"].(map[string]any)
		enabled := map[string]bool{}
		for _, n := range array(section["enable"]) {
			if s, ok := n.(string); ok {
				enabled[s] = true
			}
		}
		for _, a := range permission.Authorizations {
			if a.Status != "CURRENT" || !enabled[a.Record.Input.Name] || permission.Recovery || versions.Recovery {
				continue
			}
			var proof runtimecheck.Entry
			for _, v := range versions.Entries {
				if v.Event.Input.Name == a.Record.Input.Name {
					proof = v
					break
				}
			}
			for _, plugin := range locks.Plugins {
				if plugin.Name != a.Record.Input.Name {
					continue
				}
				input, err := mcpsession.From(a.Record, plugin.Entrypoint, proof)
				if err == nil {
					inputs = append(inputs, input)
				}
			}
		}
		return nil
	})
	if err != nil {
		return mcpsession.State{}, err
	}
	state, err := manager.State(inputs)
	state.Candidates = inputs
	return state, err
}
func (m *Manager) CheckMCPSession(ctx context.Context, req MCPSessionRequest) (mcpsession.Event, error) {
	var out mcpsession.Event
	if !req.Probe || req.Name == "" || len(req.ExpectedSHA256) != 64 || len(req.ExpectedReviewSHA256) != 64 || len(req.ExpectedRuntimeAttempt) != 36 {
		return out, result.Fail("INVALID_INPUT", "执行锁定运行时会话握手需要 --probe、明确插件及刚审查的授权与输入摘要", 2)
	}
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
		return m.lockProfileLayers(func() error {
			return m.withArtifactLocks(t, func() error {
				report, err := m.permissionProfile(t)
				if err != nil {
					return err
				}
				section, _ := report.Effective["mcp"].(map[string]any)
				enabled := false
				for _, n := range array(section["enable"]) {
					if n == req.Name {
						enabled = true
					}
				}
				if !enabled {
					return result.Fail("MCP_NOT_ENABLED", "插件在当前有效 Profile 中未启用；未启动", 4)
				}
				lock, err := m.mcpLockState(ctx, t)
				if err != nil {
					return err
				}
				candidates, checks, err := m.permissionInputs(t, lock, report)
				if err != nil {
					return err
				}
				for _, f := range checks {
					if f.Status == "FAIL" {
						return result.Fail("MCP_SESSION_INPUT_INVALID", "安装、完整产物或安全证据无效；未执行", 4)
					}
				}
				p, err := m.permissionManager()
				if err != nil {
					return err
				}
				s, err := p.State(candidates)
				if err != nil {
					return err
				}
				if s.SHA256 != req.ExpectedSHA256 || s.ReviewSHA256 != req.ExpectedReviewSHA256 {
					return result.Fail("PLAN_STALE", "授权或审查输入已变化；未执行会话握手", 3)
				}
				e, ok := t.Entries["plugins.lock"]
				if !ok {
					return result.Fail("MCP_SESSION_INPUT_INVALID", "缺少安装锁", 4)
				}
				b, err := m.Store.Data(e)
				if err != nil {
					return err
				}
				parsed, err := pluginlock.Parse(b)
				if err != nil {
					return err
				}
				var plugin *pluginlock.Plugin
				for _, v := range parsed.Plugins {
					if v.Name == req.Name {
						copy := v
						plugin = &copy
					}
				}
				if plugin == nil {
					return result.Fail("MCP_SESSION_INPUT_INVALID", "安装锁中没有此插件", 4)
				}
				conf, err := m.mcpConfig(t)
				if err != nil {
					return err
				}
				var server map[string]any
				for _, raw := range array(conf["servers"]) {
					v := raw.(map[string]any)
					if v["name"] == req.Name {
						server = v
					}
				}
				if server == nil || server["transport"] != "stdio" {
					return result.Fail("MCP_SESSION_INPUT_INVALID", "只支持已定义的 stdio 插件", 4)
				}
				return p.WithCurrent(ctx, candidates, req.Name, func(approval pluginpermission.Record) error {
					// Check the full approval digest again under the held permission lock.
					// Candidate review is covered by the outer source/layer/artifact locks.
					locked, err := p.Store.Capture(p.Store.Root)
					if err != nil {
						return err
					}
					if workstore.Digest(locked) != req.ExpectedSHA256 {
						return result.Fail("PLAN_STALE", "授权在读取后变化；未执行会话握手", 3)
					}

					runtimeInput, err := runtimecheck.From(approval)
					if err != nil {
						return err
					}
					runtimeManager, err := m.runtimeManager()
					if err != nil {
						return err
					}
					versions, err := runtimeManager.State([]runtimecheck.Input{runtimeInput})
					if err != nil {
						return err
					}
					var proof runtimecheck.Entry
					// A newer failure or incomplete attempt supersedes any older success.
					for _, v := range versions.Entries {
						if v.Event.Input.Name == req.Name {
							proof = v
							break
						}
					}
					if versions.Recovery || proof.Event.Attempt != req.ExpectedRuntimeAttempt {
						return result.Fail("PLAN_STALE", "运行时核验记录已变化或待恢复；未启动", 3)
					}
					input, err := mcpsession.From(approval, plugin.Entrypoint, proof)
					if err != nil {
						return err
					}
					refs, err := secref.Definition(server)
					if err != nil {
						return err
					}
					sessions, err := m.sessionManager()
					if err != nil {
						return err
					}
					artifactRoot := filepath.Join(m.Store.Local, filepath.FromSlash(plugin.Artifact.Path))
					check := func(checkCtx context.Context) error {
						if err := checkCtx.Err(); err != nil {
							return err
						}
						current, err := m.Store.Capture(m.Store.Root)
						if err != nil {
							return err
						}
						if workstore.Digest(current) != workstore.Digest(t) {
							return result.Fail("PLAN_STALE", "源在会话中变化", 3)
						}
						grantTree, err := p.Store.Capture(p.Store.Root)
						if err != nil {
							return err
						}
						if workstore.Digest(grantTree) != req.ExpectedSHA256 {
							return result.Fail("PLAN_STALE", "授权在会话中变化", 3)
						}
						activeReport, err := m.permissionProfile(t)
						if err != nil {
							return err
						}
						if workstore.Hash(jsonBytes(activeReport.Effective)) != workstore.Hash(jsonBytes(report.Effective)) {
							return result.Fail("PLAN_STALE", "有效配置在会话中变化", 3)
						}
						verify, err := pluginlock.Verify(checkCtx, m.Store.Local, *plugin, server)
						if err != nil || verify.ArtifactStatus != "PASS" {
							return result.Fail("MCP_SESSION_ARTIFACT_CHANGED", "产物在会话中变化", 4)
						}
						return nil
					}
					out, err = sessions.Probe(ctx, input, func(audit func(mcprelay.Event) error) (string, int) {
						probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
						defer cancel()
						vault, err := credentials.New(m.Store.Local)
						if err != nil {
							return "MCP_SESSION_START_FAILED", 0
						}
						var process *mcpsession.Process
						deliveryErr := vault.WithEnvironment(probeCtx, credentials.Delivery{BindingSHA256: workstore.Hash(jsonBytes(p.Binding)), AuthorizationSHA256: workstore.Hash(jsonBytes(approval)), InputSHA256: workstore.Hash(jsonBytes(approval.Input))}, refs, func(env []string) error {
							if err := check(probeCtx); err != nil {
								return err
							}
							var startErr error
							process, startErr = mcpsession.Start(probeCtx, artifactRoot, input, env)
							return startErr
						})
						if deliveryErr != nil || process == nil {
							return "MCP_SESSION_START_FAILED", 0
						}
						binding := mcprelay.Binding{Project: m.Owner, Scope: m.Scope, Copy: m.Copy, Plugin: req.Name, DataLevel: approval.DataLevel, Destination: approval.Destination}
						code, count := process.Handshake(probeCtx, input, *approval.Input.ToolPolicy, binding, check, audit)
						verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer verifyCancel()
						if err := check(verifyCtx); err != nil {
							return "MCP_SESSION_ARTIFACT_CHANGED", 0
						}
						return code, count
					})
					return err
				})
			})
		})
	})
	return out, err
}

func (m *Manager) RecoverMCPSessions() (int, error) {
	manager, err := m.sessionManager()
	if err != nil {
		return 0, err
	}
	return manager.Recover()
}
