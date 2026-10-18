package native

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func nativePublicationFixture(t *testing.T) (*Manager, source.ConfigurationCandidate, ConfigurationPublicationMaterial, *workstore.Coordinator, projectsnapshot.PublicationCommit) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("local Git unavailable")
	}
	m, candidate, executable, home := nativeConfigurationFixture(t)
	material, coordinator, proof := compilePublicationFixture(t, m, candidate, executable, home)
	return m, candidate, material, coordinator, proof
}

func compilePublicationFixture(t *testing.T, m *Manager, candidate source.ConfigurationCandidate, executable, home string) (ConfigurationPublicationMaterial, *workstore.Coordinator, projectsnapshot.PublicationCommit) {
	t.Helper()
	fakeProbes(m, discoveredGuide)
	m.probe.rules = fakeRuleLoad
	checked, err := m.PrepareConfigurationCandidate(context.Background(), candidate, CheckRequest{Tools: []string{"codex-cli"}, Probe: true, RuleProbe: true}, executable, home)
	if err != nil || checked.Status != "CHECKED" {
		t.Fatal(checked.Status, err)
	}
	binding := projectsnapshot.Binding{Project: m.Source.Owner, Scope: m.Source.Scope, Copy: m.Source.Copy, SourceRoot: m.Source.Store.Root, NativeRoot: m.Root}
	publisher := binding
	publisher.Copy, publisher.NativeRoot = workstore.ID(), t.TempDir()
	publisher.SourceRoot = filepath.Join(publisher.NativeRoot, ".aidock")
	id := workstore.ID()
	coordinator, err := projectsnapshot.PublicationCoordinator(m.Source.Store.Local, m.Source.Owner, m.Source.Scope, id, []projectsnapshot.Binding{binding})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(coordinator.Blobs.Root), 0700); err != nil {
		t.Fatal(err)
	}
	material, err := m.CompileConfigurationPublication(context.Background(), candidate, checked.ID, checked.Digest, id, executable, home, coordinator.Blobs)
	if err != nil {
		t.Fatal(err)
	}
	before, err := coordinator.Blobs.Capture(coordinator.Blobs.Root)
	if err != nil {
		t.Fatal(err)
	}
	targets := []workstore.BatchTarget{{Path: "control", Before: before, After: workstore.Clone(before)}}
	for _, target := range material.Targets() {
		target.Path = projectsnapshot.PublicationMemberPrefix(binding.Copy) + target.Path
		targets = append(targets, target)
	}
	tx, err := coordinator.PreviewObserved("apply", targets)
	if err != nil {
		t.Fatal(err)
	}
	proof := projectsnapshot.PublicationCommit{Version: 1, Publication: id, Publisher: publisher, Members: []projectsnapshot.Binding{binding}, Transaction: tx}
	return material, coordinator, proof
}

func TestConfigurationPublicationMaterialUsesRealGlobalJournalAndFrozenView(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "recover"}[fail], func(t *testing.T) {
			m, _, material, coordinator, proof := nativePublicationFixture(t)
			ctx := context.Background()
			if strings.TrimSpace(string(jsonData(material))) != "{}" {
				t.Fatal("opaque material exposes native or source fields")
			}
			beforeSource, beforeLocal := snapshotDraftTrees(t, m)
			beforeNative := snapshotNativeTrees(t, m)
			r, err := material.Capture(ctx, proof)
			if err != nil || r.Version != 5 || r.Publication == nil || r.Coordinated != nil {
				t.Fatal("did not archive actual common proof", err)
			}
			_, err = m.loadAppliedRecord(ctx, material.ActiveKey())
			code(t, err, "APPLIED_VIEW_NOT_COMMITTED")
			if history, err := m.ProfileApplicationHistory(); err != nil || len(history) != 0 {
				t.Fatal("a member journal was fabricated", history, err)
			}
			if history, err := m.snapshotReplayHistoryRecords(); err != nil || len(history) != 0 {
				t.Fatal("a replay journal was fabricated", history, err)
			}
			coordinator.Validate = func(workstore.BatchPlan) error { return material.Validate(ctx, false) }
			coordinator.BeforeCommit = func(workstore.BatchPlan) error { return material.Validate(ctx, true) }
			if fail {
				coordinator.Hook = func(stage string, n int) error {
					if stage == "target" && n == 2 {
						return errors.New("owned global publication failure")
					}
					return nil
				}
			}
			err = source.WithConfigurationTransactionLocks([]*source.Manager{m.Source}, []string{source.LocalProfileRoot(m.Source.Store.Local, m.Source.Owner, m.Source.Scope, m.Source.Copy)}, func() error {
				_, err := coordinator.Apply(ctx, proof.Transaction.ID, proof.Transaction.Digest)
				return err
			})
			if (err != nil) != fail {
				t.Fatal(err)
			}
			list, err := m.ProjectSnapshots(ctx)
			if err != nil || len(list.Items) != 1 {
				t.Fatal("global snapshot not visible in project history", err)
			}
			if list.Items[0].Publication != proof.Publication || list.Items[0].PublicationMembers != 1 {
				t.Fatal("public snapshot omitted real common batch identity")
			}
			if fail {
				if list.Items[0].TransactionState != "recovered" {
					t.Fatal(list.Items[0].TransactionState)
				}
				_, err = m.loadAppliedRecord(ctx, material.ActiveKey())
				code(t, err, "APPLIED_VIEW_NOT_COMMITTED")
				s, l := snapshotDraftTrees(t, m)
				if workstore.Digest(s) != workstore.Digest(beforeSource) || workstore.Digest(l) != workstore.Digest(beforeLocal) || !reflectTreesEqual(beforeNative, snapshotNativeTrees(t, m)) {
					t.Fatal("shared fault recovery left source/Local/native changes")
				}
				return
			}
			if list.Items[0].TransactionState != "committed" {
				t.Fatal(list.Items[0].TransactionState)
			}
			verified, err := m.CheckProjectSnapshot(ctx, r.ID)
			if err != nil || verified.TransactionState != "committed" || verified.Publication != proof.Publication {
				t.Fatal("byte verification lost the common commit state", err)
			}
			assessed, err := m.AssessProjectSnapshot(ctx, r.ID)
			if err != nil || assessed.Snapshot.TransactionState != "committed" || assessed.Snapshot.Publication != proof.Publication {
				t.Fatal("historical assessment lost the common commit state", err)
			}
			view, err := m.loadAppliedRecord(ctx, material.ActiveKey())
			if err != nil || view.Record.ID != r.ID {
				t.Fatal("real common commit did not authorize its frozen view", err)
			}
			called := false
			if err := m.withAppliedCommit(ctx, view, func() error { called = true; return nil }); err != nil || !called {
				t.Fatal("could not check global journal under the native lock", err)
			}
			if err := m.profileApplicationBatch().WithCurrent(func() error { return m.appliedCommitLocked(view) }); err != nil {
				t.Fatal("locked restoration used an independent member journal instead of the real common commit", err)
			}
			// A later saved draft is not the earlier fixed application input.
			state, err := m.Source.Profile()
			if err != nil {
				t.Fatal(err)
			}
			state.Profile["rules"] = []any{"LATER_SAVED_DRAFT_RULE"}
			p, err := m.Source.ProfilePlan(ctx, source.ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.Source.Apply(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
				t.Fatal(err)
			}
			view, err = m.loadAppliedRecord(ctx, material.ActiveKey())
			if err != nil || !strings.Contains(string(jsonData(view.Frozen.Effective.Rules)), "FUTURE_PRIVATE_RULE") || strings.Contains(string(jsonData(view.Frozen.Effective.Rules)), "LATER_SAVED_DRAFT_RULE") {
				t.Fatal("later draft replaced the frozen application", err)
			}
			journals, err := filepath.Glob(filepath.Join(coordinator.Local, "coordinated-transactions", "*", "journals", proof.Transaction.ID+".json"))
			if err != nil || len(journals) != 1 {
				t.Fatal(journals, err)
			}
			original, err := os.ReadFile(journals[0])
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Remove(journals[0]); err != nil {
				t.Fatal(err)
			}
			_, missing := m.loadAppliedRecord(ctx, material.ActiveKey())
			missingLocked := m.profileApplicationBatch().WithCurrent(func() error { return m.appliedCommitLocked(view) })
			if err := os.WriteFile(journals[0], original, 0600); err != nil {
				t.Fatal(err)
			}
			code(t, missing, "APPLIED_VIEW_NOT_COMMITTED")
			code(t, missingLocked, "APPLIED_VIEW_NOT_COMMITTED")
		})
	}
}

func TestConfigurationPublicationCannotUseStaticStaleOrAlteredMemberMaterial(t *testing.T) {
	m, c, material, coordinator, proof := nativePublicationFixture(t)
	ctx := context.Background()
	static, err := m.PrepareConfigurationCandidate(ctx, c, CheckRequest{Tools: []string{"codex-cli"}}, material.prepared.Executable, material.prepared.Home)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.CompileConfigurationPublication(ctx, c, static.ID, static.Digest, proof.Publication, material.prepared.Executable, material.prepared.Home, coordinator.Blobs)
	code(t, err, "CONFIGURATION_CHECK_REQUIRED")
	altered := material.Targets()
	if err := coordinator.Blobs.PutFile(&altered[0].After, "foreign.txt", []byte("not selected by original compiler"), 0644); err != nil {
		t.Fatal(err)
	}
	if workstore.Digest(altered[0].After) == workstore.Digest(material.Targets()[0].After) {
		t.Fatal("opaque material shares caller-mutable maps")
	}
	for i := range proof.Transaction.Targets {
		if proof.Transaction.Targets[i].Path == projectsnapshot.PublicationMemberPrefix(m.Source.Copy)+"source" {
			proof.Transaction.Targets[i].After = altered[0].After
		}
	}
	proof.Transaction.Digest = workstore.BatchPlanDigest(proof.Transaction)
	_, err = material.Capture(ctx, proof)
	code(t, err, "PUBLICATION_COMMIT_INVALID")
	write(t, filepath.Join(m.Root, "AGENTS.md"), []byte("retain manual change after full check"))
	_, err = m.CompileConfigurationPublication(ctx, c, material.prepared.Report.ID, material.prepared.Report.Digest, proof.Publication, material.prepared.Executable, material.prepared.Home, coordinator.Blobs)
	code(t, err, "CONFIGURATION_CHECK_REQUIRED")
}
