package native

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/config"
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
