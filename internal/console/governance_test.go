package console

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestGovernanceAPIReadonlyCheckAndEnableFailureReport(t *testing.T) {
	s, c, csrf := sourceServer(t)
	origin := "http://" + s.address
	m, e := s.engine.Sources()
	if e != nil {
		t.Fatal(e)
	}
	p, e := m.Plan(context.Background(), source.Request{Action: "create", Name: "http-governed", Description: "example"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Apply(context.Background(), p.Plan.ID); e != nil {
		t.Fatal(e)
	}
	state, _ := m.State()
	skill := state.Index.Skills[0]
	req := source.Request{Action: "governance", ID: skill.ID, Revision: skill.Revision, Governance: &source.Governance{Dependencies: map[string]any{}, Compatibility: map[string]any{"platforms": []string{"unavailable-platform"}}, Requirements: map[string]any{}}}
	b, _ := json.Marshal(req)
	w := request(s, "POST", "/api/source/plan", string(b), c, origin, csrf)
	var preview struct {
		OK    bool
		Items source.Preview
	}
	if e = json.Unmarshal(w.Body.Bytes(), &preview); e != nil || !preview.OK || preview.Items.Preflight.Status != "FAIL" {
		t.Fatal(w.Body.String(), e)
	}
	b, _ = json.Marshal(map[string]string{"plan_id": preview.Items.Plan.ID, "digest": preview.Items.Plan.Digest})
	w = request(s, "POST", "/api/source/apply", string(b), c, origin, csrf)
	var saved struct{ OK bool }
	_ = json.Unmarshal(w.Body.Bytes(), &saved)
	if !saved.OK {
		t.Fatal(w.Body.String())
	}
	before, _ := m.Store.Capture(m.Store.Root)
	for _, x := range []struct {
		method string
		cookie bool
		want   int
	}{{"GET", false, 401}, {"POST", true, 405}, {"GET", true, 200}} {
		cookie := c
		if !x.cookie {
			cookie = nil
		}
		w = request(s, x.method, "/api/source/check", "", cookie, origin, csrf)
		if w.Code != x.want {
			t.Fatal(w.Code, w.Body.String())
		}
		if x.want == 200 {
			var out struct {
				OK    bool
				Items source.Preflight
			}
			if e = json.Unmarshal(w.Body.Bytes(), &out); e != nil || !out.OK || out.Items.Status != "FAIL" || out.Items.NativeReady || out.Items.Owner != s.engine.SourceOwner {
				t.Fatal(w.Body.String(), e)
			}
		}
	}
	b, _ = json.Marshal(source.Request{Action: "enable", ID: skill.ID})
	w = request(s, "POST", "/api/source/plan", string(b), c, origin, csrf)
	var blocked struct {
		OK       bool
		Code     string
		Recovery source.Preflight
	}
	_ = json.Unmarshal(w.Body.Bytes(), &blocked)
	if blocked.OK || blocked.Code != "SKILL_PREFLIGHT_FAILED" || blocked.Recovery.Status != "FAIL" {
		t.Fatal(w.Body.String())
	}
	after, _ := m.Store.Capture(m.Store.Root)
	if workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("negative API wrote source")
	}
}
