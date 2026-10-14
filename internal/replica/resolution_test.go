package replica

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func resolveRequest(t *testing.T, m *Manager, b []Binding, choices map[string]string) ResolveRequest {
	t.Helper()
	req := Request{From: b[0].ID, Targets: []string{}}
	for _, copy := range b[1:] {
		req.Targets = append(req.Targets, copy.ID)
	}
	r, err := m.Compare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	out := ResolveRequest{Request: req, Comparison: r.Digest, Decisions: []Decision{}}
	for _, target := range r.Targets {
		for _, entry := range target.Entries {
			if entry.Status != "SAME" {
				choice := choices[target.Copy.ID]
				if choice == "" {
					choice = "skip"
				}
				out.Decisions = append(out.Decisions, Decision{Target: target.Copy.ID, Entry: entry.ID, Choice: choice})
			}
		}
	}
	return out
}
func captureSources(t *testing.T, s []*source.Manager) []string {
	t.Helper()
	out := []string{}
	for _, copy := range s {
		tree, err := copy.Store.Capture(copy.Store.Root)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, workstore.Digest(tree))
	}
	return out
}
func unchanged(t *testing.T, s []*source.Manager, before []string) {
	t.Helper()
	after := captureSources(t, s)
	for i, d := range before {
		if after[i] != d {
			t.Fatalf("real observed copy %d was modified", i)
		}
	}
}

func TestResolutionRehearsalFiveCopiesPreservesSourcesAndReversesBaseline(t *testing.T) {
	m, b, s := fixture(t, 5)
	ctx := context.Background()
	baseline(t, m, b)
	applySource(t, s[0], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "references/notes.txt", Content: "publisher"}}})
	applySource(t, s[2], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "references/notes.txt", Content: "target conflict"}}})
	applySource(t, s[3], source.Request{Action: "disable", ID: skill})
	// A target-only file and directory are preserved as local source content.
	if err := os.MkdirAll(filepath.Join(s[1].Store.Root, ".drafts", "empty"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s[1].Store.Root, ".drafts", "draft.txt"), []byte("keep draft"), 0600); err != nil {
		t.Fatal(err)
	}
	before := captureSources(t, s)
	control, _ := m.Control.Capture(m.Control.Root)
	req := resolveRequest(t, m, b, map[string]string{b[1].ID: "source", b[2].ID: "target", b[3].ID: "source"})
	p, err := m.Resolve(ctx, req)
	if err != nil || len(p.Targets) != 2 {
		t.Fatal(p, err)
	}
	unchanged(t, s, before)
	r, err := m.Rehearse(ctx, p.ID, p.Digest)
	if err != nil || r.Status != "APPLIED" {
		t.Fatal(r, err)
	}
	if r.Coordination != "fixed-roots" || len(r.Roots) != 3 {
		t.Fatal("missing independent source/control bindings", r)
	}
	v, loaded, err := m.loadRehearsal(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	batch := m.rehearsalCoordinator(v, loaded)
	for _, target := range p.Targets {
		store := &workstore.Store{Root: filepath.Join(filepath.Dir(batch.Local), "copies", target.Copy.ID, "project", ".aidock"), Local: batch.Local}
		got, err := store.Capture(store.Root)
		if err != nil || workstore.Digest(got) != workstore.Digest(target.After) {
			t.Fatal("candidate not materialized", err)
		}
		copy, err := source.New(store.Root, batch.Local, owner, "server")
		if err != nil {
			t.Fatal(err)
		}
		catalog, err := copy.Shared()
		if err != nil {
			t.Fatal(err)
		}
		publisher, err := s[0].Shared()
		if err != nil || catalog.Entries[0].Digest != publisher.Entries[0].Digest {
			t.Fatal("complete skill not adopted", catalog, err)
		}
		if _, ok := got.Entries["empty"]; ok {
			t.Fatal("unrooted directory leaked")
		}
	}
	current, _ := m.Control.Capture(m.Control.Root)
	if workstore.Digest(control) != workstore.Digest(current) {
		t.Fatal("real baseline advanced during rehearsal")
	}
	rp, err := m.RehearsalReverse(r.ID, r.Transactions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	r, err = m.RehearsalApply(ctx, r.ID, rp.ID, rp.Digest)
	if err != nil || r.Status != "ROLLED_BACK" {
		t.Fatal(r, err)
	}
	for _, target := range p.Targets {
		store := &workstore.Store{Root: filepath.Join(filepath.Dir(batch.Local), "copies", target.Copy.ID, "project", ".aidock"), Local: batch.Local}
		got, err := store.Capture(store.Root)
		if err != nil || workstore.Digest(got) != workstore.Digest(target.Before) {
			t.Fatal("rollback incomplete", err)
		}
	}
	rp, err = m.RehearsalReverse(r.ID, r.Transactions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	r, err = m.RehearsalApply(ctx, r.ID, rp.ID, rp.Digest)
	if err != nil || r.Status != "UNDONE" {
		t.Fatal(r, err)
	}
	parent := filepath.Dir(batch.Roots[1].Root)
	if err = os.Rename(parent, parent+"-offline"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(parent+"-offline", parent)
	history, err := m.RehearsalHistory()
	if err != nil || len(history) != 1 || !history[0].Pending || history[0].Warning == "" {
		t.Fatal("unavailable target hid recovery record", history, err)
	}
	if err = os.Rename(parent+"-offline", parent); err != nil {
		t.Fatal(err)
	}
	unchanged(t, s, before)
	latest, err := m.RehearsalLatest()
	if err != nil || latest.Status != "UNDONE" {
		t.Fatal(latest, err)
	}
}
func TestResolutionExplicitDecisionsUnknownBaseAndStaleAreBlocked(t *testing.T) {
	m, b, s := fixture(t, 2)
	ctx := context.Background()
	applySource(t, s[0], source.Request{Action: "disable", ID: skill})
	for _, copy := range b {
		if err := os.Remove(filepath.Join(filepath.Dir(copy.Root), "..aidock.aidock-lock")); err != nil {
			t.Fatal(err)
		}
	}
	req := resolveRequest(t, m, b, map[string]string{b[1].ID: "source"})
	missing := req
	missing.Decisions = nil
	_, err := m.Resolve(ctx, missing)
	code(t, err, "REPLICA_DECISION_REQUIRED")
	duplicate := req
	duplicate.Decisions = append(append([]Decision{}, req.Decisions...), req.Decisions[0])
	_, err = m.Resolve(ctx, duplicate)
	code(t, err, "REPLICA_DECISION_INVALID")
	p, err := m.Resolve(ctx, req)
	if err != nil {
		t.Fatal("explicit unknown-baseline choice rejected", err)
	}
	for _, copy := range b {
		if _, err := os.Stat(filepath.Join(filepath.Dir(copy.Root), "..aidock.aidock-lock")); !os.IsNotExist(err) {
			t.Fatal("resolution preview created a target lock", err)
		}
	}
	_, err = m.Rehearse(ctx, p.ID, "wrong")
	code(t, err, "PLAN_CONFIRMATION_REQUIRED")
	applySource(t, s[1], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "references/notes.txt", Content: "new target"}}})
	_, err = m.Rehearse(ctx, p.ID, p.Digest)
	code(t, err, "PLAN_STALE")
}
func TestRehearsalCompleteFailureRecoveryAndFreshManagerResume(t *testing.T) {
	m, b, s := fixture(t, 3)
	ctx := context.Background()
	baseline(t, m, b)
	applySource(t, s[0], source.Request{Action: "disable", ID: skill})
	before := captureSources(t, s)
	p, err := m.Resolve(ctx, resolveRequest(t, m, b, map[string]string{b[1].ID: "source", b[2].ID: "source"}))
	if err != nil {
		t.Fatal(err)
	}
	m.RehearsalHook = func(stage string, i int) error {
		if stage == "target-complete" && i == 1 || stage == "recovered-target" && i == 0 {
			return fmt.Errorf("injected interruption")
		}
		return nil
	}
	_, err = m.Rehearse(ctx, p.ID, p.Digest)
	code(t, err, "RECOVERY_CONFLICT")
	latest, err := m.RehearsalLatest()
	if err != nil || !latest.Pending {
		t.Fatal(latest, err)
	}
	_, err = m.Rehearse(ctx, p.ID, p.Digest)
	code(t, err, "REPLICA_RECOVERY_REQUIRED")
	fresh, err := New(s[0])
	if err != nil {
		t.Fatal(err)
	}
	r, err := fresh.RehearsalRecover(latest.ID)
	if err != nil || r.Status != "RECOVERED" {
		t.Fatal(r, err)
	}
	v, loaded, err := fresh.loadRehearsal(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	batch := fresh.rehearsalCoordinator(v, loaded)
	for _, target := range p.Targets {
		store := &workstore.Store{Root: filepath.Join(filepath.Dir(batch.Local), "copies", target.Copy.ID, "project", ".aidock"), Local: batch.Local}
		got, err := store.Capture(store.Root)
		if err != nil || workstore.Digest(got) != workstore.Digest(target.Before) {
			t.Fatal("batch recovery incomplete", err)
		}
	}
	unchanged(t, s, before)
}
func TestResolutionRetirementAndBranchMismatch(t *testing.T) {
	m, b, s := fixture(t, 2)
	ctx := context.Background()
	baseline(t, m, b)
	applySource(t, s[0], source.Request{Action: "retire", ID: skill, Reason: "fixture retirement"})
	p, err := m.Resolve(ctx, resolveRequest(t, m, b, map[string]string{b[1].ID: "source"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Rehearse(ctx, p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	for _, copy := range b {
		if err = os.MkdirAll(filepath.Join(filepath.Dir(copy.Root), ".git"), 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(filepath.Dir(copy.Root), ".git", "HEAD"), []byte("ref: refs/heads/other\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	_, err = m.Resolve(ctx, resolveRequest(t, m, b, map[string]string{b[1].ID: "source"}))
	code(t, err, "REPLICA_BINDING_BLOCKED")
}

func TestPendingCoordinatedRehearsalProtectsSingleRootAndRecoversWithoutPublisher(t *testing.T) {
	m, b, s := fixture(t, 2)
	ctx := context.Background()
	baseline(t, m, b)
	applySource(t, s[0], source.Request{Action: "disable", ID: skill})
	p, err := m.Resolve(ctx, resolveRequest(t, m, b, map[string]string{b[1].ID: "source"}))
	if err != nil {
		t.Fatal(err)
	}
	m.RehearsalHook = func(stage string, i int) error {
		if stage == "target-complete" && i == 0 || stage == "recovered-target" && i == 0 {
			return fmt.Errorf("interrupted")
		}
		return nil
	}
	_, err = m.Rehearse(ctx, p.ID, p.Digest)
	code(t, err, "RECOVERY_CONFLICT")
	latest, err := m.RehearsalLatest()
	if err != nil || latest == nil || !latest.Pending {
		t.Fatal(latest, err)
	}
	v, loaded, err := m.loadRehearsal(latest.ID)
	if err != nil {
		t.Fatal(err)
	}
	c := m.rehearsalCoordinator(v, loaded)
	target, err := source.New(c.Roots[1].Root, c.Local, owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	state, err := target.State()
	if err != nil || !state.Pending || state.Warning == "" {
		t.Fatal("single-root UI failed to expose group recovery", state, err)
	}
	_, err = target.Plan(ctx, source.Request{Action: "disable", ID: skill})
	code(t, err, "COORDINATED_RECOVERY_REQUIRED")
	_, err = target.Store.Recover()
	code(t, err, "COORDINATED_RECOVERY_REQUIRED")
	// Published source is irrelevant once the durable candidate is approved.
	if err = os.Rename(s[0].Store.Root, s[0].Store.Root+"-offline"); err != nil {
		t.Fatal(err)
	}
	fresh, err := New(s[0])
	if err != nil {
		t.Fatal(err)
	}
	r, err := fresh.RehearsalRecover(latest.ID)
	if err != nil || r.Status != "RECOVERED" {
		t.Fatal(r, err)
	}
	if _, err = os.Stat(s[0].Store.Root); !os.IsNotExist(err) {
		t.Fatal("recovery recreated missing publisher", err)
	}
	got, err := target.Store.Capture(target.Store.Root)
	if err != nil || workstore.Digest(got) != workstore.Digest(p.Targets[0].Before) {
		t.Fatal("target not restored from backup", err)
	}
}

func TestLegacyRehearsalHistoryRollbackAndUndoRemainReadable(t *testing.T) {
	m, b, s := fixture(t, 2)
	ctx := context.Background()
	applySource(t, s[0], source.Request{Action: "disable", ID: skill})
	p, err := m.Resolve(ctx, resolveRequest(t, m, b, map[string]string{b[1].ID: "source"}))
	if err != nil {
		t.Fatal(err)
	}
	v := rehearsalRecord{Version: 1, ID: workstore.ID(), Plan: p.ID, PlanDigest: p.Digest, Owner: owner, Scope: "server", Created: time.Now().UTC().Format(time.RFC3339Nano)}
	v.Digest = rehearsalDigest(v)
	bx := m.rehearsalBatch(v, p)
	if err = os.MkdirAll(bx.Boundary, 0700); err != nil {
		t.Fatal(err)
	}
	if err = saveLocal(filepath.Join(m.resolutionDir(), "sessions", v.ID+".json"), v); err != nil {
		t.Fatal(err)
	}
	seed := []workstore.BatchTarget{{Path: "control", Before: workstore.Tree{Entries: map[string]workstore.Entry{}}, After: p.ControlBefore}}
	apply := []workstore.BatchTarget{{Path: "control", Before: p.ControlBefore, After: p.ControlAfter}}
	for _, target := range p.Targets {
		path := "copies/" + target.Copy.ID + "/.aidock"
		seed = append(seed, workstore.BatchTarget{Path: path, Before: workstore.Tree{Entries: map[string]workstore.Entry{}}, After: target.Before})
		apply = append(apply, workstore.BatchTarget{Path: path, Before: target.Before, After: target.After})
	}
	for i, targets := range [][]workstore.BatchTarget{seed, apply} {
		action := "seed"
		if i == 1 {
			action = "apply"
		}
		plan, err := bx.Preview(action, targets)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = bx.Apply(ctx, plan.ID, plan.Digest); err != nil {
			t.Fatal(err)
		}
	}
	r, err := m.RehearsalLatest()
	if err != nil || r.ID != v.ID || r.Status != "APPLIED" || r.Coordination != "" {
		t.Fatal(r, err)
	}
	for _, want := range []string{"ROLLED_BACK", "UNDONE"} {
		plan, err := m.RehearsalReverse(r.ID, r.Transactions[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Kind != "native-targets" {
			t.Fatal("legacy journal changed writer", plan.Kind)
		}
		report, err := m.RehearsalApply(ctx, r.ID, plan.ID, plan.Digest)
		if err != nil || report.Status != want {
			t.Fatal(report, err)
		}
		r = &report
	}
}
