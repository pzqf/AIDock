package console

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/native"
)

func TestProjectSnapshotAPIReadBindingAuthenticationAndStrictCheck(t *testing.T) {
	s, c, csrf := sourceServer(t)
	s.engine.NativeRoot = filepath.Dir(s.engine.SourceRoot)
	s.engine.SourceCopy = "22222222-2222-4222-8222-222222222222"
	origin := "http://" + s.address
	if w := request(s, "GET", "/api/snapshots", "", nil, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := request(s, "GET", "/api/snapshots?root=C:/&project=foreign", "", c, "", "")
	var out struct {
		OK    bool
		Items native.ProjectSnapshots
	}
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || !out.OK || out.Items.Project != s.engine.SourceOwner || len(out.Items.Items) != 0 {
		t.Fatal(w.Body.String())
	}
	if w := request(s, "POST", "/api/snapshots", "{}", c, origin, csrf); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", "/api/snapshots/check", "{}", c, origin, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	for _, body := range []string{`null`, `{"snapshot_id":"x","snapshot_id":"y"}`, `{"Snapshot_id":"x"}`, `{"snapshot_id":"x","command":"PRIVATE_SNAPSHOT_INPUT"}`} {
		w = request(s, "POST", "/api/snapshots/check", body, c, origin, csrf)
		if !strings.Contains(w.Body.String(), "INVALID_INPUT") || strings.Contains(w.Body.String(), "PRIVATE_SNAPSHOT_INPUT") {
			t.Fatal(w.Body.String())
		}
	}
}
