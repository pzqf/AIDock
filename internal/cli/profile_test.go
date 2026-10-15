package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
}
