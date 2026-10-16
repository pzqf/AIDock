package source

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

func suppressedReferenceFixture(t *testing.T) (*Manager, *Manager, string, Export, workstore.Tree, SnapshotConfiguration) {
	t.Helper()
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "project-guide", Description: "project"})
	m.Copy = "reference-copy"
	l := rulesLibrary(t, m)
	id := apply(t, l, Request{Action: "create", Name: "disabled-global", Description: "global"}).SkillID
	apply(t, l, Request{Action: "edit", ID: id, Files: []Patch{{Path: "scripts/never-run.js", Content: "throw new Error('NEVER_EXECUTE_ARCHIVED_REFERENCE')"}}})
	apply(t, l, Request{Action: "enable", ID: id})
	unrelated := apply(t, l, Request{Action: "create", Name: "unrelated-private", Description: "must not archive"}).SkillID
	state, err := l.Profile()
	if err != nil {
		t.Fatal(err)
	}
	delete(switches(state.Profile)["pins"].(map[string]any), unrelated)
	write(t, filepath.Join(l.Store.Root, "profile.yaml"), jsonBytes(state.Profile))
	local := ruleProfile("local")
	local["skills"] = map[string]any{"disable": []any{id}}
	root := l.Store.Root
	saveLocal(t, m, ProfileEdit{Profile: local, LibraryRoot: &root})
	m = reloadCopy(t, m)
	bundle, tree, inputs := historicalObservation(t, m)
	return m, l, id, bundle, tree, inputs
}

func TestSnapshotRetainsSuppressedGlobalReferenceWithoutAdoptingOrEnabling(t *testing.T) {
	m, l, id, bundle, tree, inputs := suppressedReferenceFixture(t)
	before, global := capture(t, m), capture(t, l)
	if inputs.Version != 2 || len(inputs.ReferencePackages) != 1 || inputs.ReferencePackages[0].Skill.ID != id || len(bundle.Packages) != 0 || workstore.Digest(tree) != workstore.Digest(before) {
		t.Fatal("unrelated package retained or suppressed reference activated")
	}
	if _, ok := inputs.ReferencePackages[0].Tree.Entries["scripts/never-run.js"]; !ok {
		t.Fatal("complete reference attachment missing")
	}
	// Make live authority unreadable: recovery must use the frozen package.
	write(t, filepath.Join(l.Store.Root, "skills.yaml"), []byte("BROKEN_LIVE_LIBRARY"))
	m.Library, m.ProfileLibrary = nil, false
	draft, err := m.DraftReplay(context.Background(), tree, bundle, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if workstore.Hash(jsonBytes(draft.Report.Effective)) != workstore.Hash(jsonBytes(bundle.ProfileReport.Effective)) {
		t.Fatal("suppressed reference changed effective configuration")
	}
	idx, shared, err := m.load(&draft.SourceAfter)
	if err != nil {
		t.Fatal(err)
	}
	_, effective, err := m.resolve(&draft.SourceAfter, idx, shared)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range effective {
		if s.Skill.ID == id {
			found = s.Owner == "global" && s.Scope == "global" && s.Layer == "inherited" && s.ReadOnly
		}
	}
	if !found {
		t.Fatal("recovered reference lost immutable global provenance")
	}
	unchangedManager(t, m, before)
	// The only live library edit was the deliberate injection, never recovery.
	write(t, filepath.Join(l.Store.Root, "skills.yaml"), mustData(t, l, global.Entries["skills.yaml"]))
	unchangedManager(t, l, global)
}

func mustData(t *testing.T, m *Manager, entry workstore.Entry) []byte {
	t.Helper()
	b, err := m.Store.Data(entry)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSnapshotReferenceValidationRejectsAlteredOrUnrelatedMaterial(t *testing.T) {
	m, _, _, bundle, _, inputs := suppressedReferenceFixture(t)
	for _, kind := range []string{"identity", "revision", "body-list", "duplicate", "unrelated", "legacy-version", "missing-body"} {
		t.Run(kind, func(t *testing.T) {
			c := inputs
			c.ReferencePackages = append([]ExportPackage{}, inputs.ReferencePackages...)
			c.ReferencePackages[0].Tree = workstore.Clone(c.ReferencePackages[0].Tree)
			switch kind {
			case "identity":
				c.ReferencePackages[0].Owner = m.Owner
			case "revision":
				c.ReferencePackages[0].Skill.Revision = "sha256:" + workstore.Hash([]byte("changed"))
			case "body-list":
				delete(c.ReferencePackages[0].Tree.Entries, "scripts/never-run.js")
			case "duplicate":
				c.ReferencePackages = append(c.ReferencePackages, c.ReferencePackages[0])
			case "unrelated":
				c.Portable.Effective = cloneProfile(c.Portable.Effective)
				c.Portable.Effective["skills"] = map[string]any{}
				other := bundle
				other.ProfileReport = nil
				if validateSnapshotReferences(c, other) == nil {
					t.Fatal("unreferenced library package accepted")
				}
				return
			case "legacy-version":
				c.Version = 1
			case "missing-body":
				if validateSnapshotReferenceBytes(c.ReferencePackages, func(e workstore.Entry) ([]byte, error) { return []byte("tampered bytes"), nil }) == nil {
					t.Fatal("body corruption accepted")
				}
				return
			}
			c.Digest = SnapshotConfigurationDigest(c)
			if err := ValidateSnapshotConfiguration(c, bundle, m.Store.Local); err == nil {
				t.Fatal("bad reference accepted", kind)
			}
		})
	}
}

func TestSnapshotRetainsBothReferencePinsAndHonorsLocalSelection(t *testing.T) {
	m, l, id, _, _, first := suppressedReferenceFixture(t)
	old := first.ReferencePackages[0].Skill.Revision
	apply(t, l, Request{Action: "edit", ID: id, Files: []Patch{{Path: "scripts/never-run.js", Content: "throw new Error('NEW_VERSION_MUST_NOT_REPLACE_OLD')"}}})
	newRevision := selectedSkill(t, l, id).Revision
	global, err := l.Profile()
	if err != nil {
		t.Fatal(err)
	}
	switches(global.Profile)["pins"].(map[string]any)[id] = old
	write(t, filepath.Join(l.Store.Root, "profile.yaml"), jsonBytes(global.Profile))
	local := ruleProfile("local")
	local["skills"] = map[string]any{"disable": []any{id}, "pins": map[string]any{id: newRevision}}
	saveLocal(t, m, ProfileEdit{Profile: local})
	bundle, tree, inputs := historicalObservation(t, m)
	if len(inputs.ReferencePackages) != 2 {
		t.Fatal("different explicit layer pins were silently narrowed")
	}
	before := capture(t, m)
	draft, err := m.DraftReplay(context.Background(), tree, bundle, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if workstore.Hash(jsonBytes(draft.Report.Effective)) != workstore.Hash(jsonBytes(bundle.ProfileReport.Effective)) {
		t.Fatal("different layer pins changed on recovery")
	}
	idx, shared, err := m.load(&draft.SourceAfter)
	if err != nil {
		t.Fatal(err)
	}
	_, skills, err := m.resolve(&draft.SourceAfter, idx, shared)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := m.selectProfileSkills(draft.SourceAfter, skills, draft.Report.Effective)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range selected {
		if item.Skill.ID == id {
			found = item.Skill.Revision == newRevision && len(item.Versions) == 2
		}
	}
	if !found {
		t.Fatal("final Local pin did not select its complete frozen version")
	}
	unchangedManager(t, m, before)
}

func knownReferenceFixture(t *testing.T) (*Manager, *Manager, string, Skill) {
	t.Helper()
	m, l, id, _, _ := layeredInheritedFixture(t)
	apply(t, l, Request{Action: "edit", ID: id, Files: []Patch{{Path: "scripts/never-run.js", Content: "throw new Error('UNADOPTED_VERSION_MUST_NOT_RUN')"}}})
	c := selectedSkill(t, l, id)
	m.ProfileLibrary = true
	local := ruleProfile("local")
	local["skills"] = map[string]any{"disable": []any{id}, "pins": map[string]any{id: c.Revision}}
	saveLocal(t, m, ProfileEdit{Profile: local})
	return m, l, id, c
}

func TestSnapshotRetainsMissingRevisionOfAnAlreadyInheritedID(t *testing.T) {
	m, l, id, c := knownReferenceFixture(t)
	before, global := capture(t, m), capture(t, l)
	bundle, tree, inputs := historicalObservation(t, m)
	if len(inputs.ReferencePackages) != 1 || inputs.ReferencePackages[0].Skill.Revision != c.Revision || len(bundle.Packages) != 0 {
		t.Fatal("known ID hid its unadopted revision or activated it")
	}
	// Recovery has no live library. The existing A/B association and C body
	// must coexist, without replacing the shared primary or losing Local C.
	write(t, filepath.Join(l.Store.Root, "skills.yaml"), []byte("BROKEN_LIVE_LIBRARY"))
	m.Library, m.ProfileLibrary = nil, false
	draft, err := m.DraftReplay(context.Background(), tree, bundle, inputs)
	if err != nil {
		t.Fatal(err)
	}
	idx, shared, err := m.load(&draft.SourceAfter)
	if err != nil {
		t.Fatal(err)
	}
	inherit, effective, err := m.resolve(&draft.SourceAfter, idx, shared)
	if err != nil {
		t.Fatal(err)
	}
	if len(inherit.Skills) != 1 || inherit.Skills[0].Skill.Revision == c.Revision || len(inherit.Skills[0].Versions) != 2 || len(m.missingSnapshotPins(inputs, bundle, effective, draft.SourceAfter)) != 0 {
		t.Fatal("reference replaced prior association, lost versions or retained an uncovered pin")
	}
	selected, err := m.selectProfileSkills(draft.SourceAfter, effective, draft.Report.Effective)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range selected {
		if item.Skill.ID == id {
			found = item.Skill.Revision == c.Revision && item.Layer == "inherited" && item.ReadOnly
			pkg, err := m.cached(&draft.SourceAfter, item.Skill)
			if err != nil || pkg.Entries["scripts/never-run.js"].Hash == "" {
				t.Fatal("exact complete archived body missing", err)
			}
		}
	}
	if !found || workstore.Hash(jsonBytes(draft.Report.Effective)) != workstore.Hash(jsonBytes(bundle.ProfileReport.Effective)) {
		t.Fatal("Local pin or frozen semantics changed")
	}
	unchangedManager(t, m, before)
	write(t, filepath.Join(l.Store.Root, "skills.yaml"), mustData(t, l, global.Entries["skills.yaml"]))
	unchangedManager(t, l, global)
}

func TestSnapshotRejectsLegacyKnownIDWithoutItsReferencedRevision(t *testing.T) {
	m, _, _, _ := knownReferenceFixture(t)
	bundle, tree, inputs := historicalObservation(t, m)
	for _, version := range []int{1, 2} {
		inputs.Version, inputs.ReferencePackages = version, nil
		inputs.Digest = SnapshotConfigurationDigest(inputs)
		before := capture(t, m)
		observation, err := m.ExportHistorical(context.Background(), tree, bundle, inputs)
		if err != nil || observation.SkillMaterial == nil || observation.SkillMaterial.Status != "MISSING" || observation.SkillMaterial.MissingPins != 1 {
			t.Fatal("frozen reconstruction hid missing recovery material", err)
		}
		_, err = m.DraftReplay(context.Background(), tree, bundle, inputs)
		code(t, err, "SNAPSHOT_DEPENDENCY_MATERIAL_MISSING")
		unchangedManager(t, m, before)
	}
}

func TestSnapshotDoesNotCopyRetainedRevisionsAgainOrTrustAnArbitraryCache(t *testing.T) {
	m, _, id, a, _ := layeredInheritedFixture(t)
	local := ruleProfile("local")
	local["skills"] = map[string]any{"pins": map[string]any{id: a.Revision}}
	saveLocal(t, m, ProfileEdit{Profile: local})
	_, _, inputs := historicalObservation(t, m)
	if len(inputs.ReferencePackages) != 0 {
		t.Fatal("explicitly retained version was redundantly archived as a reference")
	}
	m, _, _, c := knownReferenceFixture(t)
	before := capture(t, m)
	// A live library is unavailable. Even an arbitrary matching cache entry
	// is not a declared version and cannot silently authorize its adoption.
	write(t, filepath.Join(m.Store.Root, revisionPath(c), "SKILL.md"), []byte("ARBITRARY_CACHE"))
	m.Library = nil
	err := m.WithSnapshotInputs(func(Export, workstore.Tree, SnapshotConfiguration) error {
		t.Fatal("incomplete snapshot delivered")
		return nil
	})
	if err == nil {
		t.Fatal("missing declared material accepted")
	}
	// Only the explicit test injection may differ; snapshot capture is read-only.
	after := capture(t, m)
	delete(after.Entries, revisionPath(c)+"/SKILL.md")
	delete(after.Entries, revisionPath(c))
	if workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("capture changed managed source")
	}
}

func TestSnapshotCannotUseGlobalReferenceToReplaceAProjectOwnedRevision(t *testing.T) {
	m := manager(t)
	id := apply(t, m, Request{Action: "create", Name: "owned-guide", Description: "owned"}).SkillID
	skill := selectedSkill(t, m, id)
	wrong := "sha256:" + workstore.Hash([]byte("other revision"))
	inputs := SnapshotConfiguration{}
	inputs.Portable.Effective = map[string]any{"skills": map[string]any{"pins": map[string]any{id: wrong}}}
	_, err := m.snapshotReferences(inputs, Export{}, []EffectiveSkill{{Skill: skill, Layer: "owned"}}, capture(t, m))
	code(t, err, "SNAPSHOT_DEPENDENCY_MATERIAL_MISSING")
	if e, ok := err.(*result.Error); !ok || e.Exit != 4 {
		t.Fatal("unsupported owned revision did not fail closed")
	}
}

func TestSnapshotRefusesDriftedDisabledOwnedMaterial(t *testing.T) {
	m := manager(t)
	id := apply(t, m, Request{Action: "create", Name: "disabled-owned", Description: "must validate body"}).SkillID
	m.Copy = "disabled-drift-copy"
	bundle, tree, inputs := historicalObservation(t, m)
	before := capture(t, m)
	if err := m.Store.PutFile(&tree, "skills/"+id+"/SKILL.md", body("changed-native-name"), 0644); err != nil {
		t.Fatal(err)
	}
	bundle.Snapshot = workstore.Digest(tree)
	inputs.SourceDigest = bundle.Snapshot
	inputs.Digest = SnapshotConfigurationDigest(inputs)
	observation, err := m.ExportHistorical(context.Background(), tree, bundle, inputs)
	if err != nil || observation.SkillMaterial == nil || observation.SkillMaterial.Status != "MISSING" || observation.SkillMaterial.MissingPins != 1 {
		t.Fatal("legacy disabled body drift was presented as complete", err)
	}
	_, err = m.DraftReplay(context.Background(), tree, bundle, inputs)
	code(t, err, "SNAPSHOT_DEPENDENCY_MATERIAL_MISSING")
	unchangedManager(t, m, before)
	write(t, filepath.Join(m.Store.Root, "skills", id, "SKILL.md"), body("changed-native-name"))
	before = capture(t, m)
	err = m.WithSnapshotInputs(func(Export, workstore.Tree, SnapshotConfiguration) error {
		t.Fatal("corrupt disabled source archived as recoverable")
		return nil
	})
	code(t, err, "SNAPSHOT_DEPENDENCY_MATERIAL_MISSING")
	unchangedManager(t, m, before)
}
