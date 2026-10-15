package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMCPRelayStaticDoesNotCreateHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "never-created")
	exit, v, _ := invoke(t, "mcp", "relay-check", "--home", home, "--json")
	if exit != 0 || v["items"].(map[string]any)["status"] != "NOT_RUN" || v["items"].(map[string]any)["execution_ready"] != false {
		t.Fatal(exit, v)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("static relay check created home", err)
	}
	for _, args := range [][]string{{"mcp", "relay-check", "--command", "never-run", "--json"}, {"mcp", "relay-check", "extra", "--json"}} {
		if exit, _, _ := invoke(t, args...); exit == 0 {
			t.Fatal("accepted runtime input")
		}
	}
}
