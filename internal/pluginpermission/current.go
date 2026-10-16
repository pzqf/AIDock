package pluginpermission

import (
	"context"
	"github.com/pzqf/AIDock/internal/result"
)

// WithCurrent holds the authorization lock through a short internal launch
// handoff. Revocation and recovery cannot interleave with disclosure. It grants
// no process execution, and the callback must not re-enter this store.
func (m *Manager) WithCurrent(ctx context.Context, candidates []Candidate, name string, fn func(Record) error) error {
	if name == "" || fn == nil {
		return result.Fail("INVALID_INPUT", "需要明确插件与内部交付步骤", 2)
	}
	return m.WithCurrents(ctx, candidates, []string{name}, func(records []Record) error { return fn(records[0]) })
}

// WithCurrents keeps every selected authorization under the same lock until a
// bounded joint file transaction finishes. No authorization can be revoked
// between checking two plugins or between checking and writing their launchers.
func (m *Manager) WithCurrents(ctx context.Context, candidates []Candidate, names []string, fn func([]Record) error) error {
	if len(names) == 0 || fn == nil {
		return result.Fail("INVALID_INPUT", "需要明确插件与内部交付步骤", 2)
	}
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" || seen[name] {
			return result.Fail("INVALID_INPUT", "插件选择不可为空或重复", 2)
		}
		seen[name] = true
	}
	return m.Store.Locked(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.Store.Pending(); err != nil {
			return recovery(err)
		}
		t, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		s := m.state(t, candidates, true)
		for _, f := range s.Checks {
			if f.Status == "FAIL" {
				return result.Fail("MCP_AUTHORIZATION_STORE_INVALID", "授权记录或审计无效；未交付", 4)
			}
		}
		records := []Record{}
		for _, name := range names {
			found := false
			for _, entry := range s.Authorizations {
				if entry.Record.Input.Name == name && entry.Status == "CURRENT" {
					records = append(records, entry.Record)
					found = true
					break
				}
			}
			if !found {
				return result.Fail("MCP_AUTHORIZATION_REQUIRED", "插件没有与当前项目副本、安装及配置一致的授权；未交付", 4)
			}
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		return fn(records)
	})
}
