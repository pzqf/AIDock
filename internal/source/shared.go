package source

import (
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
	"sort"
)

// Shared observations include disabled packages and deletion tombstones. They
// exclude native outputs, drafts, machine paths and unrelated project files.
// A complete tree is kept for comparisons/backups, never an instruction to copy.
type SharedEntry struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Revision   string         `json:"revision"`
	Layer      string         `json:"layer"`
	Path       string         `json:"source_path"`
	Enabled    bool           `json:"enabled"`
	Retired    bool           `json:"retired"`
	Retirement *Retirement    `json:"retirement,omitempty"`
	Metadata   Skill          `json:"metadata"`
	Tree       workstore.Tree `json:"tree"`
	Digest     string         `json:"digest"`
}
type SharedCatalog struct {
	Root               string                `json:"root"`
	Owner              string                `json:"owner"`
	Scope              string                `json:"scope_id"`
	Snapshot           string                `json:"snapshot"`
	Entries            []SharedEntry         `json:"entries"`
	Preflight          Preflight             `json:"preflight"`
	Configurations     []SharedConfiguration `json:"configurations,omitempty"`
	ConfigurationNotes []string              `json:"configuration_notes,omitempty"`
}

func SharedDigest(e SharedEntry) string {
	e.Digest = ""
	e.Path = ""
	return workstore.Hash(jsonBytes(e))
}
func (m *Manager) Shared() (SharedCatalog, error) {
	out := SharedCatalog{Root: m.Store.Root, Owner: m.Owner, Scope: m.Scope, Entries: []SharedEntry{}}
	// Replica observation must not create a target-side lock file. Read-only
	// snapshots are rechecked after parsing; publishing will acquire real locks.
	err := func() error {
		if err := m.Store.Pending(); err != nil {
			return err
		}
		t, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		if _, ok := t.Entries[m.identityFile()]; !ok {
			return result.Fail("SOURCE_NOT_INITIALIZED", "副本必须已有明确项目技能源", 4)
		}
		idx, profile, err := m.load(&t)
		if err != nil {
			return err
		}
		_, effective, err := m.resolve(&t, idx, profile)
		if err != nil {
			return err
		}
		life, err := m.loadLifecycle(&t, idx)
		if err != nil {
			return err
		}
		retired := map[string]bool{}
		for _, v := range life.Retirements {
			retired[v.ID] = true
		}
		enabled := map[string]bool{}
		for _, id := range switches(profile)["enable"].([]any) {
			enabled[id.(string)] = true
		}
		for _, id := range switches(profile)["disable"].([]any) {
			enabled[id.(string)] = false
		}
		out.Snapshot = workstore.Digest(t)
		out.Configurations, out.ConfigurationNotes, err = m.sharedConfigurations(t, profile)
		if err != nil {
			return err
		}
		out.Preflight, err = m.preflight(t, idx, profile)
		if err != nil {
			return err
		}
		for _, v := range effective {
			base := v.Skill.Source.Path
			if v.ReadOnly {
				base = revisionPath(v.Skill)
			}
			tree := subtree(t, base)
			if err = m.checkPackage(v.Skill, tree); err != nil {
				return result.Fail("SOURCE_PACKAGE_DRIFT", v.Skill.Name+" 的完整包与固定版本不一致", 3)
			}
			metadata := v.Skill
			// Authority paths belong to each copy's binding, not shared semantics.
			metadata.Source.Path = ""
			metadata.Source.Origin = ""
			item := SharedEntry{ID: v.Skill.ID, Name: v.Skill.Name, Revision: v.Skill.Revision, Layer: v.Layer, Path: base, Enabled: enabled[v.Skill.ID] && !retired[v.Skill.ID], Retired: retired[v.Skill.ID], Metadata: metadata, Tree: tree}
			for _, retirement := range life.Retirements {
				if retirement.ID == item.ID {
					copy := retirement
					item.Retirement = &copy
				}
			}
			item.Digest = SharedDigest(item)
			out.Entries = append(out.Entries, item)
		}
		sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].ID < out.Entries[j].ID })
		current, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		if workstore.Digest(current) != out.Snapshot {
			return result.Fail("PLAN_STALE", "副本共享源在观察期间变化", 3)
		}
		return nil
	}()
	return out, err
}
