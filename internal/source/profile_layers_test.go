package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func ruleProfile(name string, rules ...string) map[string]any {
	texts := []any{}
	for _, rule := range rules {
		texts = append(texts, rule)
	}
	return map[string]any{"schema_version": 2, "name": name, "rules": texts}
}
func rulesLibrary(t *testing.T, m *Manager) *Manager {
	t.Helper()
	root := filepath.Join(t.TempDir(), "library")
	l, err := NewGlobal(root, m.Store.Local)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "library.yaml"), jsonBytes(map[string]any{"schema_version": 1, "owner": "global", "scope_id": "global"}))
	write(t, filepath.Join(root, "skills.yaml"), jsonBytes(Index{Version: 1, Owner: "global", Scope: "global", Skills: []Skill{}}))
	write(t, filepath.Join(root, "profile.yaml"), jsonBytes(ruleProfile("defaults", "全局默认")))
	return l
}
func reloadCopy(t *testing.T, m *Manager) *Manager {
	t.Helper()
	next, err := New(m.Store.Root, m.Store.Local, m.Owner, m.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if err = next.BindBridge("", m.Copy); err != nil {
		t.Fatal(err)
	}
	return next
}
func saveLocal(t *testing.T, m *Manager, req ProfileEdit) Preview {
	t.Helper()
	state, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	req.Target = "local"
	req.ExpectedSHA256 = state.Layers.LocalSHA256
	req.ExpectedLayers = state.Layers.Digest
	out, err := m.ProfilePlan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyProfile(context.Background(), "local", out.Plan.ID, out.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPersistentProfileLayersDriveExportAndKeepSharedCatalogPortable(t *testing.T) {
	m := manager(t)
	created := apply(t, m, Request{Action: "create", Name: "guide", Description: "guide"})
	apply(t, m, Request{Action: "enable", ID: created.SkillID})
	m.Copy = "copy-one"
	l := rulesLibrary(t, m)
	write(t, filepath.Join(l.Store.Root, "profiles", "base.yaml"), jsonBytes(ruleProfile("base", "基础规则")))
	child := ruleProfile("child", "子级规则")
	child["extends"] = "base"
	write(t, filepath.Join(l.Store.Root, "profiles", "child.yaml"), jsonBytes(child))
	state, _ := m.Profile()
	state.Profile["rules"] = []any{"项目规则"}
	edit, err := m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), edit.Plan.ID, edit.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	before := capture(t, m)
	local := ruleProfile("此副本", "本机规则")
	local["skills"] = map[string]any{"disable": []any{created.SkillID}}
	link := l.Store.Root
	saved := saveLocal(t, m, ProfileEdit{Profile: local, LibraryRoot: &link})
	unchangedManager(t, m, before)
	m = reloadCopy(t, m)
	state, err = m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	state.Profile["extends"] = "child"
	edit, err = m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: state.SHA256, ExpectedLayers: state.Layers.Digest, Profile: state.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), edit.Plan.ID, edit.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	exported, err := m.Export()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(exported.Rules, []string{"全局默认", "基础规则", "子级规则", "项目规则", "本机规则"}) || len(exported.Packages) != 0 || exported.ConfigurationDigest == "" {
		t.Fatal(exported)
	}
	shared, err := m.Shared()
	if err != nil || len(shared.Entries) != 1 || !shared.Entries[0].Enabled {
		t.Fatal(shared, err)
	}
	if exported.ProfileReport.Layers[len(exported.ProfileReport.Layers)-1].ID != "local" {
		t.Fatal(exported.ProfileReport)
	}
	// A distinct registered copy shares the project source, not machine state.
	other := *m
	other.Copy = "copy-two"
	other.Library = nil
	other.ProfileLibrary = false
	otherState, err := other.Profile()
	if err != nil {
		t.Fatal(err)
	}
	if otherState.Layers.Local != nil || otherState.Layers.LibraryRoot != "" {
		t.Fatal("machine binding leaked to other copy")
	}
	// Source edits made after the local save cannot be silently overwritten by
	// a stale machine restore plan.
	_, err = m.ProfilePlan(context.Background(), ProfileEdit{Target: "local", Snapshot: saved.Plan.ID, ExpectedSHA256: state.Layers.LocalSHA256})
	code(t, err, "PROFILE_RESOLUTION_FAILED")
}

func TestLocalProfileClearRollbackUndoAndFailurePreserveOriginalBytes(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "guide"})
	m.Copy = "copy-local"
	before := capture(t, m)
	first := saveLocal(t, m, ProfileEdit{Profile: ruleProfile("local", "仅本机")})
	m = reloadCopy(t, m)
	cleared := saveLocal(t, m, ProfileEdit{Clear: true})
	state, err := m.Profile()
	if err != nil || state.Layers.Local != nil {
		t.Fatal(state, err)
	}
	restored := saveLocal(t, m, ProfileEdit{Snapshot: cleared.Plan.ID})
	state, err = m.Profile()
	if err != nil || state.Layers.Local == nil {
		t.Fatal(state, err)
	}
	saveLocal(t, m, ProfileEdit{Snapshot: restored.Plan.ID})
	state, err = m.Profile()
	if err != nil || state.Layers.Local != nil {
		t.Fatal(state, err)
	}
	unchangedManager(t, m, before)
	_, err = m.Apply(context.Background(), first.Plan.ID, first.Plan.Digest)
	if err == nil {
		t.Fatal("shared writer accepted machine plan")
	}
	state, _ = m.Profile()
	plan, err := m.ProfilePlan(context.Background(), ProfileEdit{Target: "local", ExpectedSHA256: state.Layers.LocalSHA256, Profile: ruleProfile("local", "after")})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := m.localProfileStore()
	original, _ := s.Capture(s.Root)
	s.Hook = func(stage string, n int) error {
		if stage == "replaced" && n == 1 {
			return errors.New("injected write failure")
		}
		return nil
	}
	_, err = s.Apply(context.Background(), plan.Plan.ID)
	if err == nil {
		t.Fatal("injected failure ignored")
	}
	current, _ := s.Capture(s.Root)
	if workstore.Digest(current) != workstore.Digest(original) {
		t.Fatal("failed local save lost original bytes")
	}
}

func TestProfileLayerStaleLibraryAndSourceEditsBlockConfirmation(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "guide"})
	m.Copy = "copy-stale"
	l := rulesLibrary(t, m)
	link := l.Store.Root
	state, _ := m.Profile()
	p, err := m.ProfilePlan(context.Background(), ProfileEdit{Target: "local", ExpectedSHA256: state.Layers.LocalSHA256, LibraryRoot: &link})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(l.Store.Root, "profile.yaml"), jsonBytes(ruleProfile("defaults", "changed")))
	_, err = m.ApplyProfile(context.Background(), "local", p.Plan.ID, p.Plan.Digest)
	code(t, err, "PLAN_STALE")
	saveLocal(t, m, ProfileEdit{LibraryRoot: &link})
	m = reloadCopy(t, m)
	state, _ = m.Profile()
	state.Profile["description"] = "shared draft"
	p, err = m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: state.SHA256, ExpectedLayers: state.Layers.Digest, Profile: state.Profile})
	if err != nil {
		t.Fatal(err)
	}
	saveLocal(t, m, ProfileEdit{Profile: ruleProfile("local", "late change")})
	_, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest)
	code(t, err, "PLAN_STALE")
}

func TestProfileLocalBindingAndInvalidLayersCannotBeSilentlyAccepted(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "guide"})
	_, err := m.ProfilePlan(context.Background(), ProfileEdit{Target: "local", ExpectedSHA256: "x", Profile: ruleProfile("local")})
	code(t, err, "PROFILE_COPY_REQUIRED")
	m.Copy = "one"
	state, _ := m.Profile()
	bad := ruleProfile("local")
	bad["extends"] = "outside"
	_, err = m.ProfilePlan(context.Background(), ProfileEdit{Target: "local", ExpectedSHA256: state.Layers.LocalSHA256, Profile: bad})
	code(t, err, "PROFILE_LAYER_INVALID")
	saveLocal(t, m, ProfileEdit{Profile: ruleProfile("local", "safe")})
	s, _ := m.localProfileStore()
	write(t, filepath.Join(s.Root, "unmanaged.txt"), []byte("keep"))
	_, err = m.Profile()
	code(t, err, "PROFILE_LOCAL_INVALID")
	if b, _ := os.ReadFile(filepath.Join(s.Root, "unmanaged.txt")); string(b) != "keep" {
		t.Fatal("unknown file changed")
	}
}

func TestNamedProfileLibraryCyclesAndConcurrentEditsBlockSave(t *testing.T) {
	m := manager(t)
	l := rulesLibrary(t, m)
	state, err := l.Profile()
	if err != nil {
		t.Fatal(err)
	}
	a := ruleProfile("a", "A")
	b := ruleProfile("b", "B")
	b["extends"] = "a"
	items := []map[string]any{a, b}
	p, err := l.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile, NamedProfiles: &items, ExpectedNamed: state.NamedSHA256})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	state, _ = l.Profile()
	a["extends"] = "b"
	_, err = l.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile, NamedProfiles: &items, ExpectedNamed: state.NamedSHA256})
	code(t, err, "PROFILE_EXTENDS_CYCLE")
	delete(a, "extends")
	write(t, filepath.Join(l.Store.Root, "profiles", "a.yaml"), jsonBytes(ruleProfile("a", "external")))
	_, err = l.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile, NamedProfiles: &items, ExpectedNamed: state.NamedSHA256})
	code(t, err, "PLAN_STALE")
}
