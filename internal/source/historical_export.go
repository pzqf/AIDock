package source

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/bridge"
	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// ExportHistorical reconstructs an immutable observation with its original
// configuration layers. It does not adopt live library changes, write project
// files, or revive historical permission/runtime evidence. Callers must first
// materialize the archived blobs into this source's private immutable cache.
// Replica ExportCandidate deliberately retains its narrower owned-only policy.
func (m *Manager) ExportHistorical(ctx context.Context, t workstore.Tree, expected Export, inputs SnapshotConfiguration) (Export, error) {
	var out Export
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if expected.Root != m.Store.Root || expected.Owner != m.Owner || expected.Scope != m.Scope || expected.Copy != m.Copy || expected.Snapshot != workstore.Digest(t) {
		return out, result.Fail("SNAPSHOT_BINDING_MISMATCH", "历史配置不属于当前项目、目录、作用域或副本", 3)
	}
	if err := ValidateSnapshotConfiguration(inputs, expected, m.Store.Local); err != nil {
		return out, err
	}
	err := m.Store.Locked(func() error {
		if err := m.Store.Pending(); err != nil {
			return err
		}
		if err := ValidateSnapshotConfigurationBytes(inputs, expected, m.Store.Data); err != nil {
			return err
		}
		idx, shared, err := m.load(&t)
		if err != nil {
			return err
		}
		_, effective, err := m.resolve(&t, idx, shared)
		if err != nil {
			return err
		}
		resolved, err := m.resolveProfile(t, shared, effective, inputs.Layers)
		if err != nil {
			return err
		}
		effective, err = m.selectProfileSkills(t, effective, resolved.Effective)
		if err != nil {
			return err
		}
		if resolved.Digest != expected.ProfileReport.Digest || workstore.Hash(jsonBytes(resolved)) != workstore.Hash(jsonBytes(expected.ProfileReport)) {
			return result.Fail("SNAPSHOT_CONFIGURATION_INVALID", "历史分层输入不能重新解析出归档配置", 5)
		}
		out = expected
		material := m.snapshotSkillMaterial(inputs, expected, effective, t)
		out.SkillMaterial = &material
		out.ProfileReport, out.Rules, out.Packages = &resolved, profileTexts(resolved.Effective), []ExportPackage{}
		// Preserve both historical and current authority roots. A historical
		// target may never claim a newly registered external source directory.
		live, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		out.Authorities, out.BridgeDigest, err = m.replayAuthorities(live, expected, inputs)
		if err != nil {
			return err
		}
		return m.withArtifactLocks(t, func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			out.Preflight, err = m.preflight(t, idx, shared, inputs.Layers)
			out.Preflight.Candidate = true
			if err != nil {
				return err
			}
			enabled := map[string]bool{}
			for _, raw := range array(switches(resolved.Effective)["enable"]) {
				enabled[raw.(string)] = true
			}
			for _, raw := range array(switches(resolved.Effective)["disable"]) {
				enabled[raw.(string)] = false
			}
			for _, item := range effective {
				if !enabled[item.Skill.ID] {
					continue
				}
				prefix := item.Skill.Source.Path
				if item.ReadOnly {
					prefix = revisionPath(item.Skill)
				}
				pkg := subtree(t, prefix)
				if err := m.checkPackage(item.Skill, pkg); err != nil {
					return err
				}
				out.Packages = append(out.Packages, ExportPackage{EffectiveSkill: item, Tree: pkg})
			}
			// Rules uses omitempty in archive v1/v2: a decoded empty collection
			// is nil. Compare the semantic empty list rather than slice identity.
			if workstore.Hash(jsonBytes(out.Packages)) != workstore.Hash(jsonBytes(expected.Packages)) || workstore.Hash(jsonBytes(out.Rules)) != workstore.Hash(jsonBytes(append([]string{}, expected.Rules...))) {
				return result.Fail("SNAPSHOT_EFFECTIVE_MISMATCH", "历史配置重新解析出的完整包或规则与归档不一致", 5)
			}
			return nil
		})
	})
	return out, err
}

// Observe protections without following live defaults to replace frozen input.
func (m *Manager) replayAuthorities(live workstore.Tree, expected Export, inputs SnapshotConfiguration) ([]string, string, error) {
	var authorities []string
	var bridgeDigest string
	roots := map[string]bool{}
	for _, root := range expected.Authorities {
		roots[root] = true
	}
	for _, root := range []string{inputs.SkillLibraryRoot, inputs.Layers.LibraryRoot} {
		if root != "" {
			roots[root] = true
		}
	}
	if m.Library != nil {
		roots[m.Library.Store.Root] = true
	}
	// A recovery reader intentionally does not bootstrap today's Local profile.
	// Retain a valid explicitly named library root as a protection nonetheless;
	// malformed current profile contents may still be repaired from the archive.
	if m.Copy != "" {
		bindingPath := filepath.Join(LocalProfileRoot(m.Store.Local, m.Owner, m.Scope, m.Copy), "binding.json")
		body, e := workstore.ReadFile(bindingPath)
		if e != nil && !os.IsNotExist(e) {
			return nil, "", e
		}
		if e == nil {
			if v, parseErr := config.Parse(body); parseErr == nil {
				if fields, ok := v.(map[string]any); ok {
					if root, ok := fields["library_root"].(string); ok && root != "" && filepath.IsAbs(root) && filepath.Clean(root) == root {
						roots[root] = true
					}
				}
			}
		}
	}

	for p, entry := range live.Entries {
		if entry.Dir || !strings.HasPrefix(p, ".external-approvals/") {
			continue
		}
		body, err := m.Store.Data(entry)
		if err != nil {
			return nil, "", err
		}
		if _, err = config.Validate("external-approval", body); err != nil {
			return nil, "", result.Fail("SNAPSHOT_AUTHORITY_INVALID", "当前外部来源保护记录不可读取；先修复记录再评估恢复目标", 5)
		}
		var approval ExternalApproval
		if err = json.Unmarshal(body, &approval); err != nil {
			return nil, "", result.Fail("SNAPSHOT_AUTHORITY_INVALID", "当前外部来源保护记录不可读取", 5)
		}
		roots[approval.Binding.Root] = true
	}
	bridgeDigest = ""
	if m.BridgeFile != "" {
		body, err := workstore.ReadFile(m.BridgeFile)
		if err == nil {
			registry, err := bridge.Decode(m.BridgeFile, body)
			if err != nil {
				return nil, "", result.Fail("SNAPSHOT_AUTHORITY_INVALID", "当前外部桥登记不可读取", 5)
			}
			bridgeDigest = workstore.Hash(body)
			for _, item := range registry.Bridges {
				roots[item.Root] = true
			}
		} else if !os.IsNotExist(err) {
			return nil, "", err
		}
	}
	authorities = []string{}
	for root := range roots {
		authorities = append(authorities, root)
	}
	sort.Strings(authorities)
	return authorities, bridgeDigest, nil
}
