package source

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/bridge"
	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

const inheritanceFile = "skill-inheritance.yaml"

var revisionPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type InheritedSkill struct {
	Owner    string  `json:"owner"`
	Scope    string  `json:"scope_id"`
	Skill    Skill   `json:"skill"`
	Versions []Skill `json:"available_versions,omitempty"`
}
type Inheritance struct {
	Version int              `json:"schema_version"`
	Owner   string           `json:"owner"`
	Scope   string           `json:"scope_id"`
	Skills  []InheritedSkill `json:"skills"`
}
type EffectiveSkill struct {
	Skill    Skill   `json:"skill"`
	Owner    string  `json:"owner"`
	Scope    string  `json:"scope_id"`
	Layer    string  `json:"layer"`
	Status   string  `json:"status"`
	ReadOnly bool    `json:"read_only"`
	Versions []Skill `json:"available_versions,omitempty"`
}
type Candidate struct {
	Skill   Skill  `json:"skill"`
	Owner   string `json:"owner"`
	Scope   string `json:"scope_id"`
	Current bool   `json:"current"`
	Retired bool   `json:"retired"`
}

// semantic-v1 uses UTF-8 compact JSON with sorted map keys (encoding/json),
// explicit empty objects, and path-sorted manifests. Presentation and machine
// paths are excluded. The legacy absence of revision_format means package-v1.
func packageRevision(s Skill, pkg workstore.Tree) string {
	if s.RevisionFormat == "external-package-v1" {
		return bridge.PackageRevision(pkg)
	}
	if s.RevisionFormat != "semantic-v1" {
		return "sha256:" + workstore.Digest(pkg)
	}
	if s.Dependencies == nil {
		s.Dependencies = map[string]any{}
	}
	if s.Compatibility == nil {
		s.Compatibility = map[string]any{}
	}
	if s.Requirements == nil {
		s.Requirements = map[string]any{}
	}
	obj := map[string]any{"format": "semantic-v1", "package_digest": portablePackageDigest(pkg),
		"native_name": s.Name, "package_format": s.PackageFormat, "files": s.Files,
		"dependencies": s.Dependencies, "compatibility": s.Compatibility, "requirements": s.Requirements}
	for _, k := range []string{"dependencies", "compatibility", "requirements"} {
		if obj[k] == nil {
			obj[k] = map[string]any{}
		}
	}
	b, _ := json.Marshal(obj)
	return "sha256:" + workstore.Hash(b)
}

// Content closure uses portable fields only. Native mode bits stay in complete
// transaction trees for same-platform restoration; they are not cross-platform
// permission claims and are deliberately excluded from semantic-v1 identity.
func portablePackageDigest(pkg workstore.Tree) string {
	type entry struct {
		Path  string `json:"path"`
		Dir   bool   `json:"directory,omitempty"`
		Hash  string `json:"sha256,omitempty"`
		Bytes int64  `json:"bytes"`
	}
	entries := []entry{}
	for p, e := range pkg.Entries {
		entries = append(entries, entry{Path: p, Dir: e.Dir, Hash: e.Hash, Bytes: e.Size})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	b, _ := json.Marshal(entries)
	return "sha256:" + workstore.Hash(b)
}
func revisionPath(s Skill) string {
	return ".revisions/" + s.ID + "/" + strings.TrimPrefix(s.Revision, "sha256:")
}
func recordPath(s Skill) string {
	return ".revision-records/" + s.ID + "/" + strings.TrimPrefix(s.Revision, "sha256:") + ".json"
}
func governed(s Skill) Skill { s.DisplayName = ""; s.Tags = nil; s.Version = ""; return s }
func subtree(t workstore.Tree, prefix string) workstore.Tree {
	out := workstore.Tree{Exists: true, Entries: map[string]workstore.Entry{}}
	for p, e := range t.Entries {
		if strings.HasPrefix(p, prefix+"/") {
			out.Entries[strings.TrimPrefix(p, prefix+"/")] = e
		}
	}
	return out
}
func (m *Manager) treeRead(t *workstore.Tree, name string) ([]byte, error) {
	if t == nil {
		return workstore.ReadFile(filepath.Join(m.Store.Root, filepath.FromSlash(name)))
	}
	e, ok := t.Entries[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return m.Store.Data(e)
}
func (m *Manager) cached(t *workstore.Tree, s Skill) (workstore.Tree, error) {
	if !uuid.MatchString(s.ID) || !revisionPattern.MatchString(s.Revision) {
		return workstore.Tree{}, result.Fail("SOURCE_METADATA_INVALID", "无效继承身份或修订", 5)
	}
	var pkg workstore.Tree
	var e error
	if t == nil {
		pkg, e = m.Store.Capture(filepath.Join(m.Store.Root, filepath.FromSlash(revisionPath(s))))
	} else {
		pkg = subtree(*t, revisionPath(s))
	}
	if e != nil {
		return pkg, e
	}
	if e = m.checkPackage(s, pkg); e != nil {
		return pkg, e
	}
	return pkg, nil
}
func (m *Manager) checkPackage(s Skill, pkg workstore.Tree) error {
	if packageRevision(s, pkg) != s.Revision || ((s.RevisionFormat == "semantic-v1" || s.RevisionFormat == "external-package-v1") && s.PackageDigest != portablePackageDigest(pkg)) {
		return result.Fail("REVISION_CORRUPT", fmt.Sprintf("固定版本完整包/语义摘要不匹配: %s (期望 %s, 实际 %s, 包 %s/%s)", s.ID, s.Revision, packageRevision(s, pkg), s.PackageDigest, "sha256:"+workstore.Digest(pkg)), 5)
	}
	files := packageFiles(pkg, "")
	if len(files) != len(s.Files) {
		return result.Fail("REVISION_CORRUPT", "固定版本文件清单不完整", 5)
	}
	for i, f := range files {
		v := s.Files[i]
		if f.Path != v.Path || f.Bytes != v.Bytes || f.SHA256 != v.SHA256 {
			return result.Fail("REVISION_CORRUPT", "固定版本文件清单不匹配", 5)
		}
	}
	doc, ok := pkg.Entries["SKILL.md"]
	if !ok || doc.Dir {
		return result.Fail("REVISION_CORRUPT", "固定版本缺少 SKILL.md", 5)
	}
	b, e := m.Store.Data(doc)
	if e != nil {
		return e
	}
	name, e := documentName(b)
	if e != nil {
		return e
	}
	if name != s.Name {
		return result.Fail("REVISION_CORRUPT", "固定版本原生名不匹配", 5)
	}
	return nil
}
func (m *Manager) resolve(t *workstore.Tree, idx Index, profile map[string]any) (Inheritance, []EffectiveSkill, error) {
	inherit := Inheritance{Version: 1, Owner: m.Owner, Scope: m.Scope, Skills: []InheritedSkill{}}
	b, e := m.treeRead(t, inheritanceFile)
	if e == nil {
		if m.Owner == "global" {
			return inherit, nil, result.Fail("SOURCE_METADATA_INVALID", "全局库不能继承自身", 5)
		}
		v, err := config.Validate("skill-inheritance", b)
		if err != nil {
			return inherit, nil, result.Fail("SOURCE_METADATA_INVALID", err.Error(), 5)
		}
		if err = json.Unmarshal(jsonBytes(v), &inherit); err != nil {
			return inherit, nil, err
		}
		if inherit.Owner != m.Owner || inherit.Scope != m.Scope {
			return inherit, nil, result.Fail("SOURCE_IDENTITY_MISMATCH", "继承文件 owner/scope 不匹配", 3)
		}
	} else if !os.IsNotExist(e) {
		return inherit, nil, e
	}
	effective := []EffectiveSkill{}
	byID := map[string]int{}
	names := map[string]string{}
	for _, p := range inherit.Skills {
		s := p.Skill
		if _, ok := byID[s.ID]; ok {
			return inherit, nil, result.Fail("SKILL_ID_COLLISION", "继承身份重复", 3)
		}
		// Reuse the skills schema semantic checks, including path closure.
		if _, e = config.Validate("skills", jsonBytes(Index{Version: 1, Owner: p.Owner, Scope: p.Scope, Skills: []Skill{s}})); e != nil {
			return inherit, nil, result.Fail("SOURCE_METADATA_INVALID", e.Error(), 5)
		}
		if s.Source.Mode != "owned" || s.Source.Path != "skills/"+s.ID || len(s.BaseSource) != 0 {
			return inherit, nil, result.Fail("SOURCE_METADATA_INVALID", "继承必须绑定全局 owned 原源", 5)
		}
		if _, e = m.cached(t, s); e != nil {
			return inherit, nil, e
		}
		versions, err := m.inheritedVersions(t, p)
		if err != nil {
			return inherit, nil, err
		}
		// The inherited association preserves every explicitly retained version.
		// A shared pin may select one without rewriting the association or Local.
		overridden := false
		for _, own := range idx.Skills {
			if own.ID == s.ID {
				overridden = true
				break
			}
		}
		if !overridden && profile["schema_version"] == 2 {
			pin, _ := switches(profile)["pins"].(map[string]any)[s.ID].(string)
			if pin == "" {
				// Upper/Local layers may supply the final exact pin. The source
				// association still supplies a fixed shared-view fallback.
			} else if selected, ok := inheritedVersion(versions, pin); ok {
				s = selected
			} else {
				return inherit, nil, result.Fail("PIN_MISMATCH", "继承 Profile pin 缺少项目已保留的完整版本", 3)
			}
		}
		byID[s.ID] = len(effective)
		alternatives := []Skill(nil)
		if len(p.Versions) > 0 {
			alternatives = versions
		}
		effective = append(effective, EffectiveSkill{Skill: s, Owner: p.Owner, Scope: p.Scope, Layer: "inherited", Status: "PINNED", ReadOnly: true, Versions: alternatives})
	}
	for _, s := range idx.Skills {
		layer, status := "owned", "LOCAL"
		if i, ok := byID[s.ID]; ok {
			base := effective[i]
			if s.BaseSource["owner"] != base.Owner || s.BaseSource["scope_id"] != base.Scope || s.BaseSource["id"] != s.ID || s.BaseRevision == "" {
				return inherit, nil, result.Fail("SKILL_ID_COLLISION", "同 ID 资产缺少明确覆盖关系", 3)
			}
			layer, status = "override", "PINNED"
			if s.BaseRevision != base.Skill.Revision {
				status = "BASE_STALE"
			}
			// Old bases remain required even when an upper pin was changed.
			if s.BaseRevision != base.Skill.Revision {
				old := Skill{}
				data, err := m.treeRead(t, recordPath(Skill{ID: s.ID, Revision: s.BaseRevision}))
				if err != nil {
					return inherit, nil, result.Fail("REVISION_CORRUPT", "覆盖基础元数据不可取得", 5)
				}
				if err = json.Unmarshal(data, &old); err != nil {
					return inherit, nil, err
				}
				if old.ID != s.ID || old.Revision != s.BaseRevision {
					return inherit, nil, result.Fail("REVISION_CORRUPT", "基础身份不符", 5)
				}
				if _, err = m.cached(t, old); err != nil {
					return inherit, nil, err
				}
			}
			effective[i] = EffectiveSkill{Skill: s, Owner: m.Owner, Scope: m.Scope, Layer: layer, Status: status}
		} else {
			if s.BaseSource["owner"] == "global" {
				return inherit, nil, result.Fail("BASE_MISSING", "覆盖缺少固定的上层继承关系", 3)
			}
			if len(s.Upstream) > 0 {
				layer = "fork"
			}
			byID[s.ID] = len(effective)
			effective = append(effective, EffectiveSkill{Skill: s, Owner: m.Owner, Scope: m.Scope, Layer: layer, Status: status})
		}
	}
	external, e := m.loadExternal(t)
	if e != nil {
		return inherit, nil, e
	}
	for _, a := range external.Skills {
		s := a.Skill
		if _, ok := byID[s.ID]; ok {
			return inherit, nil, result.Fail("SKILL_ID_COLLISION", "外部批准包与有效集合的 ID 冲突", 3)
		}
		byID[s.ID] = len(effective)
		effective = append(effective, EffectiveSkill{Skill: s, Owner: m.Owner, Scope: m.Scope, Layer: "external", Status: "PINNED", ReadOnly: true})
	}
	for _, s := range effective {
		key := strings.ToLower(s.Skill.Name)
		if id, ok := names[key]; ok && id != s.Skill.ID {
			return inherit, nil, result.Fail("NATIVE_NAME_COLLISION", "有效技能原生名冲突", 3)
		}
		names[key] = s.Skill.ID
		// Only inherited IDs are required to match their selected pin here.
		if s.Layer == "external" && profile["schema_version"] == 2 && switches(profile)["pins"].(map[string]any)[s.Skill.ID] != s.Skill.Revision {
			return inherit, nil, result.Fail("PIN_MISMATCH", "继承 Profile pin 与完整包不符", 3)
		}
	}
	sort.Slice(effective, func(i, j int) bool { return effective[i].Skill.ID < effective[j].Skill.ID })
	return inherit, effective, nil
}

func (m *Manager) recordCandidates(after *workstore.Tree, idx Index) error {
	for _, s := range idx.Skills {
		p := recordPath(s)
		want := jsonBytes(governed(s))
		if old, ok := after.Entries[p]; ok {
			b, e := m.Store.Data(old)
			if e != nil {
				return e
			}
			if string(b) != string(want) {
				return result.Fail("REVISION_METADATA_CONFLICT", "同一修订的治理元数据不可改写；请发布新语义修订", 3)
			}
		} else if e := m.Store.PutFile(after, p, want, 0644); e != nil {
			return e
		}
	}
	return nil
}

// Read-only attachments place observation blobs/locks in the project's Local
// Root. Neither library packages nor its transaction area are written.
func (m *Manager) AttachLibrary(root string) error {
	if m.Owner == "global" {
		return result.Fail("INVALID_SOURCE_BINDING", "全局库不能绑定另一个全局库", 2)
	}
	l, e := NewGlobal(root, m.Store.Local)
	if e != nil {
		return e
	}
	if workstore.Within(m.Store.Root, l.Store.Root) || workstore.Within(l.Store.Root, m.Store.Root) {
		return result.Fail("INVALID_ROOTS", "全局库与项目源不能重叠", 2)
	}
	m.Library = l
	return nil
}
func (m *Manager) Catalog() ([]Candidate, error) {
	out := []Candidate{}
	e := m.Store.Locked(func() error {
		if m.Owner != "global" {
			return result.Fail("LIBRARY_REQUIRED", "仅全局库提供候选", 4)
		}
		if _, err := workstore.ReadFile(filepath.Join(m.Store.Root, "library.yaml")); err != nil {
			return result.Fail("LIBRARY_UNAVAILABLE", "全局库未初始化或不可读取", 5)
		}
		if err := m.Store.Pending(); err != nil {
			return err
		}
		idx, _, err := m.load(nil)
		if err != nil {
			return err
		}
		life, err := m.loadLifecycle(nil, idx)
		if err != nil {
			return err
		}
		for _, s := range idx.Skills {
			entries, err := os.ReadDir(filepath.Join(m.Store.Root, ".revision-records", s.ID))
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
					continue
				}
				rev := "sha256:" + strings.TrimSuffix(entry.Name(), ".json")
				if !revisionPattern.MatchString(rev) {
					return result.Fail("REVISION_CORRUPT", "全局修订记录路径无效", 5)
				}
				old, _, err := m.selectedUnlocked(s.ID, rev)
				if err != nil {
					return err
				}
				if rev == s.Revision {
					old.DisplayName = s.DisplayName
					old.Tags = s.Tags
					old.Version = s.Version
				}
				out = append(out, Candidate{Skill: old, Owner: "global", Scope: "global", Current: rev == s.Revision, Retired: retired(life, s.ID)})
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Skill.ID != out[j].Skill.ID {
			return out[i].Skill.ID < out[j].Skill.ID
		}
		return out[i].Skill.Revision < out[j].Skill.Revision
	})
	return out, e
}
func (m *Manager) selectedUnlocked(id, revision string) (Skill, workstore.Tree, error) {
	return m.selectedFrom(nil, id, revision)
}

func (m *Manager) selectedFrom(t *workstore.Tree, id, revision string) (Skill, workstore.Tree, error) {
	s := Skill{ID: id, Revision: revision}
	pkg := workstore.Tree{}
	if !uuid.MatchString(id) || !revisionPattern.MatchString(revision) {
		return s, pkg, result.Fail("INVALID_INPUT", "需选择稳定 UUID 和精确修订", 2)
	}
	idx, _, e := m.load(t)
	if e != nil {
		return s, pkg, e
	}
	found := false
	for _, v := range idx.Skills {
		if v.ID == id {
			found = true
		}
	}
	if !found {
		return s, pkg, result.Fail("SKILL_NOT_FOUND", "全局身份不存在", 2)
	}
	b, e := m.treeRead(t, recordPath(s))
	if e != nil {
		return s, pkg, result.Fail("REVISION_UNAVAILABLE", "全局固定修订元数据不可取得", 5)
	}
	if e = json.Unmarshal(b, &s); e != nil {
		return s, pkg, e
	}
	if s.ID != id || s.Revision != revision || s.RevisionFormat != "semantic-v1" || s.Source.Mode != "owned" || s.Source.Path != "skills/"+id {
		return s, pkg, result.Fail("REVISION_CORRUPT", "全局修订身份/格式不符", 5)
	}
	if _, e = config.Validate("skills", jsonBytes(Index{Version: 1, Owner: "global", Scope: "global", Skills: []Skill{s}})); e != nil {
		return s, pkg, e
	}
	pkg, e = m.cached(t, s)
	return s, pkg, e
}
func (m *Manager) selected(id, revision string) (Skill, workstore.Tree, error) {
	var s Skill
	var pkg workstore.Tree
	e := m.Store.Locked(func() error {
		if err := m.Store.Pending(); err != nil {
			return err
		}
		idx, _, err := m.load(nil)
		if err != nil {
			return err
		}
		life, err := m.loadLifecycle(nil, idx)
		if err != nil {
			return err
		}
		if retired(life, id) {
			return result.Fail("SKILL_RETIRED", "全局技能已退役，不能新采纳", 3)
		}
		s, pkg, err = m.selectedUnlocked(id, revision)
		return err
	})
	return s, pkg, e
}
func (m *Manager) validateInputs(ctx context.Context, inputs map[string]string) error {
	if expected, ok := inputs["profile_layers"]; ok {
		layers, err := m.profileLayers()
		if err != nil {
			return err
		}
		if expected != layers.Digest {
			return result.Fail("PLAN_STALE", "全局默认、继承库或本机覆盖已变化；请重新预览", 3)
		}
		filtered := map[string]string{}
		for k, v := range inputs {
			if k != "profile_layers" {
				filtered[k] = v
			}
		}
		inputs = filtered
	}
	if len(inputs) == 0 {
		return nil
	}
	if inputs["kind"] == "external-adopt" {
		return m.validateExternalInputs(ctx, inputs)
	}
	if inputs["kind"] == "native-rule-import" {
		return m.validateRuleImportInputs(inputs)
	}
	if len(inputs) != 4 || m.Library == nil || inputs["library_root"] != m.Library.Store.Root {
		return result.Fail("PLAN_STALE", "预览绑定的只读全局库已改变", 3)
	}
	s, _, e := m.Library.selected(inputs["id"], inputs["revision"])
	if e != nil || workstore.Hash(jsonBytes(governed(s))) != inputs["metadata"] {
		return result.Fail("PLAN_STALE", "全局固定修订、元数据或退役状态已变化，请重新预览", 3)
	}
	return nil
}
func (m *Manager) install(after *workstore.Tree, prefix string, pkg workstore.Tree, from *Manager) error {
	for p, e := range pkg.Entries {
		if e.Dir {
			after.Entries[prefix+"/"+p] = e
			for d := path.Dir(prefix + "/" + p); d != "."; d = path.Dir(d) {
				if _, ok := after.Entries[d]; !ok {
					after.Entries[d] = workstore.Directory()
				}
			}
			continue
		}
		b, err := from.Store.Data(e)
		if err != nil {
			return err
		}
		if err = m.Store.PutFile(after, prefix+"/"+p, b, e.Mode); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) inheritanceAction(ctx context.Context, req Request, out *Preview, before workstore.Tree, after *workstore.Tree, idx Index, profile map[string]any, life Lifecycle, inherit Inheritance, effective []EffectiveSkill) error {
	_ = ctx
	if !uuid.MatchString(req.ID) || !revisionPattern.MatchString(req.Revision) {
		return result.Fail("INVALID_INPUT", "动作需要稳定 ID 和明确的当前/所选固定修订", 2)
	}
	if retired(life, req.ID) {
		return result.Fail("SKILL_RETIRED", "先恢复管理再调整继承关系", 3)
	}
	if m.Owner == "global" && req.Action != "fork" {
		return result.Fail("INVALID_SOURCE_BINDING", "全局库不支持项目继承动作", 2)
	}
	at, upper := -1, -1
	for i, s := range idx.Skills {
		if s.ID == req.ID {
			at = i
		}
	}
	for i, s := range inherit.Skills {
		if s.Skill.ID == req.ID {
			upper = i
		}
	}
	out.SkillID = req.ID
	if req.Action == "inherit" {
		if m.Library == nil {
			return result.Fail("LIBRARY_REQUIRED", "先显式绑定只读 --library-root", 4)
		}
		if at >= 0 && upper < 0 {
			return result.Fail("SKILL_ID_COLLISION", "项目独立资产已使用该全局 ID", 3)
		}
		s, pkg, e := m.Library.selected(req.ID, req.Revision)
		if e != nil {
			return e
		}
		m.planInputs = map[string]string{"library_root": m.Library.Store.Root, "id": s.ID, "revision": s.Revision, "metadata": workstore.Hash(jsonBytes(governed(s)))}
		if old, ok := before.Entries[recordPath(s)]; ok {
			b, err := m.Store.Data(old)
			if err != nil {
				return err
			}
			if string(b) != string(jsonBytes(governed(s))) {
				return result.Fail("REVISION_CORRUPT", "缓存治理记录不符", 5)
			}
		}
		if _, ok := before.Entries[revisionPath(s)]; ok {
			if _, err := m.cached(&before, s); err != nil {
				return err
			}
		}
		if e = m.install(after, revisionPath(s), pkg, m.Library); e != nil {
			return e
		}
		if e = m.Store.PutFile(after, recordPath(s), jsonBytes(governed(s)), 0644); e != nil {
			return e
		}
		pin := InheritedSkill{Owner: "global", Scope: "global", Skill: s}
		if upper < 0 {
			inherit.Skills = append(inherit.Skills, pin)
			setEnabled(profile, s.ID, false)
		} else {
			// Updating the shared selected revision must not discard a version
			// that another configuration layer explicitly still selects.
			pin.Versions = retainedInheritedVersions(inherit.Skills[upper], s)
			inherit.Skills[upper] = pin
		}
		if at < 0 {
			switches(profile)["pins"].(map[string]any)[s.ID] = s.Revision
		}
	} else {
		var chosen EffectiveSkill
		found := false
		for _, v := range effective {
			if v.Skill.ID == req.ID {
				chosen = v
				found = true
			}
		}
		if !found {
			return result.Fail("SKILL_NOT_FOUND", "有效技能不存在", 2)
		}
		if req.Action == "override" || req.Action == "restore-inheritance" {
			if upper < 0 {
				return result.Fail("BASE_MISSING", "未选择全局继承基础", 3)
			}
			if inherit.Skills[upper].Skill.Revision != req.Revision {
				return result.Fail("PLAN_STALE", "所选基础固定版本已改变", 3)
			}
		} else if chosen.Skill.Revision != req.Revision {
			return result.Fail("PLAN_STALE", "分叉来源版本已改变", 3)
		}
		s := chosen.Skill
		var pkg workstore.Tree
		var e error
		switch req.Action {
		case "restore-inheritance":
			if at < 0 {
				return result.Fail("INVALID_INPUT", "尚无项目覆盖", 2)
			}
			for p := range after.Entries {
				if p == s.Source.Path || strings.HasPrefix(p, s.Source.Path+"/") {
					delete(after.Entries, p)
				}
			}
			idx.Skills = append(idx.Skills[:at], idx.Skills[at+1:]...)
			switches(profile)["pins"].(map[string]any)[s.ID] = inherit.Skills[upper].Skill.Revision
		case "override", "fork":
			if req.Action == "override" && at >= 0 {
				return result.Fail("OVERRIDE_EXISTS", "项目覆盖已存在；先保留草稿并恢复继承，再生成新覆盖", 3)
			}
			if chosen.ReadOnly {
				pkg, e = m.cached(&before, s)
			} else {
				pkg = subtree(before, s.Source.Path)
				e = m.checkPackage(s, pkg)
			}
			if e != nil {
				return e
			}
			if req.Action == "override" {
				s.BaseSource = map[string]string{"owner": "global", "scope_id": "global", "id": s.ID}
				s.BaseRevision = req.Revision
				s.Upstream = nil
			} else {
				id := req.NewID
				if id == "" {
					id = workstore.ID()
				}
				if !uuid.MatchString(id) || !nativeName.MatchString(req.Name) || req.Name == s.Name {
					return result.Fail("INVALID_INPUT", "独立分叉需新 UUID 和不同的有效原生名称", 2)
				}
				for _, v := range effective {
					if v.Skill.ID == id {
						return result.Fail("SKILL_ID_COLLISION", "分叉 ID 已使用", 3)
					}
				}
				s.Upstream = map[string]string{"owner": chosen.Owner, "scope_id": chosen.Scope, "id": s.ID, "revision": s.Revision}
				s.BaseSource = nil
				s.BaseRevision = ""
				s.ID = id
				doc := pkg.Entries["SKILL.md"]
				b, err := m.Store.Data(doc)
				if err != nil {
					return err
				}
				b, err = renameDocument(b, s.Name, req.Name)
				if err != nil {
					return err
				}
				if err = m.Store.PutFile(&pkg, "SKILL.md", b, doc.Mode); err != nil {
					return err
				}
				s.Name = req.Name
				s.Files = packageFiles(pkg, "")
				for i := range s.Files {
					for _, f := range chosen.Skill.Files {
						if f.Path == s.Files[i].Path {
							s.Files[i].Type = f.Type
						}
					}
				}
			}
			s.Source = Origin{Mode: "owned", Path: "skills/" + s.ID, Format: "native-skill"}
			s.RevisionFormat = "semantic-v1"
			s.PackageDigest = portablePackageDigest(pkg)
			s.Revision = packageRevision(s, pkg)
			if e = m.install(after, s.Source.Path, pkg, m); e != nil {
				return e
			}
			if e = m.install(after, revisionPath(s), pkg, m); e != nil {
				return e
			}
			idx.Skills = append(idx.Skills, s)
			switches(profile)["pins"].(map[string]any)[s.ID] = s.Revision
			if req.Action == "fork" {
				setEnabled(profile, s.ID, false)
			}
			out.SkillID = s.ID
		}
	}
	if m.Owner != "global" {
		sort.Slice(inherit.Skills, func(i, j int) bool { return inherit.Skills[i].Skill.ID < inherit.Skills[j].Skill.ID })
		if e := m.Store.PutFile(after, inheritanceFile, jsonBytes(inherit), 0644); e != nil {
			return e
		}
	}
	return m.finishPlan(out, req.Action, before, *after, idx, profile)
}
