package source

import (
	"encoding/json"
	"os"
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/bridge"
	"github.com/pzqf/AIDock/internal/profile"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Export is a single saved-source observation, shared by every native adapter.
// Package trees reference immutable Local Root blobs, never live upstream paths.
type Export struct {
	Root                string          `json:"root"`
	Owner               string          `json:"owner"`
	Scope               string          `json:"scope_id"`
	Copy                string          `json:"copy,omitempty"`
	Snapshot            string          `json:"snapshot"`
	Authorities         []string        `json:"authorities"`
	BridgeDigest        string          `json:"bridge_digest,omitempty"`
	Preflight           Preflight       `json:"preflight"`
	Packages            []ExportPackage `json:"packages"`
	Rules               []string        `json:"rules,omitempty"`
	ConfigurationDigest string          `json:"configuration_digest,omitempty"`
	ProfileReport       *profile.Report `json:"profile_report,omitempty"`
	// Computed for historical observations only, never part of archived input
	// identity or a permission to restore/apply the rest of a project.
	SkillMaterial *SnapshotSkillMaterial `json:"-"`
}
type ExportPackage struct {
	EffectiveSkill
	Tree workstore.Tree `json:"tree"`
}

func (m *Manager) Export() (Export, error) {
	var out Export
	err := m.WithExport(func(v Export) error { out = v; return nil })
	return out, err
}

// WithExport holds the source lock through the consumer's final preflight and
// transaction. Consumers must not call another source-locking method inside fn.
func (m *Manager) WithExport(fn func(Export) error) error {
	return m.Store.Locked(func() error {
		return m.lockProfileLayers(func() error {
			out, err := m.exportLocked()
			if err != nil {
				return err
			}
			return fn(out)
		})
	})
}

func (m *Manager) exportLocked() (Export, error) {
	out := Export{Root: m.Store.Root, Owner: m.Owner, Scope: m.Scope, Copy: m.Copy, Packages: []ExportPackage{}}
	err := func() error {
		if err := m.Store.Pending(); err != nil {
			return err
		}
		t, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		if _, ok := t.Entries[m.identityFile()]; !ok {
			return result.Fail("SOURCE_NOT_INITIALIZED", "先保存技能源，再生成工具预览", 4)
		}
		idx, profile, err := m.load(&t)
		if err != nil {
			return err
		}
		out.Snapshot = workstore.Digest(t)
		layers, err := m.profileLayers()
		if err != nil {
			return err
		}
		roots := map[string]bool{}
		for p, entry := range t.Entries {
			if entry.Dir || !strings.HasPrefix(p, ".external-approvals/") {
				continue
			}
			b, err := m.Store.Data(entry)
			if err != nil {
				return err
			}
			var approval ExternalApproval
			if err := json.Unmarshal(b, &approval); err != nil {
				return err
			}
			roots[approval.Binding.Root] = true
		}
		if m.BridgeFile != "" {
			b, err := workstore.ReadFile(m.BridgeFile)
			if err == nil {
				idx, err := bridge.Decode(m.BridgeFile, b)
				if err != nil {
					return err
				}
				out.BridgeDigest = workstore.Hash(b)
				for _, reg := range idx.Bridges {
					roots[reg.Root] = true
				}
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		out.Authorities = []string{}
		for root := range roots {
			out.Authorities = append(out.Authorities, root)
		}
		sort.Strings(out.Authorities)
		out.Preflight, err = m.preflight(t, idx, profile, layers)
		if err != nil {
			return err
		}
		_, effective, err := m.resolve(&t, idx, profile)
		if err != nil {
			return err
		}
		resolved, err := m.resolveProfile(t, profile, effective, layers)
		if err != nil {
			return err
		}
		out.ConfigurationDigest, out.ProfileReport = layers.Digest, &resolved
		effective, err = m.selectProfileSkills(t, effective, resolved.Effective)
		if err != nil {
			return err
		}
		profile = resolved.Effective
		out.Rules = profileTexts(profile)
		enabled := map[string]bool{}
		for _, id := range switches(profile)["enable"].([]any) {
			enabled[id.(string)] = true
		}
		for _, id := range switches(profile)["disable"].([]any) {
			enabled[id.(string)] = false
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
			out.Packages = append(out.Packages, ExportPackage{EffectiveSkill: item, Tree: pkg})
		}
		fresh, err := m.ConfigurationDigest()
		if err != nil {
			return err
		}
		if fresh != layers.Digest {
			return result.Fail("PLAN_STALE", "导出期间全局库或本机配置已变化", 3)
		}
		return nil
	}()
	return out, err
}
