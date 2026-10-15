package workstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func coordinatedFileFixture(t *testing.T) (*Coordinator, BatchPlan) {
	t.Helper()
	c, directoryPlan := coordinatedFixture(t)
	parent := t.TempDir()
	file := filepath.Join(parent, "CLAUDE.md")
	if err := os.WriteFile(file, []byte("\xef\xbb\xbf手写\r\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := c.Blobs.CaptureFile(file)
	if err != nil {
		t.Fatal(err)
	}
	after := Clone(before)
	if err = c.Blobs.PutFile(&after, FileContent, []byte("\xef\xbb\xbf手写\r\nmanaged"), 0644); err != nil {
		t.Fatal(err)
	}
	empty := Tree{Entries: map[string]Entry{}}
	added := Clone(empty)
	if err = c.Blobs.PutFile(&added, FileContent, []byte("new rule"), 0644); err != nil {
		t.Fatal(err)
	}
	c.Roots = []RootBinding{{Path: "rule", Root: file, File: true}, c.Roots[0], {Path: "new-rule", Root: filepath.Join(parent, "AGENTS.md"), File: true}}
	targets := []BatchTarget{{Path: "rule", File: true, Before: before, After: after}, directoryPlan.Targets[0], {Path: "new-rule", File: true, Before: empty, After: added}}
	p, err := c.Preview("apply", targets)
	if err != nil {
		t.Fatal(err)
	}
	return c, p
}

func coordinatedMixedEqual(t *testing.T, c *Coordinator, targets []BatchTarget, after bool) {
	t.Helper()
	for _, target := range targets {
		now, err := c.capture(target)
		want := target.Before
		if after {
			want = target.After
		}
		if err != nil || Digest(now) != Digest(want) {
			t.Fatal(target.Path, now, want, err)
		}
	}
}

func TestCoordinatorFilesJointRollbackUndoAndSiblingPreservation(t *testing.T) {
	c, p := coordinatedFileFixture(t)
	sibling := filepath.Join(filepath.Dir(c.Roots[0].Root), "unrelated.bin")
	if err := os.WriteFile(sibling, []byte{0, 255, 19}, 0644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.Apply(ctx, p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	coordinatedMixedEqual(t, c, p.Targets, true)
	current := p.ID
	for _, after := range []bool{false, true} {
		reverse, err := c.Reverse(current)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = c.Apply(ctx, reverse.ID, reverse.Digest); err != nil {
			t.Fatal(err)
		}
		coordinatedMixedEqual(t, c, p.Targets, after)
		current = reverse.ID
	}
	if data, err := os.ReadFile(sibling); err != nil || string(data) != string([]byte{0, 255, 19}) {
		t.Fatal("sibling touched", err)
	}
}

func TestCoordinatorFilesProcessExitRecoveryAndConflicts(t *testing.T) {
	for _, phase := range []string{"staged", "target-complete", "committed", "cleared-barrier"} {
		t.Run(phase, func(t *testing.T) {
			c, p := coordinatedFileFixture(t)
			coordinatedCrash(t, c, p, phase)
			fresh := &Coordinator{Local: c.Local, Identity: c.Identity, Roots: c.Roots}
			if phase == "target-complete" {
				// No root is restored before checking an external edit to a later file.
				path := c.Roots[2].Root
				if err := os.WriteFile(path, []byte("external"), 0644); err != nil {
					t.Fatal(err)
				}
				_, err := fresh.Recover()
				batchCode(t, err, "RECOVERY_CONFLICT")
				now, err := fresh.capture(p.Targets[0])
				if err != nil || Digest(now) != Digest(p.Targets[0].After) {
					t.Fatal("partially restored before conflict check", err)
				}
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
				coordinatedCrash(t, c, p, "recovered-target")
			}
			if _, err := fresh.Recover(); err != nil {
				t.Fatal(err)
			}
			coordinatedMixedEqual(t, fresh, p.Targets, phase == "committed" || phase == "cleared-barrier")
			if err := fresh.Pending(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCoordinatorFilesRejectTypeAndShapeAndBoundCounts(t *testing.T) {
	c, p := coordinatedFileFixture(t)
	p.Targets[0].File = false
	_, err := c.Preview("apply", p.Targets)
	batchCode(t, err, "BACKUP_INVALID")
	p.Targets[0].File = true
	p.Targets[0].After.Entries["sibling"] = Directory()
	_, err = c.Preview("apply", p.Targets)
	batchCode(t, err, "BACKUP_INVALID")
	parent := t.TempDir()
	c.Roots = nil
	for i := 0; i < 97; i++ {
		c.Roots = append(c.Roots, RootBinding{Path: fmt.Sprintf("dir-%d", i), Root: filepath.Join(parent, fmt.Sprintf("dir-%d", i))})
	}
	for i := 0; i < 64; i++ {
		c.Roots = append(c.Roots, RootBinding{Path: fmt.Sprintf("file-%d", i), Root: filepath.Join(parent, fmt.Sprintf("file-%d", i)), File: true})
	}
	if err = c.validateRoots(); err != nil {
		t.Fatal(err)
	}
	c.Roots = append(c.Roots, RootBinding{Path: "extra", Root: filepath.Join(parent, "extra"), File: true})
	batchCode(t, c.validateRoots(), "INVALID_ROOTS")
}
