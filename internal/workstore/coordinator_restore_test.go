package workstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCoordinatorSelectedRestoreUsesOriginalNodeAndCurrentReference(t *testing.T) {
	c, p := coordinatedFixture(t)
	ctx := context.Background()
	if _, err := c.Apply(ctx, p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	// An unrelated member can retain later edits while another member is restored.
	if err := os.WriteFile(filepath.Join(c.Roots[2].Root, "body"), []byte("later copy two"), 0644); err != nil {
		t.Fatal(err)
	}
	first, err := c.RestoreSelected(p.ID, map[string][]string{"copy-one": nil, "control": {"body"}})
	if err != nil || first.Reference != p.ID {
		t.Fatal(first.Reference, err)
	}
	if _, err = c.Apply(ctx, first.ID, first.Digest); err != nil {
		t.Fatal(err)
	}
	for _, target := range first.Targets {
		if target.Path == "copy-two" && Digest(target.Before) != Digest(target.After) {
			t.Fatal("unselected current edits were replaced")
		}
		if target.Path == "control" {
			if target.After.Entries["added.bin"] != target.Before.Entries["added.bin"] || target.After.Entries["body"] != p.Targets[0].Before.Entries["body"] {
				t.Fatal("control selection extended beyond its exact baseline member")
			}
		}
	}
	// Undoing the selected recovery returns its own before-images, not the full batch.
	undo, err := c.RestoreSelected(first.ID, nil)
	if err != nil || undo.Action != "undo-rollback" || undo.Reference != first.ID {
		t.Fatal(undo.Action, err)
	}
	if _, err = c.Apply(ctx, undo.ID, undo.Digest); err != nil {
		t.Fatal(err)
	}
	second, err := c.RestoreSelected(p.ID, map[string][]string{"copy-one": nil})
	if err != nil || second.Reference != undo.ID {
		t.Fatal("original snapshot could not be selected after another restore", err)
	}
	stale, err := c.RestoreSelected(p.ID, map[string][]string{"copy-one": nil})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(ctx, second.ID, second.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err = c.RestoreSelected(first.ID, nil); err == nil {
		t.Fatal("an older rollback effect was presented as whole-point recovery")
	}
	if _, err = c.Apply(ctx, stale.ID, stale.Digest); err == nil {
		t.Fatal("old selected recovery ignored the latest reference")
	}
	if _, err = c.RestoreSelected(p.ID, map[string][]string{"copy-one": nil}); err == nil {
		t.Fatal("already-restored selection manufactured another transaction")
	}
}

func TestCoordinatorSelectedRestoreRejectsConflictsAndRecoversEveryChangedRoot(t *testing.T) {
	for _, failure := range []string{"conflict", "target-fault"} {
		t.Run(failure, func(t *testing.T) {
			c, p := coordinatedFixture(t)
			ctx := context.Background()
			if _, err := c.Apply(ctx, p.ID, p.Digest); err != nil {
				t.Fatal(err)
			}
			selection := map[string][]string{"copy-one": nil, "control": nil}
			if failure == "conflict" {
				if err := os.WriteFile(filepath.Join(c.Roots[1].Root, "body"), []byte("manual edit"), 0644); err != nil {
					t.Fatal(err)
				}
				before := coordinatedTrees(t, c)
				if _, err := c.RestoreSelected(p.ID, selection); err == nil {
					t.Fatal("manual edit silently discarded")
				}
				if !reflect.DeepEqual(before, coordinatedTrees(t, c)) {
					t.Fatal("failed preview wrote files")
				}
				return
			}
			r, err := c.RestoreSelected(p.ID, selection)
			if err != nil {
				t.Fatal(err)
			}
			before := coordinatedTrees(t, c)
			c.Hook = func(stage string, index int) error {
				if stage == "target" && index == 1 {
					return errors.New("selected restore fault")
				}
				return nil
			}
			if _, err = c.Apply(ctx, r.ID, r.Digest); err == nil {
				t.Fatal("fault not injected")
			}
			if !reflect.DeepEqual(before, coordinatedTrees(t, c)) {
				t.Fatal("failed partial recovery left mixed baseline/member state")
			}
		})
	}
}
