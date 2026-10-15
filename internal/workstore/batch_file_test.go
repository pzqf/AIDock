package workstore

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func fileBatchFixture(t *testing.T) (*Batch, BatchPlan) {
	t.Helper()
	b, _ := batchFixture(t)
	b.Paths, b.FilePaths = []string{"CLAUDE.md", "AGENTS.md"}, []string{"CLAUDE.md", "AGENTS.md"}
	targets := []BatchTarget{}
	for i, p := range b.Paths {
		if i == 0 {
			if err := os.WriteFile(filepath.Join(b.Boundary, p), []byte("\xef\xbb\xbf手写\r\n"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		before, err := b.Blobs.CaptureFile(filepath.Join(b.Boundary, p))
		if err != nil {
			t.Fatal(err)
		}
		after := Clone(before)
		if err = b.Blobs.PutFile(&after, FileContent, []byte("handwritten\nmanaged"), 0644); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, BatchTarget{Path: p, File: true, Before: before, After: after})
	}
	p, err := b.Preview("apply", targets)
	if err != nil {
		t.Fatal(err)
	}
	return b, p
}
func assertFileBefore(t *testing.T, b *Batch, p BatchPlan) {
	t.Helper()
	for _, target := range p.Targets {
		now, err := b.capture(target)
		if err != nil || Digest(now) != Digest(target.Before) {
			t.Fatal(target.Path, now, err)
		}
	}
}
func TestBatchSingleFilesRollbackUndoAndMixedHistoricalReferences(t *testing.T) {
	b, p := fileBatchFixture(t)
	// An older transaction used directory-only targets under this same identity.
	old := p.Targets[0]
	old.File = false
	old.Path = ".old/skills"
	old.Before = Tree{Entries: map[string]Entry{}}
	old.After = old.Before
	b.Paths = append(b.Paths, old.Path)
	previous, err := b.Preview("apply", []BatchTarget{old})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Apply(context.Background(), previous.ID, previous.Digest); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(b.Boundary, "unrelated.bin"), []byte{0, 1, 255}, 0644); err != nil {
		t.Fatal(err)
	}
	j, err := b.Apply(context.Background(), p.ID, p.Digest)
	if err != nil {
		t.Fatal(err)
	}
	r, err := b.Reverse(j.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	rolled, err := b.Apply(context.Background(), r.ID, r.Digest)
	if err != nil {
		t.Fatal(err)
	}
	assertFileBefore(t, b, p)
	u, err := b.Reverse(rolled.Plan.ID)
	if err != nil || u.Action != "undo-rollback" {
		t.Fatal(u, err)
	}
	if _, err = b.Apply(context.Background(), u.ID, u.Digest); err != nil {
		t.Fatal(err)
	}
	for _, target := range p.Targets {
		now, e := b.capture(target)
		if e != nil || Digest(now) != Digest(target.After) {
			t.Fatal(target.Path, e)
		}
	}
	if data, e := os.ReadFile(filepath.Join(b.Boundary, "unrelated.bin")); e != nil || string(data) != string([]byte{0, 1, 255}) {
		t.Fatal(e)
	}
	history, err := NativeHistoryForRoot(b.Local, b.Boundary)
	if err != nil || len(history) != 4 {
		t.Fatal(len(history), err)
	}
}
func TestBatchSingleFileFailureAndExternalChanges(t *testing.T) {
	for _, stage := range []string{"target", "staged", "target-complete"} {
		t.Run(stage, func(t *testing.T) {
			b, p := fileBatchFixture(t)
			b.Hook = func(s string, i int) error {
				if s == stage && (stage != "target" || i == 1) {
					return errors.New("injected")
				}
				return nil
			}
			_, err := b.Apply(context.Background(), p.ID, p.Digest)
			batchCode(t, err, "NATIVE_APPLY_FAILED")
			assertFileBefore(t, b, p)
		})
	}
	t.Run("stale", func(t *testing.T) {
		b, p := fileBatchFixture(t)
		_ = os.WriteFile(filepath.Join(b.Boundary, "AGENTS.md"), []byte("external"), 0644)
		_, err := b.Apply(context.Background(), p.ID, p.Digest)
		batchCode(t, err, "PLAN_STALE")
		now, _ := b.capture(p.Targets[0])
		if Digest(now) != Digest(p.Targets[0].Before) {
			t.Fatal("first file touched")
		}
	})
	t.Run("type", func(t *testing.T) {
		b, p := fileBatchFixture(t)
		p.Targets[0].File = false
		_, err := b.Preview("apply", p.Targets)
		batchCode(t, err, "BACKUP_INVALID")
	})
	t.Run("directory", func(t *testing.T) {
		b, _ := fileBatchFixture(t)
		_ = os.MkdirAll(filepath.Join(b.Boundary, "AGENTS.md"), 0755)
		if _, err := b.Blobs.CaptureFile(filepath.Join(b.Boundary, "AGENTS.md")); err == nil {
			t.Fatal("directory accepted")
		}
	})
}
func TestBatchSingleFileProcessHelper(t *testing.T) {
	phase := os.Getenv("AIDOCK_FILE_BATCH_CHILD")
	if phase == "" {
		return
	}
	b := &Batch{Boundary: os.Getenv("AIDOCK_FILE_BATCH_ROOT"), Local: os.Getenv("AIDOCK_FILE_BATCH_LOCAL"), Identity: "project/server/copy", Paths: []string{"CLAUDE.md", "AGENTS.md"}, FilePaths: []string{"CLAUDE.md", "AGENTS.md"}}
	b.Hook = func(stage string, i int) error {
		if stage == phase && (phase != "target-complete" || i == 0) {
			os.Exit(95)
		}
		return nil
	}
	_, err := b.Apply(context.Background(), os.Getenv("AIDOCK_FILE_BATCH_PLAN"), os.Getenv("AIDOCK_FILE_BATCH_DIGEST"))
	if err != nil {
		t.Fatal(err)
	}
}
func TestBatchSingleFileProcessRecoveryAndConflict(t *testing.T) {
	for _, phase := range []string{"target-complete", "staged"} {
		t.Run(phase, func(t *testing.T) {
			b, p := fileBatchFixture(t)
			cmd := exec.Command(os.Args[0], "-test.run=^TestBatchSingleFileProcessHelper$")
			cmd.Env = append(os.Environ(), "AIDOCK_FILE_BATCH_CHILD="+phase, "AIDOCK_FILE_BATCH_ROOT="+b.Boundary, "AIDOCK_FILE_BATCH_LOCAL="+b.Local, "AIDOCK_FILE_BATCH_PLAN="+p.ID, "AIDOCK_FILE_BATCH_DIGEST="+p.Digest)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 95 {
				t.Fatal(string(out), err)
			}
			batchCode(t, b.Pending(), "NATIVE_RECOVERY_REQUIRED")
			first, _ := b.capture(p.Targets[0])
			_ = os.WriteFile(filepath.Join(b.Boundary, "AGENTS.md"), []byte("external edits"), 0644)
			_, err = b.Recover()
			batchCode(t, err, "RECOVERY_CONFLICT")
			now, _ := b.capture(p.Targets[0])
			if Digest(first) != Digest(now) {
				t.Fatal("first file restored before checking conflict")
			}
			if err = os.Remove(filepath.Join(b.Boundary, "AGENTS.md")); err != nil {
				t.Fatal(err)
			}
			b.Hook = nil
			if n, err := b.Recover(); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			assertFileBefore(t, b, p)
			if err = b.Pending(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
