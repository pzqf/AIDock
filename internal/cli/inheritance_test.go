package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func TestCLIExplicitGlobalAndReadOnlyLibraryBindings(t *testing.T) {
	d := t.TempDir()
	globalRoot, home := filepath.Join(d, "global"), filepath.Join(d, "global-data")
	l, e := source.NewGlobal(globalRoot, filepath.Join(home, "local"))
	if e != nil {
		t.Fatal(e)
	}
	p, e := l.Plan(context.Background(), source.Request{Action: "create", Name: "cli-guide", Description: "example"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = l.Apply(context.Background(), p.Plan.ID); e != nil {
		t.Fatal(e)
	}
	exit, v, _ := invoke(t, "skill", "managed-list", "--global-root", globalRoot, "--home", home, "--json")
	if exit != 0 || v["items"].(map[string]any)["index"].(map[string]any)["owner"] != "global" {
		t.Fatal(exit, v)
	}
	s, e := l.State()
	if e != nil {
		t.Fatal(e)
	}
	id, rev := s.Index.Skills[0].ID, s.Index.Skills[0].Revision
	descriptor := filepath.Join(d, "inherit.json")
	b, _ := json.Marshal(source.Request{Action: "inherit", ID: id, Revision: rev})
	if e = os.WriteFile(descriptor, b, 0644); e != nil {
		t.Fatal(e)
	}
	flags := []string{"--managed-root", filepath.Join(d, "project", ".aidock"), "--project", "11111111-1111-4111-8111-111111111111", "--scope", "server", "--home", filepath.Join(d, "project-data"), "--library-root", globalRoot, "--json"}
	if e = os.MkdirAll(filepath.Join(d, "project"), 0755); e != nil {
		t.Fatal(e)
	}
	exit, v, _ = invoke(t, append([]string{"skill", "plan", "inherit", "--file", descriptor}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	plan := v["items"].(map[string]any)["plan"].(map[string]any)
	exit, v, _ = invoke(t, append([]string{"skill", "apply", "--plan", plan["id"].(string), "--expected-digest", plan["digest"].(string)}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	for _, args := range [][]string{{"--global-root", globalRoot}, {"--library-root", globalRoot, "--home", home}, {"--global-root", globalRoot, "--managed-root", filepath.Join(d, ".aidock"), "--home", home}, {"--global-root", globalRoot, "--project", id, "--home", home}} {
		exit, v, _ = invoke(t, append([]string{"skill", "managed-list", "--json"}, args...)...)
		if exit == 0 {
			t.Fatal("unsafe binding accepted", v)
		}
	}
}
