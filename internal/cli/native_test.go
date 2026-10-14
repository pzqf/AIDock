package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func TestNativeCLIRequiresBindingAndRechecksSavedPreview(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not-created")
	for _, args := range [][]string{{"plan", "--all"}, {"apply"}, {"diff"}} {
		exit, v, _ := invoke(t, append(args, "--home", home, "--json")...)
		if exit != 4 || v["code"] != "SOURCE_BINDING_REQUIRED" {
			t.Fatal(exit, v)
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("unbound commands created home")
	}
	d := t.TempDir()
	home = filepath.Join(t.TempDir(), "data")
	owner := "11111111-1111-4111-8111-111111111111"
	m, err := source.New(filepath.Join(d, ".aidock"), filepath.Join(home, "local"), owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "guide", Description: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	enabled, err := m.Plan(context.Background(), source.Request{Action: "enable", ID: p.SkillID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), enabled.Plan.ID, enabled.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	flags := []string{"--managed-root", m.Store.Root, "--home", home, "--project", owner, "--scope", "server", "--json"}
	exit, v, _ := invoke(t, append([]string{"plan", "--all"}, flags...)...)
	if exit != 4 || v["code"] != "NATIVE_BINDING_REQUIRED" {
		t.Fatal(exit, v)
	}
	flags = append(flags, "--native-root", d)
	exit, v, _ = invoke(t, append([]string{"plan", "--all"}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	items := v["items"].(map[string]any)
	id, digest := items["id"].(string), items["digest"].(string)
	if items["kind"] != "native-config" || items["write_supported"] != false || len(items["targets"].([]any)) != 2 {
		t.Fatal(items)
	}
	exit, v, _ = invoke(t, append([]string{"diff", "--plan", id, "--digest", digest}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, append([]string{"apply", "--plan", id, "--digest", digest}, flags...)...)
	if exit != 4 || v["code"] != "NATIVE_WRITE_UNAVAILABLE" {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, append([]string{"apply", "--plan", p.Plan.ID, "--digest", p.Plan.Digest}, flags...)...)
	if exit != 2 || v["code"] != "PLAN_KIND_MISMATCH" {
		t.Fatal(exit, v)
	}
	for _, path := range []string{".agents/skills", ".claude/skills"} {
		if _, err := os.Stat(filepath.Join(d, path)); !os.IsNotExist(err) {
			t.Fatal("target created")
		}
	}
	exit, v, _ = invoke(t, append([]string{"tool", "check", "--all"}, flags...)...)
	if exit != 3 || v["code"] != "EVIDENCE_INCOMPLETE" {
		t.Fatal(exit, v)
	}
	checkID := v["items"].(map[string]any)["id"].(string)
	exit, v, _ = invoke(t, append([]string{"tool", "check", "--last"}, flags...)...)
	if exit != 3 || v["items"].(map[string]any)["stale"] != false || v["items"].(map[string]any)["id"] != checkID {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, append([]string{"tool", "check", "--last", "--probe"}, flags...)...)
	if exit != 2 || v["code"] != "INVALID_INPUT" {
		t.Fatal(exit, v)
	}
	target := filepath.Join(d, ".claude/skills/guide")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("manual"), 0644); err != nil {
		t.Fatal(err)
	}
	exit, v, _ = invoke(t, append([]string{"plan", "--all"}, flags...)...)
	if exit != 5 || v["code"] != "NATIVE_PREFLIGHT_FAILED" || v["items"].(map[string]any)["blocked"] != true {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, append([]string{"tool", "check", "--last"}, flags...)...)
	if exit != 3 || v["code"] != "CHECK_STALE" || v["items"].(map[string]any)["stale"] != true {
		t.Fatal(exit, v)
	}
}
