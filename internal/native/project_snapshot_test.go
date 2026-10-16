package native

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestProjectSnapshotMissingTransactionIsUnknown(t *testing.T) {
	state, err := snapshotState(projectsnapshot.Record{}, nil)
	if err != nil || state != "unknown" {
		t.Fatal("missing original journal cannot prove that submission never started", state, err)
	}
}

func TestProjectSnapshotCannotLinkModifiedTransactionWithCopiedDigest(t *testing.T) {
	p := workstore.BatchPlan{ID: workstore.ID(), Digest: strings.Repeat("a", 64), Action: "apply"}
	r := projectsnapshot.Record{Content: projectsnapshot.Content{Transaction: p}}
	r.Transaction.Action = "rollback"
	if _, err := snapshotState(r, []workstore.BatchJournal{{Plan: p, State: "committed"}}); err == nil {
		t.Fatal("copied digest linked a different transaction body")
	}
}

func TestProjectSnapshotPrerequisitesBlockReadinessBeforeConfirmation(t *testing.T) {
	m, _ := writableFixture(t)
	fakeProbes(m, discoveredGuide)
	executable := filepath.Join(t.TempDir(), "aidock.exe")
	write(t, executable, []byte("fixed host"))
	home := filepath.Dir(m.Source.Store.Local)
	check := func(want string) {
		r, err := m.PrepareProfile(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true}, executable, home)
		if err != nil || r.Status != "NEEDS_ATTENTION" || r.Plan != nil {
			t.Fatal(r.Status, err)
		}
		found := false
		for _, gate := range r.Gates {
			found = found || gate.Code == want
		}
		if !found {
			t.Fatal("missing preparation gate", want, r.Gates)
		}
	}
	check("SNAPSHOT_BINDING_REQUIRED")
	m.Source.Copy = "22222222-2222-4222-8222-222222222222"
	t.Setenv("PATH", t.TempDir())
	check("SNAPSHOT_GIT_UNAVAILABLE")
}

func TestProjectSnapshotsLinkPriorAppliedSourceAndKeepDraftHistorical(t *testing.T) {
	m, id := writableFixture(t)
	m.Source.Copy = "22222222-2222-4222-8222-222222222222"
	fakeProbes(m, discoveredGuide)
	executable := filepath.Join(t.TempDir(), "aidock.exe")
	write(t, executable, []byte("fixed unexecuted host"))
	home := filepath.Dir(m.Source.Store.Local)
	prepare := func() ProfileApplicationPlan {
		r, err := m.PrepareProfile(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true}, executable, home)
		if err != nil || r.Status != "READY" || r.Plan == nil {
			t.Fatal(r.Status, err)
		}
		return *r.Plan
	}
	first := prepare()
	if _, err := m.ApplyProfileApplication(context.Background(), first.ID, first.Digest); err != nil {
		t.Fatal(err)
	}
	rows, err := m.ProjectSnapshots(context.Background())
	if err != nil || len(rows.Items) != 1 || rows.Items[0].TransactionState != "committed" || rows.Items[0].Integrity != "NOT_CHECKED" {
		t.Fatal(rows, err)
	}
	firstSnapshot := rows.Items[0].ID
	applySource(t, m.Source, source.Request{Action: "edit", ID: id, Files: []source.Patch{{Path: "SKILL.md", Content: "---\nname: guide\ndescription: example\n---\nSNAPSHOT_PRIVATE_SKILL_BODY"}}})
	second := prepare()
	if _, err = m.ApplyProfileApplication(context.Background(), second.ID, second.Digest); err != nil {
		t.Fatal(err)
	}
	rows, err = m.ProjectSnapshots(context.Background())
	if err != nil || len(rows.Items) != 2 || rows.Items[0].PreviousApplied != firstSnapshot {
		t.Fatal(rows, err)
	}
	if strings.Contains(string(jsonData(rows)), "SNAPSHOT_PRIVATE_SKILL_BODY") {
		t.Fatal("snapshot list disclosed source body")
	}
	reverse, err := m.ReverseProfileApplication(context.Background(), second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyProfileApplicationReverse(context.Background(), reverse.ID, reverse.Digest); err != nil {
		t.Fatal(err)
	}
	checked, err := m.CheckProjectSnapshot(context.Background(), rows.Items[0].ID)
	if err != nil || checked.Integrity != "PASS_ARCHIVED_BYTES" || checked.Replay != "NOT_IMPLEMENTED" || checked.TransactionState != "committed" {
		t.Fatal(checked, err)
	}
	state, err := m.Source.State()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(m.Source.Store.Root, state.Index.Skills[0].Source.Path, "SKILL.md"))
	if err != nil || !strings.Contains(string(body), "SNAPSHOT_PRIVATE_SKILL_BODY") {
		t.Fatal("native rollback unexpectedly reverted saved draft")
	}
	if checked.ConfigurationInputs != "CAPTURED_ORIGINAL_LAYERS" {
		t.Fatal("new application omitted configuration originals")
	}
	// Assessment reconstructs old bytes even when today's saved draft cannot
	// parse, and never exposes rule/local/configuration/native file bodies.
	write(t, filepath.Join(m.Source.Store.Root, "profile.yaml"), []byte("rules: [broken PRIVATE_CURRENT_VALUE"))
	before := configurationTree(t, m.Root)
	assessment, err := m.AssessProjectSnapshot(context.Background(), firstSnapshot)
	if err != nil || assessment.Reconstruction != "PASS_FROZEN_INPUTS" || assessment.Snapshot.Integrity != "PASS_ARCHIVED_BYTES" {
		t.Fatal(assessment, err)
	}
	if !reflectTreesEqual(before, configurationTree(t, m.Root)) || strings.Contains(string(jsonData(assessment)), "PRIVATE_") || strings.Contains(string(jsonData(assessment)), "effective_profile") {
		t.Fatal("assessment changed live files or disclosed bodies")
	}
	a, err := m.projectSnapshotArchive()
	if err != nil {
		t.Fatal(err)
	}
	original, err := a.Load(context.Background(), firstSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	legacy := original.Content
	legacy.Configuration = nil
	old, err := a.Capture(context.Background(), legacy, m.Source.Store, m.connectionStore())
	if err != nil {
		t.Fatal(err)
	}
	meta, err := m.CheckProjectSnapshot(context.Background(), old.ID)
	if err != nil || meta.ConfigurationInputs != "LEGACY_MISSING" {
		t.Fatal(meta, err)
	}
	if _, err = m.AssessProjectSnapshot(context.Background(), old.ID); err == nil || !strings.Contains(err.Error(), "未保存原始分层输入") {
		t.Fatal("legacy snapshot fabricated original configuration", err)
	}
}

func TestProjectSnapshotsFailedApplicationIsRecoveredIntent(t *testing.T) {
	m, _ := writableFixture(t)
	m.Source.Copy = "22222222-2222-4222-8222-222222222222"
	fakeProbes(m, discoveredGuide)
	executable := filepath.Join(t.TempDir(), "aidock.exe")
	write(t, executable, []byte("fixed unexecuted host"))
	before := configurationTree(t, m.Root)
	r, err := m.PrepareProfile(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true}, executable, filepath.Dir(m.Source.Store.Local))
	if err != nil || r.Plan == nil {
		t.Fatal(err)
	}
	m.applicationHook = func(stage string, n int) error {
		if stage == "target" && n == 1 {
			return errors.New("injected snapshot-linked application failure")
		}
		return nil
	}
	if _, err = m.ApplyProfileApplication(context.Background(), r.Plan.ID, r.Plan.Digest); err == nil {
		t.Fatal("missing injected failure")
	}
	if !reflectTreesEqual(before, configurationTree(t, m.Root)) {
		t.Fatal("failed application changed targets")
	}
	rows, err := m.ProjectSnapshots(context.Background())
	if err != nil || len(rows.Items) != 1 || rows.Items[0].TransactionState != "recovered" {
		t.Fatal(rows, err)
	}
}
