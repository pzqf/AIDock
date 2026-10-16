package source

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func TestSnapshotConfigurationRetainsPortableAndOriginalLocalSeparately(t *testing.T) {
	m := manager(t)
	created := apply(t, m, Request{Action: "create", Name: "guide", Description: "guide"})
	apply(t, m, Request{Action: "enable", ID: created.SkillID})
	m.Copy = "snapshot-copy"
	l := rulesLibrary(t, m)
	write(t, filepath.Join(l.Store.Root, "profiles", "base.yaml"), jsonBytes(ruleProfile("base", "BASE_RULE")))
	// Bind the library before assigning its named project profile.
	libraryRoot := l.Store.Root
	saveLocal(t, m, ProfileEdit{Profile: ruleProfile("local", "LOCAL_PRIVATE_RULE"), LibraryRoot: &libraryRoot})
	m = reloadCopy(t, m)
	current, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	current.Profile["rules"] = []any{"PROJECT_RULE"}
	current.Profile["extends"] = "base"
	p, err := m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: current.SHA256, ExpectedLayers: current.Layers.Digest, Profile: current.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	s, err := m.localProfileStore()
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("# original local formatting\r\nschema_version: 2\r\nname: local\r\nrules: ['LOCAL_PRIVATE_RULE']\r\n")
	if err = os.WriteFile(filepath.Join(s.Root, "profile.yaml"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	before, libraryBefore := capture(t, m), capture(t, l)
	var cfg SnapshotConfiguration
	var bundle Export
	err = m.WithSnapshotInputs(func(export Export, tree workstore.Tree, configuration SnapshotConfiguration) error {
		bundle, cfg = export, configuration
		if workstore.Digest(tree) != workstore.Digest(before) {
			t.Fatal("source observation changed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(profileTexts(cfg.Portable.Effective), []string{"全局默认", "BASE_RULE", "PROJECT_RULE"}) || !reflect.DeepEqual(bundle.Rules, []string{"全局默认", "BASE_RULE", "PROJECT_RULE", "LOCAL_PRIVATE_RULE"}) {
		t.Fatal(cfg.Portable.Effective, bundle.Rules)
	}
	if !cfg.ProfileLibrary || cfg.SkillLibraryRoot != l.Store.Root || len(cfg.Layers.History) != 0 || cfg.Layers.Recovery {
		t.Fatal(cfg)
	}
	body, err := m.Store.Data(cfg.LocalTree.Entries["profile.yaml"])
	if err != nil || !bytes.Equal(body, raw) {
		t.Fatal("local original bytes were normalized or lost", err)
	}
	for _, entry := range before.Entries {
		if !entry.Dir {
			body, err := m.Store.Data(entry)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(body, []byte("LOCAL_PRIVATE_RULE")) {
				t.Fatal("local value entered portable source")
			}
		}
	}
	unchangedManager(t, m, before)
	unchangedManager(t, l, libraryBefore)
	write(t, filepath.Join(l.Store.Root, "profile.yaml"), jsonBytes(ruleProfile("new defaults", "NEW_GLOBAL_RULE")))
	write(t, filepath.Join(s.Root, "profile.yaml"), jsonBytes(ruleProfile("new local", "NEW_LOCAL_RULE")))
	if err = ValidateSnapshotConfiguration(cfg, bundle, m.Store.Local); err != nil {
		t.Fatal("historical observation depended on current files", err)
	}
	if err = ValidateSnapshotConfigurationBytes(cfg, bundle, m.Store.Data); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotPortableObservationCanContainLocallySuppressedUnadoptedGlobal(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "project-guide", Description: "local"})
	m.Copy = "snapshot-copy"
	l := rulesLibrary(t, m)
	global := apply(t, l, Request{Action: "create", Name: "global-guide", Description: "global"})
	apply(t, l, Request{Action: "enable", ID: global.SkillID})
	local := ruleProfile("local")
	local["skills"] = map[string]any{"disable": []any{global.SkillID}}
	root := l.Store.Root
	saveLocal(t, m, ProfileEdit{Profile: local, LibraryRoot: &root})
	m = reloadCopy(t, m)
	err := m.WithSnapshotInputs(func(bundle Export, _ workstore.Tree, cfg SnapshotConfiguration) error {
		if len(bundle.Packages) != 0 {
			t.Fatal("unadopted package activated")
		}
		if !strings.Contains(string(jsonBytes(cfg.Portable.Effective)), global.SkillID) {
			t.Fatal("portable observation omitted suppressed input")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotConfigurationRefusesMismatchedLocalOriginal(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "guide"})
	m.Copy = "snapshot-copy"
	saveLocal(t, m, ProfileEdit{Profile: ruleProfile("local", "EXPECTED")})
	var bundle Export
	var cfg SnapshotConfiguration
	if err := m.WithSnapshotInputs(func(b Export, _ workstore.Tree, s SnapshotConfiguration) error { bundle, cfg = b, s; return nil }); err != nil {
		t.Fatal(err)
	}
	tree := workstore.Clone(cfg.LocalTree)
	if err := m.Store.PutFile(&tree, "profile.yaml", jsonBytes(ruleProfile("local", "CHANGED")), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.LocalTree = tree
	cfg.Layers.LocalSHA256 = workstore.Digest(tree)
	cfg.Digest = SnapshotConfigurationDigest(cfg)
	if err := ValidateSnapshotConfigurationBytes(cfg, bundle, m.Store.Data); err == nil {
		t.Fatal("different local body accepted")
	}
}
