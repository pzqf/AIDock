package project

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func registered(t *testing.T) (*Manager, Plan) {
	t.Helper()
	m, root := setup(t)
	p, err := m.Plan(context.Background(), Request{Root: root, Name: "移除测试项目", Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	return m, p
}

func TestRemovalPreservesRootsAndRetiresMemberIdentity(t *testing.T) {
	m, registered := registered(t)
	b := registered.Binding
	s, _ := source.New(b.Root, m.Local, b.Project, b.Scope)
	r, _ := replica.New(s)
	// The directory also has hand-maintained tool files. Removal owns neither.
	tool := filepath.Join(filepath.Dir(b.Root), ".claude", "skills", "manual", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(tool), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tool, []byte("keep manual"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := s.Store.Capture(b.Root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Remove(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Resolve(b.ID); err != nil {
		t.Fatal("preview removed binding", err)
	}
	if len(workstore.Changes(p.Batch.Targets[0].Before, p.Batch.Targets[0].After)) != 0 {
		t.Fatal("source changes in removal")
	}
	if len(workstore.Changes(p.Batch.Targets[1].Before, p.Batch.Targets[1].After)) != 1 {
		t.Fatal("removal changed recovery assets/baselines")
	}
	_, err = m.Apply(context.Background(), p.ID, "wrong")
	errorCode(t, err, "PLAN_CONFIRMATION_REQUIRED")
	h, err := m.Apply(context.Background(), p.ID, p.Digest)
	if err != nil || h.State != "committed" || h.Pending {
		t.Fatal(h, err)
	}
	_, err = m.Resolve(b.ID)
	errorCode(t, err, "PROJECT_NOT_REGISTERED")
	after, err := s.Store.Capture(b.Root)
	if err != nil || workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("source changed", err)
	}
	bytes, err := os.ReadFile(tool)
	if err != nil || string(bytes) != "keep manual" {
		t.Fatal("manual tool changed", err)
	}
	registry, err := r.List()
	if err != nil || len(registry.Copies) != 0 || len(registry.Removed) != 1 || registry.Removed[0].Binding.ID != b.Copy {
		t.Fatal(registry, err)
	}
	_, err = r.Register(replica.Binding{ID: b.Copy, Root: b.Root, Name: b.Name})
	errorCode(t, err, "REPLICA_MEMBER_REMOVED")
	// Opening preserved files creates a fresh member; old baselines cannot be reused.
	open, err := m.Plan(context.Background(), Request{Root: filepath.Dir(b.Root), Name: b.Name})
	if err != nil || open.Binding.Copy == b.Copy {
		t.Fatal(open, err)
	}
	if _, err = m.Apply(context.Background(), open.ID, open.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Recover(p.ID); err != nil {
		t.Fatal("completed removal recovery should only close its own markers", err)
	}
	items, err := m.List()
	if err != nil || len(items) != 1 || items[0].ID != open.Binding.ID {
		t.Fatal("old operation resurrected removed ID", items, err)
	}
}

func TestRemovalRechecksSnapshotReferencesAfterPreview(t *testing.T) {
	m, reg := registered(t)
	p, err := m.Remove(context.Background(), reg.Binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	s := &workstore.Store{Root: reg.Binding.Root, Local: m.Local}
	before, err := s.Capture(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	after := workstore.Clone(before)
	if err = s.PutFile(&after, "temporary.txt", []byte("snapshot content"), 0600); err != nil {
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
	// Bytes match the preview again. Only the lock-time reference gate catches this.
	_, err = m.Apply(context.Background(), p.ID, p.Digest)
	errorCode(t, err, "BINDING_REFERENCED")
	if _, err = m.Resolve(reg.Binding.ID); err != nil {
		t.Fatal(err)
	}
	_, err = m.Remove(context.Background(), reg.Binding.ID)
	errorCode(t, err, "BINDING_REFERENCED")

}

func TestRemovalFindsNativeSnapshotsFromLegacyCopyIdentity(t *testing.T) {
	m, reg := registered(t)
	p, err := m.Remove(context.Background(), reg.Binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	s := &workstore.Store{Root: reg.Binding.Root, Local: m.Local}
	batch := &workstore.Batch{Boundary: filepath.Dir(s.Root), Local: m.Local, Identity: reg.Binding.Project + "/" + reg.Binding.Scope + "/legacy-copy", Paths: []string{".agents/skills"}, Blobs: s}
	before, err := s.Capture(filepath.Join(batch.Boundary, ".agents", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	after := workstore.Clone(before)
	after.Exists = true
	if err = s.PutFile(&after, "guide/SKILL.md", []byte("native snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	change, err := batch.Preview("apply", []workstore.BatchTarget{{Path: ".agents/skills", Before: before, After: after}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = batch.Apply(context.Background(), change.ID, change.Digest); err != nil {
		t.Fatal(err)
	}
	_, err = m.Apply(context.Background(), p.ID, p.Digest)
	errorCode(t, err, "BINDING_REFERENCED")
	_, err = m.Remove(context.Background(), reg.Binding.ID)
	errorCode(t, err, "BINDING_REFERENCED")
	// A corrupt journal cannot hide a reference by losing its root field.
	journal := filepath.Join(m.Local, "native-transactions", workstore.Hash([]byte(strings.ToLower(batch.Boundary)+"\n"+batch.Identity)), "journals", change.ID+".json")
	bytes, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err = json.Unmarshal(bytes, &record); err != nil {
		t.Fatal(err)
	}
	record["plan"].(map[string]any)["root"] = ""
	bytes, _ = json.Marshal(record)
	if err = os.WriteFile(journal, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = m.Remove(context.Background(), reg.Binding.ID)
	errorCode(t, err, "BACKUP_INVALID")
}

func TestRemovalFailureRestoresMembershipAndNeverRewritesSource(t *testing.T) {
	m, reg := registered(t)
	p, err := m.Remove(context.Background(), reg.Binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	m.Hook = func(stage string, i int) error {
		if stage == "target-complete" && i == 1 {
			return errors.New("registry failure")
		}
		return nil
	}
	_, err = m.Apply(context.Background(), p.ID, p.Digest)
	errorCode(t, err, "COORDINATED_APPLY_FAILED")
	if _, err = m.Resolve(reg.Binding.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(reg.Binding.Root, "project.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestRemovalCrashHelper(t *testing.T) {
	if os.Getenv("AIDOCK_REMOVE_CRASH") != "1" {
		return
	}
	m, err := New(os.Getenv("AIDOCK_PROJECT_LOCAL"))
	if err != nil {
		t.Fatal(err)
	}
	m.Hook = func(stage string, i int) error {
		if stage == "target-complete" && i == 1 {
			os.Exit(93)
		}
		return nil
	}
	_, err = m.Apply(context.Background(), os.Getenv("AIDOCK_PROJECT_PLAN"), os.Getenv("AIDOCK_PROJECT_DIGEST"))
	t.Fatal("crash missed", err)
}

func TestRemovalActualProcessExitAndRestartRecovery(t *testing.T) {
	m, reg := registered(t)
	p, err := m.Remove(context.Background(), reg.Binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestRemovalCrashHelper$")
	cmd.Env = append(os.Environ(), "AIDOCK_REMOVE_CRASH=1", "AIDOCK_PROJECT_LOCAL="+m.Local, "AIDOCK_PROJECT_PLAN="+p.ID, "AIDOCK_PROJECT_DIGEST="+p.Digest)
	out, err := cmd.CombinedOutput()
	if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 93 {
		t.Fatal(err, string(out))
	}
	fresh, _ := New(m.Local)
	hs, err := fresh.History()
	if err != nil || len(hs) != 2 {
		t.Fatal(hs, err)
	}
	pending := false
	for _, h := range hs {
		if h.Plan.ID == p.ID {
			pending = h.Pending
		}
	}
	if !pending {
		t.Fatal("removal interruption hidden")
	}
	if _, err = fresh.Recover(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.Resolve(reg.Binding.ID); err != nil {
		t.Fatal("membership not recovered", err)
	}
	if _, err = os.Stat(filepath.Join(reg.Binding.Root, "skills.yaml")); err != nil {
		t.Fatal("source removed", err)
	}
}
