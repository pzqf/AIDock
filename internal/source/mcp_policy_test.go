package source

import (
	"context"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/mcppolicy"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestToolPolicyRawDraftRejectsAmbiguityWithoutEchoOrMutation(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "test"})
	s, _ := m.MCP()
	before, _ := m.Store.Capture(m.Store.Root)
	for _, raw := range []string{`{"adapter":"scoped-public-query-v1","tools":[],"tools":[]}`, `{"Adapter":"scoped-public-query-v1","tools":[]}`, `{"adapter":"scoped-public-query-v1","tools":null}`, `{"private-sentinel":true}`, `private-sentinel`} {
		_, err := m.MCPPlan(context.Background(), MCPEdit{ExpectedSHA256: s.SHA256, Config: mcpFixture(), ToolPolicyDocuments: map[string]string{"0": raw}})
		code(t, err, "MCP_TOOL_POLICY_INVALID")
		if strings.Contains(err.Error(), "private-sentinel") {
			t.Fatal("draft leak", err)
		}
	}
	for _, key := range []string{"-1", "01", "1", "name"} {
		_, err := m.MCPPlan(context.Background(), MCPEdit{ExpectedSHA256: s.SHA256, Config: mcpFixture(), ToolPolicyDocuments: map[string]string{key: string(jsonBytes(mcppolicy.Example()))}})
		code(t, err, "MCP_TOOL_POLICY_INVALID")
	}
	after, _ := m.Store.Capture(m.Store.Root)
	if workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("invalid draft changed source")
	}
	for _, raw := range []string{`{"tool_policy_documents":null}`, `{"tool_policy_documents":{"0":null}}`, `{"tool_policy_documents":{"0":"{}","0":"{}"}}`, `{"Tool_policy_documents":{}}`} {
		if _, err := ParseMCPEdit([]byte(raw)); err == nil {
			t.Fatal("ambiguous draft accepted")
		}
	}
}

func TestToolPolicySourcePreviewRollbackAndUndo(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "test"})
	ctx := context.Background()
	state, _ := m.MCP()
	original, _ := m.Store.Capture(m.Store.Root)
	p, err := m.MCPPlan(ctx, MCPEdit{ExpectedSHA256: state.SHA256, Config: mcpFixture(), ToolPolicyDocuments: map[string]string{"0": string(jsonBytes(mcppolicy.Example()))}})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := m.Store.Capture(m.Store.Root)
	if workstore.Digest(before) != workstore.Digest(original) {
		t.Fatal("preview wrote source")
	}
	if _, err = m.ApplyMCP(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	fresh, _ := New(m.Store.Root, m.Store.Local, m.Owner, m.Scope)
	saved, err := fresh.MCP()
	if err != nil {
		t.Fatal(err)
	}
	policy, err := mcppolicy.FromDefinition(saved.Config["servers"].([]any)[0].(map[string]any))
	if err != nil || policy == nil || policy.Digest() != mcppolicy.Example().Digest() || saved.ExecutionReady {
		t.Fatal(saved, err)
	}
	rolled := apply(t, fresh, Request{Action: "rollback", Snapshot: p.Plan.ID})
	after, _ := m.Store.Capture(m.Store.Root)
	if workstore.Digest(after) != workstore.Digest(original) {
		t.Fatal("rollback did not restore original tree")
	}
	apply(t, fresh, Request{Action: "rollback", Snapshot: rolled.Plan.ID})
	saved, _ = fresh.MCP()
	remove, err := fresh.MCPPlan(ctx, MCPEdit{ExpectedSHA256: saved.SHA256, Config: saved.Config, ToolPolicyDocuments: map[string]string{"0": ""}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.ApplyMCP(ctx, remove.Plan.ID, remove.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	saved, _ = fresh.MCP()
	if _, exists := saved.Config["servers"].([]any)[0].(map[string]any)["tool_policy"]; exists {
		t.Fatal("clear did not remove policy")
	}
}

func TestToolPolicyChangesLockAndApprovalRequiresSeparateRenewedConsent(t *testing.T) {
	m, preparation := installFixture(t)
	ctx := context.Background()
	installEdit(t, m, allowInstall(preparation.Name))
	permissionApply(t, m, permissionPlan(t, m, grantPermission(preparation.Name)))
	s, _ := m.MCP()
	p, err := m.MCPPlan(ctx, MCPEdit{ExpectedSHA256: s.SHA256, Config: s.Config, ToolPolicyDocuments: map[string]string{"0": string(jsonBytes(mcppolicy.Example()))}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCP(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	a, err := m.MCPPermission(ctx)
	if err != nil || a.Authorizations[0].Status == "CURRENT" {
		t.Fatal("policy reused approval", a, err)
	}
	lock, err := m.MCPLock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	failed := false
	for _, plugin := range lock.Plugins {
		for _, f := range plugin.Checks {
			if f.Code == "MCP_LOCK_DEFINITION_MISMATCH" {
				failed = true
			}
		}
	}
	if !failed {
		t.Fatal("policy did not invalidate definition lock")
	}
	refreshInstallLock(t, m, preparation)
	installEdit(t, m, allowInstall(preparation.Name))
	a, _ = m.MCPPermission(ctx)
	r := grantPermission(preparation.Name)
	r.ExpectedSHA256 = a.SHA256
	r.ExpectedReviewSHA256 = a.ReviewSHA256
	_, err = m.MCPPermissionPlan(ctx, r)
	code(t, err, "AUTHORIZATION_TOOL_CONFIRMATION_REQUIRED")
	r.ConfirmTools = true
	permissionApply(t, m, permissionPlan(t, m, r))
	a, _ = m.MCPPermission(ctx)
	old := a.Authorizations[0].Record.ID
	if !a.Authorizations[0].Record.ToolsConfirmed || a.Authorizations[0].Record.Input.ToolPolicy == nil || a.Authorizations[0].Status != "CURRENT" || a.ExecutionReady {
		t.Fatal(a)
	}
	revoked := permissionPlan(t, m, pluginpermission.Edit{Action: "revoke", Name: preparation.Name})
	permissionApply(t, m, revoked)
	r = grantPermission("")
	r.Action = "rollback"
	r.Snapshot = revoked.Plan.ID
	a, _ = m.MCPPermission(ctx)
	r.ExpectedSHA256 = a.SHA256
	r.ExpectedReviewSHA256 = a.ReviewSHA256
	_, err = m.MCPPermissionPlan(ctx, r)
	code(t, err, "AUTHORIZATION_TOOL_CONFIRMATION_REQUIRED")
	r.ConfirmTools = true
	permissionApply(t, m, permissionPlan(t, m, r))
	a, _ = m.MCPPermission(ctx)
	if a.Authorizations[0].Record.ID == old || !a.Authorizations[0].Record.ToolsConfirmed {
		t.Fatal("restore reused authority", a)
	}
	other := *m
	other.Copy = "another-copy"
	a, _ = other.MCPPermission(ctx)
	if len(a.Authorizations) != 0 {
		t.Fatal("policy approval leaked to other copy")
	}
}
