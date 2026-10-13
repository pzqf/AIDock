package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/bridge"
)

func TestBridgeCLIAndDoctorShareDriftWithoutInitializingHome(t *testing.T) {
	d := t.TempDir()
	home := filepath.Join(d, "never-created-home")
	exit, out, _ := invoke(t, "skill", "bridge", "check", "--home", home, "--json")
	if exit != 4 || out["code"] != "BRIDGE_BINDING_REQUIRED" {
		t.Fatal(exit, out)
	}
	root := filepath.Join(d, "external")
	if err := os.MkdirAll(filepath.Join(root, "skills/guide"), 0700); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(root, "skills/guide/SKILL.md")
	if err := os.WriteFile(doc, []byte("---\nname: guide\ndescription: fixture\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	idx := bridge.Index{Version: 1, Bridges: []bridge.Registration{{ID: "33333333-3333-4333-8333-333333333333", Project: "11111111-1111-4111-8111-111111111111", Scope: "server", Copy: "test", Root: root, SourcePath: "skills", SourceRole: "external-source", Format: "native-skill"}}}
	file := filepath.Join(d, "bridge.yaml")
	writeIndex := func() {
		t.Helper()
		data, _ := json.Marshal(idx)
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeIndex()
	exit, out, _ = invoke(t, "skill", "bridge", "check", "--file", file, "--home", home, "--json")
	if exit != 3 || out["code"] != "EVIDENCE_INCOMPLETE" {
		t.Fatal(exit, out)
	}
	items := out["items"].(map[string]any)
	baseline := items["bridges"].([]any)[0].(map[string]any)["current_revision"].(string)
	idx.Bridges[0].ApprovedRevision = baseline
	writeIndex()
	exit, out, _ = invoke(t, "skill", "bridge-check", "--bridge-file", file, "--home", home, "--json")
	if exit != 3 || out["items"].(map[string]any)["status"] != "UNKNOWN" {
		t.Fatal("baseline equality promoted unverified generator to PASS", exit, out)
	}
	changed := []byte("---\nname: guide\ndescription: fixture\n---\nexternal change\n")
	if err := os.WriteFile(doc, changed, 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range [][]string{{"skill", "bridge", "check"}, {"doctor"}} {
		args := append(command, "--bridge-file", file, "--home", home, "--json")
		exit, out, _ = invoke(t, args...)
		if exit != 3 || out["code"] != "EXTERNAL_SOURCE_DRIFT" {
			t.Fatal(exit, out)
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("read-only commands initialized home")
	}
	data, err := os.ReadFile(doc)
	if err != nil || string(data) != string(changed) {
		t.Fatal("external edits were overwritten", err)
	}
	if err := os.Remove(doc); err != nil {
		t.Fatal(err)
	}
	exit, out, _ = invoke(t, "skill", "bridge", "check", "--file", file, "--json")
	if exit != 5 || out["code"] != "STATIC_CHECK_FAILED" {
		t.Fatal("missing approved package did not fail", exit, out)
	}
}
