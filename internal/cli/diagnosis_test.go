package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func TestProjectDiagnosisCLIBindsSourceAndKeepsIncompleteExit(t *testing.T) {
	d, home := t.TempDir(), t.TempDir()
	owner := "11111111-1111-4111-8111-111111111111"
	m, err := source.New(filepath.Join(d, ".aidock"), filepath.Join(home, "local"), owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "guide", Description: "diagnosis"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	flags := []string{"--managed-root", m.Store.Root, "--home", home, "--project", owner, "--scope", "server", "--native-root", d, "--json"}
	exit, v, _ := invoke(t, append([]string{"doctor", "--project-status"}, flags...)...)
	if exit != 3 || v["code"] != "EVIDENCE_INCOMPLETE" {
		t.Fatal(exit, v)
	}
	r := v["items"].(map[string]any)
	if r["root"] != m.Store.Root || r["project"] != owner || r["status"] == "PASS" || len(r["tools"].([]any)) != 2 {
		t.Fatal(r)
	}
	for _, args := range [][]string{{"doctor", "--project-status", "--file", "unused"}, {"doctor", "--project-status", "--kind", "profile"}} {
		exit, v, _ = invoke(t, append(args, flags...)...)
		if exit != 2 || v["code"] != "INVALID_INPUT" {
			t.Fatal(exit, v)
		}
	}
}
