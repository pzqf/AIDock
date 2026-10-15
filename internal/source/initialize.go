package source

import (
	"strings"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// DraftInitialization prepares an empty project's canonical source. The caller
// must coordinate its installation with machine-local project registration.
// No project file is written here, and a nonempty source is never adopted.
func (m *Manager) DraftInitialization(name string) (before, after workstore.Tree, err error) {
	if m.Owner == "global" || strings.TrimSpace(name) == "" || len(name) > 120 {
		return before, after, result.Fail("INVALID_INPUT", "项目需要名称和独立项目身份", 2)
	}
	// Initialization is an observation only. The coordinator rechecks this
	// snapshot and acquires the normal root locks when the user confirms.
	err = func() error {
		if e := m.Store.Pending(); e != nil {
			return e
		}
		var e error
		before, e = m.Store.Capture(m.Store.Root)
		if e != nil {
			return e
		}
		if len(before.Entries) != 0 {
			return result.Fail("PROJECT_NOT_EMPTY", "项目技能源已有内容；请使用打开已有项目", 4)
		}
		after = workstore.Clone(before)
		after.Exists = true
		idx, profile, e := m.load(&before)
		if e != nil {
			return e
		}
		for file, item := range map[string]struct {
			kind  string
			value any
		}{
			"project.yaml": {"project", map[string]any{"schema_version": 1, "id": m.Owner, "name": name}},
			"skills.yaml":  {"skills", idx},
			"profile.yaml": {"profile", profile},
		} {
			bytes := jsonBytes(item.value)
			if _, e = config.Validate(item.kind, bytes); e != nil {
				return e
			}
			if e = m.Store.PutFile(&after, file, bytes, 0644); e != nil {
				return e
			}
		}
		return nil
	}()
	return
}
