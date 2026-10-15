package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/source"
)

func mcpCLIArtifactFixture(t *testing.T) (*source.Manager, []string) {
	t.Helper()
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
	permissions := map[string]any{"fs": map[string]any{"paths": []any{}, "mode": "ro"}, "net": map[string]any{"allow": []any{}, "default": "deny"}, "exec": false}
	src := map[string]any{"type": "local", "path": "unused-source"}
	conf := map[string]any{"schema_version": 1, "servers": []any{map[string]any{"name": "fixture", "source": src, "transport": "stdio", "entrypoint": "helper", "tier": 1, "enabled": false, "permissions": permissions}}}
	s, _ := m.MCP()
	mp, err := m.MCPPlan(ctx, source.MCPEdit{ExpectedSHA256: s.SHA256, Config: conf})
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
	if err = os.WriteFile(filepath.Join(artifact, "helper"), []byte("never execute fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{"schema_version": 1, "name": "publisher-fixture", "type": "mcp-server", "version": "1.2.3", "source": src, "permissions": permissions, "dependencies": []any{}}
	b, _ := json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(artifact, "plugin.yaml"), b, 0644); err != nil {
		t.Fatal(err)
	}
	inspected, err := m.InspectMCPLock(ctx, pluginlock.Preparation{Name: "fixture", ArtifactPath: "mcp-artifacts/fixture", Version: "1.2.3", Runtime: pluginlock.Runtime{Kind: "native", Version: "1.2.3", Constraint: "=1.2.3", Executable: "helper"}})
	if err != nil {
		t.Fatal(err)
	}
	lp, err := m.MCPLockPlan(ctx, source.MCPLockEdit{ExpectedSHA256: inspected.ExpectedSHA256, Document: inspected.Document})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPLock(ctx, lp.Plan.ID, lp.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	flags := []string{"--managed-root", root, "--home", home, "--project", owner, "--scope", "server", "--json"}
	return m, flags
}

func TestMCPInstallCLIExplicitRiskSBOMRollbackAndRecovery(t *testing.T) {
	m, flags := mcpCLIArtifactFixture(t)
	d := t.TempDir()
	ctx := context.Background()
	call := func(args ...string) (int, map[string]any) {
		t.Helper()
		code, v, _ := invoke(t, append(args, flags...)...)
		return code, v
	}
	exit, v := call("mcp", "install", "state")
	if exit != 0 {
		t.Fatal(exit, v)
	}
	sha := v["items"].(map[string]any)["sha256"].(string)
	file := filepath.Join(d, "request.json")
	req := plugininstall.Edit{Action: "install", ExpectedSHA256: sha, Name: "fixture"}
	if err := os.WriteFile(file, plugininstall.JSON(req), 0600); err != nil {
		t.Fatal(err)
	}
	exit, v = call("mcp", "install", "plan", "--file", file)
	if exit != 4 || v["code"] != "INSTALL_RISK_CONFIRMATION_REQUIRED" {
		t.Fatal(exit, v)
	}
	req.AllowUnverified = true
	req.RiskReason = "isolated fixture registration only"
	if err := os.WriteFile(file, plugininstall.JSON(req), 0600); err != nil {
		t.Fatal(err)
	}
	exit, v = call("mcp", "install", "plan", "--file", file)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	plan := v["items"].(map[string]any)["plan"].(map[string]any)
	exit, v = call("mcp", "install", "sbom", "--name", "fixture")
	if exit != 4 || v["code"] != "INSTALL_NOT_FOUND" {
		t.Fatal("preview generated installed SBOM", exit, v)
	}
	exit, v = call("mcp", "install", "apply", "--plan", plan["id"].(string), "--expected-digest", "wrong")
	if exit != 3 {
		t.Fatal(exit, v)
	}
	exit, v = call("mcp", "install", "apply", "--plan", plan["id"].(string), "--expected-digest", plan["digest"].(string))
	if exit != 0 {
		t.Fatal(exit, v)
	}
	exit, v = call("mcp", "install", "sbom", "--name", "fixture")
	if exit != 0 || v["items"].(map[string]any)["status"] != "CURRENT" {
		t.Fatal(exit, v)
	}
	state, _ := m.MCPInstall(ctx)
	req = plugininstall.Edit{Action: "rollback", Snapshot: plan["id"].(string), ExpectedSHA256: state.SHA256}
	if err := os.WriteFile(file, plugininstall.JSON(req), 0600); err != nil {
		t.Fatal(err)
	}
	exit, v = call("mcp", "install", "plan", "--file", file)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	plan = v["items"].(map[string]any)["plan"].(map[string]any)
	exit, v = call("mcp", "install", "apply", "--plan", plan["id"].(string), "--expected-digest", plan["digest"].(string))
	if exit != 0 {
		t.Fatal(exit, v)
	}
	exit, v = call("mcp", "install", "sbom", "--name", "fixture")
	if exit != 4 {
		t.Fatal(exit, v)
	}
	exit, v = call("mcp", "install", "recover")
	if exit != 0 {
		t.Fatal(exit, v)
	}
}
