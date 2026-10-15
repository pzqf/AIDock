package console

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func TestMCPLockAPIAuthenticationMethodsStrictInputAndScopedConfirmation(t *testing.T) {
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
	w := request(s, "GET", "/api/mcp/lock/state", "", c, "", "")
	var state struct {
		OK    bool
		Items source.MCPLockState
	}
	if err = json.Unmarshal(w.Body.Bytes(), &state); err != nil || !state.OK {
		t.Fatal(w.Body.String(), err)
	}
	b, _ := json.Marshal(source.MCPLockEdit{ExpectedSHA256: state.Items.SHA256, Document: state.Items.Document})
	for _, x := range []struct {
		cookie        *http.Cookie
		origin, token string
		status        int
	}{{nil, origin, csrf, 401}, {c, origin, "", 401}, {c, "https://evil.invalid", csrf, 403}} {
		for _, path := range []string{"/api/mcp/lock/inspect", "/api/mcp/lock/plan", "/api/mcp/lock/apply"} {
			w = request(s, "POST", path, string(b), x.cookie, x.origin, x.token)
			if w.Code != x.status {
				t.Fatal(path, w.Code, w.Body.String())
			}
		}
	}
	for _, x := range []struct{ method, path string }{{"POST", "/api/mcp/lock/state"}, {"GET", "/api/mcp/lock/inspect"}, {"GET", "/api/mcp/lock/plan"}, {"GET", "/api/mcp/lock/apply"}} {
		w = request(s, x.method, x.path, "{}", c, origin, csrf)
		if w.Code != 405 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/mcp/lock/inspect", "/api/mcp/lock/plan", "/api/mcp/lock/apply"} {
		w = request(s, "POST", path, `{"command":"must-not-run"}`, c, origin, csrf)
		var bad struct {
			OK   bool
			Code string
		}
		json.Unmarshal(w.Body.Bytes(), &bad)
		if bad.OK || bad.Code != "INVALID_INPUT" {
			t.Fatal(w.Body.String())
		}
	}
	w = request(s, "POST", "/api/mcp/lock/plan", string(b), c, origin, csrf)
	var preview struct {
		OK    bool
		Items source.MCPLockPreview
	}
	if err = json.Unmarshal(w.Body.Bytes(), &preview); err != nil || !preview.OK {
		t.Fatal(w.Body.String(), err)
	}
	if _, err = os.Stat(filepath.Join(m.Store.Root, "plugins.lock")); !os.IsNotExist(err) {
		t.Fatal("preview saved source")
	}
	for _, plan := range []map[string]string{{"plan_id": preview.Items.Plan.ID}, {"plan_id": p.Plan.ID, "digest": p.Plan.Digest}} {
		b, _ = json.Marshal(plan)
		w = request(s, "POST", "/api/mcp/lock/apply", string(b), c, origin, csrf)
		var bad struct {
			OK   bool
			Code string
		}
		json.Unmarshal(w.Body.Bytes(), &bad)
		if bad.OK || bad.Code != "INVALID_INPUT" {
			t.Fatal(w.Body.String())
		}
	}
	b, _ = json.Marshal(map[string]string{"plan_id": preview.Items.Plan.ID, "digest": preview.Items.Plan.Digest})
	w = request(s, "POST", "/api/mcp/lock/apply", string(b), c, origin, csrf)
	var saved struct{ OK bool }
	if err = json.Unmarshal(w.Body.Bytes(), &saved); err != nil || !saved.OK {
		t.Fatal(w.Body.String(), err)
	}
	stateNow, err := m.MCPLock(context.Background())
	if err != nil || !stateNow.Present || stateNow.ExecutionReady {
		t.Fatal(stateNow, err)
	}
}
