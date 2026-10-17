package native

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func TestReplayReverseAndUndoRestoreAppliedResultAndSeparateSavedDraft(t *testing.T) {
	m, p := checkedReplayApplicationFixture(t)
	s0, l0 := snapshotDraftTrees(t, m)
	n0 := snapshotNativeTrees(t, m)
	if _, err := m.applySnapshotReplayApplication(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	s1, l1 := snapshotDraftTrees(t, m)
	n1 := snapshotNativeTrees(t, m)
	write(t, filepath.Join(m.Source.Store.Root, "knowledge", "after-apply.md"), []byte("NEW_MEMORY_MUST_SURVIVE"))
	reverse, err := m.reverseSnapshotReplay(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.applySnapshotReplayReverse(context.Background(), reverse.Transaction.ID, reverse.Digest); err != nil {
		t.Fatal(err)
	}
	s, l := snapshotDraftTrees(t, m)
	// New unrelated memory is preserved through both directions.
	delete(s.Entries, "knowledge/after-apply.md")
	if workstore.Digest(s) != workstore.Digest(s0) || workstore.Digest(l) != workstore.Digest(l0) || !reflectTreesEqual(n0, snapshotNativeTrees(t, m)) {
		t.Fatal("rollback did not restore original saved draft and applied native result")
	}
	undo, err := m.reverseSnapshotReplay(context.Background(), reverse.Transaction.ID)
	if err != nil {
		t.Fatal(err)
	}
	if undo.Transaction.Action != "undo-rollback" {
		t.Fatal(undo.Transaction.Action)
	}
	if _, err = m.applySnapshotReplayReverse(context.Background(), undo.Transaction.ID, undo.Digest); err != nil {
		t.Fatal(err)
	}
	s, l = snapshotDraftTrees(t, m)
	delete(s.Entries, "knowledge/after-apply.md")
	if workstore.Digest(s) != workstore.Digest(s1) || workstore.Digest(l) != workstore.Digest(l1) || !reflectTreesEqual(n1, snapshotNativeTrees(t, m)) {
		t.Fatal("undo did not restore common applied state")
	}
	if b, err := os.ReadFile(filepath.Join(m.Source.Store.Root, "knowledge/after-apply.md")); err != nil || string(b) != "NEW_MEMORY_MUST_SURVIVE" {
		t.Fatal("new memory reverted", err)
	}
}

func TestReplayReverseFailureRestoresCurrentCommonState(t *testing.T) {
	m, p := checkedReplayApplicationFixture(t)
	if _, err := m.applySnapshotReplayApplication(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	beforeSource, beforeLocal := snapshotDraftTrees(t, m)
	beforeNative := snapshotNativeTrees(t, m)
	reverse, err := m.reverseSnapshotReplay(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	m.snapshotReplayHook = func(stage string, n int) error {
		if stage == "target" && n == 5 {
			return errors.New("reverse failure")
		}
		return nil
	}
	if _, err = m.applySnapshotReplayReverse(context.Background(), reverse.Transaction.ID, reverse.Digest); err == nil {
		t.Fatal("fault did not stop reverse")
	}
	s, l := snapshotDraftTrees(t, m)
	if workstore.Digest(s) != workstore.Digest(beforeSource) || workstore.Digest(l) != workstore.Digest(beforeLocal) || !reflectTreesEqual(beforeNative, snapshotNativeTrees(t, m)) {
		t.Fatal("reverse failure left partial roots")
	}
}
