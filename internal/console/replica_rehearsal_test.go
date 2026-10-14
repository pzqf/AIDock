package console

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestReplicaResolutionAndRehearsalAPIProtectsObservedProjects(t *testing.T) {
	s, c, csrf := sourceServer(t)
	origin := "http://" + s.address
	ctx := context.Background()
	current, err := s.engine.Sources()
	if err != nil {
		t.Fatal(err)
	}
	targetRoot := filepath.Join(t.TempDir(), ".aidock")
	target, err := source.New(targetRoot, current.Store.Local, current.Owner, current.Scope)
	if err != nil {
		t.Fatal(err)
	}
	id := workstore.ID()
	for _, m := range []*source.Manager{current, target} {
		p, err := m.Plan(ctx, source.Request{Action: "create", ID: id, Name: "guide", Description: "API fixture"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = m.Apply(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
			t.Fatal(err)
		}
	}
	p, err := current.Plan(ctx, source.Request{Action: "edit", ID: id, Files: []source.Patch{{Path: "SKILL.md", Content: "---\nname: guide\ndescription: API changed\n---\nnew body\n"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = current.Apply(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	m, err := s.engine.Replicas()
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for i, root := range []string{current.Store.Root, targetRoot} {
		p, err := m.Register(replica.Binding{Name: []string{"one", "two"}[i], Root: root})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = m.Apply(ctx, p.ID, p.Digest); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.Inputs["binding_id"])
	}
	before, _ := target.Store.Capture(targetRoot)
	report, err := m.Compare(ctx, replica.Request{From: ids[0], Targets: []string{ids[1]}})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/replica/resolve", "/api/replica/rehearsal", "/api/replica/rehearsal/plan", "/api/replica/rehearsal/apply", "/api/replica/rehearsal/recover"} {
		if w := request(s, "POST", path, `{}`, c, origin, ""); w.Code != 401 {
			t.Fatalf("unguarded %s: %d", path, w.Code)
		}
		w := request(s, "POST", path, `{"foreign_root":"C:/"}`, c, origin, csrf)
		var out struct{ Code string }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out.Code != "INVALID_INPUT" {
			t.Fatal(w.Body.String())
		}
	}
	post := func(path string, body any, v any) {
		t.Helper()
		b, _ := json.Marshal(body)
		w := request(s, "POST", path, string(b), c, origin, csrf)
		var out struct {
			OK    bool
			Items json.RawMessage
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.OK {
			t.Fatal(w.Body.String(), err)
		}
		if err := json.Unmarshal(out.Items, v); err != nil {
			t.Fatal(err)
		}
	}
	var plan replica.Resolution
	post("/api/replica/resolve", replica.ResolveRequest{Request: replica.Request{From: ids[0], Targets: []string{ids[1]}}, Comparison: report.Digest, Decisions: []replica.Decision{{Target: ids[1], Entry: id, Choice: "source"}}}, &plan)
	if _, err = os.Stat(filepath.Join(m.Source.Store.Local, "replica-resolutions")); err != nil {
		t.Fatal(err)
	}
	var run replica.Rehearsal
	post("/api/replica/rehearsal", map[string]string{"plan_id": plan.ID, "digest": plan.Digest}, &run)
	if run.Status != "APPLIED" {
		t.Fatal(run)
	}
	var reverse workstore.BatchPlan
	post("/api/replica/rehearsal/plan", map[string]string{"session": run.ID, "transaction": run.Transactions[0].ID}, &reverse)
	post("/api/replica/rehearsal/apply", map[string]string{"session": run.ID, "plan_id": reverse.ID, "digest": reverse.Digest}, &run)
	if run.Status != "ROLLED_BACK" {
		t.Fatal(run)
	}
	post("/api/replica/rehearsal/recover", map[string]string{"session": run.ID}, &run)
	w := request(s, "GET", "/api/replica/rehearsal/latest", "", c, origin, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	after, _ := target.Store.Capture(targetRoot)
	if workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("API modified observed project")
	}
}
