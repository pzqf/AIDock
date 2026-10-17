package native

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func checkedReplayApplicationFixture(t *testing.T) (*Manager, snapshotReplayApplication) {
	t.Helper()
	m, req, exe, home := replayPreparationFixture(t)
	req.Preparation.Probe, req.Preparation.RuleProbe = true, true
	prepared, err := m.PrepareSnapshotReplay(context.Background(), req, exe, home)
	if err != nil || prepared.Status != "CHECKED" {
		t.Fatal(prepared.Status, err)
	}
	p, err := m.planSnapshotReplayApplication(context.Background(), prepared.ID, prepared.Digest, exe, home)
	if err != nil {
		t.Fatal(err)
	}
	return m, p
}

func TestReplayApplicationCommitsProjectedSourceLocalAndNativeWithOriginalDraftBackups(t *testing.T) {
	m, p := checkedReplayApplicationFixture(t)
	beforeSource, beforeLocal := snapshotDraftTrees(t, m)
	if workstore.Digest(beforeSource) != workstore.Digest(p.Transaction.Targets[0].Before) || workstore.Digest(beforeLocal) != workstore.Digest(p.Transaction.Targets[1].Before) {
		t.Fatal("proposal did not preserve actual saved drafts")
	}
	m.probe.version = func(context.Context, toolBinary, string, string, string) (string, error) {
		t.Fatal("apply ran another tool probe")
		return "", nil
	}
	j, err := m.applySnapshotReplayApplication(context.Background(), p.ID, p.Digest)
	if err != nil || j.State != "committed" {
		t.Fatal(j.State, err)
	}
	s, l := snapshotDraftTrees(t, m)
	expected, _ := replayApplicationTrees(p, true)
	if workstore.Digest(s) != workstore.Digest(expected) || workstore.Digest(l) != workstore.Digest(p.Frozen.LocalTree) {
		t.Fatal("complete projected roots not committed")
	}
	applied, err := m.loadAppliedRecord(context.Background(), p.ActiveKey)
	if err != nil || applied.Record.Coordinated == nil {
		t.Fatal("committed common view cannot be read by runtime owner", err)
	}
	if workstore.Digest(applied.Record.Coordinated.Targets[0].Before) != workstore.Digest(beforeSource) {
		t.Fatal("original saved draft lost in private archive")
	}
	if _, err = m.applySnapshotReplayApplication(context.Background(), p.ID, p.Digest); err == nil {
		t.Fatal("consumed proposal reapplied")
	}
}

func TestReplayApplicationFailureRestoresOriginalDraftsAndDoesNotCreateAppliedAuthority(t *testing.T) {
	m, p := checkedReplayApplicationFixture(t)
	beforeSource, beforeLocal := snapshotDraftTrees(t, m)
	beforeNative := snapshotNativeTrees(t, m)
	called := false
	m.snapshotReplayHook = func(stage string, n int) error {
		if stage == "target" && n == 5 {
			called = true
			return errors.New("joint application failure")
		}
		return nil
	}
	if _, err := m.applySnapshotReplayApplication(context.Background(), p.ID, p.Digest); err == nil || !called {
		t.Fatal("failure not exercised", err)
	}
	s, l := snapshotDraftTrees(t, m)
	if workstore.Digest(s) != workstore.Digest(beforeSource) || workstore.Digest(l) != workstore.Digest(beforeLocal) || !reflectTreesEqual(beforeNative, snapshotNativeTrees(t, m)) {
		t.Fatal("failure lost original draft or left partial native state")
	}
	if _, err := m.loadAppliedRecord(context.Background(), p.ActiveKey); err == nil {
		t.Fatal("recovered intent became runtime authority")
	}
}

func TestReplayApplicationRejectsChangedSavedDraftAndWrongConfirmation(t *testing.T) {
	m, p := checkedReplayApplicationFixture(t)
	if _, err := m.applySnapshotReplayApplication(context.Background(), p.ID, strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong confirmation accepted")
	}
	applySource(t, m.Source, source.Request{Action: "create", Name: "after-preview", Description: "saved after preview"})
	beforeSource, beforeLocal := snapshotDraftTrees(t, m)
	beforeNative := snapshotNativeTrees(t, m)
	if _, err := m.applySnapshotReplayApplication(context.Background(), p.ID, p.Digest); err == nil {
		t.Fatal("changed saved draft accepted")
	}
	s, l := snapshotDraftTrees(t, m)
	if workstore.Digest(s) != workstore.Digest(beforeSource) || workstore.Digest(l) != workstore.Digest(beforeLocal) || !reflectTreesEqual(beforeNative, snapshotNativeTrees(t, m)) {
		t.Fatal("stale proposal changed roots")
	}
}
