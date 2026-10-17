package native

import (
	"context"
	"strings"
	"testing"
)

func TestAppliedViewDistinguishesSavedDraftWithoutChangingNative(t *testing.T) {
	m, id, _ := snapshotDraftFixture(t)
	before := snapshotNativeTrees(t, m)
	sourceBefore, localBefore := snapshotDraftTrees(t, m)
	report, err := m.AppliedViews(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.DraftObservation != "OBSERVED" || report.Recovery || len(report.Items) != 1 {
		t.Fatal(report)
	}
	item := report.Items[0]
	if item.Snapshot != id || item.Mode != "FROZEN" || item.SourceDifference != "DIFFERENT" || item.LocalDifference != "DIFFERENT" || len(item.Binding) != 64 {
		t.Fatal(item)
	}
	for _, target := range item.Targets {
		if target.Status != "MATCH" {
			t.Fatal(target)
		}
	}
	body := string(jsonData(report))
	if strings.Contains(body, "PRIVATE_") || strings.Contains(body, "SKILL.md") {
		t.Fatal("applied status leaked source bodies")
	}
	if !reflectTreesEqual(before, snapshotNativeTrees(t, m)) {
		t.Fatal("status changed native")
	}
	s, l := snapshotDraftTrees(t, m)
	if string(jsonData(s)) != string(jsonData(sourceBefore)) || string(jsonData(l)) != string(jsonData(localBefore)) {
		t.Fatal("status changed saved draft")
	}
}
