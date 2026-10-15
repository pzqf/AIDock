package native

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/source"
)

func TestMCPEnabledDeclarationCannotBeSilentlyOmittedFromNativeApply(t *testing.T) {
	m, _ := fixture(t)
	b, err := config.ReadFile(filepath.Join("..", "config", "testdata", "mcp.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := config.Validate("mcp", b)
	if err != nil {
		t.Fatal(err)
	}
	conf := v.(map[string]any)
	conf["servers"].([]any)[0].(map[string]any)["enabled"] = true
	state, err := m.Source.MCP()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Source.MCPPlan(context.Background(), source.MCPEdit{ExpectedSHA256: state.SHA256, Config: conf})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	native, err := m.Plan(context.Background(), Request{Tools: []string{"claude-code", "codex-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	if !native.Blocked || !has(native, "MCP_EXECUTION_PENDING") || !has(native, "MCP_LOCK_MISSING") {
		t.Fatal(native)
	}
	for _, target := range native.Targets {
		if target.Status != "FAIL" {
			t.Fatal(target)
		}
	}
}

func TestMCPLocalMatchingLockCannotBypassExecutionAndSourceTrust(t *testing.T) {
	m, _ := fixture(t)
	ctx := context.Background()
	b, err := config.ReadFile(filepath.Join("..", "config", "testdata", "mcp.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := config.Validate("mcp", b)
	if err != nil {
		t.Fatal(err)
	}
	conf := v.(map[string]any)
	server := conf["servers"].([]any)[0].(map[string]any)
	server["enabled"] = true
	server["source"] = map[string]any{"type": "local", "path": "not-read"}
	server["entrypoint"] = "helper"
	state, err := m.Source.MCP()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Source.MCPPlan(ctx, source.MCPEdit{ExpectedSHA256: state.SHA256, Config: conf})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.ApplyMCP(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(m.Source.Store.Local, "mcp-artifacts", "fixture", "helper")
	write(t, program, []byte("never execute fixture"))
	if err = os.Chmod(program, 0755); err != nil {
		t.Fatal(err)
	}
	inspected, err := m.Source.InspectMCPLock(ctx, pluginlock.Preparation{Name: server["name"].(string), ArtifactPath: "mcp-artifacts/fixture", Version: "1.2.3", Runtime: pluginlock.Runtime{Kind: "native", Version: "1.2.3", Constraint: "=1.2.3", Executable: "helper"}})
	if err != nil {
		t.Fatal(err)
	}
	lp, err := m.Source.MCPLockPlan(ctx, source.MCPLockEdit{ExpectedSHA256: inspected.ExpectedSHA256, Document: inspected.Document})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.ApplyMCPLock(ctx, lp.Plan.ID, lp.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	native, err := m.Plan(ctx, Request{Tools: []string{"claude-code", "codex-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	if !native.Blocked || !has(native, "MCP_ARTIFACT_MATCH") || !has(native, "MCP_SOURCE_TRUST_UNVERIFIED") || !has(native, "MCP_EXECUTION_PENDING") || has(native, "MCP_LOCK_MISSING") {
		t.Fatal(native)
	}
	write(t, program, []byte("changed"))
	native, err = m.Plan(ctx, Request{Tools: []string{"claude-code", "codex-cli"}})
	if err != nil || !native.Blocked || !has(native, "MCP_ARTIFACT_CHANGED") {
		t.Fatal(native, err)
	}
}
