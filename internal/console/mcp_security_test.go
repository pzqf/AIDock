package console

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginsecurity"
	"github.com/pzqf/AIDock/internal/source"
)

func TestMCPSecurityAPIAuthMethodsStrictInputRecoveryAndBinding(t *testing.T) {
	s, c, csrf := sourceServer(t)
	origin := "http://" + s.address
	m, _ := s.engine.Sources()
	ctx := context.Background()
	p, err := m.Plan(ctx, source.Request{Action: "create", Name: "guide", Description: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"state", "plan", "apply", "recover"} {
		w := request(s, "GET", "/api/mcp/security/"+path, "", nil, "", "")
		if w.Code != 401 {
			t.Fatal(path, w.Code)
		}
		method := "GET"
		if path == "state" {
			method = "POST"
		}
		w = request(s, method, "/api/mcp/security/"+path, "{}", c, origin, csrf)
		if w.Code != 405 {
			t.Fatal(path, w.Code)
		}
	}
	for _, x := range []struct {
		cookie       *http.Cookie
		origin, csrf string
		status       int
	}{{nil, origin, csrf, 401}, {c, origin, "", 401}, {c, "https://evil.invalid", csrf, 403}} {
		for _, path := range []string{"plan", "apply", "recover"} {
			w := request(s, "POST", "/api/mcp/security/"+path, "{}", x.cookie, x.origin, x.csrf)
			if w.Code != x.status {
				t.Fatal(path, w.Code, w.Body.String())
			}
		}
	}
	for _, path := range []string{"plan", "apply", "recover"} {
		w := request(s, "POST", "/api/mcp/security/"+path, `{"execute":true}`, c, origin, csrf)
		var v struct {
			OK   bool
			Code string
		}
		if err = json.Unmarshal(w.Body.Bytes(), &v); err != nil || v.OK || v.Code != "INVALID_INPUT" {
			t.Fatal(w.Body.String(), err)
		}
	}
	w := request(s, "GET", "/api/mcp/security/state", "", c, "", "")
	var v struct {
		OK    bool
		Items pluginsecurity.State
	}
	if err = json.Unmarshal(w.Body.Bytes(), &v); err != nil || !v.OK || v.Items.ExecutionReady || len(v.Items.Documents) != 0 {
		t.Fatal(w.Body.String(), err)
	}
	w = bindingRequest(s, c, csrf, "not-registered", "GET", "/api/mcp/security/state", nil)
	var failure struct {
		OK   bool
		Code string
	}
	json.Unmarshal(w.Body.Bytes(), &failure)
	if failure.OK || failure.Code != "INVALID_INPUT" {
		t.Fatal(w.Body.String())
	}
	w = request(s, "GET", "/mcp-security-view.js", "", c, "", "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}
