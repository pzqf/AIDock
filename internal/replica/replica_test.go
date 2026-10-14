package replica

import (
	"context"
	"errors"
	"fmt"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const owner = "11111111-1111-4111-8111-111111111111"
const skill = "22222222-2222-4222-8222-222222222222"

func applySource(t *testing.T, s *source.Manager, req source.Request) {
	t.Helper()
	p, err := s.Plan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T, n int) (*Manager, []Binding, []*source.Manager) {
	t.Helper()
	parent := t.TempDir()
	local := filepath.Join(t.TempDir(), "local")
	pkg := filepath.Join(t.TempDir(), "package")
	if err := os.MkdirAll(filepath.Join(pkg, "empty"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "SKILL.md"), []byte("---\nname: guide\ndescription: same\n---\nbase\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "asset.bin"), []byte{0, 255, 7}, 0644); err != nil {
		t.Fatal(err)
	}
	bindings := []Binding{}
	sources := []*source.Manager{}
	for i := 0; i < n; i++ {
		root := filepath.Join(parent, workstore.ID(), ".aidock")
		if err := os.MkdirAll(filepath.Dir(root), 0755); err != nil {
			t.Fatal(err)
		}
		s, err := source.New(root, local, owner, "server")
		if err != nil {
			t.Fatal(err)
		}
		applySource(t, s, source.Request{Action: "import", ID: skill, PackagePath: pkg, SourceRole: "local-original"})
		applySource(t, s, source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "references/notes.txt", Content: "attachment"}}})
		applySource(t, s, source.Request{Action: "enable", ID: skill})
		bindings = append(bindings, Binding{ID: workstore.ID(), Name: filepath.Base(filepath.Dir(root)), Root: root, Excludes: []string{}})
		sources = append(sources, s)
	}
	m, err := New(sources[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bindings {
		p, err := m.Register(b)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = m.Apply(context.Background(), p.ID, p.Digest); err != nil {
			t.Fatal(err)
		}
	}
	return m, bindings, sources
}
func code(t *testing.T, err error, want string) {
	t.Helper()
	var e *result.Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("want %s got %v", want, err)
	}
}
func status(t *testing.T, m *Manager, from, to Binding, want string) {
	t.Helper()
	r, err := m.Compare(context.Background(), Request{From: from.ID, Targets: []string{to.ID}})
	if err != nil || len(r.Targets) != 1 || len(r.Targets[0].Entries) != 1 || r.Targets[0].Entries[0].Status != want {
		t.Fatal(r, err)
	}
}
func baseline(t *testing.T, m *Manager, b []Binding) {
	t.Helper()
	req := Request{From: b[0].ID, Targets: []string{}}
	for _, v := range b[1:] {
		req.Targets = append(req.Targets, v.ID)
	}
	p, err := m.BaselinePlan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != "replica-control" {
		t.Fatal(p.Kind)
	}
	if _, err = m.Apply(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
}
func TestFiveCopiesCommonBaselineAndThreeWayDecisions(t *testing.T) {
	m, b, s := fixture(t, 5)
	ctx := context.Background()
	before := []string{}
	for _, v := range s {
		tree, err := v.Store.Capture(v.Store.Root)
		if err != nil {
			t.Fatal(err)
		}
		before = append(before, workstore.Digest(tree))
	}
	baseline(t, m, b)
	for i := 1; i < len(b)-1; i++ {
		baseline(t, m, b[i:])
	}
	for _, to := range b[1:] {
		status(t, m, b[0], to, "SAME")
	}
	for i, v := range s {
		tree, _ := v.Store.Capture(v.Store.Root)
		if workstore.Digest(tree) != before[i] {
			t.Fatal("baseline modified a project")
		}
	}
	applySource(t, s[0], source.Request{Action: "disable", ID: skill})
	status(t, m, b[0], b[1], "SOURCE_CHANGED")
	status(t, m, b[1], b[0], "TARGET_CHANGED")
	applySource(t, s[1], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "references/notes.txt", Content: "target"}}})
	status(t, m, b[0], b[1], "CONFLICT")
	p, err := m.BaselinePlan(ctx, Request{From: b[0].ID, Targets: []string{b[1].ID}})
	code(t, err, "BASELINE_NOTHING_TO_CONFIRM")
	if p.ID != "" {
		t.Fatal("made conflicting baseline")
	}
	applySource(t, s[2], source.Request{Action: "retire", ID: skill, Reason: "test deletion"})
	status(t, m, b[2], b[0], "CONFLICT")
	status(t, m, b[2], b[3], "SOURCE_DELETED")
}
func TestUnknownBaselineDuplicateAndStaleConfirmation(t *testing.T) {
	m, b, s := fixture(t, 2)
	ctx := context.Background()
	applySource(t, s[1], source.Request{Action: "disable", ID: skill})
	status(t, m, b[0], b[1], "BASELINE_UNKNOWN")
	_, err := m.BaselinePlan(ctx, Request{From: b[0].ID, Targets: []string{b[1].ID}})
	code(t, err, "BASELINE_NOTHING_TO_CONFIRM")
	_, err = m.Register(b[0])
	code(t, err, "REPLICA_DUPLICATE")
	_, err = m.Compare(ctx, Request{From: b[0].ID, Targets: []string{b[0].ID}})
	code(t, err, "INVALID_INPUT")
	applySource(t, s[1], source.Request{Action: "enable", ID: skill})
	p, err := m.BaselinePlan(ctx, Request{From: b[0].ID, Targets: []string{b[1].ID}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Apply(ctx, p.ID, "bad")
	code(t, err, "PLAN_CONFIRMATION_REQUIRED")
	applySource(t, s[0], source.Request{Action: "disable", ID: skill})
	_, err = m.Apply(ctx, p.ID, p.Digest)
	code(t, err, "PLAN_STALE")
	control, _ := m.Control.Capture(m.Control.Root)
	if _, ok := control.Entries[pairPath(b[0].ID, b[1].ID)]; ok {
		t.Fatal("stale baseline accepted")
	}
}
func TestLocalExclusionAndMissingCopyRemainVisible(t *testing.T) {
	m, b, s := fixture(t, 3)
	ctx := context.Background()
	baseline(t, m, b)
	// Policy is protected local registration, not project content.
	control, _ := m.Control.Capture(m.Control.Root)
	reg, err := m.registry(control)
	if err != nil {
		t.Fatal(err)
	}
	for i := range reg.Copies {
		if reg.Copies[i].ID == b[1].ID {
			reg.Copies[i].Excludes = []string{"skills/guide"}
		}
	}
	after := workstore.Clone(control)
	if err = m.Control.PutFile(&after, "replicas.json", data(reg), 0600); err != nil {
		t.Fatal(err)
	}
	// Test policy edit through the same recoverable control transaction.
	p, err := m.Control.Preview("replica-register", control, after)
	if err != nil {
		t.Fatal(err)
	}
	m.Control.Validate = nil
	if _, err = m.Control.Apply(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	applySource(t, s[0], source.Request{Action: "disable", ID: skill})
	status(t, m, b[0], b[1], "LOCAL_EXCLUDED")
	if err = os.Rename(s[2].Store.Root, s[2].Store.Root+"-gone"); err != nil {
		t.Fatal(err)
	}
	r, err := m.Compare(ctx, Request{From: b[0].ID, Targets: []string{b[1].ID, b[2].ID}})
	if err != nil || len(r.Targets) != 2 {
		t.Fatal(r, err)
	}
	missing := false
	for _, target := range r.Targets {
		if target.Copy.ID == b[2].ID {
			missing = target.Status == "UNAVAILABLE"
		}
	}
	if !missing {
		t.Fatal("missing target hidden")
	}
}
func TestBaselineBackupLossIsNotGuessed(t *testing.T) {
	m, b, _ := fixture(t, 2)
	baseline(t, m, b)
	control, _ := m.Control.Capture(m.Control.Root)
	base, _, err := m.baseline(control, b[0].ID, b[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	item := base.Entries[skill]
	if !item.Tree.Entries["empty"].Dir {
		t.Fatal("baseline lost empty directory")
	}
	if b, err := m.Control.Data(item.Tree.Entries["asset.bin"]); err != nil || string(b) != string([]byte{0, 255, 7}) {
		t.Fatal("baseline lost binary", err)
	}
	file := item.Tree.Entries["SKILL.md"]
	// Find the exact protected blob for this one test backup; project is retained.
	path := filepath.Join(m.Control.Local, "source-transactions", workstore.Hash([]byte(strings.ToLower(m.Control.Root))), "blobs", file.Hash)
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, err = m.Compare(context.Background(), Request{From: b[0].ID, Targets: []string{b[1].ID}})
	code(t, err, "BASELINE_BACKUP_MISSING")
}

func TestReplicaObservationCreatesNoTargetLockAndControlFailureRecovers(t *testing.T) {
	m, b, _ := fixture(t, 2)
	ctx := context.Background()
	for _, copy := range b {
		lock := filepath.Join(filepath.Dir(copy.Root), "..aidock.aidock-lock")
		if err := os.Remove(lock); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Compare(ctx, Request{From: b[0].ID, Targets: []string{b[1].ID}}); err != nil {
		t.Fatal(err)
	}
	for _, copy := range b {
		if _, err := os.Stat(filepath.Join(filepath.Dir(copy.Root), "..aidock.aidock-lock")); !os.IsNotExist(err) {
			t.Fatal("read-only observation created target lock", err)
		}
	}
	before, err := m.Control.Capture(m.Control.Root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.BaselinePlan(ctx, Request{From: b[0].ID, Targets: []string{b[1].ID}})
	if err != nil {
		t.Fatal(err)
	}
	m.Control.Hook = func(stage string, _ int) error {
		if stage == "replaced" {
			return fmt.Errorf("fixture control write failure")
		}
		return nil
	}
	_, err = m.Apply(ctx, p.ID, p.Digest)
	code(t, err, "SOURCE_APPLY_FAILED")
	after, err := m.Control.Capture(m.Control.Root)
	if err != nil || workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("control failure lost registration", err)
	}
}
