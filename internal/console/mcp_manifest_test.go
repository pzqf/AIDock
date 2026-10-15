package console

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func TestMCPManifestAPIIsAuthenticatedReadOnlyAndProjectBound(t *testing.T) {
	s, c, csrf := sourceServer(t)
	origin := "http://" + s.address
	m, err := s.engine.Sources()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "guide", Description: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	w := request(s, "GET", "/api/mcp/manifest/state", "", nil, "", "")
	if w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(s, "POST", "/api/mcp/manifest/state", "{}", c, origin, csrf)
	if w.Code != 405 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(s, "GET", "/api/mcp/manifest/state", "", c, "", "")
	var out struct {
		OK    bool
		Items source.MCPManifestState
	}
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.OK || out.Items.Status != "UNKNOWN" || out.Items.ExecutionReady || len(out.Items.Plugins) != 0 {
		t.Fatal(w.Body.String(), err)
	}
	w = request(s, "GET", "/api/mcp/manifest/state?name=unlocked", "", c, "", "")
	var failed struct {
		OK   bool
		Code string
	}
	json.Unmarshal(w.Body.Bytes(), &failed)
	if failed.OK || failed.Code != "MCP_LOCK_ENTRY_MISSING" {
		t.Fatal(w.Body.String())
	}
	w = bindingRequest(s, c, csrf, "not-registered", "GET", "/api/mcp/manifest/state", nil)
	json.Unmarshal(w.Body.Bytes(), &failed)
	if failed.OK || failed.Code != "INVALID_INPUT" {
		t.Fatal("unknown project binding accepted", w.Body.String())
	}
	w = request(s, "GET", "/mcp-manifest-view.js", "", c, "", "")
	if w.Code != 200 {
		t.Fatal("manifest UI unavailable", w.Code)
	}
}
