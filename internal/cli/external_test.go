package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/bridge"
	"github.com/pzqf/AIDock/internal/source"
)

func TestCLIExternalAdoptAndOfflineSelect(t *testing.T) {
	d := t.TempDir()
	root, home, external := filepath.Join(d, "project/.aidock"), filepath.Join(d, "home"), filepath.Join(d, "external")
	owner := "11111111-1111-4111-8111-111111111111"
	if err := os.MkdirAll(filepath.Dir(root), 0755); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(external, "skills/guide/SKILL.md")
	if err := os.MkdirAll(filepath.Dir(doc), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(doc, []byte("---\nname: guide\ndescription: fixture\n---\nfirst"), 0644); err != nil {
		t.Fatal(err)
	}
	b := bridge.Registration{ID: "33333333-3333-4333-8333-333333333333", Project: owner, Scope: "server", Copy: "test", Root: external, SourcePath: "skills", SourceRole: "external-source", Format: "native-skill"}
	bridgeFile := filepath.Join(d, "bridge.json")
	data, _ := json.Marshal(bridge.Index{Version: 1, Bridges: []bridge.Registration{b}})
	if err := os.WriteFile(bridgeFile, data, 0644); err != nil {
		t.Fatal(err)
	}
	report, err := bridge.Check(context.Background(), bridge.Index{Version: 1, Bridges: []bridge.Registration{b}})
	if err != nil {
		t.Fatal(err)
	}
	c := report.Bridges[0].Candidates[0]
	req := source.Request{Action: "external-adopt", BridgeID: b.ID, PackagePath: c.Skill.Path, Revision: c.Revision, BridgeRevision: report.Bridges[0].CurrentRevision}
	descriptor := filepath.Join(d, "adopt.json")
	data, _ = json.Marshal(req)
	if err := os.WriteFile(descriptor, data, 0644); err != nil {
		t.Fatal(err)
	}
	flags := []string{"--managed-root", root, "--home", home, "--project", owner, "--scope", "server", "--copy", "test", "--bridge-file", bridgeFile, "--json"}
	exit, v, _ := invoke(t, append([]string{"skill", "plan", "external-adopt", "--file", descriptor}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	p := v["items"].(map[string]any)["plan"].(map[string]any)
	exit, v, _ = invoke(t, append([]string{"skill", "apply", "--plan", p["id"].(string), "--expected-digest", p["digest"].(string)}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, append([]string{"skill", "managed-list"}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	state := v["items"].(map[string]any)
	a := state["external_skills"].([]any)[0].(map[string]any)
	if len(state["index"].(map[string]any)["skills"].([]any)) != 0 {
		t.Fatal("external authority copied to owned index")
	}
	if err := os.Rename(external, external+"-offline"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(bridgeFile); err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(source.Request{Action: "external-select", ID: a["skill"].(map[string]any)["id"].(string), Approval: a["approval_id"].(string)})
	if err := os.WriteFile(descriptor, data, 0644); err != nil {
		t.Fatal(err)
	}
	exit, v, _ = invoke(t, append([]string{"skill", "plan", "external-select", "--file", descriptor}, flags...)...)
	if exit != 0 {
		t.Fatal("offline CLI restore failed", exit, v)
	}
}
