package native

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestPublicationRestorationUsesSharedCommitAndCurrentGrant(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		t.Run(map[bool]string{false: "restore", true: "revoked"}[revoked], func(t *testing.T) {
			m, candidate, executable, home := nativePublicationMCPFixture(t)
			ctx := context.Background()
			material, c, proof := compilePublicationFixture(t, m, candidate, executable, home)
			if _, err := material.Capture(ctx, proof); err != nil {
				t.Fatal(err)
			}
			c.Validate = func(workstore.BatchPlan) error { return material.Validate(ctx, false) }
			c.BeforeCommit = func(workstore.BatchPlan) error { return material.Validate(ctx, true) }
			if _, err := c.Apply(ctx, proof.Transaction.ID, proof.Transaction.Digest); err != nil {
				t.Fatal(err)
			}
			// The test owns this kernel fixture. Its raw inverse establishes an
			// empty pre-application ledger; the actual reinstatement below must
			// use the new member review and current authority under common locks.
			c.Validate, c.BeforeCommit = nil, nil
			rollback, err := c.Reverse(proof.Transaction.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.Apply(ctx, rollback.ID, rollback.Digest); err != nil {
				t.Fatal(err)
			}
			undo, err := c.Reverse(rollback.ID)
			if err != nil {
				t.Fatal(err)
			}
			review, err := m.InspectPublicationRestoration(ctx, proof, undo.ID, undo.Digest)
			if err != nil {
				t.Fatal(err)
			}
			if err = review.Recheck(ctx); err != nil {
				t.Fatal("current shared view was not ready for a restoration preview", err)
			}
			views := review.Views()
			if len(views) != 1 || views[0].Binding != material.ActiveKey() || strings.TrimSpace(string(jsonData(review))) != "{}" {
				t.Fatal("review leaked authority or lost the original shared view", views)
			}
			views[0].Binding = "caller mutation"
			if review.Views()[0].Binding != material.ActiveKey() {
				t.Fatal("caller changed opaque review")
			}
			beforeSource, beforeLocal := snapshotDraftTrees(t, m)
			beforeNative := snapshotNativeTrees(t, m)
			if revoked {
				permission, err := m.Source.MCPPermission(ctx)
				if err != nil {
					t.Fatal(err)
				}
				revoke, err := m.Source.MCPPermissionPlan(ctx, pluginpermission.Edit{Action: "revoke", Name: "project-memory", ExpectedSHA256: permission.SHA256})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = m.Source.ApplyMCPPermission(ctx, revoke.Plan.ID, revoke.Plan.Digest); err != nil {
					t.Fatal(err)
				}
				if err = review.Recheck(ctx); err == nil {
					t.Fatal("preview still claimed current authority after revocation")
				}
			}
			c.Validate = func(tx workstore.BatchPlan) error {
				if err := c.CheckRestoration(rollback.ID, rollback.Digest, nil, tx); err != nil {
					return err
				}
				return review.Validate(ctx, false)
			}
			c.BeforeCommit = func(workstore.BatchPlan) error { return review.Validate(ctx, true) }
			err = source.WithConfigurationTransactionLocks([]*source.Manager{m.Source}, []string{source.LocalProfileRoot(m.Source.Store.Local, m.Source.Owner, m.Source.Scope, m.Source.Copy)}, func() error {
				_, err := c.Apply(ctx, undo.ID, undo.Digest)
				return err
			})
			if revoked {
				if err == nil {
					t.Fatal("restoration resurrected a revoked grant")
				}
				s, l := snapshotDraftTrees(t, m)
				if workstore.Digest(s) != workstore.Digest(beforeSource) || workstore.Digest(l) != workstore.Digest(beforeLocal) || !reflectTreesEqual(beforeNative, snapshotNativeTrees(t, m)) {
					t.Fatal("denied restoration changed project files")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				view, err := m.loadAppliedRecord(ctx, material.ActiveKey())
				if err != nil {
					t.Fatal(err)
				}
				called := false
				if err = m.withAppliedCommit(ctx, view, func() error { called = true; return nil }); err != nil || !called {
					t.Fatal("restored view lost actual common proof", err)
				}
				assertPublicationClientFrozen(t, m, material.ActiveKey(), material.prepared.Sessions[0], executable)
			}
			if history, err := m.ProfileApplicationHistory(); err != nil || len(history) != 0 {
				t.Fatal("restoration fabricated a member native journal", history, err)
			}
			if history, err := m.snapshotReplayHistoryRecords(); err != nil || len(history) != 0 {
				t.Fatal("image projection fabricated a project replay journal", history, err)
			}
		})
	}
}

func TestPublicationRestorationRejectsUncommittedAndForeignMember(t *testing.T) {
	m, _, _, c, proof := nativePublicationFixture(t)
	ctx := context.Background()
	_, err := m.InspectPublicationRestoration(ctx, proof, workstore.ID(), workstore.Hash([]byte("uncommitted")))
	code(t, err, "APPLIED_VIEW_NOT_COMMITTED")
	if _, err = c.Apply(ctx, proof.Transaction.ID, proof.Transaction.Digest); err != nil {
		t.Fatal(err)
	}
	rollback, err := c.Reverse(proof.Transaction.ID)
	if err != nil {
		t.Fatal(err)
	}
	clone := *m
	s := *m.Source
	s.Copy = workstore.ID()
	clone.Source = &s
	_, err = clone.InspectPublicationRestoration(ctx, proof, rollback.ID, rollback.Digest)
	code(t, err, "PUBLICATION_RESTORE_INVALID")
}

func TestPublicationRestorationNoViewsStillRequiresOriginalSharedJournal(t *testing.T) {
	m, _, material, c, proof := nativePublicationFixture(t)
	ctx := context.Background()
	if _, err := material.Capture(ctx, proof); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Apply(ctx, proof.Transaction.ID, proof.Transaction.Digest); err != nil {
		t.Fatal(err)
	}
	rollback, err := c.Reverse(proof.Transaction.ID)
	if err != nil {
		t.Fatal(err)
	}
	review, err := m.InspectPublicationRestoration(ctx, proof, rollback.ID, rollback.Digest)
	if err != nil || len(review.Views()) != 0 {
		t.Fatal(review.Views(), err)
	}
	if err = review.Recheck(ctx); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(c.Local, "coordinated-transactions", "*", "journals", proof.Transaction.ID+".json"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	original, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(files[0]); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.WriteFile(files[0], original, 0600); err != nil {
			t.Error(err)
		}
	}()
	code(t, review.Recheck(ctx), "APPLIED_VIEW_NOT_COMMITTED")
	code(t, review.Validate(ctx, false), "APPLIED_VIEW_NOT_COMMITTED")
}
