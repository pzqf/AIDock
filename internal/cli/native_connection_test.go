package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPConnectionCLIRejectsAmbiguousRequestsAndRequiresExplicitProbe(t *testing.T) {
	root, home := t.TempDir(), filepath.Join(t.TempDir(), "home")
	flags := []string{"--managed-root", filepath.Join(root, ".aidock"), "--native-root", root, "--home", home, "--project", "11111111-1111-4111-8111-111111111111", "--scope", "server", "--copy", "22222222-2222-4222-8222-222222222222", "--json"}
	file := filepath.Join(t.TempDir(), "request.json")
	for _, body := range []string{`{"action":"install","action":"remove"}`, `{"tools":null}`, `{"session":{"Name":"PRIVATE_CLI_SENTINEL"}}`, `{"command":"PRIVATE_CLI_SENTINEL"}`} {
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		exit, out, _ := invoke(t, append([]string{"tool", "mcp", "plan", "--file", file}, flags...)...)
		data, _ := json.Marshal(out)
		if exit != 2 || out["code"] != "INVALID_INPUT" || strings.Contains(string(data), "PRIVATE_CLI_SENTINEL") {
			t.Fatal(exit, out)
		}
	}
	for _, args := range [][]string{{"versions", "--all"}, {"versions", "--tool", "codex-cli"}, {"versions", "--probe", "--all", "--tool", "codex-cli"}, {"apply", "--plan", "invalid", "--digest", "invalid"}, {"rollback-apply", "--plan", "invalid", "--digest", "invalid"}, {"rollback-plan", "--transaction", "invalid"}} {
		exit, out, _ := invoke(t, append(append([]string{"tool", "mcp"}, args...), flags...)...)
		if exit != 2 {
			t.Fatal(args, exit, out)
		}
	}
	exit, out, _ := invoke(t, append([]string{"tool", "mcp", "versions"}, flags...)...)
	if exit != 0 || out["items"] != nil {
		t.Fatal(exit, out)
	}
	if _, err := os.Stat(filepath.Join(root, ".mcp.json")); !os.IsNotExist(err) {
		t.Fatal("negative requests wrote native file", err)
	}
}
