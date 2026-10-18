package console

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/engine"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestToolEvidenceAPIAuthenticatesBindsAndRejectsExtraScope(t *testing.T) {
	s, cookie, csrf := sourceServer(t)
	s.engine.Roots.Local = filepath.Join(t.TempDir(), "local")
	s.engine.NativeRoot = filepath.Dir(s.engine.SourceRoot)
	s.engine.SourceCopy = "22222222-2222-4222-8222-222222222222"
	m, err := s.engine.Sources()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "guide", Description: "API evidence fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	if w := request(s, "GET", "/api/native/evidence", "", nil, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", "/api/native/evidence", "{}", cookie, "http://"+s.address, csrf); w.Code != 405 {
		t.Fatal(w.Code)
	}
	var response struct {
		OK    bool
		Code  string
		Items engine.ToolEvidence
	}
	w := request(s, "GET", "/api/native/evidence?copy=foreign", "", cookie, "", "")
	if json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Code != "INVALID_INPUT" {
		t.Fatal(w.Body.String())
	}
	before, err := m.Store.Capture(s.engine.NativeRoot)
	if err != nil {
		t.Fatal(err)
	}
	w = request(s, "GET", "/api/native/evidence", "", cookie, "", "")
	if json.Unmarshal(w.Body.Bytes(), &response) != nil || !response.OK || response.Items.Current.Owner != s.engine.SourceOwner || response.Items.Current.Copy != s.engine.SourceCopy || response.Items.Current.Probe || response.Items.Last != nil || response.Items.Applied == nil {
		t.Fatal(w.Body.String())
	}
	after, err := m.Store.Capture(s.engine.NativeRoot)
	if err != nil || workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("evidence API changed source or native files", err)
	}
}
