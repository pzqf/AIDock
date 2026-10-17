package native

import (
	"context"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func TestReplayControlSafePreviewStalenessAndConsumedState(t *testing.T) {
	m, p := checkedReplayApplicationFixture(t)
	report, err := m.ReadSnapshotReplayPlan(context.Background(), p.ID, p.Digest)
	if err != nil || !report.CanApply || len(report.SourceChanges) == 0 {
		t.Fatal(report, err)
	}
	body := string(jsonData(report))
	if strings.Contains(body, "PRIVATE_") || strings.Contains(body, "SECRET_SENTINEL") {
		t.Fatal("public proposal disclosed body")
	}
	applySource(t, m.Source, source.Request{Action: "create", Name: "saved-after-joint-proposal", Description: "draft changed"})
	report, err = m.ReadSnapshotReplayPlan(context.Background(), p.ID, p.Digest)
	if err != nil || report.CanApply || report.Code == "" || len(report.SourceChanges)+len(report.LocalChanges)+len(report.NativeChanges) != 0 {
		t.Fatal("stale proposal kept success or differences", report, err)
	}
}

func TestReplayControlHistoryAndReverseHaveSafePublicResults(t *testing.T) {
	m, p := checkedReplayApplicationFixture(t)
	if _, err := m.ApplySnapshotReplay(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	consumed, err := m.ReadSnapshotReplayPlan(context.Background(), p.ID, p.Digest)
	if err != nil || consumed.CanApply || consumed.Code != "PLAN_CONSUMED" {
		t.Fatal(consumed, err)
	}
	rows, err := m.SnapshotReplayHistory()
	if err != nil || len(rows) != 1 || !rows[0].CanReverse {
		t.Fatal(rows, err)
	}
	r, err := m.ReverseSnapshotReplay(context.Background(), p.ID)
	if err != nil || !r.CanApply {
		t.Fatal(r, err)
	}
	if _, err = m.ApplySnapshotReplay(context.Background(), r.ID, r.Digest); err != nil {
		t.Fatal(err)
	}
	rows, err = m.SnapshotReplayHistory()
	if err != nil || len(rows) != 2 || !rows[0].CanReverse || rows[1].CanReverse {
		t.Fatal(rows, err)
	}
	if strings.Contains(string(jsonData(rows)), "PRIVATE_") {
		t.Fatal("history leaked body")
	}
	inspection, err := m.InspectSnapshotReplayRecovery()
	if err != nil || inspection.Pending {
		t.Fatal(inspection, err)
	}
	if n, err := m.RecoverSnapshotReplay(); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
