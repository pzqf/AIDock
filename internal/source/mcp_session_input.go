package source

import (
	"context"
	"errors"
	"time"

	"github.com/pzqf/AIDock/internal/mcpsession"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/runtimecheck"
	"github.com/pzqf/AIDock/internal/workstore"
)

// A reader can temporarily own one of the stores. Retry only before entering
// the caller's step; never repeat a startup, disclosure or transfer.
func (m *Manager) withMCPClientInput(ctx context.Context, req MCPSessionRequest, fn func(lockedMCPSession) error) error {
	deadline := time.Now().Add(2 * time.Second)
	for {
		entered := false
		err := m.withMCPSessionInput(ctx, req, func(s lockedMCPSession) error { entered = true; return fn(s) })
		var classified *result.Error
		if entered || !errors.As(err, &classified) || classified.Code != "SOURCE_BUSY" || !time.Now().Before(deadline) {
			return err
		}
		timer := time.NewTimer(15 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type lockedMCPSession struct {
	Input    mcpsession.Input
	Approval pluginpermission.Record
	Plugin   pluginlock.Plugin
	Server   map[string]any
	Check    func(context.Context) error
}

func (m *Manager) withMCPSessionInput(ctx context.Context, req MCPSessionRequest, fn func(lockedMCPSession) error) error {
	if fn == nil {
		return result.Fail("INVALID_INPUT", "需要明确的会话消费者", 2)
	}
	return m.withMCPSessionInputs(ctx, []MCPSessionRequest{req}, false, func(s []lockedMCPSession, _ *Export) error { return fn(s[0]) })
}

// Source, effective layers, artifacts and every selected authorization remain
// locked through one bounded consumer. exact requires the complete effective
// MCP set, and supplies the source export observed under those same locks.
func (m *Manager) withMCPSessionInputs(ctx context.Context, requests []MCPSessionRequest, exact bool, fn func([]lockedMCPSession, *Export) error) error {
	names := []string{}
	seen := map[string]bool{}
	if fn == nil || len(requests) > 64 {
		return result.Fail("INVALID_INPUT", "需要明确且有限的会话选择", 2)
	}
	for _, req := range requests {
		if !req.Probe || req.Name == "" || seen[req.Name] || len(req.ExpectedSHA256) != 64 || len(req.ExpectedReviewSHA256) != 64 || len(req.ExpectedRuntimeAttempt) != 36 {
			return result.Fail("INVALID_INPUT", "会话启动需要明确确认、插件与刚审查的授权和运行时记录", 2)
		}
		seen[req.Name] = true
		names = append(names, req.Name)
	}
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
				enabled := map[string]bool{}
				for _, name := range array(section["enable"]) {
					enabled[name.(string)] = true
				}
				for _, name := range names {
					if !enabled[name] {
						return result.Fail("MCP_NOT_ENABLED", "插件在当前有效 Profile 中未启用；未启动", 4)
					}
				}
				var bundle *Export
				if exact {
					if len(enabled) != len(seen) {
						return result.Fail("PROFILE_MCP_SELECTION_MISMATCH", "联合应用必须明确包含当前有效 Profile 的全部 MCP；不能遗漏或额外启用", 4)
					}
					value, e := m.exportLocked()
					if e != nil {
						return e
					}
					bundle = &value
				}
				if len(requests) == 0 {
					return fn([]lockedMCPSession{}, bundle)
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
				recheck := func(checkCtx context.Context) error {
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
					active, err := m.permissionProfile(t)
					if err != nil {
						return err
					}
					if workstore.Hash(jsonBytes(active.Effective)) != workstore.Hash(jsonBytes(report.Effective)) {
						return result.Fail("PLAN_STALE", "有效配置在会话中变化", 3)
					}
					return nil
				}
				return m.withMCPSessionAuthorization(ctx, t, p, candidates, requests, recheck, func(s []lockedMCPSession) error { return fn(s, bundle) })
			})
		})
	})
}

// withMCPSessionAuthorization shares the exact approval/runtime/stdio gates
// for live and frozen candidate configuration. It starts no process. The
// caller owns source/layer/artifact locks through the bounded consumer.
func (m *Manager) withMCPSessionAuthorization(ctx context.Context, t workstore.Tree, p *pluginpermission.Manager, candidates []pluginpermission.Candidate, requests []MCPSessionRequest, recheck func(context.Context) error, use func([]lockedMCPSession) error) error {
	if use == nil || recheck == nil || len(requests) == 0 || len(requests) > 64 {
		return result.Fail("INVALID_INPUT", "需要明确且有限的已核验连接消费者", 2)
	}
	names := []string{}
	seen := map[string]bool{}
	for _, req := range requests {
		if !req.Probe || req.Name == "" || seen[req.Name] || len(req.ExpectedSHA256) != 64 || len(req.ExpectedReviewSHA256) != 64 || len(req.ExpectedRuntimeAttempt) != 36 {
			return result.Fail("INVALID_INPUT", "连接需要明确的当前授权与运行时绑定", 2)
		}
		seen[req.Name] = true
		names = append(names, req.Name)
	}
	state, err := p.State(candidates)
	if err != nil {
		return err
	}
	for _, req := range requests {
		if state.SHA256 != req.ExpectedSHA256 || state.ReviewSHA256 != req.ExpectedReviewSHA256 {
			return result.Fail("PLAN_STALE", "授权或审查输入已变化；未执行会话握手", 3)
		}
	}
	e, ok := t.Entries["plugins.lock"]
	if !ok {
		return result.Fail("MCP_SESSION_INPUT_INVALID", "缺少安装锁", 4)
	}
	data, err := m.Store.Data(e)
	if err != nil {
		return err
	}
	parsed, err := pluginlock.Parse(data)
	if err != nil {
		return err
	}
	conf, err := m.mcpConfig(t)
	if err != nil {
		return err
	}
	return p.WithCurrents(ctx, candidates, names, func(approvals []pluginpermission.Record) error {
		grantTree, err := p.Store.Capture(p.Store.Root)
		if err != nil {
			return err
		}
		for _, req := range requests {
			if workstore.Digest(grantTree) != req.ExpectedSHA256 {
				return result.Fail("PLAN_STALE", "授权在读取后变化；未执行会话握手", 3)
			}
		}
		runtimeInputs := []runtimecheck.Input{}
		for _, approval := range approvals {
			input, e := runtimecheck.From(approval)
			if e != nil {
				return e
			}
			runtimeInputs = append(runtimeInputs, input)
		}
		runtimeManager, err := m.runtimeManager()
		if err != nil {
			return err
		}
		versions, err := runtimeManager.State(runtimeInputs)
		if err != nil {
			return err
		}
		selected := []lockedMCPSession{}
		for i, req := range requests {
			approval := approvals[i]
			var plugin *pluginlock.Plugin
			for _, v := range parsed.Plugins {
				if v.Name == req.Name {
					copy := v
					plugin = &copy
					break
				}
			}
			if plugin == nil {
				return result.Fail("MCP_SESSION_INPUT_INVALID", "安装锁中没有此插件", 4)
			}
			var server map[string]any
			for _, raw := range array(conf["servers"]) {
				value := raw.(map[string]any)
				if value["name"] == req.Name {
					server = value
					break
				}
			}
			if server == nil || server["transport"] != "stdio" {
				return result.Fail("MCP_SESSION_INPUT_INVALID", "只支持已定义的 stdio 插件", 4)
			}
			var proof runtimecheck.Entry
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
			check := func(checkCtx context.Context) error {
				if err := checkCtx.Err(); err != nil {
					return err
				}
				if err := recheck(checkCtx); err != nil {
					return err
				}
				grants, err := p.Store.Capture(p.Store.Root)
				if err != nil {
					return err
				}
				if workstore.Digest(grants) != req.ExpectedSHA256 {
					return result.Fail("PLAN_STALE", "授权在会话中变化", 3)
				}
				verify, err := pluginlock.Verify(checkCtx, m.Store.Local, *plugin, server)
				if err != nil || verify.ArtifactStatus != "PASS" {
					return result.Fail("MCP_SESSION_ARTIFACT_CHANGED", "产物在会话中变化", 4)
				}
				return nil
			}
			selected = append(selected, lockedMCPSession{Input: input, Approval: approval, Plugin: *plugin, Server: server, Check: check})
		}
		return use(selected)
	})
}
