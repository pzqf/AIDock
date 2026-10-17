package native

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func replayTransactionFixture(t *testing.T) (*Manager, source.ReplayDraft, []workstore.BatchTarget) {
	t.Helper()
	m, id, _ := snapshotDraftFixture(t)
	write(t, filepath.Join(m.Root, ".codex", "private.json"), []byte("NATIVE_ONLY_PRIVATE_SIBLING_V077"))
	d, _, err := m.snapshotReplayCandidate(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	current, err := m.connectionStore().CaptureFile(filepath.Join(m.Root, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	after := workstore.Clone(current)
	if err = m.connectionStore().PutFile(&after, workstore.FileContent, []byte("# replay transaction fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	skill := workstore.Tree{Exists: true, Entries: map[string]workstore.Entry{}}
	if err = m.connectionStore().PutFile(&skill, "SKILL.md", []byte("new native fixture content"), 0644); err != nil {
		t.Fatal(err)
	}
	absent, err := m.connectionStore().Capture(filepath.Join(m.Root, ".claude", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	return m, d, []workstore.BatchTarget{{Path: ".codex/config.toml", File: true, Before: current, After: after}, {Path: ".claude/skills", Before: absent, After: skill}}
}

func TestReplayTransactionPreservesSiblingsAndReversesAllSavedRoots(t *testing.T) {
	m, d, native := replayTransactionFixture(t)
	initialSource, initialLocal := snapshotDraftTrees(t, m)
	originalNative := snapshotNativeTrees(t, m)
	targets, err := m.snapshotReplayTargets(d, native)
	if err != nil {
		t.Fatal(err)
	}
	c := m.snapshotReplayCoordinator()
	if len(targets) != 9 || len(c.Roots) != 9 {
		t.Fatal("incomplete fixed root set")
	}
	var codex workstore.BatchTarget
	for _, target := range targets {
		if target.Path == "native/.codex" {
			codex = target
		}
	}
	if codex.After.Entries["private.json"] != codex.Before.Entries["private.json"] {
		t.Fatal("unmanaged sibling changed")
	}
	p, err := c.PreviewObserved("apply", targets)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	s, l := snapshotDraftTrees(t, m)
	if workstore.Digest(s) != workstore.Digest(d.SourceAfter) || workstore.Digest(l) != workstore.Digest(d.LocalAfter) {
		t.Fatal("source and Local not jointly applied")
	}
	if body, err := os.ReadFile(filepath.Join(m.Root, ".codex", "private.json")); err != nil || string(body) != "NATIVE_ONLY_PRIVATE_SIBLING_V077" {
		t.Fatal("private sibling changed", err)
	}
	undo, err := c.Reverse(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(context.Background(), undo.ID, undo.Digest); err != nil {
		t.Fatal(err)
	}
	s, l = snapshotDraftTrees(t, m)
	if workstore.Digest(s) != workstore.Digest(initialSource) || workstore.Digest(l) != workstore.Digest(initialLocal) || !reflectTreesEqual(originalNative, snapshotNativeTrees(t, m)) {
		t.Fatal("joint undo did not restore original saved draft and native roots")
	}
	if _, err = os.Stat(filepath.Join(m.Root, ".claude")); !os.IsNotExist(err) {
		t.Fatal("new tool parent left behind", err)
	}
	err = filepath.WalkDir(filepath.Join(m.Source.Store.Local, "source-transactions"), func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		body, err := os.ReadFile(p)
		if err == nil && strings.Contains(string(body), "NATIVE_ONLY_PRIVATE_SIBLING_V077") {
			t.Fatal("native private body entered ordinary source blob cache")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReplayTransactionSecondTargetFailureRestoresEveryRoot(t *testing.T) {
	for _, failure := range []int{1, 5, 8} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			m, d, native := replayTransactionFixture(t)
			originalSource, originalLocal := snapshotDraftTrees(t, m)
			originalNative := snapshotNativeTrees(t, m)
			targets, err := m.snapshotReplayTargets(d, native)
			if err != nil {
				t.Fatal(err)
			}
			c := m.snapshotReplayCoordinator()
			p, err := c.PreviewObserved("apply", targets)
			if err != nil {
				t.Fatal(err)
			}
			called := false
			c.Hook = func(stage string, n int) error {
				if stage == "target" && n == failure {
					called = true
					return errors.New("second target fixture failure")
				}
				return nil
			}
			if _, err = c.Apply(context.Background(), p.ID, p.Digest); err == nil || !called {
				t.Fatal("failure not exercised", err)
			}
			s, l := snapshotDraftTrees(t, m)
			if workstore.Digest(s) != workstore.Digest(originalSource) || workstore.Digest(l) != workstore.Digest(originalLocal) || !reflectTreesEqual(originalNative, snapshotNativeTrees(t, m)) {
				t.Fatal("failure left partial roots")
			}
			if err = m.Source.Store.Pending(); err != nil {
				t.Fatal("successful recovery left barrier", err)
			}
		})
	}
}

func TestReplayTransactionExternalEditRetainsBarrierAndNeverSilentlyOverwrites(t *testing.T) {
	m, d, native := replayTransactionFixture(t)
	targets, err := m.snapshotReplayTargets(d, native)
	if err != nil {
		t.Fatal(err)
	}
	c := m.snapshotReplayCoordinator()
	p, err := c.PreviewObserved("apply", targets)
	if err != nil {
		t.Fatal(err)
	}
	edited := filepath.Join(m.Source.Store.Root, "profile.yaml")
	c.Hook = func(stage string, n int) error {
		if stage == "target" && n == 1 {
			write(t, edited, []byte("PRIVATE_MANUAL_CONFLICT"))
			return errors.New("fixture external edit")
		}
		return nil
	}
	if _, err = c.Apply(context.Background(), p.ID, p.Digest); err == nil {
		t.Fatal("conflict did not block")
	}
	if err = m.Source.Store.Pending(); err == nil {
		t.Fatal("conflicting recovery lost barrier")
	}
	if body, err := os.ReadFile(edited); err != nil || string(body) != "PRIVATE_MANUAL_CONFLICT" {
		t.Fatal("external edit was overwritten", err)
	}
	c.Hook = nil
	if _, err = c.Recover(); err == nil {
		t.Fatal("ordinary recovery silently accepted conflicting bytes")
	}
	// Explicitly restore a known side after separately preserving the manual
	// fixture bytes. This models the documented operator reconciliation step.
	known, err := m.Source.Store.Data(d.SourceAfter.Entries["profile.yaml"])
	if err != nil {
		t.Fatal(err)
	}
	write(t, edited, known)
	if _, err = c.Recover(); err != nil {
		t.Fatal(err)
	}
	s, l := snapshotDraftTrees(t, m)
	if workstore.Digest(s) != workstore.Digest(d.SourceBefore) || workstore.Digest(l) != workstore.Digest(d.LocalBefore) {
		t.Fatal("reconciled recovery did not restore original saved drafts")
	}
}

func TestReplayTransactionRejectsStaleAndUnregisteredNativeTargets(t *testing.T) {
	m, d, native := replayTransactionFixture(t)
	native[0].Before = workstore.Clone(native[0].After)
	if _, err := m.snapshotReplayTargets(d, native); err == nil {
		t.Fatal("stale before accepted")
	}
	native[0].Path = ".codex/private.json"
	if _, err := m.snapshotReplayTargets(d, native); err == nil {
		t.Fatal("arbitrary private sibling accepted as compiler target")
	}
}

func TestReplayArchiveIntentBecomesCommittedOnlyThroughItsCommonJournal(t *testing.T) {
	m, d, native := replayTransactionFixture(t)
	targets, err := m.snapshotReplayTargets(d, native)
	if err != nil {
		t.Fatal(err)
	}
	c := m.snapshotReplayCoordinator()
	tx, err := c.PreviewObserved("apply", targets)
	if err != nil {
		t.Fatal(err)
	}
	nativeTx := workstore.BatchPlan{Version: 1, Kind: "native-targets", ID: tx.ID, Root: m.Root, Identity: "profile/" + m.Source.Owner + "/" + m.Source.Scope + "/" + m.Source.Copy, Action: "apply", Targets: native}
	nativeTx.Digest = workstore.BatchPlanDigest(nativeTx)
	a, err := m.projectSnapshotArchive()
	if err != nil {
		t.Fatal(err)
	}
	var record projectsnapshot.Record
	err = m.Source.WithReplaySnapshotInputs(context.Background(), d, func(bundle source.Export, tree workstore.Tree, cfg source.SnapshotConfiguration) error {
		var e error
		record, e = a.Capture(context.Background(), projectsnapshot.Content{Trigger: "aidock replay", Source: tree, Effective: bundle, Configuration: &cfg, ActiveKey: strings.Repeat("b", 64), Adapters: map[string]string{"codex-cli": "0.3.0", "claude-code": "0.3.0"}, Transaction: nativeTx, Coordinated: &tx}, m.Source.Store, m.connectionStore())
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	checked, err := m.CheckProjectSnapshot(context.Background(), record.ID)
	if err != nil || checked.TransactionState != "unknown" {
		t.Fatal("archive intent became commit authority", checked, err)
	}
	// Exercise the private transaction substrate. No public replay apply API is
	// exposed until qualification and policy validation are bound to this plan.
	if _, err = c.Apply(context.Background(), tx.ID, tx.Digest); err != nil {
		t.Fatal(err)
	}
	checked, err = m.CheckProjectSnapshot(context.Background(), record.ID)
	if err != nil || checked.TransactionState != "committed" {
		t.Fatal("matching common commit not recognized", checked, err)
	}
	changed := tx
	changed.Digest = strings.Repeat("0", 64)
	if _, err = snapshotState(record, []workstore.BatchJournal{{Plan: changed, State: "committed"}}); err == nil {
		t.Fatal("different common journal accepted")
	}
}
