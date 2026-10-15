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
	for _, args := range [][]string{{"tool", "prepare", "--all", "--tool", "codex-cli"}, {"tool", "prepare", "--tool", "unknown"}, {"tool", "prepare", "--all", "--rule-probe"}} {
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
