package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/source"
)

func TestMCPCLIStoresDefinitionsWithExplicitConfirmation(t *testing.T) {
	d := t.TempDir()
	root, home := filepath.Join(d, ".aidock"), filepath.Join(d, "home")
	owner := "11111111-1111-4111-8111-111111111111"
	m, err := source.New(root, filepath.Join(home, "local"), owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "guide", Description: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	flags := []string{"--managed-root", root, "--home", home, "--project", owner, "--scope", "server", "--json"}
	exit, v, _ := invoke(t, append([]string{"mcp", "list"}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	state := v["items"].(map[string]any)
	definition := map[string]any{"schema_version": 1, "servers": []any{}}
	b, _ := json.Marshal(map[string]any{"expected_sha256": state["sha256"], "config": definition})
	file := filepath.Join(d, "edit.json")
	if err = os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
	exit, v, _ = invoke(t, append([]string{"mcp", "plan", "--file", file}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	if _, err = os.Stat(filepath.Join(root, "mcp.yaml")); !os.IsNotExist(err) {
		t.Fatal("CLI preview changed source")
	}
	plan := v["items"].(map[string]any)["plan"].(map[string]any)
	exit, v, _ = invoke(t, append([]string{"mcp", "apply", "--plan", plan["id"].(string)}, flags...)...)
	if exit == 0 {
		t.Fatal("missing digest allowed")
	}
	exit, v, _ = invoke(t, append([]string{"mcp", "apply", "--plan", plan["id"].(string), "--expected-digest", plan["digest"].(string)}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, append([]string{"mcp", "check"}, flags...)...)
	if exit != 0 || v["items"].(map[string]any)["execution_ready"] != false {
		t.Fatal(exit, v)
	}
	b, err = os.ReadFile(filepath.Join("..", "config", "testdata", "mcp.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := config.Validate("mcp", b)
	if err != nil {
		t.Fatal(err)
	}
	for _, transport := range []string{"stdio", "http"} {
		current, err := m.MCP()
		if err != nil {
			t.Fatal(err)
		}
		conf := raw.(map[string]any)
		conf["servers"].([]any)[0].(map[string]any)["transport"] = transport
		p, err := m.MCPPlan(context.Background(), source.MCPEdit{ExpectedSHA256: current.SHA256, Config: conf})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = m.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
			t.Fatal(err)
		}
		exit, v, _ = invoke(t, append([]string{"mcp", "list"}, flags...)...)
		if exit != 0 {
			t.Fatal(exit, v)
		}
		exit, v, _ = invoke(t, append([]string{"mcp", "check"}, flags...)...)
		want, code := 3, "EVIDENCE_INCOMPLETE"
		if transport == "http" {
			want, code = 5, "STATIC_CHECK_FAILED"
		}
		if exit != want || v["code"] != code || v["items"].(map[string]any)["execution_ready"] != false {
			t.Fatal(exit, v)
		}
	}
	exit, v, stderr := invoke(t, append([]string{"mcp", "exec"}, flags...)...)
	if exit != 4 || v != nil || !strings.Contains(stderr, "FEATURE_NOT_IMPLEMENTED") {
		t.Fatal(exit, v, stderr)
	}
}
