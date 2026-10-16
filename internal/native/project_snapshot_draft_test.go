package native

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func saveSnapshotLocal(t *testing.T, m *Manager, name string) {
	t.Helper()
	s, err := m.Source.Profile()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Source.ProfilePlan(context.Background(), source.ProfileEdit{Target: "local", ExpectedSHA256: s.Layers.LocalSHA256, ExpectedLayers: s.Layers.Digest, Profile: map[string]any{"schema_version": 2, "name": name}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.ApplyProfile(context.Background(), "local", p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
}

func snapshotDraftFixture(t *testing.T) (*Manager, string, source.Export) {
	t.Helper()
	m, id := writableFixture(t)
	m.Source.Copy = "22222222-2222-4222-8222-222222222222"
	saveSnapshotLocal(t, m, "ORIGINAL_PRIVATE_LOCAL")
	fakeProbes(m, discoveredGuide)
	executable := filepath.Join(t.TempDir(), "aidock.exe")
	write(t, executable, []byte("fixed unexecuted host"))
	p, err := m.PrepareProfile(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true}, executable, filepath.Dir(m.Source.Store.Local))
	if err != nil || p.Plan == nil {
		t.Fatal(p.Status, err)
	}
	if _, err = m.ApplyProfileApplication(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	exported, err := m.Source.Export()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := m.ProjectSnapshots(context.Background())
	if err != nil || len(rows.Items) != 1 {
		t.Fatal(rows, err)
	}
	applySource(t, m.Source, source.Request{Action: "edit", ID: id, Files: []source.Patch{{Path: "SKILL.md", Content: "---\nname: guide\ndescription: example\n---\nCURRENT_PRIVATE_UNAPPLIED_DRAFT"}}})
	saveSnapshotLocal(t, m, "CURRENT_PRIVATE_LOCAL_DRAFT")
	write(t, filepath.Join(m.Source.Store.Root, "knowledge/retain.md"), []byte("CURRENT_PRIVATE_MEMORY"))
	return m, rows.Items[0].ID, exported
}

func snapshotDraftTrees(t *testing.T, m *Manager) (workstore.Tree, workstore.Tree) {
	t.Helper()
	s, err := m.Source.Store.Capture(m.Source.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	l, err := m.Source.Store.Capture(source.LocalProfileRoot(m.Source.Store.Local, m.Source.Owner, m.Source.Scope, m.Source.Copy))
	if err != nil {
		t.Fatal(err)
	}
	return s, l
}

func snapshotNativeTrees(t *testing.T, m *Manager) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range []string{".claude/skills", ".agents/skills", "CLAUDE.md", "AGENTS.md", ".mcp.json", ".codex/config.toml", connectionLedgerPath} {
		for path, value := range configurationTree(t, filepath.Join(m.Root, filepath.FromSlash(p))) {
			out[path] = value
		}
	}
	return out
}

func TestSnapshotDraftRestoresTwoRootsAndUndoKeepsNativeAndCurrentDrafts(t *testing.T) {
	m, id, original := snapshotDraftFixture(t)
	beforeSource, beforeLocal := snapshotDraftTrees(t, m)
	nativeBefore := snapshotNativeTrees(t, m)
	p, err := m.PrepareSnapshotDraft(context.Background(), id)
	if err != nil || len(p.SourceChanges) == 0 || len(p.LocalChanges) == 0 {
		t.Fatal(p, err)
	}
	if p.Project != m.Source.Owner || p.Scope != m.Source.Scope || p.Copy != m.Source.Copy || p.SourceRoot != m.Source.Store.Root || p.LocalRoot != source.LocalProfileRoot(m.Source.Store.Local, m.Source.Owner, m.Source.Scope, m.Source.Copy) {
		t.Fatal("preview omitted exact recovery binding", p)
	}
	if strings.Contains(string(jsonData(p)), "CURRENT_PRIVATE_") || strings.Contains(string(jsonData(p)), "ORIGINAL_PRIVATE_") {
		t.Fatal("draft preview disclosed bodies")
	}
	s, l := snapshotDraftTrees(t, m)
	if workstore.Digest(s) != workstore.Digest(beforeSource) || workstore.Digest(l) != workstore.Digest(beforeLocal) {
		t.Fatal("prepare changed live roots")
	}
	if _, err = m.ApplySnapshotDraft(context.Background(), p.ID, strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong digest accepted")
	}
	j, err := m.ApplySnapshotDraft(context.Background(), p.ID, p.Digest)
	if err != nil || j.State != "committed" {
		t.Fatal(j.State, err)
	}
	fresh, err := m.Source.Export()
	if err != nil || workstore.Hash(jsonData(fresh.ProfileReport.Effective)) != workstore.Hash(jsonData(original.ProfileReport.Effective)) {
		t.Fatal("restored draft effective semantics differ", err)
	}
	if !reflectTreesEqual(nativeBefore, snapshotNativeTrees(t, m)) {
		t.Fatal("draft restoration modified native tools")
	}
	if _, err = m.ApplySnapshotDraft(context.Background(), p.ID, p.Digest); err == nil {
		t.Fatal("consumed restore accepted")
	}
	reverse, err := m.ReverseSnapshotDraft(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplySnapshotDraft(context.Background(), reverse.ID, reverse.Digest); err != nil {
		t.Fatal(err)
	}
	s, l = snapshotDraftTrees(t, m)
	if workstore.Digest(s) != workstore.Digest(beforeSource) || workstore.Digest(l) != workstore.Digest(beforeLocal) {
		t.Fatal("undo did not restore both current unapplied drafts")
	}
	undo, err := m.ReverseSnapshotDraft(context.Background(), reverse.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplySnapshotDraft(context.Background(), undo.ID, undo.Digest); err != nil {
		t.Fatal(err)
	}
	if !reflectTreesEqual(nativeBefore, snapshotNativeTrees(t, m)) {
		t.Fatal("undo modified native tools")
	}
	rows, err := m.ProjectSnapshots(context.Background())
	if err != nil || len(rows.Items) != 1 {
		t.Fatal("draft restoration manufactured application history", rows, err)
	}
}

func TestSnapshotDraftFailureAndStalePlansPreserveBothRoots(t *testing.T) {
	for _, kind := range []string{"stale-source", "stale-local", "second-root-failure"} {
		t.Run(kind, func(t *testing.T) {
			m, id, _ := snapshotDraftFixture(t)
			p, err := m.PrepareSnapshotDraft(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "stale-source":
				write(t, filepath.Join(m.Source.Store.Root, "knowledge/new.md"), []byte("new external memory must stay"))
			case "stale-local":
				write(t, filepath.Join(source.LocalProfileRoot(m.Source.Store.Local, m.Source.Owner, m.Source.Scope, m.Source.Copy), "profile.yaml"), []byte("CHANGED_LOCAL_DRAFT"))
			case "second-root-failure":
				m.snapshotDraftHook = func(stage string, n int) error {
					if stage == "target" && n == 1 {
						return errors.New("injected second-root failure")
					}
					return nil
				}
			}
			s, l := snapshotDraftTrees(t, m)
			if _, err = m.ApplySnapshotDraft(context.Background(), p.ID, p.Digest); err == nil {
				t.Fatal("stale/failing operation succeeded")
			}
			nowS, nowL := snapshotDraftTrees(t, m)
			if workstore.Digest(s) != workstore.Digest(nowS) || workstore.Digest(l) != workstore.Digest(nowL) {
				t.Fatal("failure replaced live draft bytes")
			}
		})
	}
}

func TestSnapshotDraftProcessExitRequiresJointRecovery(t *testing.T) {
	if payload := os.Getenv("AIDOCK_SNAPSHOT_DRAFT_HELPER"); payload != "" {
		var v struct{ Source, Local, Native, Copy, ID, Digest string }
		if json.Unmarshal([]byte(payload), &v) != nil {
			os.Exit(91)
		}
		s, err := source.New(v.Source, v.Local, owner, "server")
		if err != nil {
			os.Exit(92)
		}
		s.Copy = v.Copy
		m, err := New(s, v.Native)
		if err != nil {
			os.Exit(93)
		}
		m.snapshotDraftHook = func(stage string, n int) error {
			if stage == "target-complete" && n == 0 {
				os.Exit(97)
			}
			return nil
		}
		_, _ = m.ApplySnapshotDraft(context.Background(), v.ID, v.Digest)
		os.Exit(94)
	}
	m, id, _ := snapshotDraftFixture(t)
	p, err := m.PrepareSnapshotDraft(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	s, l := snapshotDraftTrees(t, m)
	payload, _ := json.Marshal(struct{ Source, Local, Native, Copy, ID, Digest string }{m.Source.Store.Root, m.Source.Store.Local, m.Root, m.Source.Copy, p.ID, p.Digest})
	child := exec.Command(os.Args[0], "-test.run=^TestSnapshotDraftProcessExitRequiresJointRecovery$")
	child.Env = append(os.Environ(), "AIDOCK_SNAPSHOT_DRAFT_HELPER="+string(payload))
	err = child.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 97 {
		t.Fatal("missing real process interruption", err)
	}
	if err = m.Source.Store.Pending(); err == nil {
		t.Fatal("partial source/local transaction did not block ordinary source operations")
	}
	inspection, err := m.InspectSnapshotDraftRecovery()
	if err != nil {
		t.Fatal(inspection, err)
	}
	if _, err = m.RecoverSnapshotDraft(); err != nil {
		t.Fatal(err)
	}
	a, b := snapshotDraftTrees(t, m)
	if workstore.Digest(s) != workstore.Digest(a) || workstore.Digest(l) != workstore.Digest(b) {
		t.Fatal("joint recovery lost original drafts")
	}
	if err = m.Source.Store.Pending(); err != nil {
		t.Fatal("recovery barrier retained", err)
	}
}
