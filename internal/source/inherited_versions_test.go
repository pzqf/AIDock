package source

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func layeredInheritedFixture(t *testing.T) (*Manager, *Manager, string, Skill, Skill) {
	t.Helper()
	m, l := manager(t), library(t)
	apply(t, m, Request{Action: "create", Name: "project-guide", Description: "project"})
	m.Copy = "layered-version-copy"
	id := apply(t, l, Request{Action: "create", Name: "version-guide", Description: "shared A", Document: "VERSION_A_BODY"}).SkillID
	a := selectedSkill(t, l, id)
	attach(t, m, l)
	apply(t, m, Request{Action: "inherit", ID: id, Revision: a.Revision})
	apply(t, l, Request{Action: "edit", ID: id, Files: []Patch{{Path: "SKILL.md", Content: "---\nname: version-guide\ndescription: shared B\n---\nVERSION_B_BODY"}}})
	b := selectedSkill(t, l, id)
	apply(t, m, Request{Action: "inherit", ID: id, Revision: b.Revision})
	apply(t, m, Request{Action: "enable", ID: id})
	return m, l, id, a, b
}

func TestInheritedVersionsLocalPinControlsExportAndPreservesSharedAndLibrary(t *testing.T) {
	m, l, id, a, b := layeredInheritedFixture(t)
	before, global := capture(t, m), capture(t, l)
	local := ruleProfile("local")
	local["skills"] = map[string]any{"pins": map[string]any{id: a.Revision}}
	saveLocal(t, m, ProfileEdit{Profile: local})
	state, err := m.State()
	if err != nil || state.ProfileError != "" {
		t.Fatal(state.ProfileError, err)
	}
	for _, s := range state.Effective {
		if s.Skill.ID == id && (s.Skill.Revision != b.Revision || len(s.Versions) != 2) {
			t.Fatal("shared source changed selection")
		}
	}
	for _, s := range state.Selected {
		if s.Skill.ID == id && s.Skill.Revision != a.Revision {
			t.Fatal("resolved metadata not on final pin")
		}
	}
	export, err := m.Export()
	if err != nil || len(export.Packages) != 1 {
		t.Fatal(export.Preflight, err)
	}
	if export.Packages[0].Skill.Revision != a.Revision {
		t.Fatal("emitted shared revision instead of Local selection")
	}
	body, err := m.Store.Data(export.Packages[0].Tree.Entries["SKILL.md"])
	if err != nil || !strings.Contains(string(body), "VERSION_A_BODY") {
		t.Fatal("wrong complete package bytes", err)
	}
	check := checked(t, m)
	found := false
	for _, s := range check.Skills {
		if s.ID == id {
			found = s.Revision == a.Revision && s.Enabled && s.Status != "FAIL"
		}
	}
	if !found {
		t.Fatal("preflight did not check final selected version", check)
	}
	unchangedManager(t, m, before)
	unchangedManager(t, l, global)
	// Clearing only this copy's override returns to the exact shared B pin.
	saveLocal(t, m, ProfileEdit{Clear: true})
	export, err = m.Export()
	if err != nil || export.Packages[0].Skill.Revision != b.Revision {
		t.Fatal("clear did not restore shared selected version", err)
	}
	unchangedManager(t, m, before)
}

func TestInheritedVersionsDoNotSelectAnUnadoptedLibraryRevision(t *testing.T) {
	m, l, id, _, _ := layeredInheritedFixture(t)
	apply(t, l, Request{Action: "edit", ID: id, Files: []Patch{{Path: "SKILL.md", Content: "---\nname: version-guide\ndescription: unadopted C\n---\nVERSION_C_NOT_ADOPTED"}}})
	c := selectedSkill(t, l, id)
	// Attach defaults so the resolver knows C exists, but it still is not a
	// project-retained complete version and must not be exported automatically.
	m.ProfileLibrary = true
	before := capture(t, m)
	state, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	local := ruleProfile("local")
	local["skills"] = map[string]any{"pins": map[string]any{id: c.Revision}}
	_, err = m.ProfilePlan(context.Background(), ProfileEdit{Target: "local", ExpectedSHA256: state.Layers.LocalSHA256, ExpectedLayers: state.Layers.Digest, Profile: local})
	if err == nil {
		t.Fatal("live library revision became implicitly selectable")
	}
	unchangedManager(t, m, before)
	// Existing disabled declarations may reference an unadopted library pin.
	// Keep them readable without emitting or implicitly adopting its bytes.
	local["skills"].(map[string]any)["disable"] = []any{id}
	saveLocal(t, m, ProfileEdit{Profile: local})
	export, err := m.Export()
	if err != nil || len(export.Packages) != 0 {
		t.Fatal("disabled unadopted declaration was activated or made unreadable", err)
	}
	if !hasCheck(export.Preflight, id, "PROFILE_PIN_MISMATCH", "FAIL") {
		t.Fatal("missing disabled version diagnostic")
	}
}

func TestInheritedVersionsInvalidVariantsBlockWithoutChangingSource(t *testing.T) {
	for _, kind := range []string{"duplicate", "foreign-id", "missing-body", "changed-metadata"} {
		t.Run(kind, func(t *testing.T) {
			m, _, id, _, _ := layeredInheritedFixture(t)
			state, err := m.State()
			if err != nil {
				t.Fatal(err)
			}
			inherit := Inheritance{Version: 1, Owner: m.Owner, Scope: m.Scope, Skills: state.Inherited}
			for i := range inherit.Skills {
				if inherit.Skills[i].Skill.ID != id {
					continue
				}
				a := &inherit.Skills[i]
				switch kind {
				case "duplicate":
					a.Versions = append(a.Versions, a.Skill)
				case "foreign-id":
					a.Versions[0].ID = "33333333-3333-4333-8333-333333333333"
				case "changed-metadata":
					a.Versions[0].Name = "forged-name"
				case "missing-body":
					write(t, filepath.Join(m.Store.Root, filepath.FromSlash(revisionPath(a.Versions[0])), "SKILL.md"), []byte("BROKEN_OLD_VERSION"))
				}
			}
			write(t, filepath.Join(m.Store.Root, inheritanceFile), jsonBytes(inherit))
			before := capture(t, m)
			if _, err = m.Export(); err == nil {
				t.Fatal("invalid explicitly retained version accepted", kind)
			}
			unchangedManager(t, m, before)
		})
	}
}

func TestInheritedVersionsNewSharedSelectionDoesNotChangeIndependentCopies(t *testing.T) {
	m, l, id, a, b := layeredInheritedFixture(t)
	other := manager(t)
	attach(t, other, l)
	apply(t, other, Request{Action: "inherit", ID: id, Revision: b.Revision})
	before := capture(t, other)
	p, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	switches(p.Profile)["pins"].(map[string]any)[id] = a.Revision
	plan, err := m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: p.SHA256, ExpectedLayers: p.Layers.Digest, Profile: p.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), plan.Plan.ID, plan.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	export, err := m.Export()
	if err != nil || export.Packages[0].Skill.Revision != a.Revision {
		t.Fatal("shared retained pin could not be selected", err)
	}
	unchangedManager(t, other, before)
}

func TestInheritedVersionsCheckTheSelectedVersionsCompatibility(t *testing.T) {
	m, l, id, a, _ := layeredInheritedFixture(t)
	g := declaration()
	g.Compatibility = map[string]any{"platforms": []any{"unsupported-os"}}
	govern(t, l, id, g)
	c := selectedSkill(t, l, id)
	apply(t, m, Request{Action: "inherit", ID: id, Revision: c.Revision})
	check := checked(t, m)
	if !hasCheck(check, id, "PLATFORM_INCOMPATIBLE", "FAIL") {
		t.Fatal("selected shared compatibility was ignored", check)
	}
	local := ruleProfile("local")
	local["skills"] = map[string]any{"pins": map[string]any{id: a.Revision}}
	saveLocal(t, m, ProfileEdit{Profile: local})
	check = checked(t, m)
	if hasCheck(check, id, "PLATFORM_INCOMPATIBLE", "FAIL") {
		t.Fatal("checked unselected version instead of final Local pin", check)
	}
	state, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	local["skills"].(map[string]any)["pins"].(map[string]any)[id] = c.Revision
	before := capture(t, m)
	_, err = m.ProfilePlan(context.Background(), ProfileEdit{Target: "local", ExpectedSHA256: state.Layers.LocalSHA256, ExpectedLayers: state.Layers.Digest, Profile: local})
	if err == nil {
		t.Fatal("incompatible selected version bypassed preflight")
	}
	unchangedManager(t, m, before)
}

func TestInheritedVersionsKeepLegacyPackageV1Readable(t *testing.T) {
	m, _, id, a, _ := layeredInheritedFixture(t)
	pkg, err := m.Store.Capture(filepath.Join(m.Store.Root, filepath.FromSlash(revisionPath(a))))
	if err != nil {
		t.Fatal(err)
	}
	legacy := a
	legacy.RevisionFormat = ""
	legacy.PackageDigest = ""
	legacy.Revision = packageRevision(legacy, pkg)
	for name, entry := range pkg.Entries {
		if entry.Dir {
			continue
		}
		write(t, filepath.Join(m.Store.Root, filepath.FromSlash(revisionPath(legacy)), filepath.FromSlash(name)), mustData(t, m, entry))
	}
	inherit := Inheritance{Version: 1, Owner: m.Owner, Scope: m.Scope, Skills: []InheritedSkill{{Owner: "global", Scope: "global", Skill: legacy}}}
	write(t, filepath.Join(m.Store.Root, inheritanceFile), jsonBytes(inherit))
	_, profile, err := m.load(nil)
	if err != nil {
		t.Fatal(err)
	}
	switches(profile)["pins"].(map[string]any)[id] = legacy.Revision
	write(t, filepath.Join(m.Store.Root, "profile.yaml"), jsonBytes(profile))
	export, err := m.Export()
	if err != nil || len(export.Packages) != 1 || export.Packages[0].Skill.Revision != legacy.Revision {
		t.Fatal("legacy inherited package became unreadable", err)
	}
}
