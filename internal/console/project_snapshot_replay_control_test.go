package console

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestReplayControlAPIAuthenticationMethodsStrictInputAndBinding(t *testing.T) {
	s, c, csrf := sourceServer(t)
	s.engine.NativeRoot = filepath.Dir(s.engine.SourceRoot)
	s.engine.SourceCopy = "22222222-2222-4222-8222-222222222222"
	origin := "http://" + s.address
	for _, route := range []string{"/api/snapshots/replay/history", "/api/snapshots/replay/inspect"} {
		if w := request(s, "GET", route, "", nil, "", ""); w.Code != 401 {
			t.Fatal(route, w.Code)
		}
		w := request(s, "GET", route+"?root=C:/&project=foreign", "", c, "", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
			t.Fatal(route, w.Body.String())
		}
		if w := request(s, "POST", route, "{}", c, origin, csrf); w.Code != 405 {
			t.Fatal(route, w.Code)
		}
	}
	for _, route := range []string{"/api/snapshots/replay/plan", "/api/snapshots/replay/planned", "/api/snapshots/replay/apply", "/api/snapshots/replay/reverse", "/api/snapshots/replay/recover"} {
		if w := request(s, "GET", route, "", c, "", ""); w.Code != 405 {
			t.Fatal(route, w.Code)
		}
		if w := request(s, "POST", route, "{}", c, origin, ""); w.Code != 401 {
			t.Fatal(route, w.Code)
		}
		for _, body := range []string{`null`, `{"plan_id":"x","plan_id":"y"}`, `{"Plan_id":"x"}`, `{"command":"PRIVATE_UNTRUSTED_COMMAND"}`} {
			w := request(s, "POST", route, body, c, origin, csrf)
			if !strings.Contains(w.Body.String(), "INVALID_INPUT") || strings.Contains(w.Body.String(), "PRIVATE_UNTRUSTED_COMMAND") {
				t.Fatal(route, w.Body.String())
			}
		}
	}
}
