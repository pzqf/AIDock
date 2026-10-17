package native

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func changeIdentityField(t *testing.T, path, key, value string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields[key] = value
	body := jsonData(fields)
	write(t, path, body)
	return body
}

func TestReplayReverseNeverTakesOverChangedProjectScopeOrCopy(t *testing.T) {
	for _, kind := range []string{"project", "scope", "copy"} {
		t.Run(kind, func(t *testing.T) {
			m, p := checkedReplayApplicationFixture(t)
			if _, err := m.ApplySnapshotReplay(context.Background(), p.ID, p.Digest); err != nil {
				t.Fatal(err)
			}
			path, key, value := filepath.Join(m.Source.Store.Root, "project.yaml"), "id", "33333333-3333-4333-8333-333333333333"
			if kind == "scope" {
				path, key, value = filepath.Join(m.Source.Store.Root, "skills.yaml"), "scope_id", "foreign"
			}
			if kind == "copy" {
				path, key = filepath.Join(source.LocalProfileRoot(m.Source.Store.Local, m.Source.Owner, m.Source.Scope, m.Source.Copy), "binding.json"), "copy"
			}
			body := changeIdentityField(t, path, key, value)
			before := snapshotNativeTrees(t, m)
			report, err := m.ReverseSnapshotReplay(context.Background(), p.ID)
			if err == nil && report.CanApply {
				t.Fatal("foreign identity got executable reversal")
			}
			actual, _ := os.ReadFile(path)
			if string(actual) != string(body) || !reflectTreesEqual(before, snapshotNativeTrees(t, m)) {
				t.Fatal("foreign identity preview changed files")
			}
		})
	}
}

func TestReplayRecoveryForeignIdentityConflictIsObservedWithoutCachingOrOverwrite(t *testing.T) {
	m, p := checkedReplayApplicationFixture(t)
	s0, l0 := snapshotDraftTrees(t, m)
	n0 := snapshotNativeTrees(t, m)
	identity := filepath.Join(m.Source.Store.Root, "project.yaml")
	changed := false
	m.snapshotReplayHook = func(stage string, n int) error {
		if stage == "target" && n == 1 {
			changed = true
			changeIdentityField(t, identity, "id", "33333333-3333-4333-8333-333333333333")
			return errors.New("external identity changed")
		}
		return nil
	}
	if _, err := m.ApplySnapshotReplay(context.Background(), p.ID, p.Digest); err == nil || !changed {
		t.Fatal("foreign identity fault not exercised", err)
	}
	beforeLocal := configurationTree(t, m.Source.Store.Local)
	inspection, err := m.InspectSnapshotReplayRecovery()
	if err != nil || !inspection.Pending || inspection.Status != "CONFLICT" {
		t.Fatal(inspection, err)
	}
	if !reflectTreesEqual(beforeLocal, configurationTree(t, m.Source.Store.Local)) {
		t.Fatal("read-only recovery inspection stored current body")
	}
	m.snapshotReplayHook = nil
	if _, err = m.RecoverSnapshotReplay(); err == nil {
		t.Fatal("recovery overwrote foreign project ownership")
	}
	known, _ := replayApplicationTrees(p, true)
	body, err := m.Source.Store.Data(known.Entries["project.yaml"])
	if err != nil {
		t.Fatal(err)
	}
	write(t, identity, body)
	if _, err = m.RecoverSnapshotReplay(); err != nil {
		t.Fatal(err)
	}
	s, l := snapshotDraftTrees(t, m)
	if workstore.Digest(s) != workstore.Digest(s0) || workstore.Digest(l) != workstore.Digest(l0) || !reflectTreesEqual(n0, snapshotNativeTrees(t, m)) {
		t.Fatal("reconciled recovery did not restore original roots")
	}
}
