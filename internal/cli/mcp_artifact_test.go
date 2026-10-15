package cli

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginartifact"
	"github.com/pzqf/AIDock/internal/pluginlock"
)

func TestMCPArtifactCLIImportRollbackUndoAndRecoveryPath(t *testing.T) {
	m, flags := mcpCLIArtifactFixture(t)
	ctx := context.Background()
	lock, err := m.MCPLock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := pluginlock.Parse([]byte(lock.Document))
	if err != nil {
		t.Fatal(err)
	}
	plugin := parsed.Plugins[0]
	root := filepath.Join(m.Store.Local, filepath.FromSlash(plugin.Artifact.Path))
	archive := filepath.Join(filepath.Dir(m.Store.Root), "complete.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for _, e := range plugin.Artifact.Entries {
		h := &zip.FileHeader{Name: e.Path, Method: zip.Deflate}
		h.SetMode(os.FileMode(e.Mode))
		if e.Directory {
			h.Name += "/"
			h.SetMode(os.ModeDir | os.FileMode(e.Mode))
		}
		w, err := z.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if !e.Directory {
			b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(e.Path)))
			if err != nil {
				t.Fatal(err)
			}
			w.Write(b)
		}
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err = os.Rename(root, root+"-fixture-backup"); err != nil {
		t.Fatal(err)
	}
	call := func(args ...string) map[string]any {
		t.Helper()
		exit, v, _ := invoke(t, append(args, flags...)...)
		if exit != 0 {
			t.Fatal(exit, v)
		}
		return v["items"].(map[string]any)
	}
	state := call("mcp", "artifact", "state")
	entry := state["artifacts"].([]any)[0].(map[string]any)
	req := pluginartifact.Edit{Action: "import", Name: plugin.Name, ExpectedSHA256: entry["sha256"].(string), ExpectedLockSHA256: state["lock_sha256"].(string), ArchivePath: archive}
	file := filepath.Join(filepath.Dir(m.Store.Root), "artifact-request.json")
	plan := func(req pluginartifact.Edit) map[string]any {
		b, _ := json.Marshal(req)
		if err := os.WriteFile(file, b, 0600); err != nil {
			t.Fatal(err)
		}
		return call("mcp", "artifact", "plan", "--file", file)["plan"].(map[string]any)
	}
	p := plan(req)
	if _, err = os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("CLI preview wrote target")
	}
	exit, _, _ := invoke(t, append([]string{"mcp", "artifact", "apply", "--name", plugin.Name, "--plan", p["id"].(string)}, flags...)...)
	if exit == 0 {
		t.Fatal("accepted missing digest")
	}
	apply := func(p map[string]any) {
		call("mcp", "artifact", "apply", "--name", plugin.Name, "--plan", p["id"].(string), "--expected-digest", p["digest"].(string))
	}
	apply(p)
	state = call("mcp", "artifact", "state")
	entry = state["artifacts"].([]any)[0].(map[string]any)
	if entry["status"] != "MATCHED" || entry["execution_ready"] != false {
		t.Fatal(entry)
	}
	req.Action = "rollback"
	req.ArchivePath = ""
	req.Snapshot = p["id"].(string)
	req.ExpectedSHA256 = entry["sha256"].(string)
	rolled := plan(req)
	apply(rolled)
	state = call("mcp", "artifact", "state")
	entry = state["artifacts"].([]any)[0].(map[string]any)
	req.ExpectedSHA256 = entry["sha256"].(string)
	req.Snapshot = rolled["id"].(string)
	apply(plan(req))
	call("mcp", "artifact", "recover", "--path", plugin.Artifact.Path)
	exit, _, _ = invoke(t, append([]string{"mcp", "artifact", "recover", "--path", "../outside"}, flags...)...)
	if exit == 0 {
		t.Fatal("accepted unsafe recovery path")
	}
}
