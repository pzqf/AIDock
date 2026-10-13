package native

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pzqf/AIDock/internal/bridge"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

const owner = "11111111-1111-4111-8111-111111111111"

func write(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0644); err != nil {
		t.Fatal(err)
	}
}
func applySource(t *testing.T, m *source.Manager, req source.Request) source.Preview {
	t.Helper()
	p, err := m.Plan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	return p
}
func fixture(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	m, err := source.New(filepath.Join(root, ".aidock"), filepath.Join(t.TempDir(), "local"), owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(t.TempDir(), "package")
	write(t, filepath.Join(pkg, "SKILL.md"), []byte("---\nname: guide\ndescription: example\nallowed-tools: Read\n---\n[details](references/notes.md)\n"))
	write(t, filepath.Join(pkg, "references/notes.md"), []byte("中文参考"))
	write(t, filepath.Join(pkg, "assets/data.bin"), []byte{0, 1, 255})
	if err := os.MkdirAll(filepath.Join(pkg, "empty"), 0755); err != nil {
		t.Fatal(err)
	}
	p := applySource(t, m, source.Request{Action: "import", PackagePath: pkg, SourceRole: "local-original"})
	applySource(t, m, source.Request{Action: "enable", ID: p.SkillID})
	n, err := New(m, root)
	if err != nil {
		t.Fatal(err)
	}
	return n, p.SkillID
}
func has(p Plan, code string) bool {
	for _, t := range p.Targets {
		for _, f := range t.Findings {
			if f.Code == code {
				return true
			}
		}
	}
	return false
}
func code(t *testing.T, err error, want string) {
	t.Helper()
	var e *result.Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}
func observe(t *testing.T, m *Manager, root string) workstore.Tree {
	t.Helper()
	v, err := m.Source.Store.Capture(root)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func unchanged(t *testing.T, m *Manager, root string, before workstore.Tree) {
	t.Helper()
	if !reflect.DeepEqual(before, observe(t, m, root)) {
		t.Fatal("preview modified", root)
	}
}

func TestCompletePreviewPersistsRechecksAndNeverWrites(t *testing.T) {
	m, _ := fixture(t)
	before := observe(t, m, m.Source.Store.Root)
	p, err := m.Plan(context.Background(), Request{Tools: []string{"codex-cli", "claude-code"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != "native-config" || p.Blocked || p.WriteSupported || len(p.Targets) != 2 || p.Evidence["L2"] != "UNKNOWN" {
		t.Fatal(p)
	}
	if p.Targets[0].Adapter.ID != "claude-code" || p.Targets[1].Root != filepath.Join(m.Root, ".agents", "skills") {
		t.Fatal(p.Targets)
	}
	for _, target := range p.Targets {
		if _, err := os.Stat(target.Root); !os.IsNotExist(err) {
			t.Fatal("native target created")
		}
		tree := target.Packages[0].Tree
		if !tree.Entries["empty"].Dir || tree.Entries["assets/data.bin"].Hash != workstore.Hash([]byte{0, 1, 255}) {
			t.Fatal("incomplete package")
		}
		b, err := m.Source.Store.Data(tree.Entries["SKILL.md"])
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != "---\nname: guide\ndescription: example\nallowed-tools: Read\n---\n[details](references/notes.md)\n" {
			t.Fatal("native frontmatter altered")
		}
	}
	unchanged(t, m, m.Source.Store.Root, before)
	other, err := m.Plan(context.Background(), Request{Tools: []string{"claude-code", "codex-cli"}})
	if err != nil || other.ContentDigest != p.ContentDigest {
		t.Fatal("non deterministic", err)
	}
	// A new manager/process can read the persisted preview using the same roots.
	reopened, err := New(m.Source, m.Root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.Diff(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	code(t, m.Apply(context.Background(), p.ID, p.Digest), "NATIVE_WRITE_UNAVAILABLE")
	code(t, m.Apply(context.Background(), p.ID, "wrong"), "PLAN_CONFIRMATION_REQUIRED")
	unchanged(t, m, m.Source.Store.Root, before)
	write(t, filepath.Join(m.Root, ".agents/skills/unrelated/notes.txt"), []byte("user"))
	code(t, m.Apply(context.Background(), p.ID, p.Digest), "PLAN_STALE")
	if b, _ := os.ReadFile(filepath.Join(m.Root, ".agents/skills/unrelated/notes.txt")); string(b) != "user" {
		t.Fatal("target changed")
	}
}
func TestUnownedEqualPackageStillCollidesAndOtherEntriesSurvive(t *testing.T) {
	m, _ := fixture(t)
	seed, err := m.Plan(context.Background(), Request{Tools: []string{"claude-code"}})
	if err != nil {
		t.Fatal(err)
	}
	materialize(t, m, seed.Targets[0])
	if err := os.Remove(filepath.Join(seed.Targets[0].Root, ownershipFile)); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(m.Root, ".agents/skills/unrelated/notes.txt"), []byte("user"))
	before := observe(t, m, filepath.Join(m.Root, ".claude/skills"))
	p, err := m.Plan(context.Background(), Request{Tools: []string{"claude-code", "codex-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Blocked || !has(p, "NATIVE_TARGET_COLLISION") || len(p.Targets[0].Changes) != 0 {
		t.Fatal(p)
	}
	if p.Targets[1].After.Entries["unrelated/notes.txt"] != p.Targets[1].Before.Entries["unrelated/notes.txt"] {
		t.Fatal("unrelated lost")
	}
	code(t, m.Apply(context.Background(), p.ID, p.Digest), "NATIVE_PREFLIGHT_FAILED")
	unchanged(t, m, filepath.Join(m.Root, ".claude/skills"), before)
}

func TestRegisteredAuthorityCannotBecomeNativeTarget(t *testing.T) {
	m, _ := fixture(t)
	file := filepath.Join(t.TempDir(), "bridge.json")
	reg := bridge.Registration{ID: "33333333-3333-4333-8333-333333333333", Project: owner, Scope: "server", Copy: "test", Root: filepath.Join(m.Root, ".claude"), SourcePath: "skills", SourceRole: "external-source", Format: "native-skill", Packages: []string{"guide"}}
	write(t, file, jsonData(bridge.Index{Version: 1, Bridges: []bridge.Registration{reg}}))
	if err := m.Source.BindBridge(file, "test"); err != nil {
		t.Fatal(err)
	}
	_, err := m.Plan(context.Background(), Request{Tools: []string{"claude-code"}})
	code(t, err, "EXTERNAL_TARGET_OVERLAP")
	// The observation refuses overlap before reading or initializing authority.
	if _, err := os.Stat(reg.Root); !os.IsNotExist(err) {
		t.Fatal("authority created")
	}
}

func TestReadOnlyLibraryCannotBecomeNativeTarget(t *testing.T) {
	m, _ := fixture(t)
	if err := os.MkdirAll(filepath.Join(m.Root, ".agents"), 0755); err != nil {
		t.Fatal(err)
	}
	lib, err := source.NewGlobal(filepath.Join(m.Root, ".agents/skills"), filepath.Join(t.TempDir(), "local"))
	if err != nil {
		t.Fatal(err)
	}
	applySource(t, lib, source.Request{Action: "create", Name: "library-guide", Description: "global"})
	if err := m.Source.AttachLibrary(lib.Store.Root); err != nil {
		t.Fatal(err)
	}
	before := observe(t, m, lib.Store.Root)
	_, err = New(m.Source, m.Root)
	code(t, err, "INVALID_ROOTS")
	unchanged(t, m, lib.Store.Root, before)
}

// Materialize a proposed fixture tree only inside t.TempDir. There is no native
// writer in production; this seeds last-owned outputs for drift/idempotency QA.
func materialize(t *testing.T, m *Manager, target Target) {
	t.Helper()
	for p, e := range target.After.Entries {
		full := filepath.Join(target.Root, filepath.FromSlash(p))
		if e.Dir {
			if err := os.MkdirAll(full, os.FileMode(e.Mode)); err != nil {
				t.Fatal(err)
			}
			continue
		}
		b, err := m.Source.Store.Data(e)
		if err != nil {
			t.Fatal(err)
		}
		write(t, full, b)
		if err = os.Chmod(full, os.FileMode(e.Mode)); err != nil {
			t.Fatal(err)
		}
	}
}
func TestOwnedIdempotencyDisableRemovalAndDrift(t *testing.T) {
	m, id := fixture(t)
	p, err := m.Plan(context.Background(), Request{Tools: []string{"claude-code"}})
	if err != nil {
		t.Fatal(err)
	}
	materialize(t, m, p.Targets[0])
	write(t, filepath.Join(p.Targets[0].Root, "unrelated/notes.txt"), []byte("manual"))
	q, err := m.Plan(context.Background(), Request{Tools: []string{"claude-code"}})
	if err != nil || q.Blocked || len(q.Targets[0].Changes) != 0 {
		t.Fatal("not idempotent", q, err)
	}
	applySource(t, m.Source, source.Request{Action: "disable", ID: id})
	_, err = m.Diff(context.Background(), q.ID, q.Digest)
	code(t, err, "PLAN_STALE")
	q, err = m.Plan(context.Background(), Request{Tools: []string{"claude-code"}})
	if err != nil || q.Blocked {
		t.Fatal(q, err)
	}
	if _, ok := q.Targets[0].After.Entries["guide"]; ok {
		t.Fatal("disabled package retained in proposal")
	}
	if _, ok := q.Targets[0].After.Entries["unrelated/notes.txt"]; !ok {
		t.Fatal("unrelated removed")
	}
	// User edits to a disabled/obsolete package must also block removal.
	write(t, filepath.Join(p.Targets[0].Root, "guide/assets/data.bin"), []byte{0, 2, 254})
	before := observe(t, m, p.Targets[0].Root)
	q, err = m.Plan(context.Background(), Request{Tools: []string{"claude-code"}})
	if err != nil || !q.Blocked || !has(q, "NATIVE_TARGET_DRIFT") || len(q.Targets[0].Changes) != 0 {
		t.Fatal(q, err)
	}
	unchanged(t, m, p.Targets[0].Root, before)
}
func TestBindingKindTamperAndReservedNames(t *testing.T) {
	m, id := fixture(t)
	_, err := New(m.Source, t.TempDir())
	code(t, err, "NATIVE_BINDING_MISMATCH")
	for _, ids := range [][]string{nil, {"unsupported"}, {"codex-cli", "codex-cli"}} {
		if _, err := m.Plan(context.Background(), Request{Tools: ids}); err == nil {
			t.Fatal("invalid selection accepted")
		}
	}
	s := applySource(t, m.Source, source.Request{Action: "rename", ID: id, Name: "synced"})
	_, err = m.Diff(context.Background(), s.Plan.ID, s.Plan.Digest)
	code(t, err, "PLAN_KIND_MISMATCH")
	p, err := m.Plan(context.Background(), Request{Tools: []string{"claude-code", "codex-cli"}})
	if err != nil || !p.Blocked || !has(p, "NATIVE_RESERVED_NAME") {
		t.Fatal(p, err)
	}
	b, _ := os.ReadFile(m.file(p.ID))
	write(t, m.file(p.ID), append(b, []byte("{}")...))
	_, err = m.Diff(context.Background(), p.ID, p.Digest)
	code(t, err, "PLAN_CORRUPT")
}
func TestForeignOwnershipAndFileSlotsBlock(t *testing.T) {
	m, _ := fixture(t)
	root := filepath.Join(m.Root, ".claude/skills")
	write(t, filepath.Join(root, ownershipFile), []byte(`{"schema_version":1,"owner":"foreign"}`))
	p, err := m.Plan(context.Background(), Request{Tools: []string{"claude-code"}})
	if err != nil || !p.Blocked || !has(p, "NATIVE_OWNERSHIP_INVALID") {
		t.Fatal(p, err)
	}
	write(t, filepath.Join(m.Root, ".agents/skills/guide"), []byte("file slot"))
	p, err = m.Plan(context.Background(), Request{Tools: []string{"codex-cli"}})
	if err != nil || !p.Blocked || !has(p, "NATIVE_TARGET_COLLISION") {
		t.Fatal(p, err)
	}
}
