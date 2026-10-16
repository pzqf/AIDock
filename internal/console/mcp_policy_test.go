package console

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/mcppolicy"
	"github.com/pzqf/AIDock/internal/source"
)

func TestMCPPolicyAPITemplateAuthAndOriginalDraftValidation(t *testing.T) {
	s, c, csrf := sourceServer(t)
	origin := "http://" + s.address
	if w := request(s, "GET", "/api/mcp/policy/template", "", nil, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", "/api/mcp/policy/template", "{}", c, origin, csrf); w.Code != 405 {
		t.Fatal(w.Code)
	}
	w := request(s, "GET", "/api/mcp/policy/template", "", c, "", "")
	var template struct {
		OK    bool
		Items mcppolicy.Policy
	}
	if json.Unmarshal(w.Body.Bytes(), &template) != nil || !template.OK || template.Items.Digest() != mcppolicy.Example().Digest() {
		t.Fatal(w.Body.String())
	}
	m, err := s.engine.Sources()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	created, err := m.Plan(ctx, source.Request{Action: "create", Name: "policy-guide", Description: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(ctx, created.Plan.ID, created.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	state, _ := m.MCP()
	conf := map[string]any{"schema_version": 1, "servers": []any{map[string]any{"name": "owned", "source": map[string]any{"type": "local", "path": "owned"}, "entrypoint": "index.js", "transport": "stdio", "tier": 0, "enabled": false, "permissions": map[string]any{"fs": map[string]any{"paths": []any{}, "mode": "ro"}, "net": map[string]any{"allow": []any{}, "default": "deny"}, "exec": false}}}}
	for _, raw := range []string{`{"adapter":"scoped-public-query-v1","tools":[],"tools":[]}`, `{"private-sentinel":true}`} {
		body, _ := json.Marshal(source.MCPEdit{ExpectedSHA256: state.SHA256, Config: conf, ToolPolicyDocuments: map[string]string{"0": raw}})
		w = request(s, "POST", "/api/mcp/plan", string(body), c, origin, csrf)
		var bad struct {
			OK   bool
			Code string
		}
		if json.Unmarshal(w.Body.Bytes(), &bad) != nil || bad.OK || bad.Code != "MCP_TOOL_POLICY_INVALID" || strings.Contains(w.Body.String(), "private-sentinel") {
			t.Fatal(w.Body.String())
		}
	}
	body, _ := json.Marshal(source.MCPEdit{ExpectedSHA256: state.SHA256, Config: conf, ToolPolicyDocuments: map[string]string{"0": string(mustPolicyJSON(t, mcppolicy.Example()))}})
	w = request(s, "POST", "/api/mcp/plan", string(body), c, origin, csrf)
	var preview struct {
		OK    bool
		Items source.MCPPreview
	}
	if json.Unmarshal(w.Body.Bytes(), &preview) != nil || !preview.OK || preview.Items.Candidate.ExecutionReady {
		t.Fatal(w.Body.String())
	}
	current, _ := m.MCP()
	if current.Present {
		t.Fatal("policy preview saved source")
	}
}
func mustPolicyJSON(t *testing.T, p mcppolicy.Policy) []byte {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
