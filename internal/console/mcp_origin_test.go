package console

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/pluginorigin"
	"github.com/pzqf/AIDock/internal/source"
)

func TestMCPOriginAPISessionCSRFStrictInputAndScopedConfirmation(t *testing.T) {
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
		cookie        *http.Cookie
		origin, token string
		status        int
	}{{nil, origin, csrf, 401}, {c, origin, "", 401}, {c, "https://evil.invalid", csrf, 403}} {
		for _, path := range []string{"/api/mcp/origin/plan", "/api/mcp/origin/apply", "/api/mcp/origin/recover"} {
			w := request(s, "POST", path, "{}", x.cookie, x.origin, x.token)
			if w.Code != x.status {
				t.Fatal(path, w.Code, w.Body.String())
			}
		}
	}
	for _, path := range []string{"state", "show", "plan", "apply", "recover"} {
		method := "GET"
		if path == "state" || path == "show" {
			method = "POST"
		}
		w := request(s, method, "/api/mcp/origin/"+path, "{}", c, origin, csrf)
		if w.Code != 405 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"plan", "apply", "recover"} {
		w := request(s, "POST", "/api/mcp/origin/"+path, `{"private_key":"must not accept"}`, c, origin, csrf)
		var bad struct {
			OK   bool
			Code string
		}
		json.Unmarshal(w.Body.Bytes(), &bad)
		if bad.OK || bad.Code != "INVALID_INPUT" {
			t.Fatal(w.Body.String())
		}
	}
	w := request(s, "GET", "/api/mcp/origin/state", "", c, "", "")
	var state struct {
		OK    bool
		Items pluginorigin.State
	}
	if err = json.Unmarshal(w.Body.Bytes(), &state); err != nil || !state.OK {
		t.Fatal(w.Body.String(), err)
	}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	keys := pluginorigin.Keys{Version: 1, Keys: []pluginorigin.Key{{Label: "Fixture", PublicKey: base64.StdEncoding.EncodeToString(pub), SourceType: "npm", SourceID: "fixture", ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339), Enabled: true}}}
	b, _ := json.Marshal(pluginorigin.Edit{Action: "keys", ExpectedSHA256: state.Items.SHA256, Keys: &keys})
	w = request(s, "POST", "/api/mcp/origin/plan", string(b), c, origin, csrf)
	var preview struct {
		OK    bool
		Items pluginorigin.Preview
	}
	if err = json.Unmarshal(w.Body.Bytes(), &preview); err != nil || !preview.OK {
		t.Fatal(w.Body.String(), err)
	}
	stateNow, _ := m.MCPOrigin(ctx)
	if len(stateNow.Keys.Keys) != 0 {
		t.Fatal("preview registered key")
	}
	for _, digest := range []string{"", "wrong"} {
		b, _ = json.Marshal(map[string]string{"plan_id": preview.Items.Plan.ID, "digest": digest})
		w = request(s, "POST", "/api/mcp/origin/apply", string(b), c, origin, csrf)
		var bad struct{ OK bool }
		json.Unmarshal(w.Body.Bytes(), &bad)
		if bad.OK {
			t.Fatal(w.Body.String())
		}
	}
	b, _ = json.Marshal(map[string]string{"plan_id": preview.Items.Plan.ID, "digest": preview.Items.Plan.Digest})
	w = request(s, "POST", "/api/mcp/origin/apply", string(b), c, origin, csrf)
	var saved struct{ OK bool }
	if err = json.Unmarshal(w.Body.Bytes(), &saved); err != nil || !saved.OK {
		t.Fatal(w.Body.String(), err)
	}
	stateNow, err = m.MCPOrigin(ctx)
	if err != nil || len(stateNow.Keys.Keys) != 1 || stateNow.Keys.Keys[0].ID == "" || stateNow.AuditCount != 1 || stateNow.ExecutionReady {
		t.Fatal(stateNow, err)
	}
	w = request(s, "POST", "/api/mcp/origin/recover", "{}", c, origin, csrf)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}
