package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuditCLINeedsExplicitBindingAndValidQuery(t *testing.T) {
	exit, v, _ := invoke(t, "audit", "--json")
	if exit != 4 || v["code"] != "SOURCE_BINDING_REQUIRED" {
		t.Fatal(exit, v)
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "local"), 0700); err != nil {
		t.Fatal(err)
	}
	flags := []string{"--managed-root", filepath.Join(t.TempDir(), ".aidock"), "--home", home, "--project", "11111111-1111-4111-8111-111111111111", "--scope", "server", "--json"}
	exit, v, _ = invoke(t, append([]string{"audit"}, flags...)...)
	if exit != 0 || v["items"].(map[string]any)["total"] != float64(0) {
		t.Fatal(exit, v)
	}
	for _, args := range [][]string{{"audit", "--category", "unknown"}, {"audit", "--page", "-1"}} {
		exit, v, _ = invoke(t, append(args, flags...)...)
		if exit != 2 || v["code"] != "INVALID_INPUT" {
			t.Fatal(exit, v)
		}
	}
}
