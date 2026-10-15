package console

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestMCPRelayAPIAuthAndFixedInputs(t *testing.T) {
	s, c, csrf := sourceServer(t)
	origin := "http://" + s.address
	for _, x := range []struct {
		cookie        *http.Cookie
		origin, token string
		code          int
	}{{nil, origin, csrf, 401}, {c, origin, "", 401}, {c, "https://evil.invalid", csrf, 403}} {
		w := request(s, "POST", "/api/mcp/relay/check", `{"probe":true}`, x.cookie, x.origin, x.token)
		if w.Code != x.code {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := request(s, "GET", "/api/mcp/relay/check", "", c, "", "")
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
	w = request(s, "POST", "/api/mcp/relay/check", `{"probe":false}`, c, origin, csrf)
	var out struct {
		OK    bool
		Items struct {
			Status         string
			ExecutionReady bool `json:"execution_ready"`
		}
	}
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || !out.OK || out.Items.Status != "NOT_RUN" || out.Items.ExecutionReady {
		t.Fatal(w.Body.String())
	}
	for _, body := range []string{`{"probe":true,"command":"never-run"}`, `{"probe":false,"project":"other"}`, `{"probe":false,"probe":true}`} {
		w = request(s, "POST", "/api/mcp/relay/check", body, c, origin, csrf)
		var bad struct{ OK bool }
		if json.Unmarshal(w.Body.Bytes(), &bad) != nil || bad.OK {
			t.Fatal(w.Body.String())
		}
	}
}
