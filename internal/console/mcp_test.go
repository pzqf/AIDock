package console

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func TestMCPAPISecurityConfirmationAndNoLaunch(t *testing.T) {
	s, c, csrf := sourceServer(t)
	origin := "http://" + s.address
	m, err := s.engine.Sources()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "guide", Description: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	w := request(s, "GET", "/api/mcp/state", "", c, "", "")
	var state struct {
		OK    bool
		Items source.MCPState
	}
	if err = json.Unmarshal(w.Body.Bytes(), &state); err != nil || !state.OK {
		t.Fatal(w.Body.String(), err)
	}
	b, _ := json.Marshal(source.MCPEdit{ExpectedSHA256: state.Items.SHA256, Config: state.Items.Config})
	for _, x := range []struct {
		cookie        *http.Cookie
		origin, token string
		code          int
	}{{nil, origin, csrf, 401}, {c, origin, "", 401}, {c, "https://evil.invalid", csrf, 403}} {
		w = request(s, "POST", "/api/mcp/plan", string(b), x.cookie, x.origin, x.token)
		if w.Code != x.code {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, x := range []struct{ method, path string }{{"POST", "/api/mcp/state"}, {"GET", "/api/mcp/plan"}, {"GET", "/api/mcp/apply"}} {
		if w = request(s, x.method, x.path, "{}", c, origin, csrf); w.Code != 405 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w = request(s, "POST", "/api/mcp/plan", `{"expected_sha256":"x","config":{},"command":"evil"}`, c, origin, csrf)
	var bad struct {
		OK   bool
		Code string
	}
	json.Unmarshal(w.Body.Bytes(), &bad)
	if bad.OK || bad.Code != "INVALID_INPUT" {
		t.Fatal(w.Body.String())
	}
	w = request(s, "POST", "/api/mcp/plan", string(b), c, origin, csrf)
	var out struct {
		OK    bool
		Items source.MCPPreview
	}
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.OK {
		t.Fatal(w.Body.String(), err)
	}
	after, _ := m.MCP()
	if after.Present {
		t.Fatal("API preview saved definition")
	}
	confirm, _ := json.Marshal(map[string]string{"plan_id": out.Items.Plan.ID, "digest": out.Items.Plan.Digest})
	w = request(s, "POST", "/api/mcp/apply", string(confirm), c, origin, csrf)
	var saved struct{ OK bool }
	if err = json.Unmarshal(w.Body.Bytes(), &saved); err != nil || !saved.OK {
		t.Fatal(w.Body.String(), err)
	}
	after, err = m.MCP()
	if err != nil || !after.Present || after.ExecutionReady {
		t.Fatal(after, err)
	}
	// The MCP-specific confirmation rejects unrelated source operations.
	confirm, _ = json.Marshal(map[string]string{"plan_id": p.Plan.ID, "digest": p.Plan.Digest})
	w = request(s, "POST", "/api/mcp/apply", string(confirm), c, origin, csrf)
	json.Unmarshal(w.Body.Bytes(), &bad)
	if bad.OK || bad.Code != "INVALID_INPUT" {
		t.Fatal(w.Body.String())
	}
}
