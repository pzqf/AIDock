package cli

import (
	"context"
	"github.com/pzqf/AIDock/internal/source"
	"os"
	"path/filepath"
	"testing"
)

func TestRehearsalCLIApplicationRollbackUndo(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	owner := "11111111-1111-4111-8111-111111111111"
	m, err := source.New(filepath.Join(root, ".aidock"), filepath.Join(home, "local"), owner, "server")
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
	e, err := m.Plan(context.Background(), source.Request{Action: "enable", ID: p.SkillID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), e.Plan.ID, e.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	state, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	state.Profile["rules"] = []any{"CLI rules\nsecond line"}
	edit, err := m.ProfilePlan(context.Background(), source.ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), edit.Plan.ID, edit.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	flags := []string{"--managed-root", m.Store.Root, "--home", home, "--project", owner, "--scope", "server", "--native-root", root, "--json"}
	for _, args := range [][]string{{"tool", "check", "--all", "--rule-probe"}, {"tool", "check", "--last", "--rule-probe"}} {
		exit, v, _ := invoke(t, append(args, flags...)...)
		if exit != 2 || v["code"] != "INVALID_INPUT" {
			t.Fatal(exit, v)
		}
	}
	call := func(args ...string) map[string]any {
		t.Helper()
		exit, v, _ := invoke(t, append(args, flags...)...)
		if exit != 0 {
			t.Fatal(exit, v)
		}
		return v
	}
	plan := call("plan", "--all")["items"].(map[string]any)
	r := call("tool", "rehearsal", "run", "--plan", plan["id"].(string), "--digest", plan["digest"].(string))["items"].(map[string]any)
	if r["status"] != "APPLIED" {
		t.Fatal(r)
	}
	if r["transactions"].([]any)[0].(map[string]any)["targets"].(float64) != 4 {
		t.Fatal(r)
	}
	if data, err := os.ReadFile(filepath.Join(r["root"].(string), "AGENTS.md")); err != nil || len(data) == 0 {
		t.Fatal(err)
	}
	for _, want := range []string{"ROLLED_BACK", "UNDONE"} {
		tx := r["transactions"].([]any)[0].(map[string]any)
		q := call("tool", "rehearsal", "plan", "--session", r["id"].(string), "--transaction", tx["id"].(string))["items"].(map[string]any)
		r = call("tool", "rehearsal", "apply", "--session", r["id"].(string), "--plan", q["id"].(string), "--digest", q["digest"].(string))["items"].(map[string]any)
		if r["status"] != want {
			t.Fatal(r)
		}
	}
	latest := call("tool", "rehearsal", "show")["items"].(map[string]any)
	if latest["id"] != r["id"] || latest["status"] != "UNDONE" {
		t.Fatal(latest)
	}
	if len(call("tool", "history")["items"].([]any)) != 0 {
		t.Fatal("rehearsal entered actual history")
	}
	if call("tool", "recover")["items"].(float64) != 0 {
		t.Fatal("unexpected actual recovery")
	}
	for _, dir := range []string{".claude", ".agents"} {
		if _, err := os.Stat(filepath.Join(root, dir)); !os.IsNotExist(err) {
			t.Fatal("bound target written")
		}
	}
}
