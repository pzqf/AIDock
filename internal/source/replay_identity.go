package source

import (
	"context"
	"os"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Read-only structural ownership check under the common coordinator's root
// locks. No observation blobs, grants or other authority are written. Recovery
// may repair corrupt/missing bytes from its own journal, but never a declared
// foreign owner or future schema; ordinary proposals need a valid project ID.
func (m *Manager) ValidateReplayIdentity(ctx context.Context, recovery bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	check := func(path string, fields map[string]string, version int) ([]byte, error) {
		body, err := workstore.ReadFile(path)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		v, err := config.Parse(body)
		if err != nil {
			return body, nil
		}
		values, ok := v.(map[string]any)
		if !ok {
			return body, nil
		}
		if raw, exists := values["schema_version"]; exists && raw != version {
			return nil, result.Fail("REPLAY_IDENTITY_SCHEMA_UNSUPPORTED", "当前身份文件声明未知版本，不能自动降级恢复", 4)
		}
		for key, want := range fields {
			if got, ok := values[key].(string); ok && got != "" && got != want {
				return nil, result.Fail("SOURCE_IDENTITY_MISMATCH", "当前身份文件已属于另一项目、作用域或副本，保留现场", 3)
			}
		}
		return body, nil
	}
	project, err := check(filepath.Join(m.Store.Root, m.identityFile()), map[string]string{"id": m.Owner}, 1)
	if err != nil {
		return err
	}
	if !recovery {
		if _, err := config.Validate("project", project); err != nil {
			return result.Fail("REPLAY_IDENTITY_CONFIRMATION_REQUIRED", "当前项目身份不可确认，须独立修复身份后再回放", 4)
		}
	}
	if _, err = check(filepath.Join(m.Store.Root, "skills.yaml"), map[string]string{"owner": m.Owner, "scope_id": m.Scope}, 1); err != nil {
		return err
	}
	local, err := m.localProfileStore()
	if err != nil {
		return err
	}
	_, err = check(filepath.Join(local.Root, "binding.json"), map[string]string{"project": m.Owner, "scope": m.Scope, "copy": m.Copy, "source_root": m.Store.Root}, 1)
	return err
}
