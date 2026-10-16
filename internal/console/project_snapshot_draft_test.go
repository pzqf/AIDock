package console

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotDraftAPIAuthenticationStrictBodyAndReadOnlyEmptyState(t *testing.T) {
	s, c, csrf := sourceServer(t)
	s.engine.NativeRoot = filepath.Dir(s.engine.SourceRoot)
	s.engine.SourceCopy = "22222222-2222-4222-8222-222222222222"
	origin := "http://" + s.address
	for _, route := range []string{"/api/snapshots/draft/history", "/api/snapshots/draft/inspect"} {
		if w := request(s, "GET", route, "", nil, "", ""); w.Code != 401 {
			t.Fatal(w.Code)
		}
		if w := request(s, "POST", route, "{}", c, origin, csrf); w.Code != 405 {
			t.Fatal(w.Code)
		}
		w := request(s, "GET", route+"?root=C:/&copy=foreign", "", c, "", "")
		var out struct {
			OK    bool
			Items any
		}
		if json.Unmarshal(w.Body.Bytes(), &out) != nil || !out.OK {
			t.Fatal(w.Body.String())
		}
	}
	for _, route := range []string{"/api/snapshots/draft/plan", "/api/snapshots/draft/apply", "/api/snapshots/draft/reverse", "/api/snapshots/draft/recover"} {
		if w := request(s, "GET", route, "", c, "", ""); w.Code != 405 {
			t.Fatal(w.Code)
		}
		if w := request(s, "POST", route, "{}", c, origin, ""); w.Code != 401 {
			t.Fatal(w.Code)
		}
		for _, body := range []string{`null`, `{"unknown":"PRIVATE_BODY_MUST_NOT_ECHO"}`, `{"snapshot_id":"x","snapshot_id":"y"}`, `{} {}`} {
			w := request(s, "POST", route, body, c, origin, csrf)
			if !strings.Contains(w.Body.String(), "INVALID_INPUT") || strings.Contains(w.Body.String(), "PRIVATE_BODY_MUST_NOT_ECHO") {
				t.Fatal(w.Body.String())
			}
		}
	}
}
