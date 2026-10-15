package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/source"
)

func TestMCPLockCLIInventoriesPreparedFilesThenSavesWithDigest(t *testing.T) {
	d := t.TempDir()
	root, home := filepath.Join(d, ".aidock"), filepath.Join(d, "home")
	owner := "11111111-1111-4111-8111-111111111111"
	m, err := source.New(root, filepath.Join(home, "local"), owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p, err := m.Plan(ctx, source.Request{Action: "create", Name: "guide", Description: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	conf := map[string]any{"schema_version": 1, "servers": []any{map[string]any{"name": "fixture", "source": map[string]any{"type": "local", "path": "unused-source"}, "transport": "stdio", "entrypoint": "helper", "tier": 1, "enabled": false, "permissions": map[string]any{"fs": map[string]any{"paths": []any{}, "mode": "ro"}, "net": map[string]any{"allow": []any{}, "default": "deny"}, "exec": false}}}}
	state, _ := m.MCP()
	mp, err := m.MCPPlan(ctx, source.MCPEdit{ExpectedSHA256: state.SHA256, Config: conf})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCP(ctx, mp.Plan.ID, mp.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(home, "local", "mcp-artifacts", "fixture")
	if err = os.MkdirAll(artifact, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(artifact, "helper"), []byte("never-run fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	flags := []string{"--managed-root", root, "--home", home, "--project", owner, "--scope", "server", "--json"}
	exit, v, _ := invoke(t, append([]string{"mcp", "lock", "check"}, flags...)...)
	if exit != 3 || v["code"] != "EVIDENCE_INCOMPLETE" {
		t.Fatal(exit, v)
	}
	req := pluginlock.Preparation{Name: "fixture", ArtifactPath: "mcp-artifacts/fixture", Version: "1.2.3", Runtime: pluginlock.Runtime{Kind: "native", Version: "1.2.3", Constraint: "=1.2.3", Executable: "helper"}}
	file := filepath.Join(d, "request.json")
	b, _ := json.Marshal(req)
	if err = os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
	exit, v, _ = invoke(t, append([]string{"mcp", "lock", "inspect", "--file", file}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	inspection := v["items"].(map[string]any)
	if _, err = os.Stat(filepath.Join(root, "plugins.lock")); !os.IsNotExist(err) {
		t.Fatal("inspection saved source")
	}
	b, _ = json.Marshal(map[string]any{"expected_sha256": inspection["expected_sha256"], "document": inspection["document"]})
	if err = os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
	exit, v, _ = invoke(t, append([]string{"mcp", "lock", "plan", "--file", file}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	plan := v["items"].(map[string]any)["plan"].(map[string]any)
	for _, digest := range []string{"", "wrong"} {
		exit, v, _ = invoke(t, append([]string{"mcp", "lock", "apply", "--plan", plan["id"].(string), "--expected-digest", digest}, flags...)...)
		if exit == 0 {
			t.Fatal("invalid confirmation accepted", v)
		}
	}
	exit, v, _ = invoke(t, append([]string{"mcp", "lock", "apply", "--plan", plan["id"].(string), "--expected-digest", plan["digest"].(string)}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, append([]string{"mcp", "lock", "list"}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, append([]string{"mcp", "lock", "check"}, flags...)...)
	if exit != 3 || v["items"].(map[string]any)["execution_ready"] != false {
		t.Fatal(exit, v)
	}
	if err = os.WriteFile(filepath.Join(artifact, "helper"), []byte("tampered"), 0755); err != nil {
		t.Fatal(err)
	}
	exit, v, _ = invoke(t, append([]string{"mcp", "lock", "check"}, flags...)...)
	if exit != 5 || v["code"] != "STATIC_CHECK_FAILED" {
		t.Fatal(exit, v)
	}
}
