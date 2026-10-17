package native

import (
	"path/filepath"
	"strings"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

func copyReplayTree(from, to *workstore.Store, tree workstore.Tree) error {
	for _, e := range tree.Entries {
		if e.Dir {
			continue
		}
		body, err := from.Data(e)
		if err != nil {
			return err
		}
		scratch := workstore.Tree{Entries: map[string]workstore.Entry{}}
		if err = to.PutFile(&scratch, "content", body, e.Mode); err != nil {
			return err
		}
	}
	return nil
}

// Import the original reversal's immutable images into their owning caches.
// Only managed receiver fields are regenerated; saved drafts, manual native
// fields, and complete sibling containers retain the original reversal role.
func (m *Manager) replayRestoreImages(restore *SnapshotReplayRestoreTarget, tools []string) (snapshotReplayReverse, map[string]workstore.Tree, error) {
	p, err := m.loadReplayReverse(restore.Plan, restore.Digest)
	if err != nil {
		return p, nil, err
	}
	if len(p.Views) != 1 || p.Views[0].Binding != restore.Binding {
		return p, nil, result.Fail("REPLAY_MULTIPLE_VIEWS_REVIEW_REQUIRED", "恢复需要协调全部原应用视图；当前仅可为一个完整原应用重新生成启动参数", 4)
	}
	c := m.snapshotReplayCoordinator()
	if err = c.ObservePlan(p.Transaction.ID, p.Transaction.Digest); err != nil {
		return p, nil, err
	}
	for _, t := range p.Transaction.Targets {
		var root workstore.RootBinding
		for _, r := range c.Roots {
			if r.Path == t.Path {
				root = r
			}
		}
		from := &workstore.Store{Root: root.Root, Local: c.Local}
		to := m.connectionStore()
		if t.Path == "source" || t.Path == "local" {
			to = m.Source.Store
		}
		for _, tree := range []workstore.Tree{t.Before, t.After} {
			if err = copyReplayTree(from, to, tree); err != nil {
				return p, nil, err
			}
			if err = copyReplayTree(from, c.Blobs, tree); err != nil {
				return p, nil, err
			}
		}
	}
	ledger, err := m.replayLedger(p.Transaction, true)
	if err != nil {
		return p, nil, err
	}
	selected := map[string]bool{}
	for _, tool := range tools {
		selected[tool] = true
	}
	for tool, entries := range ledger.Tools {
		for _, field := range entries.Fields {
			if selected[tool] && field.AppliedKey != restore.Binding || !selected[tool] && field.AppliedKey == restore.Binding {
				return p, nil, result.Fail("REPLAY_MULTIPLE_VIEWS_REVIEW_REQUIRED", "原工具字段包含其它应用视图或不完整的工具范围，不能用单个候选覆盖", 4)
			}
		}
	}
	images := map[string]workstore.Tree{connectionLedgerPath: replayNativeImage(p.Transaction, connectionLedgerPath, true, true)}
	as, err := selectAdapters(tools)
	if err != nil {
		return p, nil, err
	}
	for _, a := range as {
		if a.MCPConfig == nil {
			return p, nil, result.Fail("CONFIG_CONTRACT_UNSUPPORTED", "原工具没有受支持的 MCP 接收合同", 4)
		}
		images[a.MCPConfig.Path] = replayNativeImage(p.Transaction, a.MCPConfig.Path, true, true)
	}
	return p, images, nil
}

func replayNativeImage(tx workstore.BatchPlan, path string, file, after bool) workstore.Tree {
	root, member, ok := replayNativeContainer(path)
	if ok {
		for _, t := range tx.Targets {
			if t.Path == "native/"+root {
				tree := t.Before
				if after {
					tree = t.After
				}
				if member != "" {
					return replayMember(tree, member, file)
				}
				return workstore.Clone(tree)
			}
		}
	}
	return workstore.Tree{Entries: map[string]workstore.Entry{}}
}

func (m *Manager) reviseReplayRestore(seed snapshotReplayReverse, compiled []workstore.BatchTarget) (workstore.BatchPlan, []workstore.BatchTarget, error) {
	c := m.snapshotReplayCoordinator()
	targets := make([]workstore.BatchTarget, len(seed.Transaction.Targets))
	for i, t := range seed.Transaction.Targets {
		t.Before, t.After = workstore.Clone(t.Before), workstore.Clone(t.After)
		targets[i] = t
	}
	// The checked skills/rules describe the original applied contract. Restore
	// their exact archived native output, including the original manual regions;
	// only the MCP compiler's desired files carry new independent receipts.
	for _, generated := range compiled {
		if generated.Path != ".mcp.json" && generated.Path != ".codex/config.toml" && generated.Path != connectionLedgerPath {
			continue
		}
		root, member, ok := replayNativeContainer(generated.Path)
		if !ok {
			return workstore.BatchPlan{}, nil, result.Fail("BACKUP_INVALID", "恢复含未适配的生成路径", 5)
		}
		for i := range targets {
			if targets[i].Path != "native/"+root {
				continue
			}
			if member == "" {
				targets[i].After = workstore.Clone(generated.After)
			} else {
				replaceReplayMember(&targets[i].After, member, generated.File, generated.After)
			}
		}
	}
	// Source cache receives source/Local only. Raw native backups remain in
	// the private native/coordinator caches, including unrelated sibling files.
	for _, t := range targets {
		from := m.Source.Store
		if strings.HasPrefix(t.Path, "native/") {
			from = m.connectionStore()
		}
		for _, tree := range []workstore.Tree{t.Before, t.After} {
			if err := copyReplayTree(from, c.Blobs, tree); err != nil {
				return workstore.BatchPlan{}, nil, err
			}
		}
	}
	tx, err := c.ReviseReversal(seed.Transaction.ID, seed.Transaction.Digest, targets)
	if err != nil {
		return tx, nil, err
	}
	// Synthetic native proof is rebased to the ACTUAL current before-image,
	// never the virtual desired backup used to refresh receiver fields.
	native := []workstore.BatchTarget{}
	for _, path := range []string{".claude/skills", ".agents/skills", "CLAUDE.md", "AGENTS.md", ".codex/config.toml", ".mcp.json", connectionLedgerPath} {
		file := filepath.Base(path) != "skills"
		native = append(native, workstore.BatchTarget{Path: path, File: file, Before: replayNativeImage(tx, path, file, false), After: replayNativeImage(tx, path, file, true)})
	}
	return tx, native, nil
}
