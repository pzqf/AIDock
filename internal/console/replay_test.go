package console

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestUnifiedReplayAPIAuthenticatesAndRejectsAmbiguousOrUnconfirmedWrites(t *testing.T) {
	s, cookie, csrf := sourceServer(t)
	s.engine.NativeRoot = filepath.Dir(s.engine.SourceRoot)
	s.engine.SourceCopy = "22222222-2222-4222-8222-222222222222"
	origin := "http://" + s.address
	if r := request(s, "POST", "/api/replay", `{}`, nil, origin, csrf); r.Code != 401 {
		t.Fatal(r.Code)
	}
	if r := request(s, "GET", "/api/replay", ``, cookie, "", ""); r.Code != 405 {
		t.Fatal(r.Code)
	}
	if r := request(s, "POST", "/api/replay", `{}`, cookie, origin, ""); r.Code != 401 {
		t.Fatal(r.Code)
	}
	id := "11111111-1111-4111-8111-111111111111"
	for _, body := range []string{`null`, `{"snapshot_id":"` + id + `","confirm":true}`, `{"snapshot_id":"` + id + `","snapshot_id":"` + id + `"}`, `{"snapshot_id":"` + id + `","copies":[]}`, `{"snapshot_id":"` + id + `","root":"PRIVATE_NO_ECHO"}`, `{"snapshot_id":"` + id + `","action":"apply","plan_id":"` + id + `","digest":"` + strings.Repeat("a", 64) + `"}`} {
		w := request(s, "POST", "/api/replay", body, cookie, origin, csrf)
		if !strings.Contains(w.Body.String(), "INVALID_INPUT") || strings.Contains(w.Body.String(), "PRIVATE_NO_ECHO") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := request(s, "POST", "/api/replay?root=C:/&project=foreign", `{"snapshot_id":"`+id+`","kind":"project"}`, cookie, origin, csrf)
	if !strings.Contains(w.Body.String(), "SNAPSHOT_NOT_FOUND") {
		t.Fatal(w.Body.String())
	}
}
