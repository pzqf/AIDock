package source

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func TestReplayProjectionPreservesLocalOriginalRulesAndCurrentProtectedData(t *testing.T) {
	m := manager(t)
	created := apply(t, m, Request{Action: "create", Name: "guide", Description: "original"})
	apply(t, m, Request{Action: "enable", ID: created.SkillID})
	m.Copy = "replay-copy"
	l := rulesLibrary(t, m)
	global := apply(t, l, Request{Action: "create", Name: "inherited-guide", Description: "global"})
	attach(t, m, l)
	apply(t, m, Request{Action: "inherit", ID: global.SkillID, Revision: selectedSkill(t, l, global.SkillID).Revision})
	apply(t, m, Request{Action: "enable", ID: global.SkillID})
	write(t, filepath.Join(l.Store.Root, "profiles", "base.yaml"), jsonBytes(ruleProfile("base", "NAMED_ORIGINAL")))
	libraryRoot := l.Store.Root
	saveLocal(t, m, ProfileEdit{Profile: ruleProfile("local", "PRIVATE_LOCAL_ORIGINAL"), LibraryRoot: &libraryRoot})
	m = reloadCopy(t, m)
	p, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	p.Profile["extends"], p.Profile["rules"] = "base", []any{"PROJECT_INLINE"}
	files := []RuleFile{{ID: workstore.ID(), Name: "original file", Body: "# RULE_FILE_ORIGINAL\r\n", Enabled: true}, {ID: workstore.ID(), Name: "disabled", Body: "DISABLED_BODY"}}
	preview, err := m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: p.SHA256, ExpectedLayers: p.Layers.Digest, Profile: p.Profile, RuleFiles: &files, ExpectedRulesSHA256: p.RulesSHA256})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), preview.Plan.ID, preview.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	local, _ := m.localProfileStore()
	originalLocal := []byte("# keep this formatting\r\nschema_version: 2\r\nname: local\r\nrules: ['PRIVATE_LOCAL_ORIGINAL']\r\n")
	write(t, filepath.Join(local.Root, "profile.yaml"), originalLocal)
	bundle, historical, inputs := historicalObservation(t, m)
	apply(t, m, Request{Action: "edit", ID: created.SkillID, Files: []Patch{{Path: "SKILL.md", Content: string(body("guide")) + "CURRENT_UNAPPLIED_DRAFT"}}})
	extra := apply(t, m, Request{Action: "create", Name: "later-guide", Description: "later dormant package"})
	for _, p := range []string{"knowledge/keep.md", "sessions/keep.json", "audit/keep.json", "secrets/keep.txt", ".git/config", "rules/notes.md"} {
		write(t, filepath.Join(m.Store.Root, filepath.FromSlash(p)), []byte("CURRENT_PROTECTED_DATA"))
	}
	write(t, filepath.Join(l.Store.Root, "profile.yaml"), []byte("rules: [broken"))
	write(t, filepath.Join(local.Root, "binding.json"), []byte("BROKEN_LOCAL_BINDING"))
	write(t, filepath.Join(local.Root, "profile.yaml"), []byte("PRIVATE_LOCAL_CURRENT_DRAFT"))
	before, libraryBefore := capture(t, m), capture(t, l)
	localBefore, err := local.Capture(local.Root)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := m.DraftReplay(context.Background(), historical, bundle, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if workstore.Hash(jsonBytes(draft.Report.Effective)) != workstore.Hash(jsonBytes(bundle.ProfileReport.Effective)) || len(draft.Digest) != 64 || !draft.Preflight.Candidate {
		t.Fatal("projection changed historical effective semantics")
	}
	if err := m.WithReplayExport(context.Background(), draft, func(exported Export) error {
		if string(jsonBytes(exported.Packages)) != string(jsonBytes(bundle.Packages)) || string(jsonBytes(exported.Rules)) != string(jsonBytes(bundle.Rules)) || exported.Snapshot != workstore.Digest(draft.SourceAfter) || exported.ConfigurationDigest != draft.Layers.Digest {
			t.Fatal("replay compiler replaced frozen packages, pins, rules or future configuration")
		}
		protected := false
		for _, root := range exported.Authorities {
			protected = protected || root == l.Store.Root
		}
		if !protected {
			t.Fatal("replay export lost historical/current library protection")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	tampered := draft
	if err := m.WithReplaySnapshotInputs(context.Background(), draft, func(exported Export, tree workstore.Tree, cfg SnapshotConfiguration) error {
		if exported.Snapshot != workstore.Digest(draft.SourceAfter) || cfg.Layers.LibraryRoot != "" || cfg.ProfileLibrary || cfg.LocalTree.Entries["profile.yaml"] != draft.LocalAfter.Entries["profile.yaml"] {
			t.Fatal("future snapshot followed live defaults or changed Local original")
		}
		if _, err := m.frozenMCPReport(FrozenMCP{Tree: tree, Effective: exported, Configuration: cfg}); err != nil {
			t.Fatal("future applied view cannot independently reconstruct", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	tampered.Authorities = nil
	if err := m.WithReplayExport(context.Background(), tampered, func(Export) error { t.Fatal("altered authority protection accepted"); return nil }); err == nil {
		t.Fatal("authority protection not bound to candidate digest")
	}
	if workstore.Digest(draft.SourceBefore) != workstore.Digest(before) || workstore.Digest(draft.LocalBefore) != workstore.Digest(localBefore) {
		t.Fatal("draft backup observations differ")
	}
	for p, entry := range before.Entries {
		if strings.Contains(p, "keep.") || p == ".git/config" || p == "rules/notes.md" || strings.HasPrefix(p, ".revisions/") || strings.HasPrefix(p, "skills/"+extra.SkillID) {
			if draft.SourceAfter.Entries[p] != entry {
				t.Fatal("protected/dormant/history entry changed", p)
			}
		}
	}
	for _, entry := range draft.SourceAfter.Entries {
		if entry.Dir {
			continue
		}
		b, err := m.Store.Data(entry)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte("PRIVATE_LOCAL_")) {
			t.Fatal("Local value leaked into shared source")
		}
	}
	localBody, err := m.Store.Data(draft.LocalAfter.Entries["profile.yaml"])
	if err != nil || !bytes.Equal(localBody, originalLocal) {
		t.Fatal("original Local formatting not preserved", err)
	}
	newBinding, err := m.Store.Data(draft.LocalAfter.Entries["binding.json"])
	if err != nil {
		t.Fatal(err)
	}
	var binding profileBinding
	if json.Unmarshal(newBinding, &binding) != nil || binding.LibraryRoot != "" || binding.SourceRoot != m.Store.Root {
		t.Fatal("project replay would follow current global defaults")
	}
	backupLocal, err := m.Store.Data(draft.LocalBefore.Entries["profile.yaml"])
	if err != nil || string(backupLocal) != "PRIVATE_LOCAL_CURRENT_DRAFT" {
		t.Fatal("current draft bytes missing from immutable backup cache", err)
	}
	unchangedManager(t, m, before)
	unchangedManager(t, l, libraryBefore)
	localNow, err := local.Capture(local.Root)
	if err != nil || workstore.Digest(localBefore) != workstore.Digest(localNow) {
		t.Fatal("projection changed active Local files", err)
	}
	again, err := m.DraftReplay(context.Background(), historical, bundle, inputs)
	if err != nil || again.Digest != draft.Digest {
		t.Fatal("same observations are not deterministic", err)
	}
}

func TestReplayProjectionRejectsForeignIdentityAndImmutableConflicts(t *testing.T) {
	for _, kind := range []string{"foreign-project", "missing-project", "corrupt-project", "foreign-scope", "future-source-schema", "immutable", "unmanaged-local", "foreign-local", "future-local-schema"} {
		t.Run(kind, func(t *testing.T) {
			m := manager(t)
			apply(t, m, Request{Action: "create", Name: "guide", Description: "original"})
			m.Copy = "replay-copy"
			bundle, historical, inputs := historicalObservation(t, m)
			local, _ := m.localProfileStore()
			switch kind {
			case "foreign-project":
				write(t, filepath.Join(m.Store.Root, "project.yaml"), jsonBytes(map[string]any{"schema_version": 1, "id": "99999999-9999-4999-8999-999999999999"}))
			case "missing-project":
				if err := os.Remove(filepath.Join(m.Store.Root, "project.yaml")); err != nil {
					t.Fatal(err)
				}
			case "corrupt-project":
				write(t, filepath.Join(m.Store.Root, "project.yaml"), []byte("id: [broken"))
			case "foreign-scope":
				write(t, filepath.Join(m.Store.Root, "skills.yaml"), jsonBytes(Index{Version: 1, Owner: m.Owner, Scope: "foreign-scope", Skills: []Skill{}}))
			case "future-source-schema":
				write(t, filepath.Join(m.Store.Root, "skills.yaml"), jsonBytes(Index{Version: 2, Owner: m.Owner, Scope: m.Scope, Skills: []Skill{}}))
			case "immutable":
				for p, e := range historical.Entries {
					if !e.Dir && strings.HasPrefix(p, ".revisions/") {
						write(t, filepath.Join(m.Store.Root, filepath.FromSlash(p)), []byte("corrupted immutable bytes"))
						break
					}
				}
			case "unmanaged-local":
				write(t, filepath.Join(local.Root, "notes.md"), []byte("do not delete"))
			case "foreign-local":
				write(t, filepath.Join(local.Root, "binding.json"), jsonBytes(profileBinding{Version: 1, Project: m.Owner, Scope: m.Scope, Copy: "foreign-copy", SourceRoot: m.Store.Root}))
			case "future-local-schema":
				write(t, filepath.Join(local.Root, "binding.json"), jsonBytes(profileBinding{Version: 2, Project: m.Owner, Scope: m.Scope, Copy: m.Copy, SourceRoot: m.Store.Root}))
			}
			before := capture(t, m)
			if _, err := m.DraftReplay(context.Background(), historical, bundle, inputs); err == nil {
				t.Fatal("unsafe recovery projection accepted", kind)
			}
			unchangedManager(t, m, before)
		})
	}
}

func TestReplayProjectionDoesNotInventUnadoptedGlobalPackage(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "project-guide", Description: "project"})
	m.Copy = "replay-copy"
	l := rulesLibrary(t, m)
	id := apply(t, l, Request{Action: "create", Name: "unadopted", Description: "global"}).SkillID
	apply(t, l, Request{Action: "enable", ID: id})
	local := ruleProfile("local")
	local["skills"] = map[string]any{"disable": []any{id}}
	root := l.Store.Root
	saveLocal(t, m, ProfileEdit{Profile: local, LibraryRoot: &root})
	m = reloadCopy(t, m)
	bundle, historical, inputs := historicalObservation(t, m)
	// Original-layer v1 snapshots did not retain unadopted reference bodies.
	inputs.Version, inputs.ReferencePackages = 1, nil
	inputs.Digest = SnapshotConfigurationDigest(inputs)
	_, err := m.DraftReplay(context.Background(), historical, bundle, inputs)
	code(t, err, "SNAPSHOT_DEPENDENCY_MATERIAL_MISSING")
}
