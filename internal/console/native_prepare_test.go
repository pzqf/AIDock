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

func TestNativePrepareAPIRetainsBindingAndCheckPreviewWithoutWrites(t *testing.T) {
	s, c, csrf := sourceServer(t)
	s.engine.NativeRoot = filepath.Dir(s.engine.SourceRoot)
	origin := "http://" + s.address
	m, err := s.engine.Sources()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "prepare-guide", Description: "API preparation"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	body := `{"tools":["claude-code","codex-cli"]}`
	if w := request(s, "POST", "/api/native/prepare", body, c, origin, ""); w.Code != 401 {
		t.Fatal("preparation missing CSRF accepted", w.Code)
	}
	if w := request(s, "GET", "/api/native/prepare", "", c, "", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
	for _, body := range []string{`{"tools":["codex-cli"],"root":"C:/"}`, `{"tools":["codex-cli"],"check_id":"caller"}`, `{"tools":["unknown"]}`, `{"tools":["codex-cli"],"application_scope":"mcp"}`, `{"tools":["codex-cli"],"rule_probe":true}`} {
		w := request(s, "POST", "/api/native/prepare", body, c, origin, csrf)
		var out struct{ OK bool }
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.OK {
			t.Fatal("accepted invalid preparation", w.Body.String(), err)
		}
	}
	w := request(s, "POST", "/api/native/prepare", body, c, origin, csrf)
	var out struct {
		OK    bool
		Items native.PrepareReport
	}
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.OK || out.Items.Status != "NEEDS_CHECK" || out.Items.Plan.WriteSupported || out.Items.Check.Probe || len(out.Items.Plan.Targets) != 2 {
		t.Fatal(w.Body.String(), err)
	}
	for _, path := range []string{"/api/native/prepare", "/api/native/check", "/api/native/plan"} {
		w := request(s, "POST", path, `{ "tools":["codex-cli"], "application_scope":"skills-rules" }`, c, origin, csrf)
		var scoped struct {
			OK    bool
			Items json.RawMessage
		}
		if err := json.Unmarshal(w.Body.Bytes(), &scoped); err != nil || !scoped.OK {
			t.Fatal(w.Body.String(), err)
		}
		var raw map[string]any
		if err := json.Unmarshal(scoped.Items, &raw); err != nil {
			t.Fatal(err)
		}
		if path == "/api/native/prepare" {
			if raw["plan"].(map[string]any)["application_scope"] != "skills-rules" || raw["check"].(map[string]any)["application_scope"] != "skills-rules" {
				t.Fatal(raw)
			}
		} else if raw["application_scope"] != "skills-rules" {
			t.Fatal(raw)
		}
	}
	n, err := s.engine.Native()
	if err != nil {
		t.Fatal(err)
	}
	q, err := n.Diff(context.Background(), out.Items.Plan.ID, out.Items.Plan.Digest)
	if err != nil || q.ContentDigest != out.Items.Check.InputDigest {
		t.Fatal("preparation returned inconsistent plan/check", q, err)
	}
	for _, target := range q.Targets {
		if _, err := os.Stat(target.Root); !os.IsNotExist(err) {
			t.Fatal("preparation wrote tool target", err)
		}
	}
}
