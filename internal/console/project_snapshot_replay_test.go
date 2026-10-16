package console

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotReplayPermissionAPIAuthMethodsAndStrictInput(t *testing.T) {
	s, c, csrf := sourceServer(t)
	s.engine.NativeRoot = filepath.Dir(s.engine.SourceRoot)
	s.engine.SourceCopy = "22222222-2222-4222-8222-222222222222"
	origin := "http://" + s.address
	for _, route := range []string{"/api/snapshots/replay/review", "/api/snapshots/replay/permission/plan", "/api/snapshots/replay/permission/apply"} {
		if w := request(s, "POST", route, "{}", nil, origin, ""); w.Code != 401 {
			t.Fatal(route, w.Code)
		}
		if w := request(s, "GET", route, "", c, "", ""); w.Code != 405 {
			t.Fatal(route, w.Code)
		}
		if w := request(s, "POST", route, "{}", c, origin, ""); w.Code != 401 {
			t.Fatal(route, w.Code)
		}
		for _, body := range []string{`null`, `{"snapshot_id":"x","snapshot_id":"y"}`, `{"snapshot_id":"x","candidate_digest":"x","authorization":{"confirm_permissions":true,"confirm_permissions":false}}`, `{"PRIVATE_OVERRIDE":"sensitive-value"}`} {
			w := request(s, "POST", route, body, c, origin, csrf)
			if !strings.Contains(w.Body.String(), "INVALID_INPUT") || strings.Contains(w.Body.String(), "sensitive-value") {
				t.Fatal(route, w.Body.String())
			}
		}
	}
}
