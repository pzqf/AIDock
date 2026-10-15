package source

import (
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Definitions and Profile switches have distinct ownership. Removing a
// definition cannot silently remove an explicit enable/disable reference.
func (m *Manager) validateMCPRemoval(t workstore.Tree, next map[string]any) error {
	previous, err := m.mcpConfig(t)
	// Explicit replacement can repair a malformed definition file. In that case
	// every existing Profile reference must exist in the replacement.
	checkAll := err != nil
	remaining := map[string]bool{}
	for _, raw := range array(next["servers"]) {
		remaining[raw.(map[string]any)["name"].(string)] = true
	}
	removed := map[string]bool{}
	for _, raw := range array(previous["servers"]) {
		name := raw.(map[string]any)["name"].(string)
		if !remaining[name] {
			removed[name] = true
		}
	}
	if len(removed) == 0 && !checkAll {
		return nil
	}
	_, shared, err := m.load(&t)
	if err != nil {
		return err
	}
	layers, err := m.profileLayers()
	if err != nil {
		return err
	}
	profiles := []map[string]any{shared, layers.Defaults, layers.Local}
	profiles = append(profiles, layers.Profiles...)
	if m.Owner == "global" {
		named, err := m.namedProfiles(t)
		if err != nil {
			return err
		}
		profiles = append(profiles, named...)
	}
	for _, value := range profiles {
		section, _ := value["mcp"].(map[string]any)
		for _, action := range []string{"enable", "disable"} {
			for _, raw := range array(section[action]) {
				if removed[raw.(string)] || checkAll && !remaining[raw.(string)] {
					return result.Fail("MCP_PROFILE_REFERENCE_EXISTS", "先在已关联 Profile 中解除 MCP 引用，再移除或改名："+raw.(string), 3)
				}
			}
		}
	}
	return nil
}
