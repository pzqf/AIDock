package workstore

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/result"
)

func batchFixture(t *testing.T) (*Batch, BatchPlan) {
	t.Helper()
	root := t.TempDir()
	local := t.TempDir()
	src := &Store{Root: filepath.Join(root, ".aidock"), Local: local}
	if err := os.MkdirAll(src.Root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src.Root, "draft.txt"), []byte("unsaved draft"), 0644); err != nil {
		t.Fatal(err)
	}
	tree := Tree{Exists: true, Entries: map[string]Entry{"guide": Directory(), "guide/empty": Directory()}}
	for p, data := range map[string][]byte{"guide/SKILL.md": []byte("original skill"), "guide/assets.bin": {0, 255, 1}} {
		if err := src.PutFile(&tree, p, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	b := &Batch{Boundary: root, Local: local, Identity: "project/server/copy", Paths: []string{".claude/skills", ".agents/skills"}, Blobs: src}
	p, err := b.Preview("apply", []BatchTarget{{Path: b.Paths[0], Before: Tree{Entries: map[string]Entry{}}, After: tree}, {Path: b.Paths[1], Before: Tree{Entries: map[string]Entry{}}, After: tree}})
	if err != nil {
		t.Fatal(err)
	}
	return b, p
}
func batchCode(t *testing.T, err error, want string) {
	t.Helper()
	var e *result.Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("want %s got %v", want, err)
	}
}
func targetTree(t *testing.T, b *Batch, p string) Tree {
	t.Helper()
	s := b.store(p)
	v, err := s.Capture(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func assertBefore(t *testing.T, b *Batch, p BatchPlan) {
	t.Helper()
	for _, v := range p.Targets {
		if Digest(targetTree(t, b, v.Path)) != Digest(v.Before) {
			t.Fatal("target not restored", v.Path)
		}
	}
}

func TestBatchApplyRollbackUndoPreservesSourceAndUnrelated(t *testing.T) {
	b, p := batchFixture(t)
	j, err := b.Apply(context.Background(), p.ID, p.Digest)
	if err != nil || j.State != "committed" {
		t.Fatal(j, err)
	}
	for _, v := range p.Targets {
		if Digest(targetTree(t, b, v.Path)) != Digest(v.After) {
			t.Fatal("incomplete target")
		}
	}
	if err = os.WriteFile(filepath.Join(b.Boundary, ".agents/skills/manual.txt"), []byte("handwritten"), 0644); err != nil {
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
	if _, err = os.Stat(filepath.Join(b.Boundary, ".claude")); !os.IsNotExist(err) {
		t.Fatal("created parent not cleaned", err)
	}
	if data, err := os.ReadFile(filepath.Join(b.Boundary, ".agents/skills/manual.txt")); err != nil || string(data) != "handwritten" {
		t.Fatal("manual content lost", err)
	}
	u, err := b.Reverse(rolled.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if u.Action != "undo-rollback" {
		t.Fatal(u)
	}
	if _, err = b.Apply(context.Background(), u.ID, u.Digest); err != nil {
		t.Fatal(err)
	}
	for _, path := range b.Paths {
		data, err := os.ReadFile(filepath.Join(b.Boundary, filepath.FromSlash(path), "guide/assets.bin"))
		if err != nil || string(data) != string([]byte{0, 255, 1}) {
			t.Fatal("binary changed", err)
		}
		if _, err = os.Stat(filepath.Join(b.Boundary, filepath.FromSlash(path), "guide/empty")); err != nil {
			t.Fatal("empty directory lost", err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(b.Boundary, ".aidock/draft.txt")); err != nil || string(data) != "unsaved draft" {
		t.Fatal("source or draft changed")
	}
	_, err = b.Apply(context.Background(), p.ID, p.Digest)
	batchCode(t, err, "PLAN_CONSUMED")
}
func TestBatchSecondTargetFailureAutomaticallyRestoresAll(t *testing.T) {
	b, p := batchFixture(t)
	b.Hook = func(stage string, i int) error {
		if stage == "target" && i == 1 {
			return errors.New("second target failure")
		}
		return nil
	}
	j, err := b.Apply(context.Background(), p.ID, p.Digest)
	batchCode(t, err, "NATIVE_APPLY_FAILED")
	if j.State != "recovered" {
		t.Fatal(j)
	}
	assertBefore(t, b, p)
	for _, parent := range p.Parents {
		if _, err = os.Stat(filepath.Join(b.Boundary, filepath.FromSlash(parent))); !os.IsNotExist(err) {
			t.Fatal("empty parent left")
		}
	}
	if err = b.Pending(); err != nil {
		t.Fatal(err)
	}
}
func TestBatchRechecksEveryTargetAndEveryBackupBeforeWriting(t *testing.T) {
	t.Run("stale second target", func(t *testing.T) {
		b, p := batchFixture(t)
		if err := os.MkdirAll(filepath.Join(b.Boundary, ".agents/skills"), 0755); err != nil {
			t.Fatal(err)
		}
		_, err := b.Apply(context.Background(), p.ID, p.Digest)
		batchCode(t, err, "PLAN_STALE")
		if targetTree(t, b, b.Paths[0]).Exists {
			t.Fatal("first target touched")
		}
	})
	t.Run("missing second backup", func(t *testing.T) {
		b, p := batchFixture(t)
		entry := p.Targets[1].After.Entries["guide/assets.bin"]
		if err := os.Remove(filepath.Join(b.store(b.Paths[1]).bucket(), "blobs", entry.Hash)); err != nil {
			t.Fatal(err)
		}
		_, err := b.Apply(context.Background(), p.ID, p.Digest)
		batchCode(t, err, "BACKUP_INVALID")
		assertBefore(t, b, p)
	})
	t.Run("confirmation", func(t *testing.T) {
		b, p := batchFixture(t)
		_, err := b.Apply(context.Background(), p.ID, "bad")
		batchCode(t, err, "PLAN_CONFIRMATION_REQUIRED")
		assertBefore(t, b, p)
	})
}
func TestBatchProcessHelper(t *testing.T) {
	if os.Getenv("AIDOCK_BATCH_CHILD") == "" {
		return
	}
	b := &Batch{Boundary: os.Getenv("AIDOCK_BATCH_ROOT"), Local: os.Getenv("AIDOCK_BATCH_LOCAL"), Identity: "project/server/copy", Paths: []string{".claude/skills", ".agents/skills"}}
	phase := os.Getenv("AIDOCK_BATCH_CHILD")
	b.Hook = func(stage string, i int) error {
		if stage == phase && (phase != "target-complete" || i == 0) {
			os.Exit(27)
		}
		return nil
	}
	var err error
	if phase == "recovered-target" {
		_, err = b.Recover()
	} else {
		_, err = b.Apply(context.Background(), os.Getenv("AIDOCK_BATCH_PLAN"), os.Getenv("AIDOCK_BATCH_DIGEST"))
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Fatal("child did not interrupt")
}
func interruptBatch(t *testing.T, b *Batch, p BatchPlan, phase string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestBatchProcessHelper$")
	cmd.Env = append(os.Environ(), "AIDOCK_BATCH_CHILD="+phase, "AIDOCK_BATCH_ROOT="+b.Boundary, "AIDOCK_BATCH_LOCAL="+b.Local, "AIDOCK_BATCH_PLAN="+p.ID, "AIDOCK_BATCH_DIGEST="+p.Digest)
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 27 {
		t.Fatal("expected process interruption", err)
	}
}
func TestBatchSurvivesProcessInterruptionAndBlocksOtherBindings(t *testing.T) {
	for _, phase := range []string{"prepared", "staged", "target-complete"} {
		t.Run(phase, func(t *testing.T) {
			b, p := batchFixture(t)
			interruptBatch(t, b, p, phase)
			batchCode(t, b.Pending(), "NATIVE_RECOVERY_REQUIRED")
			batchCode(t, b.Blobs.Pending(), "NATIVE_RECOVERY_REQUIRED")
			foreign := *b
			foreign.Local = t.TempDir()
			batchCode(t, foreign.Pending(), "RECOVERY_CONFLICT")
			reopened := *b
			n, err := reopened.Recover()
			if err != nil || n != 1 {
				t.Fatal(n, err)
			}
			assertBefore(t, b, p)
			if err = b.Blobs.Pending(); err != nil {
				t.Fatal("source still blocked", err)
			}
		})
	}
}
func TestBatchRecoveryConflictChecksAllTargetsBeforeRestoring(t *testing.T) {
	b, p := batchFixture(t)
	interruptBatch(t, b, p, "target-complete")
	first := targetTree(t, b, b.Paths[0])
	if err := os.MkdirAll(filepath.Join(b.Boundary, ".agents/skills/guide"), 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(b.Boundary, ".agents/skills/guide/SKILL.md")
	if err := os.WriteFile(path, []byte("external edit"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := b.Recover()
	batchCode(t, err, "RECOVERY_CONFLICT")
	if Digest(targetTree(t, b, b.Paths[0])) != Digest(first) {
		t.Fatal("first target restored despite second conflict")
	}
	if data, _ := os.ReadFile(path); string(data) != "external edit" {
		t.Fatal("external edit overwritten")
	}
	if err = os.RemoveAll(filepath.Join(b.Boundary, ".agents/skills/guide")); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Recover(); err != nil {
		t.Fatal(err)
	}
	assertBefore(t, b, p)
}

func TestBatchRecoveryItselfCanBeInterruptedAndResumed(t *testing.T) {
	b, p := batchFixture(t)
	interruptBatch(t, b, p, "target-complete")
	interruptBatch(t, b, p, "recovered-target")
	batchCode(t, b.Pending(), "NATIVE_RECOVERY_REQUIRED")
	n, err := b.Recover()
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	assertBefore(t, b, p)
}

func TestBatchCommittedMarkerRecoveryKeepsSuccessfulTargets(t *testing.T) {
	b, p := batchFixture(t)
	interruptBatch(t, b, p, "committed")
	n, err := b.Recover()
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
	for _, v := range p.Targets {
		if Digest(targetTree(t, b, v.Path)) != Digest(v.After) {
			t.Fatal("committed target rolled back")
		}
	}
	if err = b.Pending(); err != nil {
		t.Fatal(err)
	}
}
func TestBatchFixedTargetLockContendsAcrossLocalRoots(t *testing.T) {
	b, _ := batchFixture(t)
	other := *b
	other.Local = t.TempDir()
	if err := b.locked(func() error { _, err := other.History(); batchCode(t, err, "NATIVE_BUSY"); return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := other.History(); err != nil {
		t.Fatal("lock not released", err)
	}
}

func TestBatchUnsupportedFileDirectoryConversionRefusesBeforeWrites(t *testing.T) {
	b, p := batchFixture(t)
	after := Clone(p.Targets[0].After)
	delete(after.Entries, "guide/assets.bin")
	after.Entries["guide/assets.bin"] = Directory()
	_, err := b.Preview("apply", []BatchTarget{{Path: b.Paths[0], Before: p.Targets[0].After, After: after}})
	batchCode(t, err, "NATIVE_STRUCTURE_CHANGE_UNSUPPORTED")
	assertBefore(t, b, p)
}
func TestBatchRollbackRejectsChangedManagedPathAndStalePreview(t *testing.T) {
	b, p := batchFixture(t)
	if _, err := b.Apply(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	r, err := b.Reverse(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(b.Boundary, ".agents/skills/guide/SKILL.md")
	if err = os.WriteFile(path, []byte("user modified"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = b.Apply(context.Background(), r.ID, r.Digest)
	batchCode(t, err, "PLAN_STALE")
	_, err = b.Reverse(p.ID)
	batchCode(t, err, "RECOVERY_CONFLICT")
	if data, _ := os.ReadFile(path); string(data) != "user modified" {
		t.Fatal("edit lost")
	}
}
