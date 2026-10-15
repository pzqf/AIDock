package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/source"
)

func TestProfileCLIResolveAndSavedPlanUseSameSourceTransaction(t *testing.T) {
	d := t.TempDir()
	root, home := filepath.Join(d, ".aidock"), filepath.Join(d, "data")
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
	exit, v, _ := invoke(t, append([]string{"profile", "show"}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	state, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	state.Profile["name"] = "CLI Profile"
	file := filepath.Join(d, "edit.json")
	bytes, _ := json.Marshal(source.ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile})
	if err = os.WriteFile(file, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	exit, v, _ = invoke(t, append([]string{"profile", "plan", "--file", file}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	plan := v["items"].(map[string]any)["plan"].(map[string]any)
	exit, v, _ = invoke(t, append([]string{"skill", "apply", "--plan", plan["id"].(string), "--expected-digest", plan["digest"].(string)}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	state, err = m.Profile()
	if err != nil || state.Profile["name"] != "CLI Profile" {
		t.Fatal(state, err)
	}
	bytes, _ = json.Marshal(map[string]any{"project": state.Profile, "catalog": map[string]any{"skills": map[string]any{p.SkillID: []string{state.Skills[0].Skill.Revision}}}})
	if err = os.WriteFile(file, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	exit, v, _ = invoke(t, "profile", "resolve", "--file", file, "--json")
	if exit != 0 || v["items"].(map[string]any)["digest"] != state.Report.Digest {
		t.Fatal(exit, v)
	}
	files := []source.RuleFile{{Name: "CLI 规则", Enabled: true, Body: "# CLI\r\n保留文件原文。\r\n"}}
	bytes, _ = json.Marshal(source.ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile, RuleFiles: &files, ExpectedRulesSHA256: state.RulesSHA256})
	if err = os.WriteFile(file, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	exit, v, _ = invoke(t, append([]string{"profile", "plan", "--file", file}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	plan = v["items"].(map[string]any)["plan"].(map[string]any)
	exit, v, _ = invoke(t, append([]string{"skill", "apply", "--plan", plan["id"].(string), "--expected-digest", plan["digest"].(string)}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, append([]string{"profile", "show"}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	shown := v["items"].(map[string]any)["rule_files"].([]any)
	if len(shown) != 1 || shown[0].(map[string]any)["body"] != files[0].Body || shown[0].(map[string]any)["id"] == "" {
		t.Fatal(shown)
	}
}

func TestLocalProfileCLIRequiresOwnTargetAndRetainsSharedSource(t *testing.T) {
	d := t.TempDir()
	root, home := filepath.Join(d, ".aidock"), filepath.Join(d, "home")
	owner := "11111111-1111-4111-8111-111111111111"
	m, err := source.New(root, filepath.Join(home, "local"), owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	m.Copy = "copy-cli"
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "guide", Description: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	definition, err := config.ReadFile(filepath.Join("..", "config", "testdata", "mcp.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := config.Validate("mcp", definition)
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := m.MCP()
	mp, err := m.MCPPlan(context.Background(), source.MCPEdit{ExpectedSHA256: ms.SHA256, Config: raw.(map[string]any)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCP(context.Background(), mp.Plan.ID, mp.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	state, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(d, "local.json")
	b, _ := json.Marshal(source.ProfileEdit{Target: "local", ExpectedSHA256: state.Layers.LocalSHA256, ExpectedMCP: state.MCPSHA256, Profile: map[string]any{"schema_version": 2, "name": "local", "rules": []any{"CLI 本机规则"}, "mcp": map[string]any{"enable": []any{"fixture"}}}})
	os.WriteFile(file, b, 0600)
	flags := []string{"--managed-root", root, "--home", home, "--project", owner, "--scope", "server", "--copy", m.Copy, "--json"}
	exit, v, _ := invoke(t, append([]string{"profile", "plan", "--file", file}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	plan := v["items"].(map[string]any)["plan"].(map[string]any)
	exit, v, _ = invoke(t, append([]string{"profile", "apply", "--target", "local", "--plan", plan["id"].(string), "--expected-digest", plan["digest"].(string)}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	current, err := m.Profile()
	if err != nil || current.Layers.Local == nil || current.SHA256 != state.SHA256 {
		t.Fatal(current, err)
	}
	exit, v, _ = invoke(t, append([]string{"mcp", "list"}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	server := v["items"].(map[string]any)["servers"].([]any)[0].(map[string]any)
	if server["enabled"] != false || server["effective_enabled"] != true || server["enabled_source"] != "local" || server["execution_ready"] != false {
		t.Fatal(server)
	}
}
