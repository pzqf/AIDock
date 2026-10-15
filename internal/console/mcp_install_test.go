package console

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/source"
)

func TestMCPInstallAPIAuthenticationMethodsInputAndBinding(t *testing.T) {
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
			w := request(s, "POST", "/api/mcp/install/"+path, "{}", x.cookie, x.origin, x.csrf)
			if w.Code != x.status {
				t.Fatal(path, w.Code, w.Body.String())
			}
		}
	}
	for _, path := range []string{"state", "sbom", "plan", "apply", "recover"} {
		method := "GET"
		if path == "state" || path == "sbom" {
			method = "POST"
		}
		w := request(s, method, "/api/mcp/install/"+path, "{}", c, origin, csrf)
		if w.Code != 405 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"plan", "apply", "recover"} {
		w := request(s, "POST", "/api/mcp/install/"+path, `{"execute":true}`, c, origin, csrf)
		var out struct {
			OK   bool
			Code string
		}
		if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.OK || out.Code != "INVALID_INPUT" {
			t.Fatal(w.Body.String(), err)
		}
	}
	w := request(s, "GET", "/api/mcp/install/state", "", c, "", "")
	var state struct {
		OK    bool
		Items plugininstall.State
	}
	if err = json.Unmarshal(w.Body.Bytes(), &state); err != nil || !state.OK || state.Items.ExecutionReady || len(state.Items.Installations) != 0 {
		t.Fatal(w.Body.String(), err)
	}
	for _, path := range []string{"state", "sbom?name=fixture"} {
		w = request(s, "GET", "/api/mcp/install/"+path, "", nil, "", "")
		if w.Code != 401 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w = request(s, "GET", "/api/mcp/install/sbom?name=fixture", "", c, "", "")
	var out struct {
		OK   bool
		Code string
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.OK || out.Code != "INSTALL_NOT_FOUND" {
		t.Fatal(w.Body.String())
	}
	w = bindingRequest(s, c, csrf, "not-registered", "GET", "/api/mcp/install/state", nil)
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.OK || out.Code != "INVALID_INPUT" {
		t.Fatal(w.Body.String())
	}
	w = request(s, "GET", "/mcp-install-view.js", "", c, "", "")
	if w.Code != 200 {
		t.Fatal("install UI unavailable", w.Code)
	}
}
