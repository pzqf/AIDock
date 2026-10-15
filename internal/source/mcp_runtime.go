package source

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/runtimecheck"
	"github.com/pzqf/AIDock/internal/workstore"
)

type MCPRuntimeRequest struct {
	Name                 string `json:"name"`
	Probe                bool   `json:"probe"`
	ExpectedSHA256       string `json:"expected_sha256"`
	ExpectedReviewSHA256 string `json:"expected_review_sha256"`
}

func ParseMCPRuntimeRequest(b []byte) (MCPRuntimeRequest, error) {
	bad := func() (MCPRuntimeRequest, error) {
		return MCPRuntimeRequest{}, result.Fail("INVALID_INPUT", "版本核验请求字段或类型无效；不接受重复键、别名、空值或自定义命令", 2)
	}
	var req MCPRuntimeRequest
	v, err := config.Parse(b)
	if err != nil {
		return bad()
	}
	m, ok := v.(map[string]any)
	if !ok {
		return bad()
	}
	for k, v := range m {
		switch k {
		case "name", "expected_sha256", "expected_review_sha256":
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
	if json.Unmarshal(jsonBytes(m), &req) != nil {
		return bad()
	}
	return req, nil
}
func (m *Manager) runtimeManager() (*runtimecheck.Manager, error) {
	return runtimecheck.New(m.Store.Local, pluginpermission.Binding{Version: 1, Project: m.Owner, Scope: m.Scope, Copy: m.Copy, SourceRoot: m.Store.Root})
}
func (m *Manager) MCPRuntime(ctx context.Context) (runtimecheck.State, error) {
	permission, err := m.MCPPermission(ctx)
	if err != nil {
		return runtimecheck.State{}, err
	}
	r, err := m.runtimeManager()
	if err != nil {
		return runtimecheck.State{}, err
	}
	return r.State(currentRuntimeInputs(permission))
}

func currentRuntimeInputs(permission pluginpermission.State) []runtimecheck.Input {
	current := []runtimecheck.Input{}
	for _, entry := range permission.Authorizations {
		if entry.Status == "CURRENT" && !permission.Recovery {
			if input, err := runtimecheck.From(entry.Record); err == nil {
				current = append(current, input)
			}
		}
	}
	return current
}
func (m *Manager) CheckMCPRuntime(ctx context.Context, req MCPRuntimeRequest) (runtimecheck.Event, error) {
	var out runtimecheck.Event
	if !req.Probe || req.Name == "" || len(req.ExpectedSHA256) != 64 || len(req.ExpectedReviewSHA256) != 64 {
		return out, result.Fail("INVALID_INPUT", "执行锁定运行时版本核验需要 --probe、明确插件及刚审查的授权与输入摘要", 2)
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
						return result.Fail("RUNTIME_CHECK_INPUT_INVALID", "安装、完整产物或安全证据无效；未执行", 4)
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
					return result.Fail("PLAN_STALE", "授权或审查输入已变化；未执行版本核验", 3)
				}
				e, ok := t.Entries["plugins.lock"]
				if !ok {
					return result.Fail("RUNTIME_CHECK_INPUT_INVALID", "缺少安装锁", 4)
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
					return result.Fail("RUNTIME_CHECK_INPUT_INVALID", "安装锁中没有此插件", 4)
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
					return result.Fail("RUNTIME_CHECK_INPUT_INVALID", "只支持已定义的 stdio 插件", 4)
				}
				return p.WithCurrent(ctx, candidates, req.Name, func(approval pluginpermission.Record) error {
					// Check the full approval digest again under the held permission lock.
					// Candidate review is covered by the outer source/layer/artifact locks.
					locked, err := p.Store.Capture(p.Store.Root)
					if err != nil {
						return err
					}
					if workstore.Digest(locked) != req.ExpectedSHA256 {
						return result.Fail("PLAN_STALE", "授权在读取后变化；未执行版本核验", 3)
					}
					input, err := runtimecheck.From(approval)
					if err != nil {
						return err
					}
					r, err := m.runtimeManager()
					if err != nil {
						return err
					}
					out, err = r.Probe(ctx, input, func() (string, string) {
						code, version := runtimecheck.NodeVersion(ctx, filepath.Join(m.Store.Local, filepath.FromSlash(plugin.Artifact.Path)), input)
						verifyContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer cancel()
						check, verifyErr := pluginlock.Verify(verifyContext, m.Store.Local, *plugin, server)
						if verifyErr != nil || check.ArtifactStatus != "PASS" {
							return "RUNTIME_ARTIFACT_CHANGED", ""
						}
						return code, version
					})
					return err
				})
			})
		})
	})
	return out, err
}
func (m *Manager) RecoverMCPRuntime() (int, error) {
	r, err := m.runtimeManager()
	if err != nil {
		return 0, err
	}
	return r.Recover()
}
