package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestSourceCommandsRequireExplicitBinding(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not-created")
	for _, args := range [][]string{{"skill", "plan", "create"}, {"skill", "apply"}, {"skill", "history"}, {"skill", "recover"}, {"skill", "managed-list"}, {"skill", "check"}, {"skill", "references", "unknown"}} {
		exit, v, _ := invoke(t, append(args, "--home", home, "--json")...)
		if exit != 4 || v["code"] != "SOURCE_BINDING_REQUIRED" {
			t.Fatal(exit, v)
		}
	}
	if _, e := os.Stat(home); !os.IsNotExist(e) {
		t.Fatal("unbound commands created home")
	}
}

func TestCLILifecycleActionsShareEngine(t *testing.T) {
	d := t.TempDir()
	root, home := filepath.Join(d, ".aidock"), filepath.Join(d, "data")
	owner := "11111111-1111-4111-8111-111111111111"
	m, e := source.New(root, filepath.Join(home, "local"), owner, "server")
	if e != nil {
		t.Fatal(e)
	}
	p, e := m.Plan(context.Background(), source.Request{Action: "create", Name: "guide", Description: "example"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Apply(context.Background(), p.Plan.ID); e != nil {
		t.Fatal(e)
	}
	flags := []string{"--managed-root", root, "--home", home, "--project", owner, "--scope", "server", "--json"}
	exit, v, _ := invoke(t, append([]string{"skill", "references", p.SkillID}, flags...)...)
	if exit != 0 || v["items"].(map[string]any)["reference_scope"] != "CURRENT_BOUND_SOURCE" {
		t.Fatal(exit, v)
	}
	name := "项目约定"
	tags := []string{"server"}
	for _, req := range []source.Request{{Action: "metadata", ID: p.SkillID, DisplayName: &name, Tags: &tags}, {Action: "rename", ID: p.SkillID, Name: "guide-v2"}, {Action: "retire", ID: p.SkillID, Reason: "unused"}, {Action: "reactivate", ID: p.SkillID}} {
		b, _ := json.Marshal(req)
		descriptor := filepath.Join(d, "request.json")
		if e = os.WriteFile(descriptor, b, 0644); e != nil {
			t.Fatal(e)
		}
		exit, v, _ = invoke(t, append([]string{"skill", "plan", req.Action, "--file", descriptor}, flags...)...)
		if exit != 0 {
			t.Fatal(exit, v)
		}
		plan := v["items"].(map[string]any)["plan"].(map[string]any)
		saved, e := m.Store.LoadPlan(plan["id"].(string))
		if e != nil {
			t.Fatal(e)
		}
		if req.Action == "metadata" || req.Action == "rename" {
			same, e := m.Plan(context.Background(), req)
			if e != nil || workstore.Digest(same.Plan.After) != workstore.Digest(saved.After) {
				t.Fatal("CLI differs from source engine", e)
			}
		}
		exit, v, _ = invoke(t, append([]string{"skill", "apply", "--plan", saved.ID, "--expected-digest", saved.Digest}, flags...)...)
		if exit != 0 {
			t.Fatal(exit, v)
		}
	}
	b, _ := json.Marshal(source.Request{Action: "delete", ID: p.SkillID})
	descriptor := filepath.Join(d, "delete.json")
	if e = os.WriteFile(descriptor, b, 0644); e != nil {
		t.Fatal(e)
	}
	exit, v, _ = invoke(t, append([]string{"skill", "plan", "delete", "--file", descriptor}, flags...)...)
	if exit != 4 || v["code"] != "DELETE_SCOPE_UNKNOWN" {
		t.Fatal(exit, v)
	}
}
func TestCLIAndSharedSourceEngineEquivalent(t *testing.T) {
	d := t.TempDir()
	home, root := filepath.Join(d, "data"), filepath.Join(d, ".aidock")
	owner := "11111111-1111-4111-8111-111111111111"
	descriptor := filepath.Join(d, "create.yaml")
	if e := os.WriteFile(descriptor, []byte("name: guide\ndescription: CLI example\ndocument: body\n"), 0644); e != nil {
		t.Fatal(e)
	}
	flags := []string{"--managed-root", root, "--home", home, "--project", owner, "--scope", "server", "--json"}
	exit, v, _ := invoke(t, append([]string{"skill", "plan", "create", "--file", descriptor}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	p := v["items"].(map[string]any)
	plan := p["plan"].(map[string]any)
	m, e := source.New(root, filepath.Join(home, "local"), owner, "server")
	if e != nil {
		t.Fatal(e)
	}
	same, e := m.Plan(context.Background(), source.Request{Action: "create", ID: p["skill_id"].(string), Name: "guide", Description: "CLI example", Document: "body"})
	if e != nil {
		t.Fatal(e)
	}
	saved, e := m.Store.LoadPlan(plan["id"].(string))
	if e != nil || workstore.Digest(same.Plan.After) != workstore.Digest(saved.After) {
		t.Fatal("CLI result differs from engine", e)
	}
	exit, v, _ = invoke(t, append([]string{"skill", "apply", "--plan", saved.ID, "--expected-digest", saved.Digest}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, append([]string{"skill", "managed-list"}, flags...)...)
	if exit != 0 || len(v["items"].(map[string]any)["index"].(map[string]any)["skills"].([]any)) != 1 {
		t.Fatal(exit, v)
	}
}
