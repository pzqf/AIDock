package native

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func TestReplayRestoreRevisionPreservesDraftsRebasesNativeProofAndRecoversAllRoots(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "fault"}[fail], func(t *testing.T) {
			m, d, native := replayTransactionFixture(t)
			targets, err := m.snapshotReplayTargets(d, native)
			if err != nil {
				t.Fatal(err)
			}
			c := m.snapshotReplayCoordinator()
			original, err := c.PreviewObserved("apply", targets)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.Apply(context.Background(), original.ID, original.Digest); err != nil {
				t.Fatal(err)
			}
			beforeSource, beforeLocal := snapshotDraftTrees(t, m)
			beforeNative := snapshotNativeTrees(t, m)
			seed, err := m.reverseSnapshotReplay(context.Background(), original.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range seed.Transaction.Targets {
				for _, root := range c.Roots {
					if root.Path != target.Path {
						continue
					}
					from := &workstore.Store{Root: root.Root, Local: c.Local}
					to := m.connectionStore()
					if root.Path == "source" || root.Path == "local" {
						to = m.Source.Store
					}
					for _, tree := range []workstore.Tree{target.Before, target.After} {
						if err = copyReplayTree(from, to, tree); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			generated := workstore.Tree{Exists: true, Entries: map[string]workstore.Entry{}}
			if err = m.connectionStore().PutFile(&generated, workstore.FileContent, []byte("# refreshed current authorization fixture\n"), 0600); err != nil {
				t.Fatal(err)
			}
			tx, proof, err := m.reviseReplayRestore(seed, []workstore.BatchTarget{{Path: ".codex/config.toml", File: true, After: generated}})
			if err != nil || tx.Action != "rollback" || tx.Reference != original.ID || tx.ID == seed.Transaction.ID {
				t.Fatal(tx.Action, err)
			}
			for i, target := range tx.Targets {
				if workstore.Digest(target.Before) != workstore.Digest(seed.Transaction.Targets[i].Before) {
					t.Fatal("actual current before image changed", target.Path)
				}
				if (target.Path == "source" || target.Path == "local") && workstore.Digest(target.After) != workstore.Digest(seed.Transaction.Targets[i].After) {
					t.Fatal("saved draft substituted with applied source", target.Path)
				}
				if target.Path == "native/.codex" && target.After.Entries["private.json"] != target.Before.Entries["private.json"] {
					t.Fatal("private sibling lost")
				}
			}
			for _, target := range proof {
				if workstore.Digest(target.Before) != workstore.Digest(replayNativeImage(seed.Transaction, target.Path, target.File, false)) || workstore.Digest(target.After) != workstore.Digest(replayNativeImage(tx, target.Path, target.File, true)) {
					t.Fatal("synthetic proof did not describe actual full transaction", target.Path)
				}
			}
			if !reflectTreesEqual(beforeNative, snapshotNativeTrees(t, m)) {
				t.Fatal("revision wrote native targets")
			}
			if fail {
				c.Hook = func(stage string, index int) error {
					if stage == "target" && index == 5 {
						return errors.New("restore revision injected failure")
					}
					return nil
				}
			}
			_, err = c.Apply(context.Background(), tx.ID, tx.Digest)
			if (err != nil) != fail {
				t.Fatal(err)
			}
			s, l := snapshotDraftTrees(t, m)
			if fail {
				if workstore.Digest(s) != workstore.Digest(beforeSource) || workstore.Digest(l) != workstore.Digest(beforeLocal) || !reflectTreesEqual(beforeNative, snapshotNativeTrees(t, m)) {
					t.Fatal("regenerated restore left a partial state")
				}
			} else if workstore.Digest(s) != workstore.Digest(seed.Transaction.Targets[0].After) || workstore.Digest(l) != workstore.Digest(seed.Transaction.Targets[1].After) {
				t.Fatal("saved drafts were not retained")
			}
			if _, err = m.Source.Store.Data(generated.Entries[workstore.FileContent]); err == nil {
				t.Fatal("raw regenerated native body escaped to source cache")
			}
		})
	}
}

func TestReplayProposalAssociationAmbiguityBlocksWithoutWrites(t *testing.T) {
	m, p := checkedReplayApplicationFixture(t)
	s, l := snapshotDraftTrees(t, m)
	n := snapshotNativeTrees(t, m)
	if err := save(m.replayReverseFile(p.ID), []byte("{}")); err != nil {
		t.Fatal(err)
	}
	_, err := m.ReadSnapshotReplayPlan(context.Background(), p.ID, p.Digest)
	code(t, err, "BACKUP_INVALID")
	_, err = m.ApplySnapshotReplay(context.Background(), p.ID, p.Digest)
	code(t, err, "BACKUP_INVALID")
	s1, l1 := snapshotDraftTrees(t, m)
	if workstore.Digest(s) != workstore.Digest(s1) || workstore.Digest(l) != workstore.Digest(l1) || !reflectTreesEqual(n, snapshotNativeTrees(t, m)) {
		t.Fatal("ambiguous proposal wrote managed roots")
	}
	if filepath.Dir(m.replayApplicationFile(p.ID)) == filepath.Dir(m.replayReverseFile(p.ID)) {
		t.Fatal("application and reversal association kinds conflated")
	}
}
