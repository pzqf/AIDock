package projectsnapshot

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func coordinatedSnapshotFixture(t *testing.T) (*Archive, Content, *workstore.Store, *workstore.Store) {
	t.Helper()
	a, c, s, n := layeredSnapshotFixture(t)
	before := workstore.Clone(c.Source)
	if err := s.PutFile(&before, "profile.yaml", []byte("PRIVATE_SAVED_SOURCE_BEFORE_REPLAY"), 0644); err != nil {
		t.Fatal(err)
	}
	localBefore := workstore.Clone(c.Configuration.LocalTree)
	if err := s.PutFile(&localBefore, "profile.yaml", []byte("PRIVATE_SAVED_LOCAL_BEFORE_REPLAY"), 0644); err != nil {
		t.Fatal(err)
	}
	c.Trigger = "aidock replay"
	c.ActiveKey = strings.Repeat("a", 64)
	c.Transaction.Version = 1
	c.Transaction.Kind = "native-targets"
	c.Transaction.Digest = workstore.BatchPlanDigest(c.Transaction)
	area := filepath.Join(a.Local, "native-mcp-private", workstore.Hash([]byte(a.Binding.NativeRoot+"\x00"+a.Binding.Project+"\x00"+a.Binding.Scope+"\x00"+a.Binding.Copy)), "snapshot-replay")
	p := workstore.BatchPlan{Version: 1, Kind: "coordinated-roots", ID: c.Transaction.ID, Root: area, Identity: "snapshot-replay/" + a.Binding.Project + "/" + a.Binding.Scope + "/" + a.Binding.Copy, Action: "apply"}
	p.Roots = []workstore.RootBinding{{Path: "source", Root: s.Root}, {Path: "local", Root: source.LocalProfileRoot(a.Local, a.Binding.Project, a.Binding.Scope, a.Binding.Copy)}}
	p.Targets = []workstore.BatchTarget{{Path: "source", Before: before, After: c.Source}, {Path: "local", Before: localBefore, After: c.Configuration.LocalTree}}
	for _, name := range []string{".claude", ".agents", ".codex", "CLAUDE.md", "AGENTS.md", ".mcp.json", ".aidock-mcp-ownership.json"} {
		file := name != ".claude" && name != ".agents" && name != ".codex"
		p.Roots = append(p.Roots, workstore.RootBinding{Path: "native/" + name, Root: filepath.Join(a.Binding.NativeRoot, name), File: file, NativeProject: a.Binding.NativeRoot})
		nativeBefore := workstore.Tree{Entries: map[string]workstore.Entry{}}
		nativeAfter := workstore.Clone(nativeBefore)
		if name == ".codex" {
			if err := n.PutFile(&nativeBefore, "private.json", []byte("PRIVATE_NATIVE_SIBLING_NOT_IN_GIT"), 0600); err != nil {
				t.Fatal(err)
			}
			nativeAfter = workstore.Clone(nativeBefore)
			nativeAfter.Entries["config.toml"] = c.Transaction.Targets[0].After.Entries[workstore.FileContent]
		}
		p.Targets = append(p.Targets, workstore.BatchTarget{Path: "native/" + name, File: file, Before: nativeBefore, After: nativeAfter})
	}
	p.Digest = workstore.BatchPlanDigest(p)
	c.Coordinated = &p
	return a, c, s, n
}

func TestArchiveV4BindsCommonTransactionAndSeparatesNativePrivateBodies(t *testing.T) {
	a, c, s, n := coordinatedSnapshotFixture(t)
	r := mustCapture(t, a, c, s, n)
	if r.Version != 4 || r.Coordinated.Digest != c.Coordinated.Digest {
		t.Fatal("common transaction omitted")
	}
	legacyRefs, err := a.git(context.Background(), []string{"for-each-ref", "--format=%(refname:strip=3)", "refs/aidock/snapshots/"}, nil, 4096)
	if err != nil || strings.TrimSpace(string(legacyRefs)) != "" {
		t.Fatal("new common schema poisoned legacy ordinary-launcher namespace", string(legacyRefs), err)
	}
	refs, err := a.References(context.Background())
	if err != nil || len(refs) != 1 || refs[0] != r.ID {
		t.Fatal("new reader omitted common namespace", refs, err)
	}
	loaded, err := a.Load(context.Background(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	before := loaded.Coordinated.Targets[0].Before.Entries["profile.yaml"]
	body, err := a.SourceData(context.Background(), loaded, before)
	if err != nil || string(body) != "PRIVATE_SAVED_SOURCE_BEFORE_REPLAY" {
		t.Fatal("saved pre-replay draft missing", err)
	}
	var private workstore.Entry
	for _, target := range loaded.Coordinated.Targets {
		if target.Path == "native/.codex" {
			private = target.Before.Entries["private.json"]
		}
	}
	body, err = a.nativeBlobs().Data(private)
	if err != nil || string(body) != "PRIVATE_NATIVE_SIBLING_NOT_IN_GIT" {
		t.Fatal("complete native sibling not privately archived", err)
	}
	if _, err = a.SourceData(context.Background(), loaded, private); err == nil {
		t.Fatal("native sibling escaped into source Git content")
	}
	destination := &workstore.Store{Root: a.Binding.NativeRoot, Local: filepath.Join(a.Local, "replay-native-material")}
	if _, err = a.MaterializeNative(context.Background(), r.ID, destination); err != nil {
		t.Fatal(err)
	}
	if _, err = destination.Data(private); err != nil {
		t.Fatal("native sibling not privately materialized", err)
	}
}

func TestArchiveV4RejectsForeignRootsAndMismatchedCompilerTargets(t *testing.T) {
	a, c, s, n := coordinatedSnapshotFixture(t)
	wrong := *c.Coordinated
	wrong.Roots = append([]workstore.RootBinding{}, wrong.Roots...)
	wrong.Roots[0].Root = filepath.Join(t.TempDir(), "foreign")
	wrong.Digest = workstore.BatchPlanDigest(wrong)
	c.Coordinated = &wrong
	if _, err := a.Capture(context.Background(), c, s, n); err == nil {
		t.Fatal("foreign source root accepted")
	}
	a, c, s, n = coordinatedSnapshotFixture(t)
	c.Transaction.Targets[0].Before = workstore.Clone(c.Transaction.Targets[0].After)
	c.Transaction.Digest = workstore.BatchPlanDigest(c.Transaction)
	if _, err := a.Capture(context.Background(), c, s, n); err == nil {
		t.Fatal("compiler/native-container before mismatch accepted")
	}
}

func TestArchiveV4RestoredSavedDraftCanDifferFromFrozenAppliedConfiguration(t *testing.T) {
	a, c, s, n := coordinatedSnapshotFixture(t)
	p := *c.Coordinated
	p.Action = "rollback"
	p.Reference = workstore.ID()
	p.Targets = append([]workstore.BatchTarget{}, p.Targets...)
	for i := range p.Targets {
		if p.Targets[i].Path == "source" {
			p.Targets[i].After = workstore.Clone(p.Targets[i].After)
			if err := s.PutFile(&p.Targets[i].After, "profile.yaml", []byte("PRIVATE_SAVED_DRAFT_AFTER_RESTORE"), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	p.Digest = workstore.BatchPlanDigest(p)
	c.Coordinated = &p
	c.RestoredDraft = true
	c.RestoreOf = p.Reference
	r := mustCapture(t, a, c, s, n)
	loaded, err := a.Load(context.Background(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if workstore.Digest(loaded.Coordinated.Targets[0].After) == workstore.Digest(loaded.Source) {
		t.Fatal("saved draft was replaced by applied config")
	}
	b, err := a.SourceData(context.Background(), loaded, loaded.Coordinated.Targets[0].After.Entries["profile.yaml"])
	if err != nil || string(b) != "PRIVATE_SAVED_DRAFT_AFTER_RESTORE" {
		t.Fatal("separate saved draft bytes not archived", err)
	}
	c.RestoredDraft = false
	if _, err = a.Capture(context.Background(), c, s, n); err == nil {
		t.Fatal("unmarked divergent saved draft accepted")
	}
}
