package source

import (
	"sort"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Only explicit configuration references are retained. An unrelated library
// asset is not recovery input and must not enter the project's private archive.
func snapshotReferencePins(s SnapshotConfiguration, bundle Export) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	values := []map[string]any{s.Portable.Effective, s.Layers.Local}
	if bundle.ProfileReport != nil {
		values = append(values, bundle.ProfileReport.Effective)
	}
	for _, v := range values {
		section, _ := v["skills"].(map[string]any)
		pins, _ := section["pins"].(map[string]any)
		for id, raw := range pins {
			if revision, ok := raw.(string); ok {
				if out[id] == nil {
					out[id] = map[string]bool{}
				}
				out[id][revision] = true
			}
		}
	}
	return out
}

func (m *Manager) snapshotReferences(s SnapshotConfiguration, bundle Export, skills []EffectiveSkill) ([]ExportPackage, error) {
	known := map[string]bool{}
	for _, skill := range skills {
		known[skill.Skill.ID] = true
	}
	required := snapshotReferencePins(s, bundle)
	ids := []string{}
	for id := range required {
		if !known[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	if m.Library == nil {
		return nil, result.Fail("SNAPSHOT_DEPENDENCY_MATERIAL_MISSING", "配置引用缺少可核验的完整固定版本，不能创建可恢复快照", 4)
	}
	library, err := m.Library.Store.Capture(m.Library.Store.Root)
	if err != nil {
		return nil, err
	}
	out := []ExportPackage{}
	for _, id := range ids {
		versions := []string{}
		for revision := range required[id] {
			versions = append(versions, revision)
		}
		sort.Strings(versions)
		for _, revision := range versions {
			skill, pkg, err := m.Library.selectedFrom(&library, id, revision)
			if err != nil {
				return nil, err
			}
			// Move body references into the project's private immutable cache;
			// the library and the saved project source are never changed.
			copied := workstore.Tree{Exists: pkg.Exists, Entries: map[string]workstore.Entry{}}
			for name, entry := range pkg.Entries {
				if entry.Dir {
					copied.Entries[name] = entry
					continue
				}
				body, err := m.Library.Store.Data(entry)
				if err != nil {
					return nil, err
				}
				if err = m.Store.PutFile(&copied, name, body, entry.Mode); err != nil {
					return nil, err
				}
			}
			out = append(out, ExportPackage{EffectiveSkill: EffectiveSkill{Skill: skill, Owner: "global", Scope: "global", Layer: "inherited", Status: "PINNED", ReadOnly: true}, Tree: copied})
		}
	}
	return out, nil
}

func validateSnapshotReferences(s SnapshotConfiguration, bundle Export) error {
	bad := func() error {
		return result.Fail("SNAPSHOT_REFERENCE_INVALID", "快照引用包身份、版本或文件清单无效", 5)
	}
	needed, seen := snapshotReferencePins(s, bundle), map[string]bool{}
	for _, p := range s.ReferencePackages {
		skill := p.Skill
		key := skill.ID + "/" + skill.Revision
		if seen[key] || !needed[skill.ID][skill.Revision] || p.Owner != "global" || p.Scope != "global" || p.Layer != "inherited" || p.Status != "PINNED" || !p.ReadOnly || !p.Tree.Exists || p.Tree.Entries == nil || skill.Source.Mode != "owned" || skill.Source.Path != "skills/"+skill.ID || skill.RevisionFormat != "semantic-v1" || len(skill.BaseSource) != 0 || s.SkillLibraryRoot == "" {
			return bad()
		}
		seen[key] = true
		if _, err := config.Validate("skills", jsonBytes(Index{Version: 1, Owner: p.Owner, Scope: p.Scope, Skills: []Skill{skill}})); err != nil {
			return bad()
		}
		for name, entry := range p.Tree.Entries {
			if workstore.SafeRelative(name) != nil || !entry.Dir && (entry.Size < 0 || entry.Size > workstore.MaxFile) {
				return bad()
			}
		}
		if packageRevision(skill, p.Tree) != skill.Revision || skill.PackageDigest != portablePackageDigest(p.Tree) {
			return bad()
		}
		files := packageFiles(p.Tree, "")
		if len(files) != len(skill.Files) {
			return bad()
		}
		for i, f := range files {
			want := skill.Files[i]
			if f.Path != want.Path || f.Bytes != want.Bytes || f.SHA256 != want.SHA256 {
				return bad()
			}
		}
		doc, ok := p.Tree.Entries["SKILL.md"]
		if !ok || doc.Dir {
			return bad()
		}
	}
	return nil
}

func validateSnapshotReferenceBytes(packages []ExportPackage, data func(workstore.Entry) ([]byte, error)) error {
	for _, pkg := range packages {
		for name, e := range pkg.Tree.Entries {
			if e.Dir {
				continue
			}
			body, err := data(e)
			if err != nil {
				return err
			}
			if int64(len(body)) != e.Size || workstore.Hash(body) != e.Hash {
				return result.Fail("SNAPSHOT_REFERENCE_INVALID", "快照引用包正文与文件清单不一致", 5)
			}
			if name == "SKILL.md" {
				nativeName, err := documentName(body)
				if err != nil || nativeName != pkg.Skill.Name {
					return result.Fail("SNAPSHOT_REFERENCE_INVALID", "快照引用包入口与身份不一致", 5)
				}
			}
		}
	}
	return nil
}

func (m *Manager) restoreSnapshotReferences(t workstore.Tree, inherit Inheritance, known map[string]bool, inputs SnapshotConfiguration, expected Export) (workstore.Tree, error) {
	if len(inputs.ReferencePackages) == 0 {
		return t, nil // Legacy snapshots retain their explicit missing-body guard.
	}
	t = workstore.Clone(t)
	selected := map[string]string{}
	portable, _ := inputs.Portable.Effective["skills"].(map[string]any)
	sharedPins, _ := portable["pins"].(map[string]any)
	final, _ := expected.ProfileReport.Effective["skills"].(map[string]any)
	finalPins, _ := final["pins"].(map[string]any)
	for id := range snapshotReferencePins(inputs, expected) {
		if known[id] {
			continue
		}
		// Keep the frozen shared selection as the source-view pin. Every other
		// referenced version is explicitly retained for Local layer resolution.
		selected[id], _ = sharedPins[id].(string)
		if selected[id] == "" {
			selected[id], _ = finalPins[id].(string)
		}
	}
	retained := map[string][]Skill{}
	for _, pkg := range inputs.ReferencePackages {
		if known[pkg.Skill.ID] {
			continue
		}
		material := workstore.Tree{Exists: true, Entries: map[string]workstore.Entry{}}
		if err := m.install(&material, revisionPath(pkg.Skill), pkg.Tree, m); err != nil {
			return t, err
		}
		if err := m.Store.PutFile(&material, recordPath(pkg.Skill), jsonBytes(governed(pkg.Skill)), 0644); err != nil {
			return t, err
		}
		for name, entry := range material.Entries {
			if old, ok := t.Entries[name]; ok && old != entry {
				return t, result.Fail("REPLAY_IMMUTABLE_CONFLICT", "归档引用与历史不可变版本冲突，不能替换旧记录", 3)
			}
			t.Entries[name] = entry
		}
		retained[pkg.Skill.ID] = append(retained[pkg.Skill.ID], pkg.Skill)
	}
	for id, versions := range retained {
		primary, ok := inheritedVersion(versions, selected[id])
		if !ok {
			continue
		} // The caller retains its missing-material guard.
		association := InheritedSkill{Owner: "global", Scope: "global", Skill: primary}
		for _, version := range versions {
			if version.Revision != primary.Revision {
				association.Versions = append(association.Versions, version)
			}
		}
		sort.Slice(association.Versions, func(i, j int) bool { return association.Versions[i].Revision < association.Versions[j].Revision })
		inherit.Skills = append(inherit.Skills, association)
		known[id] = true
	}
	sort.Slice(inherit.Skills, func(i, j int) bool { return inherit.Skills[i].Skill.ID < inherit.Skills[j].Skill.ID })
	if err := m.Store.PutFile(&t, inheritanceFile, jsonBytes(inherit), 0644); err != nil {
		return t, err
	}
	return t, nil
}
