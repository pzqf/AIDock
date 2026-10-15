package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeConfigurationCLIReadOnly(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(t.TempDir(), "not-created")
	flags := []string{"--managed-root", filepath.Join(root, ".aidock"), "--native-root", root, "--home", home, "--project", "11111111-1111-4111-8111-111111111111", "--scope", "server", "--json"}
	original := []byte(`{"mcpServers":{"custom":{"env":{"SECRET":"DO_NOT_DISPLAY"}}}}`)
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), original, 0644); err != nil {
		t.Fatal(err)
	}
	exit, v, _ := invoke(t, append([]string{"tool", "config", "--all"}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "DO_NOT_DISPLAY") {
		t.Fatal("exposed value")
	}
	items := v["items"].(map[string]any)
	files := items["files"].([]any)
	if len(files) != 2 || files[0].(map[string]any)["state"] != "READABLE" || files[1].(map[string]any)["state"] != "ABSENT" {
		t.Fatal(items)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("created home during read", err)
	}
	read, _ := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if string(read) != string(original) {
		t.Fatal("configuration changed")
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"SECRET":"DO_NOT_DISPLAY",`), 0644); err != nil {
		t.Fatal(err)
	}
	exit, v, _ = invoke(t, append([]string{"tool", "config", "--all"}, flags...)...)
	if exit != 5 || v["code"] != "STATIC_CHECK_FAILED" {
		t.Fatal(exit, v)
	}
	b, _ = json.Marshal(v)
	if strings.Contains(string(b), "DO_NOT_DISPLAY") {
		t.Fatal("parser exposed value")
	}
	for _, args := range [][]string{{"tool", "config"}, {"tool", "config", "--all", "--tool", "codex-cli"}} {
		exit, v, _ = invoke(t, append(args, flags...)...)
		if exit != 2 {
			t.Fatal(exit, v)
		}
	}
}
