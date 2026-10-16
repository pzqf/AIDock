package console

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPConnectionAPIBindingAuthenticationAndStrictInput(t *testing.T) {
	s, cookie, csrf := sourceServer(t)
	s.engine.NativeRoot = filepath.Dir(s.engine.SourceRoot)
	s.engine.SourceCopy = "22222222-2222-4222-8222-222222222222"
	origin := "http://" + s.address
	path := filepath.Join(s.engine.NativeRoot, ".mcp.json")
	original := []byte(`{"mcpServers":{"personal":{"command":"not-executed","env":{"KEY":"PRIVATE_NATIVE_SENTINEL"}}}}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"plan", "apply", "recover", "rollback/plan", "rollback/apply", "versions/check"} {
		url := "/api/native/mcp/" + route
		if w := request(s, "POST", url, `{}`, cookie, origin, ""); w.Code != 401 {
			t.Fatal(route, w.Code)
		}
		if w := request(s, "POST", url, `{}`, cookie, "https://evil.invalid", csrf); w.Code != 403 {
			t.Fatal(route, w.Code)
		}
		if w := request(s, "GET", url, "", cookie, origin, csrf); w.Code != 405 {
			t.Fatal(route, w.Code)
		}
	}
	for _, route := range []string{"history", "versions"} {
		url := "/api/native/mcp/" + route
		if w := request(s, "GET", url, "", nil, origin, csrf); w.Code != 401 {
			t.Fatal(route, w.Code)
		}
		if w := request(s, "POST", url, `{}`, cookie, origin, csrf); w.Code != 405 {
			t.Fatal(route, w.Code)
		}
		w := request(s, "GET", url, "", cookie, origin, csrf)
		var out struct{ OK bool }
		if json.Unmarshal(w.Body.Bytes(), &out) != nil || !out.OK || strings.Contains(w.Body.String(), "PRIVATE_NATIVE_SENTINEL") {
			t.Fatal(w.Body.String())
		}
	}
	for _, tc := range []struct{ route, body string }{
		{"plan", `{"action":"install","action":"remove","private":"PRIVATE_NATIVE_SENTINEL"}`},
		{"plan", `{"Action":"install","session":{"name":"PRIVATE_NATIVE_SENTINEL"}}`},
		{"plan", `{"action":"install","command":"PRIVATE_NATIVE_SENTINEL"}`},
		{"plan", `{"tools":null}`},
		{"apply", `{"plan_id":"PRIVATE_NATIVE_SENTINEL","plan_id":"second","digest":"x"}`},
		{"apply", `{"Plan_id":"PRIVATE_NATIVE_SENTINEL"}`},
		{"rollback/plan", `{"transaction":null}`},
		{"rollback/apply", `{"digest":123}`},
		{"recover", `{"root":"PRIVATE_NATIVE_SENTINEL"}`},
		{"versions/check", `{"tools":["codex-cli"],"tools":["claude-code"]}`},
		{"versions/check", `{"tools":null}`},
	} {
		w := request(s, "POST", "/api/native/mcp/"+tc.route, tc.body, cookie, origin, csrf)
		var out struct{ Code string }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out.Code != "INVALID_INPUT" || strings.Contains(w.Body.String(), "PRIVATE_NATIVE_SENTINEL") {
			t.Fatal(tc.route, w.Body.String())
		}
	}
	read, err := os.ReadFile(path)
	if err != nil || string(read) != string(original) {
		t.Fatal("negative/read requests changed configuration", err)
	}
}
