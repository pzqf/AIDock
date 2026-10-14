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

func TestReplicaCLIRegistrationComparisonAndStrictInput(t *testing.T) {
	owner := workstore.ID()
	parent, local := t.TempDir(), t.TempDir()
	root := filepath.Join(parent, ".aidock")
	s, err := source.New(root, local, owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Plan(context.Background(), source.Request{Action: "create", Name: "guide", Description: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Store.Capture(root)
	args := []string{"--managed-root", root, "--home", local, "--project", owner, "--scope", "server", "--json"}
	run := func(cmd ...string) map[string]any {
		t.Helper()
		exit, v, stderr := invoke(t, append(cmd, args...)...)
		if exit != 0 {
			t.Fatal(exit, v, stderr)
		}
		return v
	}
	file := filepath.Join(t.TempDir(), "binding.json")
	for _, raw := range []string{`{"name":"bad","root":"ignored","typo":true}`, `{} {}`} {
		if err = os.WriteFile(file, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		exit, _, _ := invoke(t, append([]string{"replica", "register", "--file", file}, args...)...)
		if exit == 0 {
			t.Fatal("accepted non-strict registration")
		}
	}
	b, _ := json.Marshal(map[string]any{"name": "one", "root": root, "local_excludes": []string{}})
	if err = os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
	v := run("replica", "register", "--file", file)
	plan := v["items"].(map[string]any)
	if len(run("replica", "list")["items"].(map[string]any)["copies"].([]any)) != 0 {
		t.Fatal("preview registered a copy")
	}
	run("replica", "confirm", "--plan", plan["id"].(string), "--digest", plan["digest"].(string))
	if len(run("replica", "list")["items"].(map[string]any)["copies"].([]any)) != 1 {
		t.Fatal("confirmation missing")
	}
	run("replica", "recover")
	after, _ := s.Store.Capture(root)
	if workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("CLI modified project")
	}
}
