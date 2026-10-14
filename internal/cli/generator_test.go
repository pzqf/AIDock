package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/generator"
	"github.com/pzqf/AIDock/internal/source"
)

func TestGeneratorCLIExplicitHomeClosedExecutionAndStageLifecycle(t *testing.T) {
	for _, args := range [][]string{{"generator", "history"}, {"generator", "inspect", "--adapter", "x"}, {"generator", "stage", "--adapter", "x", "--digest", "x"}, {"generator", "show", "--stage", "x"}, {"generator", "clean", "--stage", "x", "--digest", "x"}} {
		exit, out, _ := invoke(t, append(args, "--json")...)
		if exit != 4 || out["code"] != "SOURCE_BINDING_REQUIRED" {
			t.Fatal(exit, out)
		}
	}
	exit, out, _ := invoke(t, "generator", "run", "--json")
	if exit != 4 || out["code"] != "GENERATOR_EXECUTION_UNAVAILABLE" {
		t.Fatal(exit, out)
	}
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	root := filepath.Join(dir, "project", ".aidock")
	project := "11111111-1111-4111-8111-111111111111"
	if err := os.MkdirAll(filepath.Dir(root), 0755); err != nil {
		t.Fatal(err)
	}
	m, err := source.New(root, filepath.Join(home, "local"), project, "server")
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "cli-guide", Description: "CLI fixture", Document: "body"})
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
	runtime := filepath.Join(dir, "runtime")
	if err = os.Mkdir(runtime, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(runtime, "fixture.bin"), []byte("must not launch"), 0644); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "adapter.json")
	bytes, _ := json.Marshal(generator.Contract{Version: 1, ID: "cli-fixture", Release: "1.0", Protocol: generator.Protocol, Runtime: runtime, Entry: "fixture.bin", Tools: []string{"codex-cli"}, Network: "deny", Children: "deny"})
	if err = os.WriteFile(adapter, bytes, 0644); err != nil {
		t.Fatal(err)
	}
	binding := []string{"--home", home, "--managed-root", root, "--project", project, "--scope", "server", "--json"}
	run := func(args ...string) (int, map[string]any) {
		exit, out, _ := invoke(t, append(args, binding...)...)
		return exit, out
	}
	exit, out = run("generator", "inspect", "--adapter", adapter)
	if exit != 0 {
		t.Fatal(exit, out)
	}
	preview := out["items"].(map[string]any)
	if preview["execution_ready"] != false {
		t.Fatal(preview)
	}
	exit, out = run("generator", "stage", "--adapter", adapter, "--digest", preview["digest"].(string))
	if exit != 0 {
		t.Fatal(exit, out)
	}
	o := out["items"].(map[string]any)
	s := o["stage"].(map[string]any)
	exit, out = run("generator", "validate", "--stage", s["id"].(string), "--digest", s["digest"].(string))
	if exit != 5 || out["code"] != "STATIC_CHECK_FAILED" {
		t.Fatal(exit, out)
	}
	exit, out = run("generator", "show", "--stage", s["id"].(string))
	if exit != 0 {
		t.Fatal(exit, out)
	}
	current := out["items"].(map[string]any)
	exit, out = run("generator", "clean", "--stage", s["id"].(string), "--digest", current["observation_digest"].(string))
	if exit != 0 || out["items"].(map[string]any)["state"] != "CLEANED" {
		t.Fatal(exit, out)
	}
	exit, out = run("generator", "history")
	if exit != 0 || len(out["items"].([]any)) != 1 {
		t.Fatal(exit, out)
	}
}
