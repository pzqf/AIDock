package source

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

const owner = "11111111-1111-4111-8111-111111111111"

func manager(t *testing.T) *Manager {
	t.Helper()
	d := t.TempDir()
	m, e := New(filepath.Join(d, ".aidock"), filepath.Join(d, "local"), owner, "server")
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func write(t *testing.T, p string, b []byte) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, b, 0644); e != nil {
		t.Fatal(e)
	}
}
func apply(t *testing.T, m *Manager, r Request) Preview {
	t.Helper()
	p, e := m.Plan(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	j, e := m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest)
	if e != nil || j.State != "committed" {
		t.Fatal(j.State, e)
	}
	return p
}
func code(t *testing.T, e error, want string) {
	t.Helper()
	var v *result.Error
	if !errors.As(e, &v) || v.Code != want {
		t.Fatalf("want %s, got %v", want, e)
	}
}
func body(name string) []byte {
	return []byte("---\nname: " + name + "\ndescription: example\n---\n\noriginal\n")
}

func TestCreateEditSwitchRollbackAndUndo(t *testing.T) {
	m := manager(t)
	first := apply(t, m, Request{Action: "create", Name: "project-guide", Description: "Example", Document: "original"})
	id := first.SkillID
	original, e := m.Store.Capture(m.Store.Root)
	if e != nil {
		t.Fatal(e)
	}
	edited := apply(t, m, Request{Action: "edit", ID: id, Files: []Patch{{Path: "SKILL.md", Content: string(body("project-guide")) + "changed\n"}, {Path: "notes/draft.md", Content: "draft"}}})
	latest, e := m.Store.Capture(m.Store.Root)
	if e != nil {
		t.Fatal(e)
	}
	reverted := apply(t, m, Request{Action: "rollback", Snapshot: edited.Plan.ID})
	f, e := m.File(id, "SKILL.md")
	if e != nil || strings.Contains(f["content"].(string), "changed") {
		t.Fatal(f, e)
	}
	if _, e = os.Stat(filepath.Join(m.Store.Root, "skills", id, "notes/draft.md")); e == nil {
		t.Fatal("draft survived rollback")
	}
	apply(t, m, Request{Action: "rollback", Snapshot: reverted.Plan.ID})
	got, e := m.Store.Capture(m.Store.Root)
	if e != nil || workstore.Digest(got) != workstore.Digest(latest) {
		t.Fatal("undo did not restore all bytes", e)
	}
	apply(t, m, Request{Action: "enable", ID: id})
	apply(t, m, Request{Action: "disable", ID: id})
	state, e := m.State()
	if e != nil {
		t.Fatal(e)
	}
	s := switches(state.Profile)
	if len(s["enable"].([]any)) != 0 || s["disable"].([]any)[0] != id || state.NativeApplied {
		t.Fatal(state)
	}
	if state.Index.Skills[0].ID != id || state.Index.Skills[0].Revision == first.Plan.After.Entries["skills.yaml"].Hash {
		t.Fatal("invalid stable id/revision")
	}
	for p := range original.Entries {
		if strings.HasPrefix(p, ".revisions/") {
			if _, ok := got.Entries[p]; !ok {
				t.Fatal("old revision deleted")
			}
		}
	}
	_, e = m.Plan(context.Background(), Request{Action: "rollback", Snapshot: first.Plan.ID})
	code(t, e, "SOURCE_ROLLBACK_UNSUPPORTED")
}
func TestImmutableRevisionCannotBeSilentlyRepaired(t *testing.T) {
	m := manager(t)
	p := apply(t, m, Request{Action: "create", Name: "guide", Description: "Example"})
	state, e := m.State()
	if e != nil {
		t.Fatal(e)
	}
	revision := strings.TrimPrefix(state.Index.Skills[0].Revision, "sha256:")
	write(t, filepath.Join(m.Store.Root, ".revisions", p.SkillID, revision, "SKILL.md"), []byte("corrupt"))
	doc, e := m.File(p.SkillID, "SKILL.md")
	if e != nil {
		t.Fatal(e)
	}
	_, e = m.Plan(context.Background(), Request{Action: "edit", ID: p.SkillID, Files: []Patch{{Path: "SKILL.md", Content: doc["content"].(string)}}})
	code(t, e, "REVISION_CORRUPT")
}
func TestImportPreservesBinaryScriptBOMAndEmptyDirectory(t *testing.T) {
	m := manager(t)
	pkg := filepath.Join(t.TempDir(), "package")
	files := map[string][]byte{"SKILL.md": append([]byte{239, 187, 191}, body("import-guide")...), "image.bin": {0, 255, 100}, "scripts/never-run.ps1": []byte("throw 'must never execute'"), "notes.md": []byte("[binary](image.bin)")}
	for p, b := range files {
		write(t, filepath.Join(pkg, p), b)
	}
	if e := os.Mkdir(filepath.Join(pkg, "empty"), 0755); e != nil {
		t.Fatal(e)
	}
	imported := apply(t, m, Request{Action: "import", PackagePath: pkg, SourceRole: "local-original"})
	for p, b := range files {
		got, e := os.ReadFile(filepath.Join(m.Store.Root, "skills", imported.SkillID, p))
		if e != nil || string(got) != string(b) {
			t.Fatal(p, e)
		}
	}
	if _, e := os.Stat(filepath.Join(m.Store.Root, "skills", imported.SkillID, "empty")); e != nil {
		t.Fatal(e)
	}
	f, e := m.File(imported.SkillID, "image.bin")
	if e != nil || f["type"] != "binary" || f["content"] != "" {
		t.Fatal(f, e)
	}
	_, e = m.Plan(context.Background(), Request{Action: "edit", ID: imported.SkillID, Files: []Patch{{Path: "image.bin", Content: "corrupt"}}})
	code(t, e, "BINARY_EDIT_UNSUPPORTED")
	apply(t, m, Request{Action: "edit", ID: imported.SkillID, Files: []Patch{{Path: "notes.md", Content: "updated"}}})
	for p, b := range files {
		got, e := os.ReadFile(filepath.Join(pkg, p))
		if e != nil || string(got) != string(b) {
			t.Fatal("import changed original", p, e)
		}
	}
}
func TestBindingOwnershipCollisionsAndPackageReferences(t *testing.T) {
	m := manager(t)
	created := apply(t, m, Request{Action: "create", Name: "guide", Description: "Example"})
	requests := []struct {
		r Request
		c string
	}{
		{Request{Action: "create", Name: "guide", Description: "Duplicate"}, "NATIVE_NAME_COLLISION"},
		{Request{Action: "create", ID: created.SkillID, Name: "other", Description: "Duplicate"}, "SKILL_ID_COLLISION"},
		{Request{Action: "create", Name: "missing", Description: "Example", Document: "[missing](missing.md)"}, "PACKAGE_INVALID"},
		{Request{Action: "import", SourceRole: "generated-mirror"}, "SOURCE_ROLE_REQUIRED"},
		{Request{Action: "import", SourceRole: "plugin-owned"}, "SOURCE_ROLE_REQUIRED"},
		{Request{Action: "edit", ID: created.SkillID, Files: []Patch{{Path: "SKILL.md", Content: string(body("renamed"))}}}, "RENAME_ACTION_REQUIRED"},
	}
	before, e := m.Store.Capture(m.Store.Root)
	if e != nil {
		t.Fatal(e)
	}
	for _, x := range requests {
		_, e = m.Plan(context.Background(), x.r)
		code(t, e, x.c)
	}
	now, e := m.Store.Capture(m.Store.Root)
	if e != nil || workstore.Digest(now) != workstore.Digest(before) {
		t.Fatal("rejected plans wrote source", e)
	}
	other, e := New(m.Store.Root, m.Store.Local, "22222222-2222-4222-8222-222222222222", "server")
	if e != nil {
		t.Fatal(e)
	}
	_, e = other.State()
	code(t, e, "SOURCE_IDENTITY_MISMATCH")
	unowned := manager(t)
	write(t, filepath.Join(unowned.Store.Root, "keep.txt"), []byte("keep"))
	_, e = unowned.Plan(context.Background(), Request{Action: "create", Name: "guide", Description: "x"})
	code(t, e, "OWNERSHIP_REQUIRED")
	other = manager(t)
	_, e = other.Apply(context.Background(), created.Plan.ID)
	if e == nil {
		t.Fatal("cross root plan accepted")
	}
	_, e = m.Apply(context.Background(), created.Plan.ID, "wrong digest")
	code(t, e, "PLAN_STALE")
}
func TestMetadataAndMigrationPreservedWithRollback(t *testing.T) {
	m := manager(t)
	created := apply(t, m, Request{Action: "create", Name: "guide", Description: "Example"})
	project := []byte("schema_version: 1\nid: " + owner + "\nname: 工程\nstack: [go]\nai_tools: [codex-cli]\n")
	write(t, filepath.Join(m.Store.Root, "project.yaml"), project)
	state, e := m.State()
	if e != nil {
		t.Fatal(e)
	}
	state.Index.Skills[0].DisplayName = "项目说明"
	state.Index.Skills[0].Tags = []string{"project"}
	state.Index.Skills[0].Version = "1.2"
	state.Index.Skills[0].Source.Origin = "local-copy"
	write(t, filepath.Join(m.Store.Root, "skills.yaml"), jsonBytes(state.Index))
	oldProfile := []byte("schema_version: 1\nname: server\ndescription: preserved\nskills:\n  enable: [guide]\nplugins:\n  disable: [example]\nconfig_overrides:\n  sample: value\nrules: [keep]\n")
	write(t, filepath.Join(m.Store.Root, "profile.yaml"), oldProfile)
	_, e = m.Plan(context.Background(), Request{Action: "disable", ID: created.SkillID})
	code(t, e, "MIGRATION_REQUIRED")
	migrated := apply(t, m, Request{Action: "migrate"})
	state, e = m.State()
	if e != nil {
		t.Fatal(e)
	}
	if state.Profile["schema_version"] != 2 || state.Profile["description"] != "preserved" {
		t.Fatal(state.Profile)
	}
	migratedSkills := switches(state.Profile)
	if migratedSkills["enable"].([]any)[0] != created.SkillID || migratedSkills["pins"].(map[string]any)[created.SkillID] != state.Index.Skills[0].Revision {
		t.Fatal(migratedSkills)
	}
	undone := apply(t, m, Request{Action: "rollback", Snapshot: migrated.Plan.ID})
	got, _ := os.ReadFile(filepath.Join(m.Store.Root, "profile.yaml"))
	if string(got) != string(oldProfile) {
		t.Fatal("migration original bytes lost")
	}
	apply(t, m, Request{Action: "rollback", Snapshot: undone.Plan.ID})
	apply(t, m, Request{Action: "disable", ID: created.SkillID})
	got, _ = os.ReadFile(filepath.Join(m.Store.Root, "project.yaml"))
	if string(got) != string(project) {
		t.Fatal("project metadata changed")
	}
	state, e = m.State()
	if e != nil {
		t.Fatal(e)
	}
	s := state.Index.Skills[0]
	if s.DisplayName != "项目说明" || s.Tags[0] != "project" || s.Version != "1.2" || s.Source.Origin != "local-copy" {
		t.Fatal(s)
	}
}
func TestPendingStateShowsRecoveryBeforeMetadataComplete(t *testing.T) {
	m := manager(t)
	p, e := m.Plan(context.Background(), Request{Action: "create", Name: "guide", Description: "Example"})
	if e != nil {
		t.Fatal(e)
	}
	// Persist a journal in the same shape as a process interrupted before metadata.
	rootDir := filepath.Join(m.Store.Local, "source-transactions", workstore.Hash([]byte(strings.ToLower(m.Store.Root))), "transactions")
	b, _ := json.Marshal(workstore.Journal{Plan: p.Plan, State: "prepared"})
	write(t, filepath.Join(rootDir, p.Plan.ID+".json"), b)
	write(t, filepath.Join(m.Store.Root, ".revisions", "partial.txt"), []byte("external"))
	state, e := m.State()
	if e != nil || !state.Pending || state.Warning == "" {
		t.Fatal(state, e)
	}
}
