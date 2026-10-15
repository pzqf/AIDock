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
		for _, entry := range s.Authorizations {
			if entry.Record.Input.Name == name && entry.Status == "CURRENT" {
				if err = ctx.Err(); err != nil {
					return err
				}
				return fn(entry.Record)
			}
		}
		return result.Fail("MCP_AUTHORIZATION_REQUIRED", "插件没有与当前项目副本、安装及配置一致的授权；未交付", 4)
	})
}
