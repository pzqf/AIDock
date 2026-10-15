package source

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

const lifecycleFile = "skill-lifecycle.yaml"

// Retain field presence so even empty/null unrelated fields cannot be silently
// ignored by an action. CLI and HTTP decoders use this same strict boundary.
func (req *Request) UnmarshalJSON(data []byte) error {
	type plain Request
	var decoded plain
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e := d.Decode(&decoded); e != nil {
		return e
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(data, &fields); e != nil {
		return e
	}
	*req = Request(decoded)
	for key := range fields {
		req.provided = append(req.provided, key)
	}
	return nil
}

func validateRequest(req Request) error {
	fields := map[string][]string{
		"create": {"id", "name", "description", "document"}, "import": {"id", "package_path", "source_role"},
		"edit": {"id", "files"}, "enable": {"id"}, "disable": {"id"}, "migrate": {}, "rollback": {"snapshot"},
		"rename": {"id", "name"}, "metadata": {"id", "display_name", "tags"}, "retire": {"id", "reason"}, "reactivate": {"id"},
		"governance":      {"id", "revision", "governance"},
		"external-adopt":  {"bridge_id", "package_path", "revision", "bridge_revision"},
		"external-select": {"id", "approval"},
		"inherit":         {"id", "revision"}, "override": {"id", "revision"}, "restore-inheritance": {"id", "revision"}, "fork": {"id", "new_id", "name", "revision"},
	}
	keys, ok := fields[req.Action]
	if !ok {
		return result.Fail("FEATURE_NOT_IMPLEMENTED", "该源动作尚未开放", 4)
	}
	allowed := map[string]bool{"action": true}
	for _, key := range keys {
		allowed[key] = true
	}
	var values map[string]any
	_ = json.Unmarshal(jsonBytes(req), &values)
	for _, key := range req.provided {
		values[key] = true
	}
	for key := range values {
		if !allowed[key] {
			return result.Fail("INVALID_INPUT", "动作不接受字段: "+key, 2)
		}
	}
	return nil
}

type Retirement struct {
	ID         string `json:"id"`
	NativeName string `json:"native_name"`
	Revision   string `json:"revision"`
	RetiredAt  string `json:"retired_at"`
	Reason     string `json:"reason"`
}
type Lifecycle struct {
	Version     int          `json:"schema_version"`
	Owner       string       `json:"owner"`
	Scope       string       `json:"scope_id"`
	Retirements []Retirement `json:"retirements"`
}
type Reference struct {
	Kind     string `json:"kind"`
	FromID   string `json:"from_id,omitempty"`
	FromName string `json:"from_name,omitempty"`
	Path     string `json:"path"`
	Revision string `json:"revision,omitempty"`
	Blocking bool   `json:"blocks_retirement"`
}
type Impact struct {
	ID               string      `json:"id"`
	Owner            string      `json:"owner"`
	Scope            string      `json:"scope_id"`
	References       []Reference `json:"references"`
	ReferenceScope   string      `json:"reference_scope"`
	Unknown          []string    `json:"unknown"`
	NativeBefore     string      `json:"native_before,omitempty"`
	NativeAfter      string      `json:"native_after,omitempty"`
	NativeCleanup    string      `json:"native_cleanup,omitempty"`
	PackagePreserved bool        `json:"package_preserved"`
}

func (m *Manager) loadLifecycle(tree *workstore.Tree, idx Index) (Lifecycle, error) {
	life := Lifecycle{Version: 1, Owner: m.Owner, Scope: m.Scope, Retirements: []Retirement{}}
	var b []byte
	var e error
	if tree == nil {
		b, e = workstore.ReadFile(filepath.Join(m.Store.Root, lifecycleFile))
	} else if entry, ok := tree.Entries[lifecycleFile]; ok {
		b, e = m.Store.Data(entry)
	} else {
		return life, nil
	}
	if os.IsNotExist(e) {
		return life, nil
	}
	if e != nil {
		return life, e
	}
	value, e := config.Validate("skill-lifecycle", b)
	if e != nil {
		return life, result.Fail("SOURCE_METADATA_INVALID", e.Error(), 5)
	}
	if e = json.Unmarshal(jsonBytes(value), &life); e != nil {
		return life, e
	}
	if life.Owner != m.Owner || life.Scope != m.Scope {
		return life, result.Fail("SOURCE_IDENTITY_MISMATCH", "退役记录 owner/scope 不匹配", 3)
	}
	seen := map[string]bool{}
	byID := map[string]Skill{}
	for _, s := range idx.Skills {
		byID[s.ID] = s
	}
	for _, r := range life.Retirements {
		s, ok := byID[r.ID]
		if seen[r.ID] || !ok || s.Name != r.NativeName || s.Revision != r.Revision {
			return life, result.Fail("SOURCE_METADATA_INVALID", "退役身份/固定版本不一致或重复", 5)
		}
		if _, e = time.Parse(time.RFC3339, r.RetiredAt); e != nil {
			return life, result.Fail("SOURCE_METADATA_INVALID", "无效退役时间", 5)
		}
		seen[r.ID] = true
	}
	return life, nil
}
func retired(l Lifecycle, id string) bool {
	for _, r := range l.Retirements {
		if r.ID == id {
			return true
		}
	}
	return false
}
func validateRetiredProfile(l Lifecycle, profile map[string]any) error {
	for _, r := range l.Retirements {
		enabled := false
		disabled := false
		for _, id := range switches(profile)["enable"].([]any) {
			if id == r.ID {
				enabled = true
			}
		}
		for _, id := range switches(profile)["disable"].([]any) {
			if id == r.ID {
				disabled = true
			}
		}
		if enabled || !disabled {
			return result.Fail("SKILL_RETIRED", "退役技能必须保持本层禁用", 3)
		}
	}
	return nil
}
func (m *Manager) impact(idx Index, profile map[string]any, id string) *Impact {
	out := &Impact{ID: id, Owner: m.Owner, Scope: m.Scope, References: []Reference{}, ReferenceScope: "CURRENT_BOUND_SOURCE", Unknown: []string{"其他项目、全局层、离线副本的引用尚未登记", "正文中的原生名称调用和脚本动态引用未验证"}, PackagePreserved: true}
	for _, s := range idx.Skills {
		if s.ID == id {
			out.NativeBefore = s.Name
			out.NativeAfter = s.Name
		}
	}
	skills, _ := profile["skills"].(map[string]any)
	for _, key := range []string{"enable", "disable"} {
		list, _ := skills[key].([]any)
		for _, v := range list {
			if v == id {
				out.References = append(out.References, Reference{Kind: "profile", Path: "profile.yaml/skills/" + key})
			}
		}
	}
	pins, _ := skills["pins"].(map[string]any)
	if revision, ok := pins[id].(string); ok {
		out.References = append(out.References, Reference{Kind: "profile-pin", Path: "profile.yaml/skills/pins/" + id, Revision: revision})
	}
	for _, s := range idx.Skills {
		refs, _ := s.Dependencies["skills"].([]any)
		for i, v := range refs {
			ref, _ := v.(map[string]any)
			if ref["owner"] == m.Owner && ref["scope_id"] == m.Scope && ref["id"] == id {
				revision, _ := ref["revision"].(string)
				out.References = append(out.References, Reference{Kind: "dependency", FromID: s.ID, FromName: s.Name, Path: referencePath(s.ID, "dependencies.skills", i), Revision: revision, Blocking: true})
			}
		}
		for _, kind := range []string{"base_source", "upstream"} {
			ref := s.BaseSource
			revision := s.BaseRevision
			if kind == "upstream" {
				ref = s.Upstream
				revision = ref["revision"]
			}
			if ref["owner"] == m.Owner && ref["scope_id"] == m.Scope && ref["id"] == id {
				out.References = append(out.References, Reference{Kind: kind, FromID: s.ID, FromName: s.Name, Path: "skills.yaml/" + s.ID + "/" + kind, Revision: revision, Blocking: true})
			}
		}
	}
	sort.Slice(out.References, func(i, j int) bool { return out.References[i].Path < out.References[j].Path })
	return out
}
func referencePath(id, kind string, index int) string {
	b, _ := json.Marshal(index)
	return "skills.yaml/" + id + "/" + kind + "/" + string(b)
}
func (m *Manager) References(id string) (*Impact, error) {
	var out *Impact
	e := m.Store.Locked(func() error {
		if e := m.Store.Pending(); e != nil {
			return e
		}
		idx, p, e := m.load(nil)
		if e != nil {
			return e
		}
		life, e := m.loadLifecycle(nil, idx)
		if e != nil {
			return e
		}
		if e = validateRetiredProfile(life, p); e != nil {
			return e
		}
		found := false
		_, effective, e := m.resolve(nil, idx, p)
		if e != nil {
			return e
		}
		targetOwner, targetScope := m.Owner, m.Scope
		all := idx
		all.Skills = []Skill{}
		for _, item := range effective {
			all.Skills = append(all.Skills, item.Skill)
			if item.Skill.ID == id {
				found = true
				targetOwner, targetScope = item.Owner, item.Scope
			}
		}
		if !found {
			return result.Fail("SKILL_NOT_FOUND", "技能 id 不存在", 2)
		}
		binding := &Manager{Store: m.Store, Owner: targetOwner, Scope: targetScope}
		out = binding.impact(all, p, id)
		return nil
	})
	return out, e
}

func (m *Manager) lifecycleAction(req Request, idx *Index, profile map[string]any, life *Lifecycle, before workstore.Tree, after *workstore.Tree) error {
	at := -1
	for i, s := range idx.Skills {
		if s.ID == req.ID {
			at = i
		}
	}
	if at < 0 {
		return result.Fail("SKILL_NOT_FOUND", "技能 id 不存在", 2)
	}
	s := &idx.Skills[at]
	if s.Source.Mode != "owned" || s.Source.Path != "skills/"+s.ID {
		return result.Fail("OWNERSHIP_REQUIRED", "仅管理 owned 包", 4)
	}
	switch req.Action {
	case "metadata":
		if req.DisplayName == nil && req.Tags == nil {
			return result.Fail("INVALID_INPUT", "需要展示名或标签", 2)
		}
		if req.DisplayName != nil {
			if !utf8.ValidString(*req.DisplayName) || utf8.RuneCountInString(*req.DisplayName) > 80 {
				return result.Fail("INVALID_INPUT", "展示名最长 80 字", 2)
			}
			s.DisplayName = strings.TrimSpace(*req.DisplayName)
		}
		if req.Tags != nil {
			s.Tags = *req.Tags
		}
	case "retire":
		if strings.TrimSpace(req.Reason) == "" || utf8.RuneCountInString(req.Reason) > 500 {
			return result.Fail("INVALID_INPUT", "退役需要原因，最长 500 字", 2)
		}
		for _, ref := range m.impact(*idx, profile, req.ID).References {
			if ref.Blocking {
				return &result.Error{Code: "SKILL_REFERENCED", Message: "已声明的引用尚未解除，退役被阻断: " + ref.Path, Exit: 3, Recovery: m.impact(*idx, profile, req.ID)}
			}
		}
		pkg := workstore.Tree{Exists: true, Entries: map[string]workstore.Entry{}}
		for p, v := range before.Entries {
			if strings.HasPrefix(p, s.Source.Path+"/") {
				pkg.Entries[strings.TrimPrefix(p, s.Source.Path+"/")] = v
			}
		}
		if packageRevision(*s, pkg) != s.Revision {
			return result.Fail("SOURCE_PACKAGE_DRIFT", "包与固定版本不一致；先保存源编辑，再退役", 3)
		}
		life.Retirements = append(life.Retirements, Retirement{ID: s.ID, NativeName: s.Name, Revision: s.Revision, RetiredAt: time.Now().UTC().Format(time.RFC3339Nano), Reason: strings.TrimSpace(req.Reason)})
		setEnabled(profile, s.ID, false)
	case "reactivate":
		if !retired(*life, s.ID) {
			return result.Fail("INVALID_INPUT", "技能尚未退役", 2)
		}
		records := []Retirement{}
		for _, r := range life.Retirements {
			if r.ID != s.ID {
				records = append(records, r)
			}
		}
		life.Retirements = records
		// Reactivation restores management only; enabling is a separate decision.
		setEnabled(profile, s.ID, false)
	}
	if req.Action != "metadata" {
		sort.Slice(life.Retirements, func(i, j int) bool { return life.Retirements[i].ID < life.Retirements[j].ID })
		if _, e := config.Validate("skill-lifecycle", jsonBytes(life)); e != nil {
			return result.Fail("SOURCE_METADATA_INVALID", e.Error(), 5)
		}
		return m.Store.PutFile(after, lifecycleFile, jsonBytes(life), 0644)
	}
	return nil
}
func (m *Manager) lifecycleEffects(before, after Lifecycle) []workstore.AssetEffect {
	records := map[string]Retirement{}
	for _, r := range before.Retirements {
		records[r.ID] = r
	}
	for _, r := range after.Retirements {
		records[r.ID] = r
	}
	var effects []workstore.AssetEffect
	for id, r := range records {
		was, now := retired(before, id), retired(after, id)
		if was == now {
			continue
		}
		from, to := "active", "retired"
		if was {
			from, to = to, from
		}
		effects = append(effects, workstore.AssetEffect{Kind: "skill-retirement", Owner: m.Owner, Scope: m.Scope, ID: id, NativeName: r.NativeName, Revision: r.Revision, Before: from, After: to})
	}
	sort.Slice(effects, func(i, j int) bool { return effects[i].ID < effects[j].ID })
	return effects
}
func (m *Manager) preview(action string, before, after workstore.Tree) (workstore.Plan, error) {
	beforeIdx, _, e := m.load(&before)
	if e != nil {
		return workstore.Plan{}, e
	}
	afterIdx, profile, e := m.load(&after)
	if e != nil {
		return workstore.Plan{}, e
	}
	previous, e := m.loadLifecycle(&before, beforeIdx)
	if e != nil {
		return workstore.Plan{}, e
	}
	next, e := m.loadLifecycle(&after, afterIdx)
	if e != nil {
		return workstore.Plan{}, e
	}
	if e = validateRetiredProfile(next, profile); e != nil {
		return workstore.Plan{}, e
	}
	if _, _, e = m.resolve(&after, afterIdx, profile); e != nil {
		return workstore.Plan{}, e
	}
	if _, e = m.sourceRules(after, profile); e != nil {
		return workstore.Plan{}, e
	}
	layers, e := m.profileLayers()
	if e != nil {
		return workstore.Plan{}, e
	}
	inputs := map[string]string{}
	for k, v := range m.planInputs {
		inputs[k] = v
	}
	if layers.Digest != "" {
		inputs["profile_layers"] = layers.Digest
	}
	return m.Store.PreviewBound(action, before, after, inputs, m.lifecycleEffects(previous, next)...)
}
