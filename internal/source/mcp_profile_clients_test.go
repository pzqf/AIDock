package source

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestProfileClientConfigsExactSelectionLocksAndNoLaunch(t *testing.T) {
	m, req := sessionFixture(t)
	ctx := context.Background()
	executable, home := filepath.Join(t.TempDir(), "aidock.exe"), filepath.Dir(m.Store.Local)
	before, _ := m.Export()
	called := false
	err := m.WithProfileMCPClientConfigs(ctx, []MCPSessionRequest{req}, executable, home, func(bundle Export, configs []MCPClientConfiguration) error {
		called = true
		if len(configs) != 1 || configs[0].Command != executable || bundle.Snapshot != before.Snapshot || bundle.ProfileReport.Digest != before.ProfileReport.Digest {
			t.Fatal("joint source or launch binding changed")
		}
		// Source and authorization writers cannot interleave with this bounded
		// transaction. No callbacks re-enter those stores for ordinary reads.
		_, e := m.Plan(ctx, Request{Action: "create", Name: "must-stay-blocked", Description: "lock test"})
		code(t, e, "SOURCE_BUSY")
		p, _ := m.permissionManager()
		e = p.Store.Locked(func() error { t.Fatal("authorization lock was not held"); return nil })
		code(t, e, "SOURCE_BUSY")
		return nil
	})
	if err != nil || !called {
		t.Fatal("joint consumer not reached", err)
	}
	s, err := m.MCPSessions(ctx)
	if err != nil || s.AuditCount != 0 {
		t.Fatal("configuration query started a plugin", err)
	}
	after, _ := m.Export()
	if workstore.Hash(jsonBytes(before)) != workstore.Hash(jsonBytes(after)) {
		t.Fatal("query modified source or evidence")
	}
	consumer := func(Export, []MCPClientConfiguration) error {
		t.Fatal("invalid selection reached consumer")
		return nil
	}
	code(t, m.WithProfileMCPClientConfigs(ctx, nil, executable, home, consumer), "PROFILE_MCP_SELECTION_MISMATCH")
	code(t, m.WithProfileMCPClientConfigs(ctx, []MCPSessionRequest{req, req}, executable, home, consumer), "INVALID_INPUT")
	other := req
	other.Name = "unknown-plugin"
	code(t, m.WithProfileMCPClientConfigs(ctx, []MCPSessionRequest{other}, executable, home, consumer), "MCP_NOT_ENABLED")
	permissionApply(t, m, permissionPlan(t, m, pluginpermission.Edit{Action: "revoke", Name: req.Name}))
	code(t, m.WithProfileMCPClientConfigs(ctx, []MCPSessionRequest{req}, executable, home, consumer), "PLAN_STALE")
}
