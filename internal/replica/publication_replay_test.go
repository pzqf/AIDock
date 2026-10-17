package replica

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

// This storage-proof fixture commits compiled temporary images directly. It
// does not manufacture or claim vendor discovery qualification; the opt-in
// real publication acceptance remains responsible for that independent gate.
func publicationReplayFixture(t *testing.T) (*Manager, []Binding, PublicationPlan) {
	t.Helper()
	m, bindings, copies := scopedReplicaFixture(t, 3)
	for _, copy := range copies {
		project := filepath.Join(copy.Store.Root, "project.yaml")
		if _, err := os.Stat(project); os.IsNotExist(err) {
			if err = os.WriteFile(project, data(map[string]any{"schema_version": 1, "id": owner, "name": "temporary replay fixture"}), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	baseline(t, m, bindings)
	applySource(t, copies[0], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "restore-owned.txt", Content: "published temporary material"}}})
	ctx := context.Background()
	r, err := m.Resolve(ctx, resolveRequest(t, m, bindings, map[string]string{bindings[1].ID: "source", bindings[2].ID: "source"}))
	if err != nil {
		t.Fatal(err)
	}
	g, err := m.StageGeneration(ctx, GenerationRequest{Plan: r.ID, Digest: r.Digest, Tools: []string{"codex-cli"}, ApplicationScope: native.SkillsRulesScope})
	if err != nil || g.Blocked {
		t.Fatal(g.Blocked, err)
	}
	p := PublicationPlan{Version: 1, Kind: "replica-publication", ID: workstore.ID(), Owner: m.Source.Owner, Scope: m.Source.Scope, InitiatorRoot: m.Source.Store.Root, InitiatorCopy: m.Source.Copy, Created: time.Now().UTC().Format(time.RFC3339Nano), Resolution: r, Generation: g, Inputs: []publicationInput{}}
	registry, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range registry.Copies {
		copy, err := m.copySource(b)
		if err != nil {
			t.Fatal(err)
		}
		tree, err := copy.Store.Capture(b.Root)
		if err != nil {
			t.Fatal(err)
		}
		configuration, err := copy.ConfigurationDigest()
		if err != nil {
			t.Fatal(err)
		}
		p.Inputs = append(p.Inputs, publicationInput{b, workstore.Digest(tree), configuration})
	}
	targets, err := m.publicationTargets(r, g)
	if err != nil {
		t.Fatal(err)
	}
	c := m.publicationCoordinator(p)
	p.Transaction, err = c.PreviewObserved("apply", targets)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.validatePublicationTransaction(p); err != nil {
		t.Fatal(err)
	}
	p.Digest = publicationDigest(p)
	if err = saveLocal(m.publicationFile(p.ID), p); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(ctx, p.Transaction.ID, p.Transaction.Digest); err != nil {
		t.Fatal(err)
	}
	return m, bindings, p
}

func TestPublicationReplaySelectsMemberAndPairBaselineThenUndoesOnlyThatSelection(t *testing.T) {
	m, b, p := publicationReplayFixture(t)
	ctx := context.Background()
	publisher := generatedObservation(t, m, filepath.Dir(b[0].Root))
	unselected := generatedObservation(t, m, filepath.Dir(b[2].Root))
	r, err := m.PlanPublicationReplay(ctx, p.ID, []string{b[1].ID})
	if err != nil || !r.CanApply || len(r.Copies) != 1 || r.Copies[0].ID != b[1].ID || len(r.Retained) != 1 {
		t.Fatal(r, err)
	}
	for _, change := range r.Changes {
		if change.Path != "control" && !strings.HasPrefix(change.Path, "copies/"+b[1].ID+"/") {
			t.Fatal("selection expanded to another member", change.Path)
		}
		if change.Path == "control" {
			for _, item := range change.Changes {
				if item.Path != pairPath(b[0].ID, b[1].ID) {
					t.Fatal("selection expanded to another baseline", item.Path)
				}
			}
		}
	}
	if _, err = m.ApplyPublicationReplay(ctx, p.ID, r.ID, r.Digest); err != nil {
		t.Fatal(err)
	}
	generatedEqual(t, m, filepath.Dir(b[0].Root), publisher)
	generatedEqual(t, m, filepath.Dir(b[2].Root), unselected)
	control, err := m.Control.Capture(m.Control.Root)
	if err != nil || control.Entries[pairPath(b[0].ID, b[1].ID)] != p.Resolution.ControlBefore.Entries[pairPath(b[0].ID, b[1].ID)] || control.Entries[pairPath(b[0].ID, b[2].ID)] != p.Resolution.ControlAfter.Entries[pairPath(b[0].ID, b[2].ID)] {
		t.Fatal("pair baselines were not independently restored", err)
	}
	_, err = m.PlanPublicationReplay(ctx, p.ID, []string{b[1].ID})
	code(t, err, "SNAPSHOT_CURRENT")
	undo, err := m.PlanPublicationReplay(ctx, r.ID, nil)
	if err != nil || undo.Action != "undo-rollback" || !undo.CanApply {
		t.Fatal(undo, err)
	}
	if _, err = m.ApplyPublicationReplay(ctx, p.ID, undo.ID, undo.Digest); err != nil {
		t.Fatal(err)
	}
	generatedEqual(t, m, filepath.Dir(b[2].Root), unselected)
	for _, target := range p.Transaction.Targets {
		for _, root := range m.publicationCoordinator(p).Roots {
			if root.Path == target.Path {
				if root.File {
					replicaFileEqual(t, m, root.Root, target.After)
				} else {
					generatedEqual(t, m, root.Root, target.After)
				}
			}
		}
	}
	// The original node remains usable after partial rollback and undo.
	other, err := m.PlanPublicationReplay(ctx, p.ID, []string{b[2].ID})
	if err != nil || !other.CanApply {
		t.Fatal(other, err)
	}
	if _, err = m.ApplyPublicationReplay(ctx, p.ID, other.ID, other.Digest); err != nil {
		t.Fatal(err)
	}
}

func TestPublicationReplayRejectsPublisherUnknownDuplicateAndStaleSelections(t *testing.T) {
	m, b, p := publicationReplayFixture(t)
	ctx := context.Background()
	before := generatedObservation(t, m, filepath.Dir(b[1].Root))
	for _, ids := range [][]string{{}, {b[0].ID}, {workstore.ID()}, {b[1].ID, b[1].ID}} {
		if _, err := m.PlanPublicationReplay(ctx, p.ID, ids); err == nil {
			t.Fatal("invalid selection accepted", ids)
		}
	}
	generatedEqual(t, m, filepath.Dir(b[1].Root), before)
	r, err := m.PlanPublicationReplay(ctx, p.ID, []string{b[1].ID})
	if err != nil || !r.CanApply {
		t.Fatal(r, err)
	}
	// Confirmation through an old endpoint cannot use the raw transaction digest.
	stored, err := m.loadPublicationReplay(p, r.ID, r.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyPublicationReverse(ctx, p.ID, r.ID, stored.Transaction.Digest); err == nil {
		t.Fatal("legacy confirmation bypassed selected proposal binding")
	}
	file := filepath.Join(b[1].Root, "knowledge", "new-after-preview.txt")
	if err = os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, []byte("new memory"), 0644); err != nil {
		t.Fatal(err)
	}
	stale, err := m.ReadPublicationReplay(ctx, p.ID, r.ID, r.Digest)
	if err != nil || stale.CanApply || len(stale.Changes) != 0 {
		t.Fatal(stale, err)
	}
	unchanged := generatedObservation(t, m, filepath.Dir(b[1].Root))
	if _, err = m.ApplyPublicationReplay(ctx, p.ID, r.ID, r.Digest); err == nil {
		t.Fatal("stale selection applied")
	}
	generatedEqual(t, m, filepath.Dir(b[1].Root), unchanged)
}

func TestPublicationReplayScopeChecksumCannotAdoptUnselectedRoot(t *testing.T) {
	m, b, p := publicationReplayFixture(t)
	ctx := context.Background()
	r, err := m.PlanPublicationReplay(ctx, p.ID, []string{b[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := m.loadPublicationReplay(p, r.ID, r.Digest)
	if err != nil {
		t.Fatal(err)
	}
	for i := range stored.Transaction.Targets {
		target := &stored.Transaction.Targets[i]
		if target.Path == "copies/"+b[2].ID+"/.aidock" {
			for _, original := range p.Transaction.Targets {
				if original.Path == target.Path {
					target.After = original.Before
				}
			}
		}
	}
	stored.Transaction.Digest = workstore.BatchPlanDigest(stored.Transaction)
	stored.Digest = publicationReplayDigest(stored)
	// The matching common-plan file can also be edited by the same local actor;
	// the immutable original successful journal must still restrict the result.
	c := m.publicationCoordinator(p)
	dirs, err := os.ReadDir(filepath.Join(c.Local, "coordinated-transactions"))
	if err != nil || len(dirs) != 1 {
		t.Fatal(err)
	}
	planFile := filepath.Join(c.Local, "coordinated-transactions", dirs[0].Name(), "plans", r.ID+".json")
	if err = saveLocal(planFile, stored.Transaction); err != nil {
		t.Fatal(err)
	}
	if err = saveLocal(m.publicationReplayFile(p.ID, r.ID), stored); err != nil {
		t.Fatal(err)
	}
	before := generatedObservation(t, m, filepath.Dir(b[2].Root))
	blocked, err := m.ReadPublicationReplay(ctx, p.ID, r.ID, stored.Digest)
	if err != nil || blocked.CanApply || blocked.Code != "BACKUP_INVALID" {
		t.Fatal(blocked, err)
	}
	if _, err = m.ApplyPublicationReplay(ctx, p.ID, r.ID, stored.Digest); err == nil {
		t.Fatal("recomputed checksum widened a selected recovery")
	}
	generatedEqual(t, m, filepath.Dir(b[2].Root), before)
	if !reflect.DeepEqual(blocked.Changes, []PublicationReplayChange{}) {
		t.Fatal("blocked differences retained")
	}
}

func TestPublicationReplayForeignIdentityDuringFailurePreservesBarriersUntilReconciled(t *testing.T) {
	m, b, p := publicationReplayFixture(t)
	ctx := context.Background()
	r, err := m.PlanPublicationReplay(ctx, p.ID, []string{b[1].ID})
	if err != nil || !r.CanApply {
		t.Fatal(r, err)
	}
	file := filepath.Join(b[1].Root, "project.yaml")
	original, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	m.PublicationHook = func(stage string, index int) error {
		if stage == "target" && index == 1 {
			called = true
			if err := os.WriteFile(file, data(map[string]any{"schema_version": 1, "id": "99999999-9999-4999-8999-999999999999", "name": "foreign"}), 0600); err != nil {
				return err
			}
			return errors.New("foreign identity injected during partial recovery")
		}
		return nil
	}
	if _, err = m.ApplyPublicationReplay(ctx, p.ID, r.ID, r.Digest); err == nil || !called {
		t.Fatal("foreign identity failure not exercised", err)
	}
	m.PublicationHook = nil
	inspection, err := m.InspectPublicationRecovery(p.ID)
	if err != nil || !inspection.Pending || inspection.Status != "CONFLICT" {
		t.Fatal(inspection, err)
	}
	if _, err = m.RecoverPublication(p.ID); err == nil {
		t.Fatal("recovery overwrote a declared foreign owner")
	}
	if err = os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.RecoverPublication(p.ID); err != nil {
		t.Fatal(err)
	}
	inspection, err = m.InspectPublicationRecovery(p.ID)
	if err != nil || inspection.Pending {
		t.Fatal(inspection, err)
	}
}
