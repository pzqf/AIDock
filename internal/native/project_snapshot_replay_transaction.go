package native

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/pzqf/AIDock/internal/privatefs"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

// With no common control records, observing legacy history must remain
// possible; a failed preview's observation cache is not initialized history.
func (m *Manager) snapshotReplayHistoryRecords() ([]workstore.BatchJournal, error) {
	c := m.snapshotReplayCoordinator()
	area := filepath.Join(c.Local, "coordinated-transactions")
	if err := workstore.CheckLinks(area); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(area)
	if os.IsNotExist(err) || err == nil && len(entries) == 0 {
		return []workstore.BatchJournal{}, nil
	}
	if err != nil {
		return nil, err
	}
	return c.History()
}

// All replay roots are disjoint. Tool containers retain their other files and
// can be created/removed as a whole without leaving a new parent behind after
// recovery. NativeProject uses the existing native lock for every container.
func (m *Manager) snapshotReplayCoordinator() *workstore.Coordinator {
	area := filepath.Join(m.connectionArea(), "snapshot-replay")
	roots := []workstore.RootBinding{
		{Path: "source", Root: m.Source.Store.Root},
		{Path: "local", Root: source.LocalProfileRoot(m.Source.Store.Local, m.Source.Owner, m.Source.Scope, m.Source.Copy)},
	}
	for _, p := range []string{".claude", ".agents", ".codex", "CLAUDE.md", "AGENTS.md", ".mcp.json", connectionLedgerPath} {
		file := p != ".claude" && p != ".agents" && p != ".codex"
		roots = append(roots, workstore.RootBinding{Path: "native/" + p, Root: filepath.Join(m.Root, p), File: file, NativeProject: m.Root})
	}
	return &workstore.Coordinator{Local: area, Identity: "snapshot-replay/" + m.Source.Owner + "/" + m.Source.Scope + "/" + m.Source.Copy, Roots: roots, Blobs: &workstore.Store{Root: m.Source.Store.Root, Local: area}, Hook: m.snapshotReplayHook, RecoveryCommand: "snapshot replay recover", RecoveryValidate: func(workstore.BatchPlan) error { return m.Source.ValidateReplayIdentity(context.Background(), true) }}
}

func replayNativeContainer(path string) (string, string, bool) {
	for _, root := range []string{".claude", ".agents", ".codex"} {
		if strings.HasPrefix(path, root+"/") {
			return root, strings.TrimPrefix(path, root+"/"), true
		}
	}
	for _, p := range []string{"CLAUDE.md", "AGENTS.md", ".mcp.json", connectionLedgerPath} {
		if path == p {
			return p, "", true
		}
	}
	return "", "", false
}

func replayMember(tree workstore.Tree, name string, file bool) workstore.Tree {
	if !file {
		return subtree(tree, name)
	}
	out := workstore.Tree{Entries: map[string]workstore.Entry{}}
	if e, ok := tree.Entries[name]; ok && !e.Dir {
		out.Exists = true
		out.Entries[workstore.FileContent] = e
	}
	return out
}

func replaceReplayMember(tree *workstore.Tree, name string, file bool, after workstore.Tree) {
	previous := tree.Entries[name]
	remove(tree, name)
	if !after.Exists {
		return
	}
	tree.Exists = true
	if file {
		tree.Entries[name] = after.Entries[workstore.FileContent]
		return
	}
	if previous.Dir {
		tree.Entries[name] = previous
	} else {
		tree.Entries[name] = workstore.Directory()
	}
	for p, e := range after.Entries {
		tree.Entries[name+"/"+p] = e
	}
}

// Assemble private complete-root backups. This is not an apply API: the caller
// must still bind a checked candidate and current authority before committing.
// Source and Local content never become native file authority; raw native
// before/after bytes are copied only to the restricted coordinator blob store.
func (m *Manager) snapshotReplayTargets(d source.ReplayDraft, native []workstore.BatchTarget) ([]workstore.BatchTarget, error) {
	c := m.snapshotReplayCoordinator()
	if err := privatefs.Ensure(c.Local); err != nil {
		return nil, err
	}
	copyTree := func(from *workstore.Store, t workstore.Tree) error {
		for _, e := range t.Entries {
			if e.Dir {
				continue
			}
			body, err := from.Data(e)
			if err != nil {
				return err
			}
			scratch := workstore.Tree{Entries: map[string]workstore.Entry{}}
			if err = c.Blobs.PutFile(&scratch, e.Hash, body, e.Mode); err != nil {
				return err
			}
		}
		return nil
	}
	targets := []workstore.BatchTarget{{Path: "source", Before: d.SourceBefore, After: d.SourceAfter}, {Path: "local", Before: d.LocalBefore, After: d.LocalAfter}}
	for _, t := range targets {
		for _, tree := range []workstore.Tree{t.Before, t.After} {
			if err := copyTree(m.Source.Store, tree); err != nil {
				return nil, err
			}
		}
	}
	byRoot := map[string]*workstore.BatchTarget{}
	for _, r := range c.Roots[2:] {
		var before workstore.Tree
		var err error
		if r.File {
			before, err = m.connectionStore().CaptureFile(r.Root)
		} else {
			before, err = m.connectionStore().Capture(r.Root)
		}
		if err != nil {
			return nil, err
		}
		if err = copyTree(m.connectionStore(), before); err != nil {
			return nil, err
		}
		t := &workstore.BatchTarget{Path: r.Path, File: r.File, Before: before, After: workstore.Clone(before)}
		byRoot[strings.TrimPrefix(r.Path, "native/")] = t
	}
	seen := map[string]bool{}
	for _, t := range native {
		root, member, ok := replayNativeContainer(t.Path)
		// Compiler targets are exact owned paths, never arbitrary container edits.
		allowed := t.Path == ".claude/skills" && !t.File || t.Path == ".agents/skills" && !t.File || t.Path == ".codex/config.toml" && t.File || member == "" && ok && t.File
		if !ok || !allowed || seen[t.Path] {
			return nil, result.Fail("SNAPSHOT_REPLAY_TARGET_INVALID", "共同回放含重复、未适配或不属于原生编译器的目标", 5)
		}
		seen[t.Path] = true
		target := byRoot[root]
		observed := target.Before
		if member != "" {
			observed = replayMember(target.Before, member, t.File)
		}
		if workstore.Digest(observed) != workstore.Digest(t.Before) {
			return nil, result.Fail("PLAN_STALE", "组装共同回放备份期间原生目标变化", 3)
		}
		if err := copyTree(m.connectionStore(), t.After); err != nil {
			return nil, err
		}
		if member == "" {
			target.After = workstore.Clone(t.After)
		} else {
			replaceReplayMember(&target.After, member, t.File, t.After)
		}
	}
	for _, r := range c.Roots[2:] {
		targets = append(targets, *byRoot[strings.TrimPrefix(r.Path, "native/")])
	}
	return targets, nil
}
