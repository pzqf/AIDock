package console

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/generator"
	"github.com/pzqf/AIDock/internal/source"
)

func TestGeneratorAPIAuthenticationStrictBindingAndOfflineLifecycle(t *testing.T) {
	s, c, csrf := sourceServer(t)
	origin := "http://" + s.address
	for _, tc := range []struct {
		method, path, body, origin, csrf string
		want                             int
	}{
		{"POST", "/api/generator/stage", "{}", origin, "", 401},
		{"POST", "/api/generator/stage", "{}", "https://evil.invalid", csrf, 403},
		{"GET", "/api/generator/clean", "", "", "", 405},
		{"POST", "/api/generator/history", "{}", origin, csrf, 405},
	} {
		w := request(s, tc.method, tc.path, tc.body, c, tc.origin, tc.csrf)
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := request(s, "GET", "/api/generator/history", "", nil, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	for _, path := range []string{"/api/generator/inspect", "/api/generator/stage", "/api/generator/show", "/api/generator/validate", "/api/generator/clean"} {
		w := request(s, "POST", path, `{"root":"C:/","command":"bad"}`, c, origin, csrf)
		var out struct{ Code string }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out.Code != "INVALID_INPUT" {
			t.Fatal(w.Body.String())
		}
	}
	m, err := s.engine.Sources()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "api-guide", Description: "example", Document: "offline fixture"})
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
	runtime := filepath.Join(filepath.Dir(s.engine.SourceRoot), "runtime")
	if err = os.Mkdir(runtime, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(runtime, "not-executable.bin"), []byte("never launch"), 0644); err != nil {
		t.Fatal(err)
	}
	contract := generator.Contract{Version: 1, ID: "api-fixture", Release: "1.0", Protocol: generator.Protocol, Runtime: runtime, Entry: "not-executable.bin", Tools: []string{"codex-cli"}, Network: "deny", Children: "deny"}
	adapter := filepath.Join(filepath.Dir(runtime), "adapter.json")
	bytes, _ := json.Marshal(contract)
	if err = os.WriteFile(adapter, bytes, 0644); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"adapter_path": adapter})
	w := request(s, "POST", "/api/generator/inspect", string(payload), c, origin, csrf)
	var preview struct {
		OK    bool
		Items generator.Preview
	}
	if err = json.Unmarshal(w.Body.Bytes(), &preview); err != nil || !preview.OK || preview.Items.ExecutionReady {
		t.Fatal(w.Body.String(), err)
	}
	payload, _ = json.Marshal(map[string]string{"adapter_path": adapter, "digest": preview.Items.Digest})
	w = request(s, "POST", "/api/generator/stage", string(payload), c, origin, csrf)
	var staged struct {
		OK    bool
		Items generator.Observation
	}
	_ = json.Unmarshal(w.Body.Bytes(), &staged)
	if !staged.OK || staged.Items.State != "STAGED" {
		t.Fatal(w.Body.String())
	}
	payload, _ = json.Marshal(map[string]string{"stage_id": staged.Items.Stage.ID, "digest": staged.Items.Stage.Digest})
	w = request(s, "POST", "/api/generator/validate", string(payload), c, origin, csrf)
	var validation struct {
		OK    bool
		Code  string
		Items generator.Validation
	}
	_ = json.Unmarshal(w.Body.Bytes(), &validation)
	if !validation.OK || validation.Code != "STATIC_CHECK_FAILED" || validation.Items.Status != "FAIL" || validation.Items.PublicationReady {
		t.Fatal(w.Body.String())
	}
	w = request(s, "GET", "/api/generator/history", "", c, "", "")
	var history struct {
		OK    bool
		Items []generator.Observation
	}
	_ = json.Unmarshal(w.Body.Bytes(), &history)
	if !history.OK || len(history.Items) != 1 || history.Items[0].Validation == nil {
		t.Fatal(w.Body.String())
	}
	payload, _ = json.Marshal(map[string]string{"stage_id": staged.Items.Stage.ID, "digest": history.Items[0].ObservationDigest})
	w = request(s, "POST", "/api/generator/clean", string(payload), c, origin, csrf)
	_ = json.Unmarshal(w.Body.Bytes(), &staged)
	if !staged.OK || staged.Items.State != "CLEANED" {
		t.Fatal(w.Body.String())
	}
	if _, err = os.Stat(s.engine.SourceRoot); err != nil {
		t.Fatal("source lost", err)
	}
	s.Close()
	w = request(s, "POST", "/api/generator/inspect", "{}", c, origin, csrf)
	if w.Code != 503 {
		t.Fatal("closed service accepted operation", w.Code)
	}
}
