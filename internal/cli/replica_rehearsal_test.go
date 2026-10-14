package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestReplicaCLIResolveRehearseRollbackAndUndo(t *testing.T) {
	ctx := context.Background()
	owner, skill := workstore.ID(), workstore.ID()
	home, parent := t.TempDir(), t.TempDir()
	roots := []string{filepath.Join(parent, "one", ".aidock"), filepath.Join(parent, "two", ".aidock")}
	sources := []*source.Manager{}
	for _, root := range roots {
		if err := os.MkdirAll(filepath.Dir(root), 0755); err != nil {
			t.Fatal(err)
		}
		m, err := source.New(root, filepath.Join(home, "local"), owner, "server")
		if err != nil {
			t.Fatal(err)
		}
		p, err := m.Plan(ctx, source.Request{Action: "create", ID: skill, Name: "guide", Description: "CLI fixture"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = m.Apply(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
			t.Fatal(err)
		}
		p, err = m.Plan(ctx, source.Request{Action: "enable", ID: skill})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = m.Apply(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, m)
	}
	args := []string{"--managed-root", roots[0], "--home", home, "--project", owner, "--scope", "server", "--json"}
	run := func(cmd ...string) map[string]any {
		t.Helper()
		exit, v, stderr := invoke(t, append(cmd, args...)...)
		if exit != 0 {
			t.Fatal(exit, v, stderr)
		}
		return v
	}
	file := filepath.Join(t.TempDir(), "request.json")
	ids := []string{}
	for i, root := range roots {
		bytes, _ := json.Marshal(replica.Binding{Name: []string{"one", "two"}[i], Root: root})
		if err := os.WriteFile(file, bytes, 0600); err != nil {
			t.Fatal(err)
		}
		p := run("replica", "register", "--file", file)["items"].(map[string]any)
		ids = append(ids, p["inputs"].(map[string]any)["binding_id"].(string))
		run("replica", "confirm", "--plan", p["id"].(string), "--digest", p["digest"].(string))
	}
	p, err := sources[0].Plan(ctx, source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "SKILL.md", Content: "---\nname: guide\ndescription: CLI changed\n---\nnew body\n"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sources[0].Apply(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	before := []string{}
	for _, s := range sources {
		tree, _ := s.Store.Capture(s.Store.Root)
		before = append(before, workstore.Digest(tree))
	}
	report := run("replica", "compare", "--from", ids[0], "--to", ids[1])["items"].(map[string]any)
	body, _ := json.Marshal(replica.ResolveRequest{Request: replica.Request{From: ids[0], Targets: []string{ids[1]}}, Comparison: report["digest"].(string), Decisions: []replica.Decision{{Target: ids[1], Entry: skill, Choice: "source"}}})
	if err = os.WriteFile(file, body, 0600); err != nil {
		t.Fatal(err)
	}
	resolution := run("replica", "resolve", "--file", file)["items"].(map[string]any)
	r := run("replica", "rehearsal", "run", "--plan", resolution["id"].(string), "--digest", resolution["digest"].(string))["items"].(map[string]any)
	for _, want := range []string{"ROLLED_BACK", "UNDONE"} {
		transactions := r["transactions"].([]any)
		reverse := run("replica", "rehearsal", "plan", "--session", r["id"].(string), "--transaction", transactions[0].(map[string]any)["id"].(string))["items"].(map[string]any)
		r = run("replica", "rehearsal", "apply", "--session", r["id"].(string), "--plan", reverse["id"].(string), "--digest", reverse["digest"].(string))["items"].(map[string]any)
		if r["status"] != want {
			t.Fatal(r)
		}
	}
	run("replica", "rehearsal", "recover", "--session", r["id"].(string))
	if run("replica", "rehearsal", "show", "--session", r["id"].(string))["items"].(map[string]any)["status"] != "UNDONE" {
		t.Fatal("CLI session reopen failed")
	}
	history := run("replica", "rehearsal", "history")["items"].([]any)
	if len(history) != 1 || history[0].(map[string]any)["coordination"] != "fixed-roots" {
		t.Fatal("CLI omitted fixed-root session history")
	}
	if run("replica", "rehearsal", "latest")["items"].(map[string]any)["status"] != "UNDONE" {
		t.Fatal("lost CLI history")
	}
	g := run("replica", "generation", "stage", "--plan", resolution["id"].(string), "--digest", resolution["digest"].(string), "--tools", "codex-cli")["items"].(map[string]any)
	if g["blocked"].(bool) {
		t.Fatal(g)
	}
	run("replica", "generation", "show", "--plan", g["id"].(string), "--digest", g["digest"].(string))
	r = run("replica", "rehearsal", "run-generated", "--plan", g["id"].(string), "--digest", g["digest"].(string))["items"].(map[string]any)
	if len(r["bound_roots"].([]any)) != 3 || r["generation"] == nil {
		t.Fatal("CLI omitted generated root", r)
	}
	for _, want := range []string{"ROLLED_BACK", "UNDONE"} {
		transaction := r["transactions"].([]any)[0].(map[string]any)
		reverse := run("replica", "rehearsal", "plan", "--session", r["id"].(string), "--transaction", transaction["id"].(string))["items"].(map[string]any)
		r = run("replica", "rehearsal", "apply", "--session", r["id"].(string), "--plan", reverse["id"].(string), "--digest", reverse["digest"].(string))["items"].(map[string]any)
		if r["status"] != want {
			t.Fatal(r)
		}
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(roots[1]), ".agents")); !os.IsNotExist(err) {
		t.Fatal("CLI wrote original generated root", err)
	}
	for i, s := range sources {
		tree, _ := s.Store.Capture(s.Store.Root)
		if workstore.Digest(tree) != before[i] {
			t.Fatal("CLI changed observed project")
		}
	}
}
