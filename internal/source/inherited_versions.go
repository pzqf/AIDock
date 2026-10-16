package source

import (
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Versions are explicitly retained in this project's inherited association.
// Neither a matching file in a cache nor the current global library grants a
// new selectable revision. Every retained version has its complete local body.
func (m *Manager) inheritedVersions(t *workstore.Tree, association InheritedSkill) ([]Skill, error) {
	if len(association.Versions) > 256 {
		return nil, result.Fail("SOURCE_METADATA_INVALID", "保留的继承版本超过支持数量", 5)
	}
	out := append([]Skill{association.Skill}, association.Versions...)
	seen := map[string]bool{}
	for _, skill := range out {
		if skill.ID != association.Skill.ID || seen[skill.Revision] || skill.Source.Mode != "owned" || skill.Source.Path != "skills/"+skill.ID || len(skill.BaseSource) != 0 || (skill.RevisionFormat != "" && skill.RevisionFormat != "semantic-v1") {
			return nil, result.Fail("SOURCE_METADATA_INVALID", "继承可选版本身份、来源或修订重复", 5)
		}
		seen[skill.Revision] = true
		if _, err := config.Validate("skills", jsonBytes(Index{Version: 1, Owner: association.Owner, Scope: association.Scope, Skills: []Skill{skill}})); err != nil {
			return nil, result.Fail("SOURCE_METADATA_INVALID", "继承版本清单无效", 5)
		}
		if _, err := m.cached(t, skill); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Revision < out[j].Revision })
	return out, nil
}

func inheritedVersion(versions []Skill, revision string) (Skill, bool) {
	for _, skill := range versions {
		if skill.Revision == revision {
			return skill, true
		}
	}
	return Skill{}, false
}

func retainedInheritedVersions(before InheritedSkill, selected Skill) []Skill {
	versions := map[string]Skill{}
	for _, skill := range append([]Skill{before.Skill}, before.Versions...) {
		if skill.Revision != selected.Revision {
			versions[skill.Revision] = skill
		}
	}
	out := []Skill(nil)
	for _, skill := range versions {
		out = append(out, skill)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Revision < out[j].Revision })
	return out
}

// selectProfileSkills is shared by preflight and every package export. A Local
// pin therefore changes dependency checks and emitted bytes together, while the
// saved shared package and its editor remain on the shared selected revision.
func (m *Manager) selectProfileSkills(t workstore.Tree, shared []EffectiveSkill, profile map[string]any) ([]EffectiveSkill, error) {
	section, _ := profile["skills"].(map[string]any)
	pins, _ := section["pins"].(map[string]any)
	out := append([]EffectiveSkill{}, shared...)
	names := map[string]string{}
	for i, item := range out {
		pin, _ := pins[item.Skill.ID].(string)
		if pin != "" && pin != item.Skill.Revision {
			enabled := false
			for _, raw := range array(section["enable"]) {
				if raw == item.Skill.ID {
					enabled = true
				}
			}
			for _, raw := range array(section["disable"]) {
				if raw == item.Skill.ID {
					enabled = false
				}
			}
			if item.Layer != "inherited" {
				if !enabled {
					continue
				} // Preserve existing disabled declaration diagnostics.
				return nil, result.Fail("PROFILE_PIN_MISMATCH", "最终 pin 与当前项目/外部包不同；需先明确采纳对应完整版本", 3)
			}
			selected, ok := inheritedVersion(item.Versions, pin)
			if !ok {
				if !enabled {
					continue
				} // It cannot enter an emitted enabled package.
				return nil, result.Fail("PROFILE_PIN_MISMATCH", "最终 pin 尚无项目明确保留的完整继承版本", 3)
			}
			if _, err := m.cached(&t, selected); err != nil {
				return nil, err
			}
			item.Skill = selected
			out[i] = item
		}
		key := strings.ToLower(item.Skill.Name)
		if old, exists := names[key]; exists && old != item.Skill.ID {
			return nil, result.Fail("NATIVE_NAME_COLLISION", "最终 pin 选择的原生技能名冲突，需先协调", 3)
		}
		names[key] = item.Skill.ID
	}
	return out, nil
}
