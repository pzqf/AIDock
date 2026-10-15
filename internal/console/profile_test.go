package console

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func TestProfileAPIConfirmationAndSecurity(t *testing.T) {
	s, c, csrf := sourceServer(t)
	origin := "http://" + s.address
	m, err := s.engine.Sources()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "guide", Description: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	w := request(s, "GET", "/api/profile/state", "", c, "", "")
	var out struct {
		OK    bool
		Items source.ProfileState
	}
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.OK {
		t.Fatal(w.Body.String(), err)
	}
	out.Items.Profile["name"] = "API Profile"
	rules := []source.RuleFile{{Name: "API 规则", Enabled: true, Body: "通过 API 保存完整规则文件。"}}
	body, _ := json.Marshal(source.ProfileEdit{ExpectedSHA256: out.Items.SHA256, Profile: out.Items.Profile, RuleFiles: &rules, ExpectedRulesSHA256: out.Items.RulesSHA256})
	for _, x := range []struct {
		c            *http.Cookie
		origin, csrf string
		status       int
	}{{nil, origin, csrf, 401}, {c, origin, "", 401}, {c, "https://evil.invalid", csrf, 403}} {
		if w = request(s, "POST", "/api/profile/plan", string(body), x.c, x.origin, x.csrf); w.Code != x.status {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w = request(s, "POST", "/api/profile/state", "{}", c, origin, csrf); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w = request(s, "GET", "/api/profile/plan", "", c, "", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
	w = request(s, "POST", "/api/profile/plan", string(body), c, origin, csrf)
	var preview struct {
		OK    bool
		Items source.Preview
	}
	if err = json.Unmarshal(w.Body.Bytes(), &preview); err != nil || !preview.OK {
		t.Fatal(w.Body.String(), err)
	}
	current, _ := m.Profile()
	if current.Profile["name"] == "API Profile" || len(current.RuleFiles) != 0 {
		t.Fatal("preview saved profile")
	}
	confirm, _ := json.Marshal(map[string]string{"plan_id": preview.Items.Plan.ID, "digest": preview.Items.Plan.Digest})
	w = request(s, "POST", "/api/source/apply", string(confirm), c, origin, csrf)
	var applied struct{ OK bool }
	if err = json.Unmarshal(w.Body.Bytes(), &applied); err != nil || !applied.OK {
		t.Fatal(w.Body.String(), err)
	}
	current, err = m.Profile()
	if err != nil || current.Profile["name"] != "API Profile" || len(current.RuleFiles) != 1 || current.RuleFiles[0].Body != rules[0].Body {
		t.Fatal(current, err)
	}
}

func TestLocalProfileAPIUsesSeparateConfirmationAndNeverChangesSharedSource(t *testing.T) {
	s, c, csrf := sourceServer(t)
	s.engine.SourceCopy = "copy-api"
	origin := "http://" + s.address
	m, err := s.engine.Sources()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "guide", Description: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	state, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(source.ProfileEdit{Target: "local", ExpectedSHA256: state.Layers.LocalSHA256, Profile: map[string]any{"schema_version": 2, "name": "API local", "rules": []any{"仅此副本"}}})
	w := request(s, "POST", "/api/profile/plan", string(body), c, origin, csrf)
	var out struct {
		OK    bool
		Items source.Preview
	}
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.OK {
		t.Fatal(w.Body.String(), err)
	}
	before, _ := m.Profile()
	if before.Layers.Local != nil {
		t.Fatal("preview wrote local config")
	}
	confirm, _ := json.Marshal(map[string]string{"target": "local", "plan_id": out.Items.Plan.ID, "digest": out.Items.Plan.Digest})
	if w = request(s, "POST", "/api/profile/apply", string(confirm), c, origin, ""); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(s, "POST", "/api/profile/apply", string(confirm), c, origin, csrf)
	var ok struct{ OK bool }
	if err = json.Unmarshal(w.Body.Bytes(), &ok); err != nil || !ok.OK {
		t.Fatal(w.Body.String(), err)
	}
	current, _ := m.Profile()
	if current.Layers.Local == nil || current.SHA256 != state.SHA256 {
		t.Fatal(current)
	}
	if w = request(s, "GET", "/api/profile/recover-local", "", c, "", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
}
