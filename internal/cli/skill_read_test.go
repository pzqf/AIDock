package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func createReadSkill(t *testing.T, m *source.Manager, req source.Request) source.Preview {
	t.Helper()
	p, err := m.Plan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCanonicalSkillReadsUseStableBindingAndPreserveSource(t *testing.T) {
	d := t.TempDir()
	home, root := filepath.Join(d, "data"), filepath.Join(d, "project", ".aidock")
	owner := "11111111-1111-4111-8111-111111111111"
	if err := os.Mkdir(filepath.Dir(root), 0700); err != nil {
		t.Fatal(err)
	}
	m, err := source.New(root, filepath.Join(home, "local"), owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	p := createReadSkill(t, m, source.Request{Action: "create", Name: "same-guide", Description: "当前项目说明", Document: "只读正文"})
	before, err := m.Store.Capture(root)
	if err != nil {
		t.Fatal(err)
	}
	flags := []string{"--managed-root", root, "--home", home, "--project", owner, "--scope", "server", "--json"}
	exit, canonical, _ := invoke(t, append([]string{"skill", "list"}, flags...)...)
	if exit != 0 || canonical["project"] != owner {
		t.Fatal(exit, canonical)
	}
	exit, legacy, _ := invoke(t, append([]string{"skill", "managed-list"}, flags...)...)
	if exit != 0 || !reflect.DeepEqual(canonical["items"], legacy["items"]) {
		t.Fatal("canonical list must preserve the managed state contract", exit, legacy)
	}
	exit, v, _ := invoke(t, append([]string{"skill", "show", p.SkillID}, flags...)...)
	if exit != 0 || v["items"].(map[string]any)["description"] != "当前项目说明" {
		t.Fatal(exit, v)
	}
	asset := v["items"].(map[string]any)["asset"].(map[string]any)
	if asset["skill"].(map[string]any)["id"] != p.SkillID || asset["read_only"] != false {
		t.Fatal(asset)
	}
	exit, v, _ = invoke(t, append([]string{"skill", "show", p.SkillID, "--file", "SKILL.md"}, flags...)...)
	if exit != 0 || !strings.Contains(v["items"].(map[string]any)["file"].(map[string]any)["content"].(string), "只读正文") {
		t.Fatal(exit, v)
	}
	for _, args := range [][]string{{"skill", "show", "same-guide"}, {"skill", "show", "22222222-2222-4222-8222-222222222222"}, {"skill", "show", p.SkillID, "--file", "../profile.yaml"}, {"skill", "show", p.SkillID, "--file="}, {"skill", "list", "--include-generated"}, {"skill", "scan"}} {
		exit, v, _ = invoke(t, append(args, flags...)...)
		if exit == 0 {
			t.Fatal("read accepted an ambiguous identity or unsupported scope", args, v)
		}
	}
	after, err := m.Store.Capture(root)
	if err != nil || workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("read command changed source", err)
	}
}

func TestCanonicalSkillSwitchesPreviewThenRequireDigestAndFreshSource(t *testing.T) {
	d := t.TempDir()
	home, root := filepath.Join(d, "data"), filepath.Join(d, ".aidock")
	owner := "11111111-1111-4111-8111-111111111111"
	m, err := source.New(root, filepath.Join(home, "local"), owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	p := createReadSkill(t, m, source.Request{Action: "create", Name: "switch-guide", Description: "example"})
	flags := []string{"--managed-root", root, "--home", home, "--project", owner, "--scope", "server", "--json"}
	for _, action := range []string{"enable", "disable"} {
		before, _ := m.Store.Capture(root)
		exit, v, _ := invoke(t, append([]string{"skill", action, p.SkillID}, flags...)...)
		if exit != 0 || !strings.Contains(v["summary"].(string), "skill apply --plan") {
			t.Fatal(exit, v)
		}
		after, _ := m.Store.Capture(root)
		if workstore.Digest(before) != workstore.Digest(after) {
			t.Fatal("switch preview changed source")
		}
		plan := v["items"].(map[string]any)["plan"].(map[string]any)
		exit, v, _ = invoke(t, append([]string{"skill", "apply", "--plan", plan["id"].(string)}, flags...)...)
		if exit != 2 || v["code"] != "INVALID_INPUT" {
			t.Fatal("confirmation accepted missing digest", exit, v)
		}
		exit, v, _ = invoke(t, append([]string{"skill", "apply", "--plan", plan["id"].(string), "--expected-digest", "wrong"}, flags...)...)
		if exit == 0 {
			t.Fatal("confirmation ignored digest", v)
		}
		exit, v, _ = invoke(t, append([]string{"skill", "apply", "--plan", plan["id"].(string), "--expected-digest", plan["digest"].(string)}, flags...)...)
		if exit != 0 {
			t.Fatal(exit, v)
		}
		s, err := m.State()
		if err != nil {
			t.Fatal(err)
		}
		selected := s.EffectiveProfile["skills"].(map[string]any)[action].([]any)
		if len(selected) != 1 || selected[0] != p.SkillID {
			t.Fatal("effective selection differs from confirmed action", s.EffectiveProfile)
		}
	}
	exit, v, _ := invoke(t, append([]string{"skill", "enable", p.SkillID}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	plan := v["items"].(map[string]any)["plan"].(map[string]any)
	createReadSkill(t, m, source.Request{Action: "create", Name: "new-guide", Description: "source changed"})
	before, _ := m.Store.Capture(root)
	exit, v, _ = invoke(t, append([]string{"skill", "apply", "--plan", plan["id"].(string), "--expected-digest", plan["digest"].(string)}, flags...)...)
	if exit == 0 || v["code"] != "PLAN_STALE" {
		t.Fatal("stale confirmation accepted", exit, v)
	}
	after, _ := m.Store.Capture(root)
	if workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("failed confirmation changed source")
	}
	for _, name := range []string{".agents", ".claude"} {
		if _, err := os.Stat(filepath.Join(d, name)); !os.IsNotExist(err) {
			t.Fatal("source switch wrote tool files", name, err)
		}
	}
}

func TestCanonicalSkillReadsKeepManifestCandidateContract(t *testing.T) {
	d := t.TempDir()
	home := filepath.Join(d, "not-created")
	flags := []string{"--manifest", filepath.Join("..", "..", "examples", "fixture.scan.yaml"), "--home", home, "--json"}
	exit, v, _ := invoke(t, append([]string{"skill", "list"}, flags...)...)
	if exit != 0 || v["items"].(map[string]any)["read_only"] != true {
		t.Fatal(exit, v)
	}
	candidate := v["items"].(map[string]any)["skills"].([]any)[0].(map[string]any)["candidate_id"].(string)
	exit, v, _ = invoke(t, append([]string{"skill", "show", candidate}, flags...)...)
	if exit != 0 || v["items"].(map[string]any)["candidate_id"] != candidate {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, append([]string{"skill", "show", candidate, "--file", "SKILL.md"}, flags...)...)
	if exit != 2 || v["code"] != "INVALID_INPUT" {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, "skill", "enable", candidate, "--home", home, "--json")
	if exit != 4 || v["code"] != "SOURCE_BINDING_REQUIRED" {
		t.Fatal(exit, v)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("read-only/unbound command initialized data root")
	}
}
