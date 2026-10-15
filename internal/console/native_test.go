package console

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/source"
)

func TestNativeAPIUsesSessionBindingAndMatchesCLIManager(t *testing.T) {
	s, c, csrf := sourceServer(t)
	s.engine.NativeRoot = filepath.Dir(s.engine.SourceRoot)
	origin := "http://" + s.address
	m, err := s.engine.Sources()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "guide", Description: "API"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	enabled, err := m.Plan(context.Background(), source.Request{Action: "enable", ID: p.SkillID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), enabled.Plan.ID, enabled.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	state, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	state.Profile["rules"] = []any{"API rules\nsecond line"}
	edit, err := m.ProfilePlan(context.Background(), source.ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), edit.Plan.ID, edit.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	body := `{"tools":["claude-code","codex-cli"]}`
	if w := request(s, "POST", "/api/native/plan", body, c, origin, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	for _, body := range []string{`{"tools":["codex-cli"],"root":"C:/"}`, `{"tools":["codex-cli"],"version":"0.162.1"}`} {
		w := request(s, "POST", "/api/native/plan", body, c, origin, csrf)
		var out struct{ Code string }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out.Code != "INVALID_INPUT" {
			t.Fatal(w.Body.String())
		}
	}
	w := request(s, "POST", "/api/native/plan", body, c, origin, csrf)
	var out struct {
		OK    bool
		Items native.Plan
	}
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.OK {
		t.Fatal(w.Body.String(), err)
	}
	if out.Items.Blocked || out.Items.WriteSupported || out.Items.Targets[0].Rule == nil {
		t.Fatal(w.Body.String())
	}
	n, err := s.engine.Native()
	if err != nil {
		t.Fatal(err)
	}
	q, err := n.Plan(context.Background(), native.Request{Tools: []string{"claude-code", "codex-cli"}})
	if err != nil || q.ContentDigest != out.Items.ContentDigest {
		t.Fatal("API diverged from manager", err)
	}
	req, _ := json.Marshal(map[string]string{"plan_id": out.Items.ID, "digest": out.Items.Digest})
	for _, api := range []string{"diff", "apply"} {
		w = request(s, "POST", "/api/native/"+api, string(req), c, origin, csrf)
		var response struct {
			OK   bool
			Code string
		}
		_ = json.Unmarshal(w.Body.Bytes(), &response)
		if api == "diff" && !response.OK || api == "apply" && response.Code != "NATIVE_WRITE_UNAVAILABLE" {
			t.Fatal(w.Body.String())
		}
	}
	for _, target := range out.Items.Targets {
		if _, err := os.Stat(target.Root); !os.IsNotExist(err) {
			t.Fatal("target created")
		}
	}
	if w := request(s, "GET", "/api/native/plan", "", c, "", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", "/api/native/check", body, c, origin, ""); w.Code != 401 {
		t.Fatal("probe missing CSRF accepted")
	}
	for _, body := range []string{`{"tools":["codex-cli"],"root":"C:/"}`, `{"tools":["unknown"],"probe":true}`, `{"tools":["codex-cli"],"exe":"evil.exe"}`, `{"tools":["codex-cli"],"rule_probe":true}`} {
		w := request(s, "POST", "/api/native/check", body, c, origin, csrf)
		var out struct{ OK bool }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out.OK {
			t.Fatal("check accepted unknown tool or caller-supplied path", w.Body.String())
		}
	}
	w = request(s, "POST", "/api/native/check", `{"tools":["codex-cli"],"probe":false}`, c, origin, csrf)
	var checked struct {
		OK    bool
		Items native.CheckReport
	}
	if err = json.Unmarshal(w.Body.Bytes(), &checked); err != nil || !checked.OK || checked.Items.Tools[0].L1 != "NOT_APPLIED" || checked.Items.Tools[0].L2 != "UNKNOWN" {
		t.Fatal(w.Body.String(), err)
	}
	w = request(s, "GET", "/api/native/check/latest", "", c, "", "")
	var cached struct {
		OK    bool
		Items *native.CheckReport
	}
	if err = json.Unmarshal(w.Body.Bytes(), &cached); err != nil || !cached.OK || cached.Items == nil || cached.Items.Stale || cached.Items.ID != checked.Items.ID {
		t.Fatal(w.Body.String(), err)
	}
	if w := request(s, "GET", "/api/native/check", "", c, "", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", "/api/native/check/latest", "{}", c, origin, csrf); w.Code != 405 {
		t.Fatal(w.Code)
	}
	for _, target := range out.Items.Targets {
		if _, err := os.Stat(target.Root); !os.IsNotExist(err) {
			t.Fatal("check wrote tool target")
		}
	}
	if w := request(s, "POST", "/api/native/rehearsal/run", string(req), c, origin, ""); w.Code != 401 {
		t.Fatal("rehearsal accepted missing CSRF")
	}
	w = request(s, "POST", "/api/native/rehearsal/run", `{"plan_id":"`+out.Items.ID+`","digest":"`+out.Items.Digest+`","root":"C:/"}`, c, origin, csrf)
	var invalid struct{ Code string }
	_ = json.Unmarshal(w.Body.Bytes(), &invalid)
	if invalid.Code != "INVALID_INPUT" {
		t.Fatal(w.Body.String())
	}
	w = request(s, "POST", "/api/native/rehearsal/run", string(req), c, origin, csrf)
	var rehearsed struct {
		OK    bool
		Items native.Rehearsal
	}
	_ = json.Unmarshal(w.Body.Bytes(), &rehearsed)
	if !rehearsed.OK || rehearsed.Items.Status != "APPLIED" {
		t.Fatal(w.Body.String())
	}
	w = request(s, "GET", "/api/native/rehearsal/latest", "", c, "", "")
	var latest struct {
		OK    bool
		Items native.Rehearsal
	}
	_ = json.Unmarshal(w.Body.Bytes(), &latest)
	if !latest.OK || latest.Items.ID != rehearsed.Items.ID {
		t.Fatal(w.Body.String())
	}
	for _, want := range []string{"ROLLED_BACK", "UNDONE"} {
		body, _ := json.Marshal(map[string]string{"session_id": rehearsed.Items.ID, "transaction_id": rehearsed.Items.Transactions[0].ID})
		w = request(s, "POST", "/api/native/rehearsal/plan", string(body), c, origin, csrf)
		var planned struct {
			OK    bool
			Items struct {
				ID     string
				Digest string
			}
		}
		_ = json.Unmarshal(w.Body.Bytes(), &planned)
		if !planned.OK {
			t.Fatal(w.Body.String())
		}
		body, _ = json.Marshal(map[string]string{"session_id": rehearsed.Items.ID, "plan_id": planned.Items.ID, "digest": planned.Items.Digest})
		w = request(s, "POST", "/api/native/rehearsal/apply", string(body), c, origin, csrf)
		_ = json.Unmarshal(w.Body.Bytes(), &rehearsed)
		if !rehearsed.OK || rehearsed.Items.Status != want {
			t.Fatal(w.Body.String())
		}
	}
	for _, target := range out.Items.Targets {
		if _, err := os.Stat(target.Root); !os.IsNotExist(err) {
			t.Fatal("rehearsal wrote bound tool target")
		}
	}
	w = request(s, "GET", "/api/native/transactions", "", c, "", "")
	var history struct {
		OK    bool
		Items []any
	}
	_ = json.Unmarshal(w.Body.Bytes(), &history)
	if !history.OK || len(history.Items) != 0 {
		t.Fatal("rehearsal leaked into bound history", w.Body.String())
	}
	if w = request(s, "POST", "/api/native/recover", "{}", c, origin, ""); w.Code != 401 {
		t.Fatal("recovery accepted missing CSRF")
	}
	w = request(s, "POST", "/api/native/recover", `{"root":"C:/"}`, c, origin, csrf)
	_ = json.Unmarshal(w.Body.Bytes(), &invalid)
	if invalid.Code != "INVALID_INPUT" {
		t.Fatal(w.Body.String())
	}
	w = request(s, "POST", "/api/native/recover", "{}", c, origin, csrf)
	var recovered struct {
		OK    bool
		Items int
	}
	_ = json.Unmarshal(w.Body.Bytes(), &recovered)
	if !recovered.OK || recovered.Items != 0 {
		t.Fatal(w.Body.String())
	}
}
