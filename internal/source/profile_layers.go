package source

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/profile"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// ProfileLayers describes explicitly bound inputs only. Machine overrides live
// outside the shared source, keyed by project/scope/copy, and cannot travel in a
// package export or replica shared catalog.
type ProfileLayers struct {
	Defaults       map[string]any   `json:"defaults,omitempty"`
	Profiles       []map[string]any `json:"profiles"`
	Local          map[string]any   `json:"local,omitempty"`
	Digest         string           `json:"digest"`
	LibraryRoot    string           `json:"library_root,omitempty"`
	LocalRoot      string           `json:"local_root,omitempty"`
	LocalSHA256    string           `json:"local_sha256,omitempty"`
	LocalAvailable bool             `json:"local_available"`
	History        []HistoryItem    `json:"history"`
	Recovery       bool             `json:"recovery_required"`
}

func (m *Manager) localProfileStore() (*workstore.Store, error) {
	if m.Owner == "global" || m.Copy == "" {
		return nil, result.Fail("PROFILE_COPY_REQUIRED", "本机覆盖需要明确的项目、副本和作用域绑定", 4)
	}
	key := workstore.Hash(jsonBytes([]string{m.Owner, m.Scope, m.Copy}))
	return &workstore.Store{Root: filepath.Join(m.Store.Local, "profile-overrides", key), Local: filepath.Join(m.Store.Local, "profile-override-transactions"), PlanKind: "profile-local"}, nil
}

type profileBinding struct {
	Version     int    `json:"schema_version"`
	Project     string `json:"project"`
	Scope       string `json:"scope"`
	Copy        string `json:"copy"`
	SourceRoot  string `json:"source_root"`
	LibraryRoot string `json:"library_root,omitempty"`
}

func (m *Manager) localProfileBinding(library string) []byte {
	return jsonBytes(profileBinding{1, m.Owner, m.Scope, m.Copy, m.Store.Root, library})
}
func (m *Manager) readProfileBinding(s *workstore.Store, t workstore.Tree) (profileBinding, error) {
	var binding profileBinding
	entry, ok := t.Entries["binding.json"]
	if !ok || entry.Dir {
		return binding, result.Fail("PROFILE_LOCAL_INVALID", "本机覆盖缺少绑定记录", 5)
	}
	b, err := s.Data(entry)
	if err != nil {
		return binding, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&binding); err != nil {
		return binding, result.Fail("PROFILE_LOCAL_INVALID", err.Error(), 5)
	}
	var tail any
	if err = d.Decode(&tail); err != io.EOF {
		return binding, result.Fail("PROFILE_LOCAL_INVALID", "绑定记录需要单个对象", 5)
	}
	if binding.Version != 1 || binding.Project != m.Owner || binding.Scope != m.Scope || binding.Copy != m.Copy || binding.SourceRoot != m.Store.Root {
		return binding, result.Fail("PROFILE_LOCAL_BINDING_MISMATCH", "本机覆盖属于另一个项目、作用域、副本或目录；不能自动重绑", 3)
	}
	if binding.LibraryRoot != "" && (!filepath.IsAbs(binding.LibraryRoot) || filepath.Clean(binding.LibraryRoot) != binding.LibraryRoot) {
		return binding, result.Fail("PROFILE_LOCAL_INVALID", "全局库关联须为明确的规范绝对路径", 5)
	}
	return binding, nil
}

func (m *Manager) readLocalProfile(s *workstore.Store, t workstore.Tree) (map[string]any, error) {
	if len(t.Entries) == 0 && !t.Exists {
		return nil, nil
	}
	if len(t.Entries) > 2 || len(t.Entries) < 1 {
		return nil, result.Fail("PROFILE_LOCAL_INVALID", "本机覆盖目录只接受绑定和 profile.yaml；保留未知文件等待修复", 5)
	}
	if _, err := m.readProfileBinding(s, t); err != nil {
		return nil, err
	}
	entry, ok := t.Entries["profile.yaml"]
	if !ok && len(t.Entries) == 1 {
		return nil, nil
	}
	if !ok || entry.Dir {
		return nil, result.Fail("PROFILE_LOCAL_INVALID", "本机覆盖缺少 profile.yaml", 5)
	}
	b, err := s.Data(entry)
	if err != nil {
		return nil, err
	}
	v, err := config.Validate("profile", b)
	if err != nil {
		return nil, result.Fail("PROFILE_LOCAL_INVALID", err.Error(), 5)
	}
	local := v.(map[string]any)
	if parent, _ := local["extends"].(string); parent != "" {
		return nil, result.Fail("PROFILE_LAYER_INVALID", "本机覆盖不能引入新的 extends 链", 5)
	}
	return local, nil
}

// RestoreProfileLibrary is called only after the explicit project/copy binding
// is known. It never searches neighboring projects or a user's home directory.
func (m *Manager) RestoreProfileLibrary() error {
	if m.Owner == "global" || m.Copy == "" {
		return nil
	}
	s, err := m.localProfileStore()
	if err != nil {
		return err
	}
	// A half-written binding must not make the recovery command unreachable.
	// Consumers still reject this state through profileLayers until recovery.
	if err = s.Pending(); err != nil {
		return nil
	}
	t, err := s.Capture(s.Root)
	if err != nil {
		return err
	}
	if len(t.Entries) == 0 && !t.Exists {
		return nil
	}
	binding, err := m.readProfileBinding(s, t)
	if err != nil {
		return err
	}
	if binding.LibraryRoot != "" {
		m.ProfileLibrary = true
		return m.AttachLibrary(binding.LibraryRoot)
	}
	return nil
}

func (m *Manager) namedProfiles(t workstore.Tree) ([]map[string]any, error) {
	out := []map[string]any{}
	paths := []string{}
	for p, e := range t.Entries {
		if strings.HasPrefix(p, "profiles/") && !e.Dir {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	names := map[string]bool{}
	for _, p := range paths {
		if path.Dir(p) != "profiles" || !strings.HasSuffix(p, ".yaml") {
			return nil, result.Fail("PROFILE_LIBRARY_INVALID", "Profile 目录包含未支持文件："+p, 5)
		}
		b, err := m.Store.Data(t.Entries[p])
		if err != nil {
			return nil, err
		}
		v, err := config.Validate("profile", b)
		if err != nil {
			return nil, result.Fail("PROFILE_LIBRARY_INVALID", p+"："+err.Error(), 5)
		}
		item := v.(map[string]any)
		name := item["name"].(string)
		if names[name] {
			return nil, result.Fail("PROFILE_NAME_AMBIGUOUS", "重复 Profile 名称："+name, 5)
		}
		names[name] = true
		out = append(out, item)
	}
	return out, nil
}

func (m *Manager) draftNamedProfiles(after *workstore.Tree, items []map[string]any) error {
	if m.Owner != "global" {
		return result.Fail("PROFILE_LIBRARY_READ_ONLY", "命名 Profile 只能在全局库中编辑；项目只引用", 4)
	}
	// Validate existing content before replacing it, so unknown files are never
	// silently claimed or removed by a collection edit.
	if _, err := m.namedProfiles(*after); err != nil {
		return err
	}
	for p := range after.Entries {
		if p == "profiles" || strings.HasPrefix(p, "profiles/") {
			delete(after.Entries, p)
		}
	}
	seen := map[string]bool{}
	for _, raw := range items {
		value, err := config.Validate("profile", jsonBytes(raw))
		if err != nil {
			return result.Fail("PROFILE_INVALID", err.Error(), 5)
		}
		v := value.(map[string]any)
		name := v["name"].(string)
		p := "profiles/" + name + ".yaml"
		if strings.TrimSpace(name) != name || strings.ContainsAny(name, "/\\") || workstore.SafeRelative(p) != nil || seen[strings.ToLower(name)] {
			return result.Fail("PROFILE_NAME_AMBIGUOUS", "Profile 名称重复或不能用作安全文件名："+name, 5)
		}
		seen[strings.ToLower(name)] = true
		if err = m.Store.PutFile(after, p, jsonBytes(v), 0644); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) profileLayers() (ProfileLayers, error) {
	out := ProfileLayers{Profiles: []map[string]any{}, History: []HistoryItem{}}
	observations := map[string]string{}
	if m.Library != nil && m.ProfileLibrary {
		lib := m.Library
		if err := lib.Store.Pending(); err != nil {
			return out, err
		}
		t, err := lib.Store.Capture(lib.Store.Root)
		if err != nil {
			return out, err
		}
		_, defaults, err := lib.load(&t)
		if err != nil {
			return out, err
		}
		if _, ok := t.Entries[lib.identityFile()]; ok {
			rules, err := lib.sourceRules(t, defaults)
			if err != nil {
				return out, err
			}
			out.Defaults = profileWithRules(defaults, rules)
		}
		out.Profiles, err = lib.namedProfiles(t)
		if err != nil {
			return out, err
		}
		out.LibraryRoot = lib.Store.Root
		observations[lib.Store.Root] = workstore.Digest(t)
	}
	if m.Owner != "global" && m.Copy != "" {
		s, err := m.localProfileStore()
		if err != nil {
			return out, err
		}
		out.LocalAvailable, out.LocalRoot = true, s.Root
		if err = s.Pending(); err != nil {
			out.Recovery = true
			return out, err
		}
		t, err := s.Capture(s.Root)
		if err != nil {
			return out, err
		}
		out.Local, err = m.readLocalProfile(s, t)
		if err != nil {
			return out, err
		}
		out.LocalSHA256 = workstore.Digest(t)
		observations[s.Root] = out.LocalSHA256
		js, err := s.Journals()
		if err != nil {
			return out, err
		}
		for _, j := range js {
			out.History = append(out.History, HistoryItem{ID: j.Plan.ID, Action: j.Plan.Action, State: j.State, Updated: j.Updated, Restorable: j.State == "committed"})
		}
	}
	if len(observations) > 0 {
		out.Digest = workstore.Hash(jsonBytes(observations))
	}
	return out, nil
}

func (m *Manager) resolveProfile(t workstore.Tree, v map[string]any, skills []EffectiveSkill, layers ProfileLayers) (profile.Report, error) {
	rules, err := m.sourceRules(t, v)
	if err != nil {
		return profile.Report{}, err
	}
	selected := profileWithRules(v, rules)
	if m.Owner != "global" {
		b, err := m.treeRead(&t, m.identityFile())
		if err != nil && !os.IsNotExist(err) {
			return profile.Report{}, err
		}
		if err == nil {
			raw, err := config.Validate("project", b)
			if err != nil {
				return profile.Report{}, err
			}
			name, _ := raw.(map[string]any)["profile"].(string)
			parent, _ := selected["extends"].(string)
			if name != "" {
				if parent != "" && parent != name {
					return profile.Report{}, result.Fail("AMBIGUOUS_OVERRIDE", "project.profile 与 Profile.extends 指向不同名称", 5)
				}
				selected["extends"] = name
			}
		}
	}
	catalog := profile.Catalog{Skills: map[string][]string{}}
	for _, item := range skills {
		catalog.Skills[item.Skill.ID] = []string{item.Skill.Revision}
	}
	if m.Owner == "global" {
		layers.Profiles, err = m.namedProfiles(t)
		if err != nil {
			return profile.Report{}, err
		}
		for _, item := range layers.Profiles {
			if _, err = profile.Resolve(profile.Input{Profiles: layers.Profiles, Project: item, Catalog: catalog}); err != nil {
				return profile.Report{}, err
			}
		}
	}
	return profile.Resolve(profile.Input{Defaults: layers.Defaults, Profiles: layers.Profiles, Project: selected, Local: layers.Local, Catalog: catalog})
}

// ConfigurationDigest is part of native plan/check identity. A local override
// or library edit must expire evidence even when shared source bytes did not move.
func (m *Manager) ConfigurationDigest() (string, error) {
	layers, err := m.profileLayers()
	return layers.Digest, err
}

func (m *Manager) lockProfileLayers(fn func() error) error {
	local := func() error {
		if m.Owner == "global" || m.Copy == "" {
			return fn()
		}
		s, err := m.localProfileStore()
		if err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Dir(s.Root)); os.IsNotExist(err) {
			return fn()
		} else if err != nil {
			return err
		}
		return s.Locked(fn)
	}
	if m.Library != nil {
		return m.Library.Store.Locked(local)
	}
	return local()
}

func profileTexts(v map[string]any) []string {
	out := []string{}
	for _, value := range array(v["rules"]) {
		out = append(out, value.(string))
	}
	return out
}
