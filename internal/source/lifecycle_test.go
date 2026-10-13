package source

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

func current(t *testing.T, m *Manager) State {
	t.Helper()
	s, e := m.State()
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func capture(t *testing.T, m *Manager) workstore.Tree {
	t.Helper()
	s, e := m.Store.Capture(m.Store.Root)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func unchanged(t *testing.T, before, after workstore.Tree) {
	t.Helper()
	if workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("source bytes changed")
	}
}
func effect(t *testing.T, p Preview, id, from, to string) {
	t.Helper()
	if len(p.Plan.AssetEffects) != 1 {
		t.Fatal(p.Plan.AssetEffects)
	}
	x := p.Plan.AssetEffects[0]
	if x.Owner != owner || x.Scope != "server" || x.ID != id || x.Before != from || x.After != to || x.Kind != "skill-retirement" {
		t.Fatal(x)
	}
}

func TestMetadataChangesOnlyIndexAndCanClear(t *testing.T) {
	m := manager(t)
	p := apply(t, m, Request{Action: "create", Name: "guide", Description: "example"})
	before := capture(t, m)
	revision := current(t, m).Index.Skills[0].Revision
	name, tags := " 项目约定 ", []string{"项目", "server"}
	updated := apply(t, m, Request{Action: "metadata", ID: p.SkillID, DisplayName: &name, Tags: &tags})
	if len(updated.Plan.Changes) != 1 || updated.Plan.Changes[0].Path != "skills.yaml" || len(updated.Plan.AssetEffects) != 0 {
		t.Fatal(updated.Plan)
	}
	s := current(t, m).Index.Skills[0]
	if s.DisplayName != "项目约定" || len(s.Tags) != 2 || s.Revision != revision {
		t.Fatal(s)
	}
	for path, entry := range before.Entries {
		if path != "skills.yaml" && updated.Plan.After.Entries[path] != entry {
			t.Fatal("metadata changed package/profile", path)
		}
	}
	name, tags = "", []string{}
	apply(t, m, Request{Action: "metadata", ID: p.SkillID, DisplayName: &name, Tags: &tags})
	s = current(t, m).Index.Skills[0]
	if s.DisplayName != "" || len(s.Tags) != 0 || s.Revision != revision {
		t.Fatal(s)
	}
	for _, r := range []Request{
		{Action: "metadata", ID: p.SkillID},
		{Action: "metadata", ID: p.SkillID, DisplayName: ptr(strings.Repeat("界", 81))},
		{Action: "metadata", ID: p.SkillID, Tags: ptr([]string{"same", "same"})},
		{Action: "rename", ID: p.SkillID, Name: "new", Reason: "ignored"},
		{Action: "enable", ID: p.SkillID, Files: []Patch{{Path: "SKILL.md", Content: "ignored"}}},
	} {
		before := capture(t, m)
		if _, e := m.Plan(context.Background(), r); e == nil {
			t.Fatal("invalid request accepted", r)
		}
		unchanged(t, before, capture(t, m))
	}
}
func ptr[T any](v T) *T { return &v }

func TestRetirementPreservesPackageRollbackAndUndo(t *testing.T) {
	m := manager(t)
	p := apply(t, m, Request{Action: "create", Name: "guide", Description: "example"})
	apply(t, m, Request{Action: "edit", ID: p.SkillID, Files: []Patch{{Path: "notes/draft.md", Content: "saved draft"}}})
	apply(t, m, Request{Action: "enable", ID: p.SkillID})
	before := capture(t, m)
	retirement := apply(t, m, Request{Action: "retire", ID: p.SkillID, Reason: " 已被新版替代 "})
	effect(t, retirement, p.SkillID, "active", "retired")
	s := current(t, m)
	if len(s.Retirements) != 1 || s.Retirements[0].Reason != "已被新版替代" || len(switches(s.Profile)["enable"].([]any)) != 0 || switches(s.Profile)["disable"].([]any)[0] != p.SkillID {
		t.Fatal(s)
	}
	for path, entry := range before.Entries {
		if strings.HasPrefix(path, "skills/") || strings.HasPrefix(path, ".revisions/") {
			if retirement.Plan.After.Entries[path] != entry {
				t.Fatal("retired package lost", path)
			}
		}
	}
	for _, action := range []string{"enable", "edit", "rename", "metadata", "retire"} {
		_, e := m.Plan(context.Background(), Request{Action: action, ID: p.SkillID})
		code(t, e, "SKILL_RETIRED")
	}
	for _, action := range []string{"delete", "remove"} {
		_, e := m.Plan(context.Background(), Request{Action: action, ID: p.SkillID})
		code(t, e, "DELETE_SCOPE_UNKNOWN")
	}
	_, e := m.Plan(context.Background(), Request{Action: "create", Name: "guide", Description: "collision"})
	code(t, e, "NATIVE_NAME_COLLISION")
	retiredTree := capture(t, m)
	reverted := apply(t, m, Request{Action: "rollback", Snapshot: retirement.Plan.ID})
	effect(t, reverted, p.SkillID, "retired", "active")
	unchanged(t, before, capture(t, m))
	undo := apply(t, m, Request{Action: "rollback", Snapshot: reverted.Plan.ID})
	effect(t, undo, p.SkillID, "active", "retired")
	unchanged(t, retiredTree, capture(t, m))
	reactivated := apply(t, m, Request{Action: "reactivate", ID: p.SkillID})
	effect(t, reactivated, p.SkillID, "retired", "active")
	s = current(t, m)
	if len(s.Retirements) != 0 || len(switches(s.Profile)["enable"].([]any)) != 0 || switches(s.Profile)["disable"].([]any)[0] != p.SkillID {
		t.Fatal("reactivation enabled asset", s)
	}
	apply(t, m, Request{Action: "enable", ID: p.SkillID})
}

func TestReferenceTuplesAndConservativeRetirement(t *testing.T) {
	m := manager(t)
	a := apply(t, m, Request{Action: "create", Name: "base", Description: "example"})
	b := apply(t, m, Request{Action: "create", Name: "dependent", Description: "example"})
	apply(t, m, Request{Action: "enable", ID: a.SkillID})
	s := current(t, m)
	for i := range s.Index.Skills {
		skill := &s.Index.Skills[i]
		if skill.ID != b.SkillID {
			continue
		}
		rev := switches(s.Profile)["pins"].(map[string]any)[a.SkillID].(string)
		skill.Dependencies["skills"] = []any{
			map[string]any{"owner": owner, "scope_id": "server", "id": a.SkillID, "revision": rev, "required": false},
			map[string]any{"owner": "global", "scope_id": "server", "id": a.SkillID, "revision": rev, "required": true},
			map[string]any{"owner": owner, "scope_id": "client", "id": a.SkillID, "revision": rev, "required": true},
		}
		skill.BaseSource = map[string]string{"owner": owner, "scope_id": "server", "id": a.SkillID}
		skill.BaseRevision = rev
		skill.Upstream = map[string]string{"owner": owner, "scope_id": "server", "id": a.SkillID, "revision": rev}
	}
	write(t, filepath.Join(m.Store.Root, "skills.yaml"), jsonBytes(s.Index))
	impact, e := m.References(a.SkillID)
	if e != nil {
		t.Fatal(e)
	}
	kinds := map[string]int{}
	for _, ref := range impact.References {
		kinds[ref.Kind]++
		if ref.Kind != "profile" && ref.Kind != "profile-pin" && !ref.Blocking {
			t.Fatal(ref)
		}
	}
	if kinds["dependency"] != 1 || kinds["base_source"] != 1 || kinds["upstream"] != 1 || kinds["profile"] != 1 || kinds["profile-pin"] != 1 || len(impact.Unknown) != 2 || impact.ReferenceScope != "CURRENT_BOUND_SOURCE" {
		t.Fatal(impact)
	}
	before := capture(t, m)
	_, e = m.Plan(context.Background(), Request{Action: "retire", ID: a.SkillID, Reason: "unused"})
	code(t, e, "SKILL_REFERENCED")
	var failure *result.Error
	if !errors.As(e, &failure) || failure.Recovery.(*Impact).ID != a.SkillID {
		t.Fatal(e)
	}
	unchanged(t, before, capture(t, m))
	// An ID reference remains pinned to its original immutable package on rename.
	rename := apply(t, m, Request{Action: "rename", ID: a.SkillID, Name: "base-v2"})
	if len(rename.Impact.References) != 5 || rename.Impact.NativeCleanup != "PENDING_NATIVE_APPLY" {
		t.Fatal(rename.Impact)
	}
	_, e = m.References(workstore.ID())
	code(t, e, "SKILL_NOT_FOUND")
}

func TestRetirementDriftAndCorruptMetadataFailClosed(t *testing.T) {
	for _, action := range []string{"retire", "rename"} {
		t.Run("drift-"+action, func(t *testing.T) {
			m := manager(t)
			p := apply(t, m, Request{Action: "create", Name: "guide", Description: "example"})
			write(t, filepath.Join(m.Store.Root, "skills", p.SkillID, "external.txt"), []byte("external"))
			before := capture(t, m)
			_, e := m.Plan(context.Background(), Request{Action: action, ID: p.SkillID, Name: map[string]string{"rename": "guide-v2"}[action], Reason: map[string]string{"retire": "unused"}[action]})
			code(t, e, "SOURCE_PACKAGE_DRIFT")
			unchanged(t, before, capture(t, m))
		})
	}
	for _, kind := range []string{"owner", "scope", "duplicate", "revision", "time", "unknown", "enabled"} {
		t.Run(kind, func(t *testing.T) {
			m := manager(t)
			p := apply(t, m, Request{Action: "create", Name: "guide", Description: "example"})
			apply(t, m, Request{Action: "retire", ID: p.SkillID, Reason: "unused"})
			s := current(t, m)
			life := Lifecycle{Version: 1, Owner: owner, Scope: "server", Retirements: s.Retirements}
			want := "SOURCE_METADATA_INVALID"
			switch kind {
			case "owner":
				life.Owner = workstore.ID()
				want = "SOURCE_IDENTITY_MISMATCH"
			case "scope":
				life.Scope = "client"
				want = "SOURCE_IDENTITY_MISMATCH"
			case "duplicate":
				life.Retirements = append(life.Retirements, life.Retirements[0])
			case "revision":
				life.Retirements[0].Revision = "sha256:" + strings.Repeat("0", 64)
			case "time":
				life.Retirements[0].RetiredAt = "yesterday"
			case "unknown":
				life.Retirements[0].ID = workstore.ID()
			case "enabled":
				setEnabled(s.Profile, p.SkillID, true)
				write(t, filepath.Join(m.Store.Root, "profile.yaml"), jsonBytes(s.Profile))
				want = "SKILL_RETIRED"
			}
			write(t, filepath.Join(m.Store.Root, lifecycleFile), jsonBytes(life))
			before := capture(t, m)
			_, e := m.State()
			code(t, e, want)
			_, e = m.References(p.SkillID)
			code(t, e, want)
			_, e = m.Plan(context.Background(), Request{Action: "enable", ID: p.SkillID})
			code(t, e, want)
			unchanged(t, before, capture(t, m))
		})
	}
}

func TestRetirementFailureStaleAndForgedEffects(t *testing.T) {
	m := manager(t)
	p := apply(t, m, Request{Action: "create", Name: "guide", Description: "example"})
	apply(t, m, Request{Action: "enable", ID: p.SkillID})
	before := capture(t, m)
	r, e := m.Plan(context.Background(), Request{Action: "retire", ID: p.SkillID, Reason: "unused"})
	if e != nil {
		t.Fatal(e)
	}
	m.Store.Hook = func(phase string, n int) error {
		if phase == "replaced" && n == 2 {
			return errors.New("injected after lifecycle write")
		}
		return nil
	}
	_, e = m.Apply(context.Background(), r.Plan.ID, r.Plan.Digest)
	code(t, e, "SOURCE_APPLY_FAILED")
	m.Store.Hook = nil
	unchanged(t, before, capture(t, m))
	if len(current(t, m).Retirements) != 0 {
		t.Fatal("failed retirement survived")
	}
	r, e = m.Plan(context.Background(), Request{Action: "retire", ID: p.SkillID, Reason: "unused"})
	if e != nil {
		t.Fatal(e)
	}
	write(t, filepath.Join(m.Store.Root, "skills", p.SkillID, "SKILL.md"), append(body("guide"), []byte("external")...))
	external := capture(t, m)
	_, e = m.Apply(context.Background(), r.Plan.ID, r.Plan.Digest)
	code(t, e, "PLAN_STALE")
	unchanged(t, external, capture(t, m))
	// Restore only this test's external bytes, then create a structurally valid plan
	// with forged descriptive effects. Manager validation must reject it.
	b, e := m.Store.Data(before.Entries["skills/"+p.SkillID+"/SKILL.md"])
	if e != nil {
		t.Fatal(e)
	}
	write(t, filepath.Join(m.Store.Root, "skills", p.SkillID, "SKILL.md"), b)
	forged := r.Plan.AssetEffects[0]
	forged.ID = workstore.ID()
	f, e := m.Store.Preview("retire", r.Plan.Before, r.Plan.After, forged)
	if e != nil {
		t.Fatal(e)
	}
	_, e = m.Apply(context.Background(), f.ID, f.Digest)
	code(t, e, "PLAN_STALE")
	unchanged(t, before, capture(t, m))
}

func TestEditorExpectedHashProtectsChangesBeforePreview(t *testing.T) {
	m := manager(t)
	p := apply(t, m, Request{Action: "create", Name: "guide", Description: "example"})
	f, e := m.File(p.SkillID, "SKILL.md")
	if e != nil {
		t.Fatal(e)
	}
	write(t, filepath.Join(m.Store.Root, "skills", p.SkillID, "SKILL.md"), append(body("guide"), []byte("external")...))
	before := capture(t, m)
	_, e = m.Plan(context.Background(), Request{Action: "edit", ID: p.SkillID, Files: []Patch{{Path: "SKILL.md", Content: f["content"].(string), ExpectedSHA256: f["sha256"].(string)}}})
	code(t, e, "PLAN_STALE")
	unchanged(t, before, capture(t, m))
}

func TestLifecycleCrashHelper(t *testing.T) {
	if os.Getenv("AIDOCK_LIFECYCLE_CRASH") != "1" {
		return
	}
	m, e := New(os.Getenv("AIDOCK_TEST_SOURCE"), os.Getenv("AIDOCK_TEST_LOCAL"), owner, "server")
	if e != nil {
		t.Fatal(e)
	}
	m.Store.Hook = func(phase string, n int) error {
		if phase == "replaced" && n == 2 {
			os.Exit(37)
		}
		return nil
	}
	_, e = m.Apply(context.Background(), os.Getenv("AIDOCK_TEST_PLAN"))
	t.Fatal("crash hook not reached", e)
}
func TestRetirementProcessCrashRecoveryKeepsEffects(t *testing.T) {
	m := manager(t)
	p := apply(t, m, Request{Action: "create", Name: "guide", Description: "example"})
	apply(t, m, Request{Action: "enable", ID: p.SkillID})
	before := capture(t, m)
	r, e := m.Plan(context.Background(), Request{Action: "retire", ID: p.SkillID, Reason: "unused"})
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestLifecycleCrashHelper$")
	cmd.Env = append(os.Environ(), "AIDOCK_LIFECYCLE_CRASH=1", "AIDOCK_TEST_SOURCE="+m.Store.Root, "AIDOCK_TEST_LOCAL="+m.Store.Local, "AIDOCK_TEST_PLAN="+r.Plan.ID)
	output, e := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(e, &exit) || exit.ExitCode() != 37 {
		t.Fatalf("expected process exit 37: %v %s", e, output)
	}
	fresh, e := New(m.Store.Root, m.Store.Local, owner, "server")
	if e != nil {
		t.Fatal(e)
	}
	s := current(t, fresh)
	if !s.Pending {
		t.Fatal("crash not pending")
	}
	_, e = fresh.Plan(context.Background(), Request{Action: "reactivate", ID: p.SkillID})
	code(t, e, "RECOVERY_CONFLICT")
	n, e := fresh.Recover()
	if e != nil || n != 1 {
		t.Fatal(n, e)
	}
	unchanged(t, before, capture(t, fresh))
	js, e := fresh.Store.Journals()
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, j := range js {
		if j.Plan.ID == r.Plan.ID {
			found = true
			if j.State != "recovered" || len(j.Plan.AssetEffects) != 1 {
				t.Fatal(j)
			}
		}
	}
	if !found {
		t.Fatal("recovery journal lost")
	}
	n, e = fresh.Recover()
	if e != nil || n != 0 {
		t.Fatal(n, e)
	}
}
