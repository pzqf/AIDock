package console

import (
	"archive/zip"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginartifact"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/source"
)

func TestMCPArtifactAPIAuthenticationStrictInputPreviewImportAndRollback(t *testing.T) {
	s, c, csrf := sourceServer(t)
	origin := "http://" + s.address
	m, err := s.engine.Sources()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p, err := m.Plan(ctx, source.Request{Action: "create", Name: "guide", Description: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/mcp/artifact/plan", "/api/mcp/artifact/apply", "/api/mcp/artifact/recover"} {
		for _, v := range []struct {
			cookie        *http.Cookie
			origin, token string
			status        int
		}{{nil, origin, csrf, 401}, {c, origin, "", 401}, {c, "https://evil.invalid", csrf, 403}} {
			w := request(s, "POST", path, "{}", v.cookie, v.origin, v.token)
			if w.Code != v.status {
				t.Fatal(path, w.Code, w.Body.String())
			}
		}
		w := request(s, "GET", path, "", c, origin, csrf)
		if w.Code != 405 {
			t.Fatal(w.Code, w.Body.String())
		}
		w = request(s, "POST", path, `{"command":"never execute"}`, c, origin, csrf)
		var bad struct {
			OK   bool
			Code string
		}
		json.Unmarshal(w.Body.Bytes(), &bad)
		if bad.OK || bad.Code != "INVALID_INPUT" {
			t.Fatal(w.Body.String())
		}
	}
	conf := map[string]any{"schema_version": 1, "servers": []any{map[string]any{"name": "fixture", "source": map[string]any{"type": "local", "path": "unused"}, "entrypoint": "helper", "transport": "stdio", "tier": 1, "enabled": false, "permissions": map[string]any{"fs": map[string]any{"paths": []any{}, "mode": "ro"}, "net": map[string]any{"allow": []any{}, "default": "deny"}, "exec": false}}}}
	mcp, _ := m.MCP()
	mp, err := m.MCPPlan(ctx, source.MCPEdit{ExpectedSHA256: mcp.SHA256, Config: conf})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCP(ctx, mp.Plan.ID, mp.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(m.Store.Local, "mcp-artifacts", "fixture")
	if err = os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "helper"), []byte("never execute fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	inspection, err := m.InspectMCPLock(ctx, pluginlock.Preparation{Name: "fixture", ArtifactPath: "mcp-artifacts/fixture", Version: "1.2.3", Runtime: pluginlock.Runtime{Kind: "native", Version: "1.2.3", Constraint: "=1.2.3", Executable: "helper"}})
	if err != nil {
		t.Fatal(err)
	}
	lp, err := m.MCPLockPlan(ctx, source.MCPLockEdit{ExpectedSHA256: inspection.ExpectedSHA256, Document: inspection.Document})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPLock(ctx, lp.Plan.ID, lp.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(filepath.Dir(m.Store.Root), "complete.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	w, _ := z.Create("helper")
	w.Write([]byte("never execute fixture"))
	z.Close()
	f.Close()
	if err = os.Rename(root, root+"-fixture-backup"); err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, input any) json.RawMessage {
		t.Helper()
		var text string
		if input != nil {
			b, _ := json.Marshal(input)
			text = string(b)
		}
		w := request(s, method, path, text, c, origin, csrf)
		var value struct {
			OK    bool
			Items json.RawMessage
		}
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil || !value.OK {
			t.Fatal(w.Code, w.Body.String(), err)
		}
		return value.Items
	}
	var state source.MCPArtifactState
	json.Unmarshal(call("GET", "/api/mcp/artifact/state", nil), &state)
	if len(state.Artifacts) != 1 {
		t.Fatal(state)
	}
	req := pluginartifact.Edit{Action: "import", Name: "fixture", ExpectedSHA256: state.Artifacts[0].SHA256, ExpectedLockSHA256: state.LockSHA256, ArchivePath: archive}
	var preview pluginartifact.Preview
	json.Unmarshal(call("POST", "/api/mcp/artifact/plan", req), &preview)
	if _, err = os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("API preview wrote artifact")
	}
	call("POST", "/api/mcp/artifact/apply", map[string]string{"name": "fixture", "plan_id": preview.Plan.ID, "digest": preview.Plan.Digest})
	json.Unmarshal(call("GET", "/api/mcp/artifact/state", nil), &state)
	if state.Artifacts[0].Status != "MATCHED" {
		t.Fatal(state)
	}
	req.Action = "rollback"
	req.ArchivePath = ""
	req.Snapshot = preview.Plan.ID
	req.ExpectedSHA256 = state.Artifacts[0].SHA256
	json.Unmarshal(call("POST", "/api/mcp/artifact/plan", req), &preview)
	call("POST", "/api/mcp/artifact/apply", map[string]string{"name": "fixture", "plan_id": preview.Plan.ID, "digest": preview.Plan.Digest})
	call("POST", "/api/mcp/artifact/recover", map[string]string{"artifact_path": "mcp-artifacts/fixture"})
}
