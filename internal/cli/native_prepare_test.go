package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func TestPrepareCLIHasStaticDefaultAndNeedsExplicitProbe(t *testing.T) {
	d, home := t.TempDir(), t.TempDir()
	owner := "11111111-1111-4111-8111-111111111111"
	m, err := source.New(filepath.Join(d, ".aidock"), filepath.Join(home, "local"), owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "prepare-guide", Description: "static preparation"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	flags := []string{"--managed-root", m.Store.Root, "--home", home, "--project", owner, "--scope", "server", "--native-root", d, "--json"}
	exit, v, _ := invoke(t, append([]string{"tool", "prepare", "--all"}, flags...)...)
	if exit != 3 || v["code"] != "EVIDENCE_INCOMPLETE" {
		t.Fatal(exit, v)
	}
	r := v["items"].(map[string]any)
	plan, check := r["plan"].(map[string]any), r["check"].(map[string]any)
	if r["status"] != "NEEDS_CHECK" || check["probe"] != false || plan["write_supported"] != false || len(plan["targets"].([]any)) != 2 {
		t.Fatal(r)
	}
	exit, v, _ = invoke(t, append([]string{"diff", "--plan", plan["id"].(string), "--expected-digest", plan["digest"].(string)}, flags...)...)
	if exit != 0 {
		t.Fatal("could not inspect preparation plan", exit, v)
	}
	for _, scope := range []string{"full", "skills-rules"} {
		exit, v, _ = invoke(t, append([]string{"tool", "prepare", "--all", "--application-scope", scope}, flags...)...)
		if exit != 3 || v["code"] != "EVIDENCE_INCOMPLETE" {
			t.Fatal(exit, v)
		}
		report := v["items"].(map[string]any)
		plan := report["plan"].(map[string]any)
		if scope == "skills-rules" && (plan["application_scope"] != scope || report["check"].(map[string]any)["application_scope"] != scope) {
			t.Fatal(report)
		}
		exit, v, _ = invoke(t, append([]string{"diff", "--plan", plan["id"].(string), "--digest", plan["digest"].(string)}, flags...)...)
		if exit != 0 {
			t.Fatal("scope lost on diff", exit, v)
		}
		for _, args := range [][]string{{"plan", "--all", "--application-scope", scope}, {"tool", "check", "--all", "--application-scope", scope}} {
			exit, v, _ = invoke(t, append(args, flags...)...)
			if exit != 0 && exit != 3 {
				t.Fatal(exit, v)
			}
			if scope == "skills-rules" && v["items"].(map[string]any)["application_scope"] != scope {
				t.Fatal(v)
			}
		}
	}
	for _, args := range [][]string{{"tool", "prepare", "--all", "--application-scope", "skills"}, {"plan", "--all", "--application-scope", "mcp"}, {"tool", "check", "--all", "--application-scope", "unknown"}, {"tool", "check", "--last", "--application-scope", "skills-rules"}, {"tool", "prepare", "--all", "--tool", "codex-cli"}, {"tool", "prepare", "--tool", "unknown"}, {"tool", "prepare", "--all", "--rule-probe"}} {
		exit, v, _ = invoke(t, append(args, flags...)...)
		if exit != 2 {
			t.Fatal("accepted invalid preparation", exit, v)
		}
	}
	for _, path := range []string{".agents", ".claude"} {
		if _, err := os.Stat(filepath.Join(d, path)); !os.IsNotExist(err) {
			t.Fatal("preparation wrote target", err)
		}
	}
}
