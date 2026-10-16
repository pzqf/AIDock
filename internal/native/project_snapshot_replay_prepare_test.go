package native

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func replayPreparationFixture(t *testing.T) (*Manager, SnapshotReplayPreparationRequest, string, string) {
	t.Helper()
	m, _, _ := snapshotDraftFixture(t)
	saveRules(t, m, []any{"FROZEN_PRIVATE_RULE"})
	fakeProbes(m, discoveredGuide)
	m.probe.rules = fakeRuleLoad
	executable := filepath.Join(t.TempDir(), "aidock.exe")
	write(t, executable, []byte("fixed replay test program"))
	home := filepath.Dir(m.Source.Store.Local)
	p, err := m.PrepareProfile(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true, RuleProbe: true}, executable, home)
	if err != nil || p.Plan == nil {
		t.Fatal(p.Status, err)
	}
	if _, err = m.ApplyProfileApplication(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	rows, err := m.ProjectSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id := rows.Items[0].ID
	applySource(t, m.Source, source.Request{Action: "edit", ID: p.NativePlan.Source.Packages[0].Skill.ID, Files: []source.Patch{{Path: "SKILL.md", Content: "---\nname: guide\ndescription: example\n---\nLATER_PRIVATE_DRAFT"}}})
	saveSnapshotLocal(t, m, "LATER_PRIVATE_LOCAL_DRAFT")
	body, err := os.ReadFile(filepath.Join(m.Root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(m.Root, "AGENTS.md"), append([]byte("# CURRENT_NATIVE_SECRET_SENTINEL\n"), body...))
	r, err := m.ReviewSnapshotReplayPermissions(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return m, SnapshotReplayPreparationRequest{SnapshotReplayCandidateRequest: SnapshotReplayCandidateRequest{Snapshot: id, Candidate: r.Candidate}}, executable, home
}

func TestReplayPreparationFrozenCompilationStaticExplicitProbesAndPrivateNativeBodies(t *testing.T) {
	m, req, executable, home := replayPreparationFixture(t)
	sourceBefore, localBefore := snapshotDraftTrees(t, m)
	nativeBefore := snapshotNativeTrees(t, m)
	staticCalls := 0
	m.probe.version = func(context.Context, toolBinary, string, string, string) (string, error) {
		staticCalls++
		t.Fatal("static candidate preparation launched a tool")
		return "", nil
	}
	r, err := m.PrepareSnapshotReplay(context.Background(), req, executable, home)
	if err != nil || r.Status != "NEEDS_CHECK" || r.Check.Probe || r.CanApply || !r.Fresh || staticCalls != 0 {
		t.Fatal(r.Status, err)
	}
	if strings.Contains(string(jsonData(r)), "PRIVATE_") || strings.Contains(string(jsonData(r)), "SECRET_SENTINEL") {
		t.Fatal("preparation disclosed source, Local or native bodies")
	}
	storedRaw, err := workstore.ReadFile(m.replayPreparedFile(r.ID))
	if err != nil {
		t.Fatal(err)
	}
	var stored snapshotReplayPrepared
	if strict(storedRaw, &stored) != nil {
		t.Fatal("private record not readable")
	}
	if !strings.Contains(string(storedRaw), "FROZEN_PRIVATE_RULE") {
		t.Fatal("private record lost frozen source inputs")
	}
	rule := stored.Native.Targets[0].Rule
	if rule == nil {
		t.Fatal("missing rule target")
	}
	entry := rule.Before.Entries[workstore.FileContent]
	if _, err = m.Source.Store.Data(entry); err == nil {
		t.Fatal("raw current native rule was copied into ordinary source blobs")
	}
	privateBody, err := m.connectionStore().Data(entry)
	if err != nil || !strings.Contains(string(privateBody), "SECRET_SENTINEL") {
		t.Fatal("raw native backup not retained privately", err)
	}
	if len(stored.Native.Source.Packages) != 1 {
		t.Fatal("lost complete frozen package")
	}
	privateCompiler, err := m.replayCompiler(stored.Native.Source)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range stored.Native.Source.Packages {
		for path, e := range pkg.Tree.Entries {
			if path == "SKILL.md" {
				body, err := privateCompiler.Source.Store.Data(e)
				if err != nil || strings.Contains(string(body), "LATER_PRIVATE_DRAFT") {
					t.Fatal("compiled today's draft instead of archived skill", err)
				}
			}
		}
	}
	fakeProbes(m, discoveredGuide)
	m.probe.rules = fakeRuleLoad
	req.Preparation.Probe, req.Preparation.RuleProbe = true, true
	r, err = m.PrepareSnapshotReplay(context.Background(), req, executable, home)
	if err != nil || r.Status != "CHECKED" || r.CanApply || !r.Check.Probe || !r.Check.RuleProbe || len(r.Tools) != 1 || r.Tools[0] != "codex-cli" {
		t.Fatal(r.Status, r.Gates, err)
	}
	read, err := m.ReadSnapshotReplayPreparation(context.Background(), r.ID)
	if err != nil || !read.Fresh || read.Status != "CHECKED" {
		t.Fatal(read.Status, read.Gates, err)
	}
	if err = m.Apply(context.Background(), r.ID, r.Digest); err == nil {
		t.Fatal("ordinary native apply accepted a replay inspection digest")
	}
	s, l := snapshotDraftTrees(t, m)
	if workstore.Digest(s) != workstore.Digest(sourceBefore) || workstore.Digest(l) != workstore.Digest(localBefore) || !reflectTreesEqual(nativeBefore, snapshotNativeTrees(t, m)) {
		t.Fatal("candidate preparation wrote live source, Local or native files")
	}
	write(t, filepath.Join(m.Root, "AGENTS.md"), []byte("later handwritten rule must remain"))
	read, err = m.ReadSnapshotReplayPreparation(context.Background(), r.ID)
	if err != nil || read.Fresh || read.Status != "STALE" || len(read.Changes) != 0 || !read.Check.Stale {
		t.Fatal("old inspection remained current after native drift", read, err)
	}
}

func TestReplayPreparationRejectsToolReductionStaleCandidateAndAmbiguousInput(t *testing.T) {
	m, id, _ := snapshotDraftFixture(t)
	r, err := m.ReviewSnapshotReplayPermissions(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	req := SnapshotReplayPreparationRequest{SnapshotReplayCandidateRequest: SnapshotReplayCandidateRequest{Snapshot: id, Candidate: r.Candidate}, Preparation: CheckRequest{Tools: []string{"claude-code", "codex-cli"}}}
	executable := filepath.Join(t.TempDir(), "aidock.exe")
	write(t, executable, []byte("fixed test program"))
	home := filepath.Dir(m.Source.Store.Local)
	_, err = m.PrepareSnapshotReplay(context.Background(), req, executable, home)
	code(t, err, "SNAPSHOT_REPLAY_TOOL_SELECTION_MISMATCH")
	req.Preparation.Tools = nil
	req.Preparation.Probe = true
	m.probe.version = func(context.Context, toolBinary, string, string, string) (string, error) {
		t.Fatal("wrong home started a probe")
		return "", nil
	}
	_, err = m.PrepareSnapshotReplay(context.Background(), req, executable, t.TempDir())
	code(t, err, "MCP_CLIENT_CONFIG_BINDING_INVALID")
	req.Preparation.Probe = false
	req.Candidate = strings.Repeat("0", 64)
	_, err = m.PrepareSnapshotReplay(context.Background(), req, executable, home)
	code(t, err, "PLAN_STALE")
	base := `"snapshot_id":"` + id + `","candidate_digest":"` + r.Candidate + `"`
	for _, raw := range []string{`null`, `{` + base + `,"preparation":null}`, `{` + base + `,"preparation":{"probe":true,"probe":false}}`, `{` + base + `,"preparation":{"rule_probe":true}}`, `{` + base + `,"preparation":{"application_scope":"skills-rules"}}`, `{` + base + `,"preparation":{"command":"PRIVATE_OVERRIDE"}}`, `{` + base + `,"preparation":{},"root":"PRIVATE_OVERRIDE"}`} {
		if _, err := ParseSnapshotReplayPreparationRequest([]byte(raw)); err == nil || strings.Contains(err.Error(), "PRIVATE_OVERRIDE") {
			t.Fatal(raw, err)
		}
	}
	if _, err := ParseSnapshotReplayPreparationRequest([]byte(`{` + base + `,"preparation":{"probe":false}}`)); err != nil {
		t.Fatal(err)
	}
}
