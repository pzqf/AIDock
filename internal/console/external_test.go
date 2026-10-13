package console

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/bridge"
	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/engine"
	"github.com/pzqf/AIDock/internal/source"
)

func TestHTTPExternalApprovalUsesBoundEngineAndCSRF(t *testing.T) {
	d := t.TempDir()
	external := filepath.Join(d, "external")
	doc := filepath.Join(external, "skills/guide/SKILL.md")
	if err := os.MkdirAll(filepath.Dir(doc), 0755); err != nil {
		t.Fatal(err)
	}
	original := []byte("---\nname: guide\ndescription: fixture\n---\n")
	if err := os.WriteFile(doc, original, 0644); err != nil {
		t.Fatal(err)
	}
	owner := "11111111-1111-4111-8111-111111111111"
	b := bridge.Registration{ID: "33333333-3333-4333-8333-333333333333", Project: owner, Scope: "server", Copy: "test", Root: external, SourcePath: "skills", SourceRole: "external-source", Format: "native-skill"}
	file := filepath.Join(d, "bridge.json")
	data, _ := json.Marshal(bridge.Index{Version: 1, Bridges: []bridge.Registration{b}})
	if err := os.WriteFile(file, data, 0644); err != nil {
		t.Fatal(err)
	}
	e := &engine.Engine{SourceRoot: filepath.Join(d, "project/.aidock"), SourceOwner: owner, SourceScope: "server", SourceCopy: "test", BridgeFile: file, Roots: config.Roots{Local: filepath.Join(d, "local")}}
	if err := os.MkdirAll(filepath.Dir(e.SourceRoot), 0755); err != nil {
		t.Fatal(err)
	}
	s := New(e, "127.0.0.1:32123")
	defer s.Close()
	cookie, csrf := authenticate(t, s)
	report, err := e.BridgeCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := report.Items.(bridge.Report).Bridges[0]
	c := r.Candidates[0]
	req := source.Request{Action: "external-adopt", BridgeID: b.ID, PackagePath: c.Skill.Path, Revision: c.Revision, BridgeRevision: r.CurrentRevision}
	data, _ = json.Marshal(req)
	if w := request(s, "POST", "/api/source/plan", string(data), cookie, "http://"+s.address, ""); w.Code != 401 {
		t.Fatal("missing CSRF accepted")
	}
	w := request(s, "POST", "/api/source/plan", string(data), cookie, "http://"+s.address, csrf)
	var out struct {
		OK    bool           `json:"ok"`
		Items source.Preview `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.OK {
		t.Fatal(w.Body.String(), err)
	}
	if _, err := os.Stat(e.SourceRoot); !os.IsNotExist(err) {
		t.Fatal("preview wrote project")
	}
	data, _ = json.Marshal(map[string]string{"plan_id": out.Items.Plan.ID, "digest": out.Items.Plan.Digest})
	w = request(s, "POST", "/api/source/apply", string(data), cookie, "http://"+s.address, csrf)
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatal(w.Body.String())
	}
	m, err := e.Sources()
	if err != nil {
		t.Fatal(err)
	}
	state, err := m.State()
	if err != nil || len(state.External) != 1 || len(state.Index.Skills) != 0 {
		t.Fatal(state, err)
	}
	got, err := os.ReadFile(doc)
	if err != nil || string(got) != string(original) {
		t.Fatal("upstream changed", err)
	}
	w = request(s, "POST", "/api/source/plan", `{"action":"external-adopt","copy":"foreign"}`, cookie, "http://"+s.address, csrf)
	if !strings.Contains(w.Body.String(), `"code":"INVALID_INPUT"`) {
		t.Fatal(w.Body.String())
	}
	report, err = e.BridgeCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r = report.Items.(bridge.Report).Bridges[0]
	if len(r.ProjectPins) != 1 || r.ProjectPins[0].Status != "PASS" || !r.ProjectPins[0].CacheAvailable {
		t.Fatal("selected baseline not reported", r)
	}
	if err := os.WriteFile(doc, append(original, []byte("external update")...), 0644); err != nil {
		t.Fatal(err)
	}
	report, err = e.BridgeCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r = report.Items.(bridge.Report).Bridges[0]
	if r.Status != "STALE" || r.ProjectPins[0].Status != "STALE" || report.Code != "EXTERNAL_SOURCE_DRIFT" {
		t.Fatal("project pin drift not reported", r, report.Code)
	}
	if err := os.Rename(external, external+"-missing"); err != nil {
		t.Fatal(err)
	}
	report, err = e.BridgeCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r = report.Items.(bridge.Report).Bridges[0]
	if r.Status != "FAIL" || !r.ProjectPins[0].CacheAvailable {
		t.Fatal("missing source lost cached baseline", r)
	}
	if _, err := m.File(state.External[0].Skill.ID, "SKILL.md"); err != nil {
		t.Fatal("offline cached file unavailable", err)
	}
	// Even a state request before the first adoption cannot create its lock
	// inside a declared external source. Cached offline reads stay independent.
	e.SourceRoot = filepath.Join(external+"-missing", ".aidock")
	b.Root = external + "-missing"
	data, _ = json.Marshal(bridge.Index{Version: 1, Bridges: []bridge.Registration{b}})
	if err := os.WriteFile(file, data, 0644); err != nil {
		t.Fatal(err)
	}
	w = request(s, "GET", "/api/source/state", "", cookie, "", "")
	if !strings.Contains(w.Body.String(), `"code":"INVALID_ROOTS"`) {
		t.Fatal(w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(external+"-missing", "..aidock.aidock-lock")); !os.IsNotExist(err) {
		t.Fatal("source read created lock inside authority")
	}
}
