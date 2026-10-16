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

// A reader or unrelated short transaction can temporarily own one of the
// stores. Retry contention before entering the caller's step; never repeat a
// startup, disclosure or transfer that has already been attempted.
func (m *Manager) withMCPClientInput(ctx context.Context, req MCPSessionRequest, fn func(lockedMCPSession) error) error {
	deadline := time.Now().Add(2 * time.Second)
	for {
		entered := false
		err := m.withMCPSessionInput(ctx, req, func(s lockedMCPSession) error {
			entered = true
			return fn(s)
		})
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

// withMCPSessionInput holds source, profile, artifact and permission locks only
// through a launch or individual protocol transfer. Never wait for a plugin
// response inside fn when serving a persistent client.
func (m *Manager) withMCPSessionInput(ctx context.Context, req MCPSessionRequest, fn func(lockedMCPSession) error) error {
	if !req.Probe || req.Name == "" || len(req.ExpectedSHA256) != 64 || len(req.ExpectedReviewSHA256) != 64 || len(req.ExpectedRuntimeAttempt) != 36 || fn == nil {
		return result.Fail("INVALID_INPUT", "会话启动需要明确确认、插件与刚审查的授权和运行时记录", 2)
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

					return fn(lockedMCPSession{Input: input, Approval: approval, Plugin: *plugin, Server: server, Check: check})
				})
			})
		})
	})
}
