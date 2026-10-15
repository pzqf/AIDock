package native

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func configurationTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			out[path] = "directory"
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[path] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestConfigurationReadOnlyAndValueFree(t *testing.T) {
	root := t.TempDir()
	local := t.TempDir()
	s, err := source.New(filepath.Join(root, ".aidock"), local, owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(s, root)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, ".mcp.json"), []byte(`{"model":"MODEL_SECRET","mcpServers":{"personal":{"command":"not-executed","env":{"TOKEN":"JSON_SECRET"}},"aidock-old":{"command":"not-executed"}}}`))
	write(t, filepath.Join(root, ".codex/config.toml"), []byte("# retain comment\nmodel='MODEL_SECRET'\n[mcp_servers.personal]\ncommand='not-executed'\nenv={TOKEN='TOML_SECRET'}\n"))
	beforeRoot, beforeLocal := configurationTree(t, root), configurationTree(t, local)
	report, err := m.Configuration(context.Background(), []string{"codex-cli", "claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Root != root || report.Project != owner || report.Status != "OBSERVED" || len(report.Files) != 2 {
		t.Fatalf("%+v", report)
	}
	for _, file := range report.Files {
		if file.State != "READABLE" || file.WriteSupported || file.SHA256 == "" {
			t.Fatalf("%+v", file)
		}
		for _, field := range file.Fields {
			if field.Ownership != "UNMANAGED" {
				t.Fatal(field)
			}
		}
	}
	if !report.Files[0].Fields[0].PrefixOnly || report.Files[0].Fields[0].Name != "aidock-old" {
		t.Fatal("prefix must not confer ownership")
	}
	encoded, _ := json.Marshal(report)
	for _, secret := range []string{"MODEL_SECRET", "JSON_SECRET", "TOML_SECRET", "not-executed"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("report disclosed %s", secret)
		}
	}
	if !reflect.DeepEqual(beforeRoot, configurationTree(t, root)) || !reflect.DeepEqual(beforeLocal, configurationTree(t, local)) {
		t.Fatal("read changed project or local root")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = m.Configuration(ctx, []string{"codex-cli"}); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestConfigurationReportsBothToolsAndRequiresExplicitSelection(t *testing.T) {
	m, _ := fixture(t)
	_, err := m.Configuration(context.Background(), nil)
	code(t, err, "TOOL_REQUIRED")
	_, err = m.Configuration(context.Background(), []string{"codex-cli", "codex-cli"})
	code(t, err, "INVALID_INPUT")
	r, err := m.Configuration(context.Background(), []string{"claude-code", "codex-cli"})
	if err != nil || r.Files[0].State != "ABSENT" || r.Files[1].State != "ABSENT" {
		t.Fatalf("%+v %v", r, err)
	}
	write(t, filepath.Join(m.Root, ".mcp.json"), []byte(`{"private":"secret","private":"duplicate"}`))
	write(t, filepath.Join(m.Root, ".codex/config.toml"), []byte("model='ok'\n"))
	r, err = m.Configuration(context.Background(), []string{"claude-code", "codex-cli"})
	if err != nil || r.Status != "FAIL" || r.Files[0].Checks[0].Code != "CONFIG_DUPLICATE_KEY" || r.Files[1].State != "READABLE" {
		t.Fatalf("%+v %v", r, err)
	}
	if err = os.Remove(filepath.Join(m.Root, ".mcp.json")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(m.Root, ".mcp.json"), 0755); err != nil {
		t.Fatal(err)
	}
	r, err = m.Configuration(context.Background(), []string{"claude-code"})
	if err != nil || r.Files[0].Checks[0].Code != "CONFIG_PATH_UNSAFE" {
		t.Fatalf("%+v %v", r, err)
	}
}
