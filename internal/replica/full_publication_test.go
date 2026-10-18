package replica

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestFullPublicationParserRejectsAmbiguousOrInjectedMaterial(t *testing.T) {
	req := FullPublicationRequest{Plan: workstore.ID(), Digest: workstore.Hash([]byte("resolution")), Checks: []FullPublicationCheck{{Copy: workstore.ID(), ID: workstore.ID(), Digest: workstore.Hash([]byte("check"))}}}
	raw := string(data(req))
	if got, err := ParseFullPublicationRequest([]byte(raw)); err != nil || string(data(got)) != raw {
		t.Fatal(got, err)
	}
	for _, bad := range []string{
		strings.Replace(raw, `"checks": [`, `"checks": null, "checks": [`, 1),
		strings.Replace(raw, `"checks": [`, `"source_root": "PRIVATE_INPUT", "checks": [`, 1),
		strings.Replace(raw, `"preparation_id":`, `"executable": "PRIVATE_INPUT", "preparation_id":`, 1),
		strings.Replace(raw, `"checks": [`, `"checks": {`, 1),
		`{"plan_id":null,"digest":"x","checks":[]}`,
		`plan_id: &a x\ndigest: *a\nchecks: []`,
		raw + raw,
	} {
		_, err := ParseFullPublicationRequest([]byte(bad))
		code(t, err, "INVALID_INPUT")
		if strings.Contains(err.Error(), "PRIVATE_INPUT") {
			t.Fatal("private parser input echoed")
		}
	}
	req.Checks = append(req.Checks, req.Checks[0])
	_, err := ParseFullPublicationRequest(data(req))
	code(t, err, "INVALID_INPUT")
	r := FullPublicationReadRequest{ID: workstore.ID(), Digest: workstore.Hash([]byte("preview"))}
	if _, err = ParseFullPublicationRead(data(r)); err != nil {
		t.Fatal(err)
	}
	_, err = ParseFullPublicationRead([]byte(strings.Replace(string(data(r)), `"digest":`, `"digest": null, "digest":`, 1)))
	code(t, err, "INVALID_INPUT")
}

// This is deliberately a stored, unqualified preview. Structural integrity
// and stored metadata must never be treated as fresh compiler qualification.
func storedFullPublicationFixture(t *testing.T) (*Manager, fullPublicationPlan) {
	t.Helper()
	m, _, _, req, _ := replicaCandidateFixture(t)
	r, err := m.loadResolution(req.Plan, req.Digest)
	if err != nil {
		t.Fatal(err)
	}
	return storedFullPublicationResolutionFixture(t, m, r)
}

func storedFullPublicationResolutionFixture(t *testing.T, m *Manager, r Resolution) (*Manager, fullPublicationPlan) {
	t.Helper()
	m.Source.Copy = r.Source.ID
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := fullPublicationPlan{Version: 1, ID: workstore.ID(), Created: "2026-10-18T00:00:00Z", InitiatorRoot: m.Source.Store.Root, InitiatorCopy: m.Source.Copy, Executable: exe, Home: filepath.Dir(m.Source.Store.Local), Resolution: r, Checks: []FullPublicationCheck{}}
	p.Proof = projectsnapshot.PublicationCommit{Version: 1, Publication: p.ID, Publisher: fullBinding(r.Owner, r.Scope, r.Source), Members: []projectsnapshot.Binding{}}
	for _, target := range r.Targets {
		p.Checks = append(p.Checks, FullPublicationCheck{Copy: target.Copy.ID, ID: workstore.ID(), Digest: workstore.Hash([]byte("unqualified"))})
		p.Proof.Members = append(p.Proof.Members, fullBinding(r.Owner, r.Scope, target.Copy))
	}
	c, err := p.Proof.Coordinator(m.Source.Store.Local)
	if err != nil {
		t.Fatal(err)
	}
	targets := []workstore.BatchTarget{}
	for _, root := range c.Roots {
		var before, after workstore.Tree
		switch root.Path {
		case "control":
			before, after = r.ControlBefore, r.ControlAfter
		default:
			if root.File {
				before, err = m.Control.CaptureFile(root.Root)
			} else {
				before, err = m.Control.Capture(root.Root)
			}
			if err != nil {
				t.Fatal(err)
			}
			after = workstore.Clone(before)
			for _, target := range r.Targets {
				if root.Path == projectsnapshot.PublicationMemberPrefix(target.Copy.ID)+"source" {
					before, after = target.Before, target.After
					break
				}
			}
		}
		if err = transfer(m.Control, c.Blobs, before, after); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, workstore.BatchTarget{Path: root.Path, File: root.File, Before: before, After: after})
	}
	p.Proof.Transaction, err = c.PreviewObserved("apply", targets)
	if err != nil {
		t.Fatal(err)
	}
	p.Digest = fullPublicationDigest(p)
	if err = saveLocal(m.fullPublicationFile(p.ID), p); err != nil {
		t.Fatal(err)
	}
	return m, p
}

func TestFullPublicationStoredHistoryNeverClaimsQualificationOrWrites(t *testing.T) {
	m, p := storedFullPublicationFixture(t)
	before, err := m.Control.Capture(m.Control.Root)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := m.FullPublicationHistory()
	if err != nil || len(rows) != 1 || rows[0].Fresh || rows[0].CanApply || rows[0].Status != "SAVED" || len(rows[0].Members[0].Changes) != 0 {
		t.Fatal(rows, err)
	}
	out, err := m.ReadFullPublication(context.Background(), p.ID, p.Digest)
	if err != nil || out.Status != "STALE" || out.Fresh || out.CanApply || len(out.Members[0].Changes) != 0 || out.Members[0].AppliedKey != "" {
		t.Fatal(out, err)
	}
	_, err = m.ReadFullPublication(context.Background(), p.ID, workstore.Hash([]byte("wrong")))
	code(t, err, "PLAN_CONFIRMATION_REQUIRED")
	after, err := m.Control.Capture(m.Control.Root)
	if err != nil || workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("history/read changed common baseline", err)
	}
	state, err := p.Proof.State(m.Source.Store.Local)
	if err != nil || state != "unknown" {
		t.Fatal("preview manufactured commit proof", state, err)
	}
}

func TestFullPublicationRehashedDescriptorCannotChangeAuthority(t *testing.T) {
	m, p := storedFullPublicationFixture(t)
	for name, mutate := range map[string]func(*fullPublicationPlan){
		"publisher":         func(p *fullPublicationPlan) { p.Proof.Publisher.Copy = workstore.ID() },
		"check-member":      func(p *fullPublicationPlan) { p.Checks[0].Copy = workstore.ID() },
		"foreign-root":      func(p *fullPublicationPlan) { p.Proof.Members[0].SourceRoot = filepath.Join(t.TempDir(), ".aidock") },
		"missing-member":    func(p *fullPublicationPlan) { p.Proof.Members = nil },
		"foreign-initiator": func(p *fullPublicationPlan) { p.InitiatorRoot = filepath.Join(t.TempDir(), ".aidock") },
	} {
		t.Run(name, func(t *testing.T) {
			var changed fullPublicationPlan
			if err := decode(data(p), &changed); err != nil {
				t.Fatal(err)
			}
			mutate(&changed)
			changed.Digest = fullPublicationDigest(changed)
			if err := saveLocal(m.fullPublicationFile(p.ID), changed); err != nil {
				t.Fatal(err)
			}
			if _, err := m.FullPublicationHistory(); err == nil {
				t.Fatal("history accepted rehashed foreign descriptor")
			}
			if _, err := m.ReadFullPublication(context.Background(), p.ID, changed.Digest); err == nil {
				t.Fatal("read accepted rehashed foreign descriptor")
			}
		})
	}
}

func TestFullPublicationPlanRequiresExactAllTargetReferences(t *testing.T) {
	m, _, _, req, _ := replicaCandidateFixture(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.PlanFullPublication(context.Background(), FullPublicationRequest{Plan: req.Plan, Digest: req.Digest, Checks: []FullPublicationCheck{{Copy: workstore.ID(), ID: workstore.ID(), Digest: workstore.Hash([]byte("check"))}}}, exe, filepath.Dir(m.Source.Store.Local))
	code(t, err, "FULL_PUBLICATION_CHECKS_REQUIRED")
}
