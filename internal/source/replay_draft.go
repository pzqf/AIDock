package source

import (
	"context"
	"encoding/json"
	"path"
	"strings"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/profile"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// ReplayDraft is private compiler material. It is not a restore permission or
// an API response: the joint coordinator must bind before/after digests, probe
// the candidate, require current permissions and back up all roots together.
type ReplayDraft struct {
	SourceBefore workstore.Tree `json:"-"`
	SourceAfter  workstore.Tree `json:"-"`
	LocalBefore  workstore.Tree `json:"-"`
	LocalAfter   workstore.Tree `json:"-"`
	Layers       ProfileLayers  `json:"-"`
	Report       profile.Report `json:"-"`
	Preflight    Preflight      `json:"-"`
	Digest       string         `json:"digest"`
}

// DraftReplay freezes shared configuration into a project override while
// preserving original Local values separately. Complete inherited/external
// packages retain their original pinned provenance; neither authority is read
// to replace the archived bytes, downgraded or made writable by this projection.
// Only registered source files/packages are replaced. Current unknown files,
// immutable version history, memory/session/audit/credential/Git data survive.
func (m *Manager) DraftReplay(ctx context.Context, historical workstore.Tree, expected Export, inputs SnapshotConfiguration) (ReplayDraft, error) {
	var out ReplayDraft
	if _, err := m.ExportHistorical(ctx, historical, expected, inputs); err != nil {
		return out, err
	}
	err := m.Store.Locked(func() error {
		if err := m.Store.Pending(); err != nil {
			return err
		}
		local, err := m.localProfileStore()
		if err != nil {
			return err
		}
		return local.Locked(func() error {
			if err := local.Pending(); err != nil {
				return err
			}
			before, err := m.Store.Capture(m.Store.Root)
			if err != nil {
				return err
			}
			localBefore, err := local.Capture(local.Root)
			if err != nil {
				return err
			}
			for p, entry := range localBefore.Entries {
				if entry.Dir || (p != "binding.json" && p != "profile.yaml") {
					return result.Fail("REPLAY_LOCAL_UNMANAGED", "本机配置根含未登记文件或目录，保留原件等待处理", 3)
				}
				body, err := local.Data(entry)
				if err != nil {
					return err
				}
				if p == "binding.json" {
					if parsed, err := config.Parse(body); err == nil {
						if fields, ok := parsed.(map[string]any); ok {
							if version, exists := fields["schema_version"]; exists && version != 1 {
								return result.Fail("REPLAY_LOCAL_SCHEMA_UNSUPPORTED", "当前本机绑定使用未知版本，不能通过历史恢复降级接管", 4)
							}
							for key, expected := range map[string]string{"project": m.Owner, "scope": m.Scope, "copy": m.Copy, "source_root": m.Store.Root} {
								if value, ok := fields[key].(string); ok && value != "" && value != expected {
									return result.Fail("PROFILE_LOCAL_BINDING_MISMATCH", "当前本机原件属于另一绑定，不能自动接管", 3)
								}
							}
						}
					}
				}
				scratch := workstore.Tree{Entries: map[string]workstore.Entry{}}
				if err = m.Store.PutFile(&scratch, entry.Hash, body, entry.Mode); err != nil {
					return err
				}
			}
			out, err = m.draftReplay(historical, expected, inputs, before, localBefore)
			return err
		})
	})
	return out, err
}

func (m *Manager) replayProject(t workstore.Tree) (map[string]any, error) {
	e, ok := t.Entries[m.identityFile()]
	if !ok || e.Dir {
		return nil, result.Fail("REPLAY_IDENTITY_CONFIRMATION_REQUIRED", "当前项目身份缺失；不能仅依据快照接管非空目录", 4)
	}
	body, err := m.Store.Data(e)
	if err != nil {
		return nil, err
	}
	v, err := config.Validate("project", body)
	if err != nil {
		return nil, result.Fail("REPLAY_IDENTITY_CONFIRMATION_REQUIRED", "当前项目身份损坏；需要独立身份恢复流程", 4)
	}
	project := v.(map[string]any)
	if project["id"] != m.Owner {
		return nil, result.Fail("SOURCE_IDENTITY_MISMATCH", "当前目录已属于另一项目，拒绝恢复", 3)
	}
	return project, nil
}

func (m *Manager) draftReplay(historical workstore.Tree, expected Export, inputs SnapshotConfiguration, before, localBefore workstore.Tree) (ReplayDraft, error) {
	out := ReplayDraft{SourceBefore: before, SourceAfter: workstore.Clone(before), LocalBefore: localBefore, LocalAfter: workstore.Clone(inputs.LocalTree)}
	if _, err := m.replayProject(before); err != nil {
		return out, err
	}
	// A valid current index is also scope identity. A project can use the same
	// UUID in separate scope roots; historical recovery must not reassign a
	// root that has since been validly bound to a different owner/scope.
	if entry, ok := before.Entries["skills.yaml"]; ok && !entry.Dir {
		body, err := m.Store.Data(entry)
		if err != nil {
			return out, err
		}
		if parsed, err := config.Parse(body); err == nil {
			if fields, ok := parsed.(map[string]any); ok {
				if version, exists := fields["schema_version"]; exists && version != 1 {
					return out, result.Fail("REPLAY_SOURCE_SCHEMA_UNSUPPORTED", "当前技能索引使用未知版本，不能自动降级恢复", 4)
				}
				for key, expected := range map[string]string{"owner": m.Owner, "scope_id": m.Scope} {
					if value, ok := fields[key].(string); ok && value != "" && value != expected {
						return out, result.Fail("SOURCE_IDENTITY_MISMATCH", "当前技能索引已属于另一项目或作用域，拒绝恢复", 3)
					}
				}
			}
		}
	}
	project, err := m.replayProject(historical)
	if err != nil {
		return out, err
	}
	idx, shared, err := m.load(&historical)
	if err != nil {
		return out, err
	}
	inherit, skills, err := m.resolve(&historical, idx, shared)
	if err != nil {
		return out, err
	}
	// A snapshot may mention a disabled, never-adopted global asset for which
	// it saved no package. Do not drop that declaration or invent source bytes.
	known := map[string]bool{}
	for _, skill := range skills {
		known[skill.Skill.ID] = true
	}
	historical, err = m.restoreSnapshotReferences(historical, inherit, known, inputs, expected)
	if err != nil {
		return out, err
	}
	for _, v := range []map[string]any{inputs.Portable.Effective, inputs.Layers.Local} {
		section, _ := v["skills"].(map[string]any)
		for _, action := range []string{"enable", "disable"} {
			for _, raw := range array(section[action]) {
				if !known[raw.(string)] {
					return out, result.Fail("SNAPSHOT_DEPENDENCY_MATERIAL_MISSING", "历史配置引用未采纳的全局技能，归档没有其完整包；不能忽略该身份后回放", 4)
				}
			}
		}
		pins, _ := section["pins"].(map[string]any)
		for id := range pins {
			if !known[id] {
				return out, result.Fail("SNAPSHOT_DEPENDENCY_MATERIAL_MISSING", "历史 pin 缺少项目完整包，不能从当前全局库推测", 4)
			}
		}
	}
	// Histories are unioned, never reverted. Conflicting immutable identities
	// require repair; a matching snapshot must not silently overwrite drift.
	immutable := func(p string) bool {
		for _, prefix := range []string{".revisions", ".revision-records", ".governance-records", ".external-approvals"} {
			if p == prefix || strings.HasPrefix(p, prefix+"/") {
				return true
			}
		}
		return false
	}
	for p, entry := range historical.Entries {
		if !immutable(p) {
			continue
		}
		if current, ok := before.Entries[p]; ok && current != entry {
			return out, result.Fail("REPLAY_IMMUTABLE_CONFLICT", "当前不可变版本或来源批准记录与历史身份冲突，保留现场等待修复", 3)
		}
		out.SourceAfter.Entries[p] = entry
	}
	for _, name := range []string{"skills.yaml", "profile.yaml", "mcp.yaml", "plugins.lock", inheritanceFile, externalFile, lifecycleFile} {
		if e, ok := before.Entries[name]; ok && e.Dir {
			return out, result.Fail("REPLAY_SOURCE_STRUCTURE_CONFLICT", "规范配置文件位置已变为目录，不能自动接管", 3)
		}
		delete(out.SourceAfter.Entries, name)
		if e, ok := historical.Entries[name]; ok {
			out.SourceAfter.Entries[name] = e
		}
	}
	// Current unreferenced package folders are retained as dormant material.
	// Restore only the historical index's complete, explicitly owned packages.
	for _, skill := range idx.Skills {
		prefix := skill.Source.Path
		for p := range out.SourceAfter.Entries {
			if p == prefix || strings.HasPrefix(p, prefix+"/") {
				delete(out.SourceAfter.Entries, p)
			}
		}
		if err = m.install(&out.SourceAfter, prefix, subtree(historical, prefix), m); err != nil {
			return out, err
		}
		if root, ok := historical.Entries[prefix]; ok {
			if !root.Dir {
				return out, result.Fail("REPLAY_SOURCE_STRUCTURE_CONFLICT", "历史技能包根不是目录", 3)
			}
			out.SourceAfter.Entries[prefix] = root
		}
	}
	files, _, err := m.ruleLibrary(historical)
	if err != nil {
		return out, err
	}
	for _, f := range files {
		name := ruleFilePath(f.ID)
		if current, ok := before.Entries[name]; ok && current.Dir {
			return out, result.Fail("REPLAY_SOURCE_STRUCTURE_CONFLICT", "历史规则文件位置已变为目录", 3)
		}
		out.SourceAfter.Entries[name] = historical.Entries[name]
	}
	delete(out.SourceAfter.Entries, ruleLibraryFile)
	if e, ok := historical.Entries[ruleLibraryFile]; ok {
		out.SourceAfter.Entries[ruleLibraryFile] = e
	}
	// Keep historical rule-file identities editable and remove their known
	// suffix from the expanded inline rules, so they are compiled exactly once.
	portable := cloneProfile(inputs.Portable.Effective)
	texts := profileTexts(portable)
	fileTexts := []string{}
	for _, file := range files {
		if file.Enabled {
			fileTexts = append(fileTexts, file.Body)
		}
	}
	if len(texts) < len(fileTexts) {
		return out, result.Fail("SNAPSHOT_CONFIGURATION_INVALID", "历史有序规则库不能与展开规则对应", 5)
	}
	for i, text := range fileTexts {
		if texts[len(texts)-len(fileTexts)+i] != text {
			return out, result.Fail("SNAPSHOT_CONFIGURATION_INVALID", "历史规则顺序不能安全投影", 5)
		}
	}
	inline := []any{}
	for _, text := range texts[:len(texts)-len(fileTexts)] {
		inline = append(inline, text)
	}
	portable["rules"], portable["rules_override"] = inline, true
	delete(portable, "extends")
	delete(project, "profile")
	for p, value := range map[string]any{m.identityFile(): project, "profile.yaml": portable} {
		mode := historical.Entries[p].Mode
		if err = m.Store.PutFile(&out.SourceAfter, p, jsonBytes(value), mode); err != nil {
			return out, err
		}
	}
	// Rebase only the machine association; the original Local profile bytes
	// stay separate and unchanged. A new replay never follows the live global
	// defaults behind the old binding or restores old transaction authority.
	if out.LocalAfter.Exists {
		if err = m.Store.PutFile(&out.LocalAfter, "binding.json", m.localProfileBinding(""), inputs.LocalTree.Entries["binding.json"].Mode); err != nil {
			return out, err
		}
	}
	out.Layers = ProfileLayers{Profiles: []map[string]any{}, Local: inputs.Layers.Local, LocalAvailable: true, LocalRoot: inputs.Layers.LocalRoot, LocalSHA256: workstore.Digest(out.LocalAfter), History: []HistoryItem{}}
	out.Layers.Digest = workstore.Hash(jsonBytes(map[string]string{out.Layers.LocalRoot: out.Layers.LocalSHA256}))
	for p, e := range out.SourceAfter.Entries {
		if e.Dir {
			continue
		}
		for d := path.Dir(p); d != "."; d = path.Dir(d) {
			if existing, ok := out.SourceAfter.Entries[d]; ok && !existing.Dir {
				return out, result.Fail("REPLAY_SOURCE_STRUCTURE_CONFLICT", "规范源父目录被文件占用，保留现场", 3)
			}
			if _, ok := out.SourceAfter.Entries[d]; !ok {
				out.SourceAfter.Entries[d] = workstore.Directory()
			}
		}
	}
	projectedIndex, projectedShared, err := m.load(&out.SourceAfter)
	if err != nil {
		return out, err
	}
	_, projectedSkills, err := m.resolve(&out.SourceAfter, projectedIndex, projectedShared)
	if err != nil {
		return out, err
	}
	out.Report, err = m.resolveProfile(out.SourceAfter, projectedShared, projectedSkills, out.Layers)
	if err != nil {
		return out, err
	}
	if workstore.Hash(jsonBytes(out.Report.Effective)) != workstore.Hash(jsonBytes(expected.ProfileReport.Effective)) {
		return out, result.Fail("REPLAY_EFFECTIVE_MISMATCH", "投影后的有效配置与历史观察不同，未写入源", 5)
	}
	out.Preflight, err = m.preflight(out.SourceAfter, projectedIndex, projectedShared, out.Layers)
	out.Preflight.Candidate = true
	if err != nil {
		return out, err
	}
	// Bind exact old/new roots and effective semantics. Trees remain private;
	// a future review response may expose this digest and safe change metadata.
	data, _ := json.Marshal([]string{m.Owner, m.Scope, m.Copy, m.Store.Root, workstore.Digest(before), workstore.Digest(out.SourceAfter), workstore.Digest(localBefore), workstore.Digest(out.LocalAfter), out.Layers.Digest, out.Report.Digest})
	out.Digest = workstore.Hash(data)
	return out, nil
}
