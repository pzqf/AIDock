package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestCLIGovernancePreflightAndDoctor(t *testing.T) {
	d := t.TempDir()
	root, home := filepath.Join(d, ".aidock"), filepath.Join(d, "data")
	owner := "11111111-1111-4111-8111-111111111111"
	m, e := source.New(root, filepath.Join(home, "local"), owner, "server")
	if e != nil {
		t.Fatal(e)
	}
	p, e := m.Plan(context.Background(), source.Request{Action: "create", Name: "cli-governed", Description: "example"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Apply(context.Background(), p.Plan.ID); e != nil {
		t.Fatal(e)
	}
	flags := []string{"--managed-root", root, "--home", home, "--project", owner, "--scope", "server", "--json"}
	for _, x := range []struct {
		platform string
		exit     int
		code     string
	}{{runtime.GOOS, 0, "OK"}, {"unavailable-platform", 5, "STATIC_CHECK_FAILED"}} {
		state, e := m.State()
		if e != nil {
			t.Fatal(e)
		}
		s := state.Index.Skills[0]
		req := source.Request{Action: "governance", ID: s.ID, Revision: s.Revision, Governance: &source.Governance{Dependencies: map[string]any{}, Compatibility: map[string]any{"platforms": []string{x.platform}}, Requirements: map[string]any{}}}
		b, _ := json.Marshal(req)
		file := filepath.Join(d, "request.json")
		if e = os.WriteFile(file, b, 0644); e != nil {
			t.Fatal(e)
		}
		exit, v, _ := invoke(t, append([]string{"skill", "plan", "governance", "--file", file}, flags...)...)
		if exit != 0 {
			t.Fatal(exit, v)
		}
		plan := v["items"].(map[string]any)["plan"].(map[string]any)
		exit, v, _ = invoke(t, append([]string{"skill", "apply", "--plan", plan["id"].(string), "--expected-digest", plan["digest"].(string)}, flags...)...)
		if exit != 0 {
			t.Fatal(exit, v)
		}
		before, _ := m.Store.Capture(root)
		exit, v, _ = invoke(t, append([]string{"skill", "check"}, flags...)...)
		if exit != x.exit || v["code"] != x.code {
			t.Fatal(exit, v)
		}
		if v["items"].(map[string]any)["native_ready"] != false {
			t.Fatal("overstated native readiness")
		}
		after, _ := m.Store.Capture(root)
		if workstore.Digest(before) != workstore.Digest(after) {
			t.Fatal("check wrote source")
		}
		exit, v, _ = invoke(t, append([]string{"doctor"}, flags...)...)
		if v["items"].(map[string]any)["source_preflight"] == nil || exit == 0 {
			t.Fatal(exit, v)
		}
	}
	state, _ := m.State()
	s := state.Index.Skills[0]
	req := source.Request{Action: "governance", ID: s.ID, Revision: s.Revision, Governance: &source.Governance{Dependencies: map[string]any{}, Compatibility: map[string]any{"tools": map[string]string{"codex-cli": ">=1.0.0"}}, Requirements: map[string]any{}}}
	p, e = m.Plan(context.Background(), req)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Apply(context.Background(), p.Plan.ID); e != nil {
		t.Fatal(e)
	}
	exit, v, _ := invoke(t, append([]string{"skill", "check"}, flags...)...)
	if exit != 3 || v["code"] != "EVIDENCE_INCOMPLETE" {
		t.Fatal(exit, v)
	}
}
