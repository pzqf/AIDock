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
	current := append([]mcpsession.Input{}, inputs...)
	for _, input := range inputs {
		input.Mode = "stdio"
		current = append(current, input)
	}
	state, err := manager.State(current)
	state.Candidates = inputs
	return state, err
}
func (m *Manager) CheckMCPSession(ctx context.Context, req MCPSessionRequest) (mcpsession.Event, error) {
	var out mcpsession.Event
	err := m.withMCPSessionInput(ctx, req, func(s lockedMCPSession) error {
		refs, err := secref.Definition(s.Server)
		if err != nil {
			return err
		}
		sessions, err := m.sessionManager()
		if err != nil {
			return err
		}
		out, err = sessions.Probe(ctx, s.Input, func(audit func(mcprelay.Event) error) (string, int) {
			probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
			defer cancel()
			vault, err := credentials.New(m.Store.Local)
			if err != nil {
				return "MCP_SESSION_START_FAILED", 0
			}
			var process *mcpsession.Process
			deliveryErr := vault.WithEnvironment(probeCtx, credentials.Delivery{BindingSHA256: workstore.Hash(jsonBytes(sessions.Binding)), AuthorizationSHA256: workstore.Hash(jsonBytes(s.Approval)), InputSHA256: workstore.Hash(jsonBytes(s.Approval.Input))}, refs, func(env []string) error {
				if err := s.Check(probeCtx); err != nil {
					return err
				}
				var startErr error
				process, startErr = mcpsession.Start(probeCtx, filepath.Join(m.Store.Local, filepath.FromSlash(s.Plugin.Artifact.Path)), s.Input, env)
				return startErr
			})
			if deliveryErr != nil || process == nil {
				return "MCP_SESSION_START_FAILED", 0
			}
			binding := mcprelay.Binding{Project: m.Owner, Scope: m.Scope, Copy: m.Copy, Plugin: req.Name, DataLevel: s.Approval.DataLevel, Destination: s.Approval.Destination}
			code, count := process.Handshake(probeCtx, s.Input, *s.Approval.Input.ToolPolicy, binding, s.Check, audit)
			verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer verifyCancel()
			if err := s.Check(verifyCtx); err != nil {
				return "MCP_SESSION_ARTIFACT_CHANGED", 0
			}
			return code, count
		})
		return err
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
