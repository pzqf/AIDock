package replica

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func TestFullPublicationOwnerCannotConsumeUnqualifiedStoredPreview(t *testing.T) {
	m, p := storedFullPublicationFixture(t)
	before, err := m.Control.Capture(m.Control.Root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.ApplyFullPublication(context.Background(), p.ID, "")
	code(t, err, "PLAN_CONFIRMATION_REQUIRED")
	if _, err = m.ApplyFullPublication(context.Background(), p.ID, p.Digest); err == nil {
		t.Fatal("stored structure alone became full publication authority")
	}
	after, err := m.Control.Capture(m.Control.Root)
	if err != nil || workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("unqualified full apply changed common control", err)
	}
	state, err := p.Proof.State(m.Source.Store.Local)
	if err != nil || state != "unknown" {
		t.Fatal("unqualified preview produced success proof", state, err)
	}
}

func TestFullRestorationConsumedPreviewRetainsExplicitSelectionWithoutWritePermit(t *testing.T) {
	m, p := storedFullPublicationFixture(t)
	c, err := m.fullPublicationCoordinator(p)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Deliberately unqualified kernel fixture, used only to test restoration
	// proof and metadata. Public full apply still requires complete checks.
	if _, err = c.Apply(ctx, p.Proof.Transaction.ID, p.Proof.Transaction.Digest); err != nil {
		t.Fatal(err)
	}
	selected := []string{p.Proof.Members[0].Copy}
	preview, err := m.PlanFullPublicationRestoration(ctx, p.ID, p.ID, selected)
	if err != nil {
		t.Fatal(preview.Code, err)
	}
	// This stored fixture intentionally has invalid native ownership sentinel
	// bytes. A consumed record must remain readable without using those bytes
	// as authority. Commit only its owned kernel images, not the public owner.
	stored, _, err := m.loadFullRestoration(p, preview.ID, preview.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(ctx, preview.ID, stored.Transaction.Digest); err != nil {
		t.Fatal(err)
	}
	receipt, err := m.fullPublicationExecution(p)
	if err != nil || receipt.Status != "ROLLED_BACK" || !receipt.LatestSelectionKnown || len(receipt.LatestSelectedMembers) != 1 || receipt.LatestSelectedMembers[0].ID != selected[0] {
		t.Fatal(receipt.Status, err)
	}
	consumed, err := m.ReadFullPublicationRestoration(ctx, p.ID, preview.ID, preview.Digest)
	if err != nil || consumed.CanApply || consumed.Code != "PLAN_CONSUMED" || len(consumed.Members) != 1 || consumed.Members[0].Copy.ID != selected[0] || len(consumed.Changes) != 0 {
		t.Fatal(consumed, err)
	}
	_, err = m.ApplyFullPublicationRestoration(ctx, p.ID, preview.ID, preview.Digest)
	code(t, err, "PLAN_CONSUMED")
}

func TestFullRestorationSelectionProtectsLocalHistoryAndScope(t *testing.T) {
	m, p := storedFullPublicationFixture(t)
	var node workstore.BatchPlan
	if err := decode(data(p.Proof.Transaction), &node); err != nil {
		t.Fatal(err)
	}
	member := p.Proof.Members[0].Copy
	sourcePath := "copies/" + member + "/source"
	protectedChange := false
	for i := range node.Targets {
		if node.Targets[i].Path != sourcePath {
			continue
		}
		for path, entry := range node.Targets[i].After.Entries {
			if strings.HasPrefix(path, ".revisions/") && !entry.Dir {
				entry.Hash = workstore.Hash([]byte("immutable history stays available"))
				node.Targets[i].After.Entries[path] = entry
				protectedChange = true
				break
			}
		}
	}
	if !protectedChange {
		t.Fatal("fixture has no immutable revision history")
	}
	node.Digest = workstore.BatchPlanDigest(node)
	ids, selection, err := m.fullRestorationSelection(p, node, []string{member})
	if err != nil || len(ids) != 1 || ids[0] != member {
		t.Fatal(ids, selection, err)
	}
	if _, ok := selection["copies/"+member+"/local"]; ok {
		t.Fatal("local layer became a rollback target")
	}
	for _, path := range selection[sourcePath] {
		if path == ".revisions" || strings.HasPrefix(path, ".revisions/") {
			t.Fatal("rollback would discard immutable history", path)
		}
	}
	if len(selection["control"]) != 1 || selection["control"][0] != pairPath(p.Resolution.Source.ID, member) {
		t.Fatal("selection widened the common baseline scope", selection)
	}
	for _, bad := range [][]string{nil, {}, {member, member}, {p.Resolution.Source.ID}, {workstore.ID()}} {
		_, _, err := m.fullRestorationSelection(p, node, bad)
		code(t, err, "REPLAY_COPY_SELECTION_INVALID")
	}
}

func TestFullPublicationRecoveryUsesPrivateDescriptorAndIdentityPolicy(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "restore", true: "foreign-identity"}[foreign], func(t *testing.T) {
			m, p := storedFullPublicationFixture(t)
			c, err := m.fullPublicationCoordinator(p)
			if err != nil {
				t.Fatal(err)
			}
			if c.RecoveryCommand != "replica publication full-recover --publication "+p.ID {
				t.Fatal("recovery command refers to the legacy publication namespace")
			}
			// The test deliberately interrupts only the kernel's owned fixture,
			// never the unqualified owner API. Durable markers/backup assets are
			// left for a newly constructed recovery owner to inspect and repair.
			interrupted := false
			c.Hook = func(stage string, n int) error {
				if stage == "target-complete" && n == len(p.Proof.Transaction.Targets)-1 {
					panic("owned interrupted full fixture")
				}
				return nil
			}
			func() {
				defer func() {
					if r := recover(); r != nil {
						if r != "owned interrupted full fixture" {
							t.Fatal(r)
						}
						interrupted = true
					}
				}()
				_, _ = c.Apply(context.Background(), p.Proof.Transaction.ID, p.Proof.Transaction.Digest)
			}()
			if !interrupted {
				t.Fatal("fixture never reached durable interruption")
			}
			fresh, err := New(m.Source)
			if err != nil {
				t.Fatal(err)
			}
			// Recovery is independent of the original resolution/check files.
			if err = os.Remove(filepath.Join(m.resolutionDir(), "plans", p.Resolution.ID+".json")); err != nil {
				t.Fatal(err)
			}
			project := filepath.Join(p.Resolution.Targets[0].Copy.Root, "project.yaml")
			original, err := os.ReadFile(project)
			if err != nil {
				t.Fatal(err)
			}
			if foreign {
				if err = os.WriteFile(project, []byte("schema_version: 1\nid: "+workstore.ID()+"\nname: foreign fixture\n"), 0644); err != nil {
					t.Fatal(err)
				}
				_, err = fresh.RecoverFullPublication(p.ID)
				if err == nil {
					t.Fatal("recovery adopted a foreign project")
				}
				body, err := os.ReadFile(project)
				if err != nil || string(body) == string(original) {
					t.Fatal("foreign identity was overwritten", err)
				}
				if err = os.WriteFile(project, original, 0644); err != nil {
					t.Fatal(err)
				}
			}
			inspection, err := fresh.InspectFullPublicationRecovery(p.ID)
			if err != nil || !inspection.Pending || inspection.Status != "READY" {
				t.Fatal(inspection, err)
			}
			recovered, err := fresh.RecoverFullPublication(p.ID)
			if err != nil || recovered.Status != "RECOVERED" || recovered.Pending {
				t.Fatal(recovered, err)
			}
			for _, target := range p.Proof.Transaction.Targets {
				var root string
				for _, r := range c.Roots {
					if r.Path == target.Path {
						root = r.Root
					}
				}
				capture := c.Blobs.Capture
				if target.File {
					capture = c.Blobs.CaptureFile
				}
				now, err := capture(root)
				if err != nil || workstore.Digest(now) != workstore.Digest(target.Before) {
					t.Fatal("full recovery lost original member bytes", target.Path, err)
				}
			}
		})
	}
}
