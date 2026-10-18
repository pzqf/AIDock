package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestToolEvidenceCLIRequiresBindingAndObservesWithoutSavingCheck(t *testing.T) {
	home := filepath.Join(t.TempDir(), "uncreated")
	exit, result, _ := invoke(t, "tool", "evidence", "--home", home, "--json")
	if exit != 4 || result["code"] != "SOURCE_BINDING_REQUIRED" {
		t.Fatal(exit, result)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("unbound evidence created a home", err)
	}
	root := t.TempDir()
	home = t.TempDir()
	owner := "11111111-1111-4111-8111-111111111111"
	m, err := source.New(filepath.Join(root, ".aidock"), filepath.Join(home, "local"), owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "guide", Description: "evidence fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	before, err := m.Store.Capture(root)
	if err != nil {
		t.Fatal(err)
	}
	flags := []string{"--managed-root", m.Store.Root, "--home", home, "--project", owner, "--scope", "server", "--native-root", root, "--json"}
	exit, result, _ = invoke(t, append([]string{"tool", "evidence"}, flags...)...)
	if exit != 0 || result["ok"] != true {
		t.Fatal(exit, result)
	}
	items := result["items"].(map[string]any)
	current := items["current"].(map[string]any)
	if current["owner"] != owner || current["scope_id"] != "server" || current["probe"] != false || items["last"] != nil || items["application_status"] != "COPY_BINDING_REQUIRED" {
		t.Fatal(items)
	}
	after, err := m.Store.Capture(root)
	if err != nil || workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("read-only CLI changed project bytes", err)
	}
}
