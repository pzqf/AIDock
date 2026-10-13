package config

import (
	"encoding/json"
	"fmt"
)

type SkillBinding struct{ ID, Revision string }

// MigrateProfile preserves other owned fields; ambiguous or absent names never
// silently select a UUID. The caller previews and backs up the original bytes.
func MigrateProfile(data []byte, names map[string][]SkillBinding) ([]byte, error) {
	value, e := Validate("profile-v1", data)
	if e != nil {
		return nil, e
	}
	m := value.(map[string]any)
	m["schema_version"] = 2
	if skills, ok := m["skills"].(map[string]any); ok {
		pins := map[string]any{}
		for _, key := range []string{"enable", "disable"} {
			list, ok := skills[key].([]any)
			if !ok {
				continue
			}
			mapped := []any{}
			seen := map[string]bool{}
			for _, v := range list {
				matches := names[v.(string)]
				if len(matches) != 1 {
					return nil, fmt.Errorf("MIGRATION_REFERENCE_UNRESOLVED: %s 有 %d 个身份候选", v, len(matches))
				}
				match := matches[0]
				if !seen[match.ID] {
					mapped = append(mapped, match.ID)
					seen[match.ID] = true
				}
				pins[match.ID] = match.Revision
			}
			skills[key] = mapped
		}
		skills["pins"] = pins
	}
	out, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return nil, e
	}
	out = append(out, '\n')
	_, e = Validate("profile", out)
	return out, e
}
