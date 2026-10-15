package project

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func referencedProject(t *testing.T) (*Manager, Plan, *source.Manager, source.Preview) {
	t.Helper()
	m, p := registered(t)
	s, err := source.New(p.Binding.Root, m.Local, p.Binding.Project, p.Binding.Scope)
	if err != nil {
		t.Fatal(err)
	}
	skill, err := s.Plan(context.Background(), source.Request{Action: "create", Name: "archive-guide", Description: "retained snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(context.Background(), skill.Plan.ID, skill.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	return m, p, s, skill
}

func TestArchiveRetainsReferencedBindingAndRestorePreservesRollback(t *testing.T) {
	m, registered, s, skill := referencedProject(t)
	before, err := s.Store.Capture(s.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Remove(context.Background(), registered.Binding.ID)
	errorCode(t, err, "BINDING_REFERENCED")
	p, err := m.Membership(context.Background(), registered.Binding.ID, false)
	if err != nil || len(p.References) != 1 || p.References[0].ID != skill.Plan.ID {
		t.Fatal(p, err)
	}
	if _, err = m.Resolve(registered.Binding.ID); err != nil {
		t.Fatal("archive preview changed membership", err)
	}
	if _, err = m.Apply(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	_, err = m.Resolve(registered.Binding.ID)
	errorCode(t, err, "PROJECT_ARCHIVED")
	items, err := m.List()
	if err != nil || len(items) != 0 {
		t.Fatal(items, err)
	}
	archived, err := m.Archived()
	if err != nil || len(archived) != 1 || archived[0].Binding.Copy != registered.Binding.Copy || len(archived[0].References) != 1 {
		t.Fatal(archived, err)
	}
	after, err := s.Store.Capture(s.Store.Root)
	if err != nil || workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("archive changed source", err)
	}
	r, _ := replica.New(s)
	_, err = r.Register(replica.Binding{ID: workstore.ID(), Root: s.Store.Root, Name: "duplicate"})
	errorCode(t, err, "PROJECT_ARCHIVED")
	restore, err := m.Membership(context.Background(), registered.Binding.ID, true)
	if err != nil || len(restore.Batch.Roots) != 1 || restore.Batch.Roots[0].Path != "registry" {
		t.Fatal(restore, err)
	}
	if _, err = m.Apply(context.Background(), restore.ID, restore.Digest); err != nil {
		t.Fatal(err)
	}
	b, err := m.Resolve(registered.Binding.ID)
	if err != nil || b.Copy != registered.Binding.Copy {
		t.Fatal("restore changed copy identity", b, err)
	}
	rollback, err := s.Plan(context.Background(), source.Request{Action: "rollback", Snapshot: skill.Plan.ID})
	if err != nil {
		t.Fatal("original rollback unavailable after restore", err)
	}
	if _, err = s.Apply(context.Background(), rollback.Plan.ID, rollback.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	state, err := s.State()
	if err != nil || len(state.Index.Skills) != 0 {
		t.Fatal("original snapshot did not restore source", state, err)
	}
}

func TestRestoreRegistrationDoesNotRequireOrRepairPendingSource(t *testing.T) {
	m, registered, s, _ := referencedProject(t)
	p, err := m.Membership(context.Background(), registered.Binding.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	// Simulate a separate explicit-path client leaving a source conflict while
	// inactive. Registry restoration must remain available to reach recovery.
	s.Store = &workstore.Store{Root: s.Store.Root, Local: m.Local}
	before, err := s.Store.Capture(s.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	after := workstore.Clone(before)
	if err = s.Store.PutFile(&after, "pending.txt", []byte("installed bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	change, err := s.Store.Preview("edit", before, after)
	if err != nil {
		t.Fatal(err)
	}
	s.Store.Hook = func(stage string, _ int) error {
		if stage == "replaced" {
			if e := os.WriteFile(filepath.Join(s.Store.Root, "pending.txt"), []byte("external edit"), 0600); e != nil {
				return e
			}
			return errors.New("interrupted")
		}
		return nil
	}
	_, err = s.Store.Apply(context.Background(), change.ID)
	errorCode(t, err, "RECOVERY_CONFLICT")
	s.Store.Hook = nil
	restore, err := m.Membership(context.Background(), registered.Binding.ID, true)
	if err != nil {
		t.Fatal("pending source concealed restore", err)
	}
	if _, err = m.Apply(context.Background(), restore.ID, restore.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Resolve(registered.Binding.ID); err != nil {
		t.Fatal(err)
	}
	errorCode(t, s.Store.Pending(), "RECOVERY_CONFLICT")
	bytes, err := os.ReadFile(filepath.Join(s.Store.Root, "pending.txt"))
	if err != nil || string(bytes) != "external edit" {
		t.Fatal("registry restore overwrote source", err)
	}
	if err = os.WriteFile(filepath.Join(s.Store.Root, "pending.txt"), []byte("installed bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Store.Recover(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.State(); err != nil {
		t.Fatal("source recovery unavailable after restoration", err)
	}
}

func TestArchiveReferenceChangeWithIdenticalSourceInvalidatesPlan(t *testing.T) {
	m, reg := registered(t)
	p, err := m.Membership(context.Background(), reg.Binding.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	s := &workstore.Store{Root: reg.Binding.Root, Local: m.Local}
	before, err := s.Capture(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	after := workstore.Clone(before)
	if err = s.PutFile(&after, "keep.txt", []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, trees := range [][2]workstore.Tree{{before, after}, {after, before}} {
		change, e := s.Preview("edit", trees[0], trees[1])
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.Apply(context.Background(), change.ID); e != nil {
			t.Fatal(e)
		}
	}
	_, err = m.Apply(context.Background(), p.ID, p.Digest)
	errorCode(t, err, "PLAN_STALE")
	if _, err = m.Resolve(reg.Binding.ID); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveFailureRestoresActiveMembership(t *testing.T) {
	m, reg, _, _ := referencedProject(t)
	p, err := m.Membership(context.Background(), reg.Binding.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	m.Hook = func(stage string, i int) error {
		if stage == "target-complete" && i == 1 {
			return errors.New("archive failure")
		}
		return nil
	}
	_, err = m.Apply(context.Background(), p.ID, p.Digest)
	errorCode(t, err, "COORDINATED_APPLY_FAILED")
	if _, err = m.Resolve(reg.Binding.ID); err != nil {
		t.Fatal(err)
	}
	archived, err := m.Archived()
	if err != nil || len(archived) != 0 {
		t.Fatal(archived, err)
	}
}

func TestMembershipCrashHelper(t *testing.T) {
	if os.Getenv("AIDOCK_MEMBERSHIP_CRASH") != "1" {
		return
	}
	m, err := New(os.Getenv("AIDOCK_PROJECT_LOCAL"))
	if err != nil {
		t.Fatal(err)
	}
	m.Hook = func(stage string, i int) error {
		if stage == "target-complete" && i == 0 {
			os.Exit(94)
		}
		return nil
	}
	_, err = m.Apply(context.Background(), os.Getenv("AIDOCK_PROJECT_PLAN"), os.Getenv("AIDOCK_PROJECT_DIGEST"))
	t.Fatal("crash missed", err)
}

func TestRestoreActualProcessExitRecoversArchiveAndCanRetry(t *testing.T) {
	m, reg, s, skill := referencedProject(t)
	p, err := m.Membership(context.Background(), reg.Binding.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	restore, err := m.Membership(context.Background(), reg.Binding.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestMembershipCrashHelper$")
	cmd.Env = append(os.Environ(), "AIDOCK_MEMBERSHIP_CRASH=1", "AIDOCK_PROJECT_LOCAL="+m.Local, "AIDOCK_PROJECT_PLAN="+restore.ID, "AIDOCK_PROJECT_DIGEST="+restore.Digest)
	out, err := cmd.CombinedOutput()
	if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 94 {
		t.Fatal(err, string(out))
	}
	fresh, _ := New(m.Local)
	if _, err = fresh.Recover(restore.ID); err != nil {
		t.Fatal(err)
	}
	_, err = fresh.Resolve(reg.Binding.ID)
	errorCode(t, err, "PROJECT_ARCHIVED")
	items, err := fresh.Archived()
	if err != nil || len(items) != 1 || items[0].References[0].ID != skill.Plan.ID {
		t.Fatal(items, err)
	}
	retry, err := fresh.Membership(context.Background(), reg.Binding.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.Apply(context.Background(), retry.ID, retry.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err = s.State(); err != nil {
		t.Fatal(err)
	}
}
