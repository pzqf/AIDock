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
}
