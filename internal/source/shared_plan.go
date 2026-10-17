package source

import (
	"path"
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// DraftShared constructs a candidate from immutable observations and assets.
// It writes only observation blobs under Local, never the target source root.
// Only independent owned skills qualify; inheritance and external authority
// require separate generation/lineage contracts before cross-copy publication.
func (m *Manager) DraftShared(before workstore.Tree, selected []SharedEntry, assets *workstore.Store) (workstore.Tree, Preflight, error) {
	return m.DraftSharedConfiguration(before, selected, nil, assets)
}

// DraftSharedConfiguration builds one complete candidate for independent skill
// and shared-configuration decisions. Local overrides and unowned files are
// never replaced by the publisher's versions.
func (m *Manager) DraftSharedConfiguration(before workstore.Tree, selected []SharedEntry, configurations []SharedConfiguration, assets *workstore.Store) (workstore.Tree, Preflight, error) {
	after := workstore.Clone(before)
	idx, profile, err := m.load(&before)
	if err != nil {
		return after, Preflight{}, err
	}
	if profile["schema_version"] != 2 {
		return after, Preflight{}, result.Fail("MIGRATION_REQUIRED", "副本候选要求 Profile v2", 3)
	}
	originalProfile := jsonBytes(profile)
	life, err := m.loadLifecycle(&before, idx)
	if err != nil {
		return after, Preflight{}, err
	}
	_, effective, err := m.resolve(&before, idx, profile)
	if err != nil {
		return after, Preflight{}, err
	}
	seen := map[string]bool{}
	for _, e := range selected {
		if seen[e.ID] || !uuid.MatchString(e.ID) || e.ID != e.Metadata.ID || e.Name != e.Metadata.Name || e.Revision != e.Metadata.Revision || e.Digest != SharedDigest(e) {
			return after, Preflight{}, result.Fail("REPLICA_ENTRY_INVALID", "候选条目身份或摘要不一致", 5)
		}
		seen[e.ID] = true
		if e.Layer != "owned" || e.Metadata.Source.Mode != "owned" || len(e.Metadata.BaseSource) != 0 || len(e.Metadata.Upstream) != 0 || e.Metadata.Source.PluginID != "" || e.Metadata.Source.LockDigest != "" {
			return after, Preflight{}, result.Fail("REPLICA_AUTHORITY_UNSUPPORTED", "当前仅支持独立 owned 项目技能；继承、覆盖、分叉与外部权威需各自生成适配", 4)
		}
		at := -1
		for i, s := range idx.Skills {
			if s.ID == e.ID {
				at = i
			}
		}
		for _, item := range effective {
			if item.Skill.ID == e.ID && item.Layer != "owned" {
				return after, Preflight{}, result.Fail("REPLICA_AUTHORITY_UNSUPPORTED", "目标身份属于继承、覆盖、分叉或外部源", 4)
			}
		}
		if e.Retired && (e.Enabled || e.Retirement == nil || e.Retirement.ID != e.ID || e.Retirement.NativeName != e.Name || e.Retirement.Revision != e.Revision) || !e.Retired && e.Retirement != nil {
			return after, Preflight{}, result.Fail("REPLICA_ENTRY_INVALID", "候选退役状态与完整退役记录不匹配", 5)
		}
		skill := e.Metadata
		skill.Source.Path = "skills/" + e.ID
		if at >= 0 {
			skill.Source.Origin = idx.Skills[at].Source.Origin
		}
		for p, f := range e.Tree.Entries {
			if f.Dir {
				continue
			}
			b, err := assets.Data(f)
			if err != nil {
				return after, Preflight{}, err
			}
			scratch := workstore.Tree{Entries: map[string]workstore.Entry{}}
			if err = m.Store.PutFile(&scratch, p, b, f.Mode); err != nil {
				return after, Preflight{}, err
			}
		}
		if err = m.checkPackage(skill, e.Tree); err != nil {
			return after, Preflight{}, err
		}
		rev := revisionPath(skill)
		if _, ok := after.Entries[rev]; ok && workstore.Digest(subtree(after, rev)) != workstore.Digest(e.Tree) {
			return after, Preflight{}, result.Fail("REVISION_CORRUPT", "目标不可变版本缓存发生变化", 5)
		}
		for p := range after.Entries {
			if p == skill.Source.Path || strings.HasPrefix(p, skill.Source.Path+"/") {
				delete(after.Entries, p)
			}
		}
		for _, prefix := range []string{skill.Source.Path, rev} {
			for p, f := range e.Tree.Entries {
				if f.Dir {
					after.Entries[prefix+"/"+p] = f
					for d := path.Dir(prefix + "/" + p); d != "."; d = path.Dir(d) {
						if _, ok := after.Entries[d]; !ok {
							after.Entries[d] = workstore.Directory()
						}
					}
				} else {
					b, err := m.Store.Data(f)
					if err != nil {
						return after, Preflight{}, err
					}
					if err = m.Store.PutFile(&after, prefix+"/"+p, b, f.Mode); err != nil {
						return after, Preflight{}, err
					}
				}
			}
		}
		if skill.RevisionFormat == "semantic-v1" {
			if err = m.recordGovernance(&after, skill); err != nil {
				return after, Preflight{}, err
			}
		}
		if at >= 0 {
			idx.Skills[at] = skill
		} else {
			idx.Skills = append(idx.Skills, skill)
		}
		records := []Retirement{}
		for _, record := range life.Retirements {
			if record.ID != e.ID {
				records = append(records, record)
			}
		}
		if e.Retired {
			records = append(records, *e.Retirement)
		}
		life.Retirements = records
		setEnabled(profile, e.ID, e.Enabled)
		switches(profile)["pins"].(map[string]any)[e.ID] = skill.Revision
	}
	sort.Slice(idx.Skills, func(i, j int) bool { return idx.Skills[i].ID < idx.Skills[j].ID })
	sort.Slice(life.Retirements, func(i, j int) bool { return life.Retirements[i].ID < life.Retirements[j].ID })
	if err = m.draftSharedConfigurations(before, &after, profile, configurations); err != nil {
		return after, Preflight{}, err
	}
	for _, item := range []struct {
		path, kind string
		value      any
	}{{"skills.yaml", "skills", idx}, {"profile.yaml", "profile", profile}, {lifecycleFile, "skill-lifecycle", life}} {
		if len(selected) == 0 && (item.path != "profile.yaml" || string(originalProfile) == string(jsonBytes(profile))) {
			continue
		}
		if item.path == lifecycleFile && len(life.Retirements) == 0 {
			if _, ok := before.Entries[lifecycleFile]; !ok {
				continue
			}
		}
		if _, err = config.Validate(item.kind, jsonBytes(item.value)); err != nil {
			return after, Preflight{}, err
		}
		mode := uint32(0644)
		if old, ok := before.Entries[item.path]; ok {
			mode = old.Mode
		}
		if err = m.Store.PutFile(&after, item.path, jsonBytes(item.value), mode); err != nil {
			return after, Preflight{}, err
		}
	}
	if err = validateRetiredProfile(life, profile); err != nil {
		return after, Preflight{}, err
	}
	report, err := m.preflight(after, idx, profile)
	report.Candidate = true
	if err == nil {
		err = blockConfigurationCandidate(report)
	}
	// Retirement cannot silently break declared references, even if the
	// referring package is currently disabled.
	if err == nil {
		for _, e := range selected {
			if e.Retired {
				for _, ref := range m.impact(idx, profile, e.ID).References {
					if ref.Blocking {
						err = result.Fail("SKILL_REFERENCED", "目标仍声明退役技能引用: "+ref.Path, 3)
					}
				}
			}
		}
	}
	return after, report, err
}
