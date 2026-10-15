package source

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func saveMCPDefinition(t *testing.T, m *Manager, enabled bool) {
	t.Helper()
	state, err := m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	conf := mcpFixture()
	conf["servers"].([]any)[0].(map[string]any)["enabled"] = enabled
	p, err := m.MCPPlan(context.Background(), MCPEdit{ExpectedSHA256: state.SHA256, Config: conf})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
}

func mcpProfile(name, action string) map[string]any {
	v := ruleProfile(name)
	if action != "" {
		v["mcp"] = map[string]any{action: []any{"project-memory"}}
	}
	return v
}

func saveMCPProfile(t *testing.T, m *Manager, action, parent string) Preview {
	t.Helper()
	state, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	state.Profile["mcp"] = map[string]any{}
	if action != "" {
		state.Profile["mcp"] = map[string]any{action: []any{"project-memory"}}
	}
	if parent != "" {
		state.Profile["extends"] = parent
	}
	p, err := m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: state.SHA256, ExpectedMCP: state.MCPSHA256, ExpectedLayers: state.Layers.Digest, Profile: state.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	return p
}

func assertMCPSelection(t *testing.T, m *Manager, enabled bool, layer string) ProfileState {
	t.Helper()
	state, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	if state.Report == nil || len(state.MCP) != 1 || !state.MCP[0].SelectionResolved || state.MCP[0].EffectiveEnabled != enabled || state.MCP[0].EnabledSource != layer {
		t.Fatalf("selection: %+v, checks: %+v", state.MCP, state.Checks)
	}
	preflight, err := m.Check()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, check := range preflight.ProfileChecks {
		if check.Code == "MCP_EXECUTION_PENDING" {
			found = true
		}
		if check.Code == "PROFILE_NATIVE_UNSUPPORTED" {
			t.Fatal("MCP is still treated as an unsupported Profile field")
		}
	}
	if found != enabled {
		t.Fatalf("doctor selection differs: enabled=%v, checks=%+v", enabled, preflight.ProfileChecks)
	}
	mcp, err := m.MCP()
	if err != nil || mcp.ProfileReport == nil || mcp.Servers[0].EffectiveEnabled != enabled || mcp.SelectionDigest != state.Report.Digest || mcp.ExecutionReady {
		t.Fatal(mcp, err)
	}
	return state
}

func TestProfileMCPAllLayersDriveChecksAndLocalRollback(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "test"})
	m.Copy = "profile-mcp-copy"
	saveMCPDefinition(t, m, true)
	assertMCPSelection(t, m, true, "mcp-definition")
	l := rulesLibrary(t, m)
	write(t, filepath.Join(l.Store.Root, "profile.yaml"), jsonBytes(mcpProfile("defaults", "disable")))
	write(t, filepath.Join(l.Store.Root, "profiles", "base.yaml"), jsonBytes(mcpProfile("base", "enable")))
	child := mcpProfile("child", "disable")
	child["extends"] = "base"
	write(t, filepath.Join(l.Store.Root, "profiles", "child.yaml"), jsonBytes(child))
	link := l.Store.Root
	saveLocal(t, m, ProfileEdit{LibraryRoot: &link})
	assertMCPSelection(t, m, false, "defaults")
	saveMCPProfile(t, m, "", "child")
	assertMCPSelection(t, m, false, "extends:child")
	saveMCPProfile(t, m, "enable", "child")
	assertMCPSelection(t, m, true, "project")
	before := capture(t, m)
	saveLocal(t, m, ProfileEdit{Profile: mcpProfile("local", "disable")})
	unchangedManager(t, m, before)
	m = reloadCopy(t, m)
	state := assertMCPSelection(t, m, false, "local")
	trace := []string{}
	for _, event := range state.MCP[0].SelectionTrace {
		trace = append(trace, event.Layer)
	}
	if !reflect.DeepEqual(trace, []string{"mcp-definition", "defaults", "extends:base", "extends:child", "project", "local"}) {
		t.Fatal(trace)
	}
	cleared := saveLocal(t, m, ProfileEdit{Clear: true})
	assertMCPSelection(t, m, true, "project")
	restored := saveLocal(t, m, ProfileEdit{Snapshot: cleared.Plan.ID})
	assertMCPSelection(t, m, false, "local")
	saveLocal(t, m, ProfileEdit{Snapshot: restored.Plan.ID})
	assertMCPSelection(t, m, true, "project")
	unchangedManager(t, m, before)
	// Another copy can share the same explicitly attached library, but not the
	// machine switch, its transaction history or its local root.
	other := *m
	other.Copy = "other-copy"
	otherState := assertMCPSelection(t, &other, true, "project")
	if otherState.Layers.Local != nil || len(otherState.Layers.History) != 0 {
		t.Fatal("local selection leaked to another copy")
	}
}

func TestProfileMCPUnknownReferenceRemovalAndStaleConfirmation(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "test"})
	m.Copy = "profile-mcp-stale"
	saveMCPDefinition(t, m, false)
	state := assertMCPSelection(t, m, false, "mcp-definition")
	state.Profile["mcp"] = map[string]any{"enable": []any{"missing"}}
	_, err := m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile})
	code(t, err, "PROFILE_UNKNOWN_ID")
	saveMCPProfile(t, m, "disable", "")
	mcp, _ := m.MCP()
	_, err = m.MCPPlan(context.Background(), MCPEdit{ExpectedSHA256: mcp.SHA256, Config: map[string]any{"schema_version": 1, "servers": []any{}}})
	code(t, err, "MCP_PROFILE_REFERENCE_EXISTS")
	// A definition draft expires when local selection changes, even if its
	// own definition bytes and its overall enabled result have not changed.
	conf := mcpFixture()
	conf["servers"].([]any)[0].(map[string]any)["entrypoint"] = "other.js"
	p, err := m.MCPPlan(context.Background(), MCPEdit{ExpectedSHA256: mcp.SHA256, ExpectedSelection: mcp.SelectionDigest, Config: conf})
	if err != nil {
		t.Fatal(err)
	}
	saveLocal(t, m, ProfileEdit{Profile: mcpProfile("local", "disable")})
	_, err = m.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest)
	code(t, err, "PLAN_STALE")
	_, err = m.MCPPlan(context.Background(), MCPEdit{ExpectedSHA256: mcp.SHA256, ExpectedSelection: mcp.SelectionDigest, Config: conf})
	code(t, err, "PLAN_STALE")
	state, _ = m.Profile()
	local, err := m.ProfilePlan(context.Background(), ProfileEdit{Target: "local", ExpectedSHA256: state.Layers.LocalSHA256, ExpectedMCP: state.MCPSHA256, Profile: mcpProfile("local", "enable")})
	if err != nil {
		t.Fatal(err)
	}
	saveMCPDefinition(t, m, true)
	_, err = m.ApplyProfile(context.Background(), "local", local.Plan.ID, local.Plan.Digest)
	code(t, err, "PLAN_STALE")
	_, err = m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: state.SHA256, ExpectedMCP: state.MCPSHA256, Profile: state.Profile})
	code(t, err, "PLAN_STALE")
	// Removing a reference uses inherit. It does not delete the definition or
	// the local layer, and source rollback restores the explicit shared choice.
	saveLocal(t, m, ProfileEdit{Clear: true})
	shared := saveMCPProfile(t, m, "", "")
	assertMCPSelection(t, m, true, "mcp-definition")
	apply(t, m, Request{Action: "rollback", Snapshot: shared.Plan.ID})
	assertMCPSelection(t, m, false, "project")
	if workstore.Digest(shared.Plan.Before) == workstore.Digest(shared.Plan.After) {
		t.Fatal("reference removal was not a source edit")
	}
}

func TestProfileMCPDisabledInvalidDefinitionCanBeExcludedAndRepaired(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "test"})
	saveMCPDefinition(t, m, false)
	conf := mcpFixture()
	conf["servers"].([]any)[0].(map[string]any)["transport"] = "http"
	write(t, filepath.Join(m.Store.Root, "mcp.yaml"), jsonBytes(conf))
	state := assertMCPSelection(t, m, false, "mcp-definition")
	state.Profile["mcp"] = map[string]any{"enable": []any{"project-memory"}}
	_, err := m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile})
	code(t, err, "PROFILE_RESOLUTION_FAILED")
	// A broken explicit reference stays visible and can be removed from the
	// Profile draft; it is never inferred as an enabled server.
	write(t, filepath.Join(m.Store.Root, "profile.yaml"), jsonBytes(mcpProfile("broken", "enable")))
	write(t, filepath.Join(m.Store.Root, "mcp.yaml"), jsonBytes(map[string]any{"schema_version": 1, "servers": []any{}}))
	state, err = m.Profile()
	if err != nil || state.Report != nil {
		t.Fatal(state, err)
	}
	state.Profile["mcp"] = map[string]any{}
	p, err := m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
}
