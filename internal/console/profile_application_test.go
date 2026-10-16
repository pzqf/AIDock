package console

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/source"
)

func TestProfileApplicationAPIAuthenticationBindingAndStrictInput(t *testing.T) {
	s, c, csrf := sourceServer(t)
	s.engine.NativeRoot = filepath.Dir(s.engine.SourceRoot)
	s.engine.SourceCopy = "22222222-2222-4222-8222-222222222222"
	m, err := s.engine.Sources()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "joint-guide", Description: "API joint fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	origin := "http://" + s.address
	for _, route := range []string{"/api/native/profile/review", "/api/native/profile/history"} {
		if w := request(s, "GET", route, "", nil, "", ""); w.Code != 401 {
			t.Fatal("missing auth accepted", w.Code)
		}
		w := request(s, "GET", route+"?root=C:/&project=foreign", "", c, "", "")
		var out struct {
			OK    bool
			Items json.RawMessage
		}
		if json.Unmarshal(w.Body.Bytes(), &out) != nil || !out.OK {
			t.Fatal(w.Body.String())
		}
	}
	w := request(s, "GET", "/api/native/profile/review", "", c, "", "")
	var review struct {
		Items native.ProfileApplicationReview
	}
	if json.Unmarshal(w.Body.Bytes(), &review) != nil || review.Items.Status != "READY" || len(review.Items.Entries) != 0 {
		t.Fatal(w.Body.String())
	}
	if w := request(s, "GET", "/api/native/profile/plan", "", c, "", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", "/api/native/profile/plan", `{}`, c, origin, ""); w.Code != 401 {
		t.Fatal("missing CSRF accepted", w.Code)
	}
	for _, body := range []string{`null`, `{"native_plan":"a","native_plan":"b","native_digest":"c","sessions":[]}`, `{"native_plan":"a","native_digest":"b","sessions":[],"command":"PRIVATE_JOINT_INPUT"}`} {
		w := request(s, "POST", "/api/native/profile/plan", body, c, origin, csrf)
		var out struct {
			OK   bool
			Code string
		}
		if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.OK || out.Code != "INVALID_INPUT" || strings.Contains(w.Body.String(), "PRIVATE_JOINT_INPUT") {
			t.Fatal(w.Body.String())
		}
	}
	for _, route := range []string{"/api/native/profile/apply", "/api/native/profile/rollback/plan", "/api/native/profile/rollback/apply", "/api/native/profile/recover"} {
		w := request(s, "POST", route, `{"command":"PRIVATE_JOINT_INPUT"}`, c, origin, csrf)
		if strings.Contains(w.Body.String(), "PRIVATE_JOINT_INPUT") || !strings.Contains(w.Body.String(), "INVALID_INPUT") {
			t.Fatal(w.Body.String())
		}
	}
	for _, body := range []string{`{"probe":true,"probe":false}`, `{"Probe":true}`, `{"probe":null}`, `{"command":"PRIVATE_JOINT_INPUT"}`} {
		w := request(s, "POST", "/api/native/profile/prepare", body, c, origin, csrf)
		var out struct {
			OK   bool
			Code string
		}
		if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.OK || out.Code != "INVALID_INPUT" || strings.Contains(w.Body.String(), "PRIVATE_JOINT_INPUT") {
			t.Fatal(w.Body.String())
		}
	}
	w = request(s, "POST", "/api/native/profile/prepare", `{"tools":["codex-cli"],"application_scope":"full"}`, c, origin, csrf)
	var prepared struct {
		OK    bool
		Items native.ProfilePreparation
	}
	if json.Unmarshal(w.Body.Bytes(), &prepared) != nil || !prepared.OK || prepared.Items.Status != "NEEDS_CHECK" || prepared.Items.Plan != nil || prepared.Items.Check.Probe {
		t.Fatal(w.Body.String())
	}
}
