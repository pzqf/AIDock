package source

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func mcpFixture() map[string]any {
	return map[string]any{"schema_version": 1, "servers": []any{map[string]any{
		"name": "project-memory", "source": map[string]any{"type": "npm", "package": "@fixture/memory", "version": "1.2.3"},
		"transport": "stdio", "entrypoint": "dist/index.js", "tier": 1, "enabled": false,
		"permissions": map[string]any{"fs": map[string]any{"paths": []any{"${PROJECT}"}, "mode": "ro"}, "net": map[string]any{"allow": []any{}, "default": "deny"}, "exec": false},
	}}}
}

func TestMCPDefinitionPreviewSaveRestartRollbackAndUndo(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "test"})
	original, err := m.Store.Capture(m.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	state, err := m.MCP()
	if err != nil || state.Present || state.ExecutionReady {
		t.Fatal(state, err)
	}
	conf := mcpFixture()
	p, err := m.MCPPlan(context.Background(), MCPEdit{ExpectedSHA256: state.SHA256, Config: conf})
	if err != nil {
		t.Fatal(err)
	}
	if p.Plan.Action != "mcp-edit" || p.Candidate.ExecutionReady || p.Candidate.Servers[0].Status != "UNKNOWN" {
		t.Fatal(p)
	}
	if _, err = os.Stat(filepath.Join(m.Store.Root, "mcp.yaml")); !os.IsNotExist(err) {
		t.Fatal("preview wrote source", err)
	}
	if _, err = m.ApplyMCP(context.Background(), p.Plan.ID, "wrong"); err == nil {
		t.Fatal("missing digest binding")
	}
	if _, err = m.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	fresh, err := New(m.Store.Root, m.Store.Local, m.Owner, m.Scope)
	if err != nil {
		t.Fatal(err)
	}
	state, err = fresh.MCP()
	if err != nil || !state.Present || len(state.Servers) != 1 || state.Servers[0].Enabled {
		t.Fatal(state, err)
	}
	if changes := workstore.Changes(original, p.Plan.After); len(changes) != 1 || changes[0].Path != "mcp.yaml" {
		t.Fatal(changes)
	}
	rolled := apply(t, fresh, Request{Action: "rollback", Snapshot: p.Plan.ID})
	now, err := fresh.Store.Capture(fresh.Store.Root)
	if err != nil || workstore.Digest(now) != workstore.Digest(original) {
		t.Fatal("rollback changed original source", err)
	}
	apply(t, fresh, Request{Action: "rollback", Snapshot: rolled.Plan.ID})
	state, err = fresh.MCP()
	if err != nil || !state.Present || state.ExecutionReady {
		t.Fatal(state, err)
	}
	// A hand-written YAML definition is restored byte for byte, including BOM,
	// comments and CRLF; the editor does not reinterpret it as client config.
	bytes := append([]byte{0xef, 0xbb, 0xbf}, []byte("# 保留原文\r\nschema_version: 1\r\nservers: []\r\n")...)
	write(t, filepath.Join(m.Store.Root, "mcp.yaml"), bytes)
	state, err = m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	p, err = m.MCPPlan(context.Background(), MCPEdit{ExpectedSHA256: state.SHA256, Config: conf})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	apply(t, m, Request{Action: "rollback", Snapshot: p.Plan.ID})
	got, _ := os.ReadFile(filepath.Join(m.Store.Root, "mcp.yaml"))
	if string(got) != string(bytes) {
		t.Fatal("lost original YAML bytes")
	}
}

func TestMCPDefinitionStaleCancelRepairAndNoExecution(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "test"})
	state, _ := m.MCP()
	req := MCPEdit{ExpectedSHA256: state.SHA256, Config: mcpFixture()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.MCPPlan(ctx, req); err != context.Canceled {
		t.Fatal(err)
	}
	p, err := m.MCPPlan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(m.Store.Root, "mcp.yaml"), []byte("invalid: ["))
	_, err = m.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest)
	code(t, err, "PLAN_STALE")
	_, err = m.MCPPlan(context.Background(), req)
	code(t, err, "PLAN_STALE")
	// A corrupt definition can be repaired with an explicit observed byte hash,
	// while that original byte sequence stays available for rollback.
	req.ExpectedSHA256 = workstore.Hash([]byte("invalid: ["))
	p, err = m.MCPPlan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	other, err := m.Plan(context.Background(), Request{Action: "disable", ID: mcpSkillID(t, m)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.ApplyMCP(context.Background(), other.Plan.ID, other.Plan.Digest)
	code(t, err, "INVALID_INPUT")
	_, err = m.ApplyMCP(context.Background(), p.Plan.ID, "")
	code(t, err, "INVALID_INPUT")
}

func mcpSkillID(t *testing.T, m *Manager) string {
	t.Helper()
	s, err := m.State()
	if err != nil {
		t.Fatal(err)
	}
	return s.Index.Skills[0].ID
}

func TestMCPDefinitionDiagnosticsNeverGrantRuntime(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "test"})
	state, _ := m.MCP()
	for _, version := range []string{"latest", "^1.2.3", "main", "beta", "1.2.3 || 2.0.0"} {
		conf := mcpFixture()
		s := conf["servers"].([]any)[0].(map[string]any)
		s["transport"] = "http"
		s["enabled"] = true
		s["source"].(map[string]any)["version"] = version
		p, err := m.MCPPlan(context.Background(), MCPEdit{ExpectedSHA256: state.SHA256, Config: conf})
		if err != nil {
			t.Fatal(err)
		}
		item := p.Candidate.Servers[0]
		codes := map[string]bool{}
		for _, f := range item.Checks {
			codes[f.Code] = true
		}
		if item.Status != "FAIL" || !codes["UNSUPPORTED_TRANSPORT"] || !codes["MCP_VERSION_NOT_PINNED"] || !codes["MCP_LOCK_MISSING"] || item.ExecutionReady {
			t.Fatal(item)
		}
	}
	// Detect a lock without trusting its mere presence or following source.path.
	write(t, filepath.Join(m.Store.Root, "plugins.lock"), []byte("unverified"))
	conf := mcpFixture()
	s := conf["servers"].([]any)[0].(map[string]any)
	s["source"] = map[string]any{"type": "local", "path": "../outside"}
	p, err := m.MCPPlan(context.Background(), MCPEdit{ExpectedSHA256: state.SHA256, Config: conf})
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, f := range p.Candidate.Servers[0].Checks {
		codes[f.Code] = true
	}
	if !codes["MCP_SOURCE_NOT_PORTABLE"] || !codes["MCP_LOCK_NOT_VERIFIED"] {
		t.Fatal(p.Candidate)
	}
}

func TestMCPDefinitionInterruptedWriteRecoversOriginalBytes(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "test"})
	before, _ := m.Store.Capture(m.Store.Root)
	state, _ := m.MCP()
	p, err := m.MCPPlan(context.Background(), MCPEdit{ExpectedSHA256: state.SHA256, Config: mcpFixture()})
	if err != nil {
		t.Fatal(err)
	}
	m.Store.Hook = func(stage string, _ int) error {
		if stage == "replaced" {
			return fmt.Errorf("injected interruption")
		}
		return nil
	}
	if _, err = m.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err == nil {
		t.Fatal("expected injected failure")
	}
	m.Store.Hook = nil
	if _, err = m.Recover(); err != nil {
		t.Fatal(err)
	}
	after, _ := m.Store.Capture(m.Store.Root)
	if workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("recovery lost original tree")
	}
}

func TestMCPEnabledDefinitionRemainsVisibleToDoctorAndProfile(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "test"})
	state, _ := m.MCP()
	conf := mcpFixture()
	conf["servers"].([]any)[0].(map[string]any)["enabled"] = true
	p, err := m.MCPPlan(context.Background(), MCPEdit{ExpectedSHA256: state.SHA256, Config: conf})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	report, err := m.Check()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, check := range report.ProfileChecks {
		if check.Code == "MCP_EXECUTION_PENDING" {
			found = true
		}
	}
	if !found || report.NativeReady {
		t.Fatal(report)
	}
	profile, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, check := range profile.Checks {
		if check.Code == "MCP_LOCK_MISSING" {
			found = true
		}
	}
	if !found {
		t.Fatal(profile)
	}
}
