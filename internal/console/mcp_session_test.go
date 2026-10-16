package console

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/mcpsession"
	"github.com/pzqf/AIDock/internal/source"
)

func TestMCPSessionAPIAuthMethodsExplicitProbeAndAmbiguousInputs(t *testing.T) {
	s, c, csrf := sourceServer(t)
	s.engine.SourceCopy = "session-api-copy"
	origin := "http://" + s.address
	m, err := s.engine.Sources()
	if err != nil {
		t.Fatal(err)
	}
	created, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "guide", Description: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), created.Plan.ID, created.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct {
		cookie       *http.Cookie
		origin, csrf string
		status       int
	}{{nil, origin, csrf, 401}, {c, origin, "", 401}, {c, "https://evil.invalid", csrf, 403}} {
		for _, path := range []string{"check", "recover", "client-config"} {
			w := request(s, "POST", "/api/mcp/session/"+path, "{}", x.cookie, x.origin, x.csrf)
			if w.Code != x.status {
				t.Fatal(path, w.Code, w.Body.String())
			}
		}
	}
	for _, path := range []string{"state", "check", "recover", "client-config"} {
		method := "GET"
		if path == "state" {
			method = "POST"
		}
		w := request(s, method, "/api/mcp/session/"+path, "{}", c, origin, csrf)
		if w.Code != 405 {
			t.Fatal(path, w.Code)
		}
	}
	for _, raw := range []string{`{"probe":true,"probe":false}`, `{"Probe":true}`, `{"probe":null}`, `{"expected_runtime_attempt":false}`, `{"command":"private-sentinel"}`, `{}`} {
		w := request(s, "POST", "/api/mcp/session/check", raw, c, origin, csrf)
		var out struct {
			OK   bool
			Code string
		}
		if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.OK || out.Code != "INVALID_INPUT" || strings.Contains(w.Body.String(), "private-sentinel") {
			t.Fatal(w.Body.String())
		}
	}
	for _, raw := range []string{`{"probe":true,"probe":false}`, `{"Probe":true}`, `{"command":"private-sentinel"}`} {
		w := request(s, "POST", "/api/mcp/session/client-config", raw, c, origin, csrf)
		if !strings.Contains(w.Body.String(), "INVALID_INPUT") || strings.Contains(w.Body.String(), "private-sentinel") {
			t.Fatal(w.Body.String())
		}
	}
	w := request(s, "GET", "/api/mcp/session/state", "", c, "", "")
	var state struct {
		OK    bool
		Items mcpsession.State
	}
	if json.Unmarshal(w.Body.Bytes(), &state) != nil || !state.OK || state.Items.AuditCount != 0 || state.Items.ExecutionReady || len(state.Items.Candidates) != 0 {
		t.Fatal(w.Body.String())
	}
	w = request(s, "GET", "/api/mcp/session/state", "", nil, "", "")
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	w = request(s, "POST", "/api/mcp/session/recover", `{"execute":true}`, c, origin, csrf)
	if !strings.Contains(w.Body.String(), "INVALID_INPUT") {
		t.Fatal(w.Body.String())
	}
}
