package console

import (
	"context"
	"encoding/json"
	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/source"
)

func TestMCPPermissionAPIAuthenticationMethodsInputAndBinding(t *testing.T) {
	s, c, csrf := sourceServer(t)
	origin := "http://" + s.address
	m, err := s.engine.Sources()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p, err := m.Plan(ctx, source.Request{Action: "create", Name: "guide", Description: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct {
		cookie       *http.Cookie
		origin, csrf string
		status       int
	}{{nil, origin, csrf, 401}, {c, origin, "", 401}, {c, "https://evil.invalid", csrf, 403}} {
		for _, path := range []string{"plan", "apply", "recover"} {
			w := request(s, "POST", "/api/mcp/permission/"+path, "{}", x.cookie, x.origin, x.csrf)
			if w.Code != x.status {
				t.Fatal(path, w.Code, w.Body.String())
			}
		}
	}
	for _, path := range []string{"state", "plan", "apply", "recover"} {
		method := "GET"
		if path == "state" {
			method = "POST"
		}
		w := request(s, method, "/api/mcp/permission/"+path, "{}", c, origin, csrf)
		if w.Code != 405 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"plan", "apply", "recover"} {
		w := request(s, "POST", "/api/mcp/permission/"+path, `{"execute":true}`, c, origin, csrf)
		var out struct {
			OK   bool
			Code string
		}
		if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.OK || out.Code != "INVALID_INPUT" {
			t.Fatal(w.Body.String(), err)
		}
	}
	w := request(s, "GET", "/api/mcp/permission/state", "", c, "", "")
	var state struct {
		OK    bool
		Items pluginpermission.State
	}
	if err = json.Unmarshal(w.Body.Bytes(), &state); err != nil || !state.OK || state.Items.ExecutionReady || len(state.Items.Authorizations) != 0 {
		t.Fatal(w.Body.String(), err)
	}
	for _, path := range []string{"state"} {
		w = request(s, "GET", "/api/mcp/permission/"+path, "", nil, "", "")
		if w.Code != 401 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var out struct {
		OK   bool
		Code string
	}
	w = bindingRequest(s, c, csrf, "not-registered", "GET", "/api/mcp/permission/state", nil)
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.OK || out.Code != "INVALID_INPUT" {
		t.Fatal(w.Body.String())
	}
	w = request(s, "GET", "/mcp-permission-view.js", "", c, "", "")
	if w.Code != 200 {
		t.Fatal("install UI unavailable", w.Code)
	}
}

func TestMCPPermissionAPIGrantRevokeAndExplicitRestore(t *testing.T) {
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
	manifest, _ := json.Marshal(map[string]any{"schema_version": 1, "name": "permission-fixture", "type": "mcp-server", "version": "1.2.3", "source": map[string]any{"type": "local", "path": "unused"}, "permissions": conf["servers"].([]any)[0].(map[string]any)["permissions"], "dependencies": []any{}})
	if err = os.WriteFile(filepath.Join(root, "plugin.yaml"), manifest, 0644); err != nil {
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

	installed, err := m.MCPInstall(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ip, err := m.MCPInstallPlan(ctx, plugininstall.Edit{Action: "install", Name: "fixture", ExpectedSHA256: installed.SHA256, AllowUnverified: true, RiskReason: "isolated API test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPInstall(ctx, ip.Plan.ID, ip.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, input any) json.RawMessage {
		t.Helper()
		var text string
		if input != nil {
			b, _ := json.Marshal(input)
			text = string(b)
		}
		w := request(s, method, "/api/mcp/permission/"+path, text, c, origin, csrf)
		var v struct {
			OK    bool
			Items json.RawMessage
		}
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || !v.OK {
			t.Fatal(w.Code, w.Body.String(), err)
		}
		return v.Items
	}
	state := func() pluginpermission.State {
		t.Helper()
		var v pluginpermission.State
		json.Unmarshal(call("GET", "state", nil), &v)
		return v
	}
	plan := func(r pluginpermission.Edit) pluginpermission.Preview {
		t.Helper()
		v := state()
		r.ExpectedSHA256 = v.SHA256
		if r.Action != "revoke" {
			r.ExpectedReviewSHA256 = v.ReviewSHA256
		}
		var p pluginpermission.Preview
		json.Unmarshal(call("POST", "plan", r), &p)
		return p
	}
	apply := func(p pluginpermission.Preview) {
		t.Helper()
		call("POST", "apply", map[string]string{"plan_id": p.Plan.ID, "digest": p.Plan.Digest})
	}
	gp := plan(pluginpermission.Edit{Action: "grant", Name: "fixture", ConfirmPermissions: true, Purpose: "API fixture only", DataLevel: "Public", Destination: "unknown-external-client"})
	if len(state().Authorizations) > 0 {
		t.Fatal("preview wrote authorization")
	}
	apply(gp)
	if v := state(); len(v.Authorizations) != 1 || v.Authorizations[0].Status != "CURRENT" || v.ExecutionReady {
		t.Fatal(v)
	}
	revoked := plan(pluginpermission.Edit{Action: "revoke", Name: "fixture"})
	apply(revoked)
	v := state()
	r := pluginpermission.Edit{Action: "rollback", Snapshot: revoked.Plan.ID, ExpectedSHA256: v.SHA256, ExpectedReviewSHA256: v.ReviewSHA256}
	b, _ := json.Marshal(r)
	w := request(s, "POST", "/api/mcp/permission/plan", string(b), c, origin, csrf)
	var bad struct {
		OK   bool
		Code string
	}
	json.Unmarshal(w.Body.Bytes(), &bad)
	if bad.OK || bad.Code != "AUTHORIZATION_CONFIRMATION_REQUIRED" {
		t.Fatal(w.Body.String())
	}
	r.ConfirmPermissions = true
	r.Purpose = "explicit restoration review"
	r.DataLevel = "Public"
	r.Destination = "unknown-external-client"
	apply(plan(r))
	call("POST", "recover", map[string]string{})
	if v := state(); len(v.Authorizations) != 1 || v.AuditCount != 3 {
		t.Fatal(v)
	}
}
