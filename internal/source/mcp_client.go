package source

import (
	"context"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/credentials"
	"github.com/pzqf/AIDock/internal/mcprelay"
	"github.com/pzqf/AIDock/internal/mcpsession"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/secref"
	"github.com/pzqf/AIDock/internal/workstore"
)

// ServeMCPClient owns one stdio session. The operation/audit lock is independent
// of all mutable project stores. Authority locks span only startup or a bounded
// transfer, never client lifetime or an upstream response wait.
func (m *Manager) ServeMCPClient(ctx context.Context, req MCPSessionRequest, client mcprelay.Endpoint) (mcpsession.Event, error) {
	if client.Reader == nil || client.Writer == nil {
		return mcpsession.Event{}, result.Fail("INVALID_INPUT", "客户端需要独立 stdio 读写端点", 2)
	}
	var input mcpsession.Input
	if err := m.withMCPClientInput(ctx, req, func(s lockedMCPSession) error {
		input = s.Input
		input.Mode = "stdio"
		return nil
	}); err != nil {
		return mcpsession.Event{}, err
	}
	sessions, err := m.sessionManager()
	if err != nil {
		return mcpsession.Event{}, err
	}
	return sessions.Client(ctx, input, func(audit func(mcprelay.Event) error) (string, int) {
		var process *mcpsession.Process
		var selection lockedMCPSession
		err := m.withMCPClientInput(ctx, req, func(s lockedMCPSession) error {
			selection = s
			refs, err := secref.Definition(s.Server)
			if err != nil {
				return err
			}
			vault, err := credentials.New(m.Store.Local)
			if err != nil {
				return err
			}
			return vault.WithEnvironment(ctx, credentials.Delivery{BindingSHA256: workstore.Hash(jsonBytes(sessions.Binding)), AuthorizationSHA256: workstore.Hash(jsonBytes(s.Approval)), InputSHA256: workstore.Hash(jsonBytes(s.Approval.Input))}, refs, func(env []string) error {
				if err := s.Check(ctx); err != nil {
					return err
				}
				var startErr error
				process, startErr = mcpsession.Start(ctx, filepath.Join(m.Store.Local, filepath.FromSlash(s.Plugin.Artifact.Path)), input, env)
				return startErr
			})
		})
		if err != nil || process == nil {
			return "MCP_SESSION_START_FAILED", 0
		}
		guard := func(c context.Context, transfer func() error) error {
			entered := false
			err := m.withMCPClientInput(c, req, func(s lockedMCPSession) error {
				// The expected permission/review/runtime receipts also bind source,
				// effective profile, installation and complete artifact bytes.
				entered = true
				return transfer()
			})
			if err != nil && !entered {
				return &mcprelay.Error{Code: "AUTHORIZATION_STALE"}
			}
			return err
		}
		binding := mcprelay.Binding{Project: m.Owner, Scope: m.Scope, Copy: m.Copy, Plugin: req.Name, DataLevel: selection.Approval.DataLevel, Destination: selection.Approval.Destination}
		return process.Serve(ctx, input, *selection.Approval.Input.ToolPolicy, binding, client, guard, audit)
	})
}
