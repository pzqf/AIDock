package source

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestProfileLibraryCanBeLinkedBeforeExplicitSkillAdoption(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "project-guide", Description: "local"})
	m.Copy = "library-copy"
	l := rulesLibrary(t, m)
	first := apply(t, l, Request{Action: "create", Name: "first-global", Description: "first"})
	second := apply(t, l, Request{Action: "create", Name: "second-global", Description: "second"})
	before := capture(t, m)
	libraryBefore := capture(t, l)
	root := l.Store.Root
	saveLocal(t, m, ProfileEdit{LibraryRoot: &root})
	m = reloadCopy(t, m)
	state, err := m.Profile()
	if err != nil || state.Report == nil || len(state.Layers.LibrarySkills) != 2 || len(state.Skills) != 1 {
		t.Fatal("linking a catalog must not require adopting every library skill", state, err)
	}
	exported, err := m.Export()
	if err != nil || len(exported.Packages) != 0 || exported.Preflight.Status == "FAIL" {
		t.Fatal("library pins silently exported unadopted packages", exported, err)
	}
	unchangedManager(t, m, before)
	unchangedManager(t, l, libraryBefore)
	catalog, err := l.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	var revision string
	for _, item := range catalog {
		if item.Skill.ID == first.SkillID {
			revision = item.Skill.Revision
		}
	}
	apply(t, m, Request{Action: "inherit", ID: first.SkillID, Revision: revision})
	apply(t, m, Request{Action: "enable", ID: first.SkillID})
	exported, err = m.Export()
	if err != nil || len(exported.Packages) != 1 || exported.Packages[0].Skill.ID != first.SkillID {
		t.Fatal("explicitly adopted subset did not export", exported, err)
	}
	for _, pkg := range exported.Packages {
		if pkg.Skill.ID == second.SkillID {
			t.Fatal("unadopted asset leaked into export")
		}
	}
	unchangedManager(t, l, libraryBefore)
}

func TestProfileLibraryDoesNotSilentlyEnableAnUnadoptedAsset(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "project-guide", Description: "local"})
	m.Copy = "library-copy"
	l := rulesLibrary(t, m)
	global := apply(t, l, Request{Action: "create", Name: "global-guide", Description: "global"})
	root := l.Store.Root
	saveLocal(t, m, ProfileEdit{LibraryRoot: &root})
	m = reloadCopy(t, m)
	apply(t, l, Request{Action: "enable", ID: global.SkillID})
	before := capture(t, m)
	check, err := m.Check()
	if err != nil || check.Status != "FAIL" || len(check.ProfileChecks) == 0 {
		t.Fatal("unadopted global enable was silently omitted", check, err)
	}
	_, err = m.Export()
	code(t, err, "PROFILE_SKILL_ADOPTION_REQUIRED")
	listed, err := m.State()
	if err != nil || listed.ProfileError == "" || listed.ProfileReport != nil || listed.EffectiveProfile != nil {
		t.Fatal("unresolved selection was displayed as effective", listed, err)
	}
	// The project may explicitly suppress the inherited switch, without copying
	// or changing the upstream package or losing the library association.
	state, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	local := ruleProfile("local")
	local["skills"] = map[string]any{"disable": []any{global.SkillID}}
	saveLocal(t, m, ProfileEdit{Profile: local})
	exported, err := m.Export()
	if err != nil || len(exported.Packages) != 0 || state.Report != nil {
		t.Fatal(exported, state, err)
	}
	unchangedManager(t, m, before)
}

func TestProfileLibraryKeepsUnknownIdentityAndCorruptRevisionClosed(t *testing.T) {
	for _, scenario := range []string{"unknown-id", "corrupt-revision"} {
		t.Run(scenario, func(t *testing.T) {
			m := manager(t)
			apply(t, m, Request{Action: "create", Name: "project-guide", Description: "local"})
			m.Copy = "library-copy"
			l := rulesLibrary(t, m)
			global := apply(t, l, Request{Action: "create", Name: "global-guide", Description: "global"})
			if scenario == "unknown-id" {
				state, err := l.Profile()
				if err != nil {
					t.Fatal(err)
				}
				state.Profile["skills"].(map[string]any)["disable"] = []any{"99999999-9999-4999-8999-999999999999"}
				write(t, filepath.Join(l.Store.Root, "profile.yaml"), jsonBytes(state.Profile))
			} else {
				state, err := l.State()
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range state.Index.Skills {
					if item.ID == global.SkillID {
						if err := os.WriteFile(filepath.Join(l.Store.Root, revisionPath(item), "SKILL.md"), []byte("corrupted"), 0644); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			state, err := m.Profile()
			if err != nil {
				t.Fatal(err)
			}
			root := l.Store.Root
			before := capture(t, m)
			_, err = m.ProfilePlan(context.Background(), ProfileEdit{Target: "local", ExpectedSHA256: state.Layers.LocalSHA256, LibraryRoot: &root})
			if err == nil {
				t.Fatal("invalid library became an accepted profile layer")
			}
			unchangedManager(t, m, before)
		})
	}
}
