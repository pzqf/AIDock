package source

import (
	"context"
	"path/filepath"
	"testing"

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

func TestSnapshotRetainsBothConflictingReferencePinsWithoutGuessingProjection(t *testing.T) {
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
	_, err = m.DraftReplay(context.Background(), tree, bundle, inputs)
	code(t, err, "SNAPSHOT_REFERENCE_PIN_CONFLICT")
	unchangedManager(t, m, before)
}
