// Package source manages owned packages and approved external read-only pins. Native tool configuration
// and replica propagation deliberately use different, still-closed operations.
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
	"unicode/utf8"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type Skill struct {
	ID             string            `json:"id"`
	Name           string            `json:"native_name"`
	Source         Origin            `json:"source"`
	Revision       string            `json:"revision"`
	PackageFormat  map[string]string `json:"package_format"`
	Files          []inventory.File  `json:"files"`
	Dependencies   map[string]any    `json:"dependencies"`
	Compatibility  map[string]any    `json:"compatibility"`
	Requirements   map[string]any    `json:"requirements"`
	DisplayName    string            `json:"display_name,omitempty"`
	Tags           []string          `json:"tags,omitempty"`
	Version        string            `json:"version,omitempty"`
	BaseSource     map[string]string `json:"base_source,omitempty"`
	BaseRevision   string            `json:"base_revision,omitempty"`
	Upstream       map[string]string `json:"upstream,omitempty"`
	RevisionFormat string            `json:"revision_format,omitempty"`
	PackageDigest  string            `json:"package_digest,omitempty"`
}
type Origin struct {
	Mode       string `json:"mode"`
	Path       string `json:"path"`
	Format     string `json:"format"`
	Origin     string `json:"origin,omitempty"`
	PluginID   string `json:"plugin_id,omitempty"`
	LockDigest string `json:"lock_digest,omitempty"`
}
type Index struct {
	Version int     `json:"schema_version"`
	Owner   string  `json:"owner"`
	Scope   string  `json:"scope_id"`
	Skills  []Skill `json:"skills"`
}
type Patch struct {
	Path           string `json:"path"`
	Content        string `json:"content"`
	Remove         bool   `json:"remove,omitempty"`
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
}
type Request struct {
	Action         string      `json:"action,omitempty"`
	ID             string      `json:"id,omitempty"`
	Name           string      `json:"name,omitempty"`
	Description    string      `json:"description,omitempty"`
	Document       string      `json:"document,omitempty"`
	PackagePath    string      `json:"package_path,omitempty"`
	SourceRole     string      `json:"source_role,omitempty"`
	Files          []Patch     `json:"files,omitempty"`
	Snapshot       string      `json:"snapshot,omitempty"`
	DisplayName    *string     `json:"display_name,omitempty"`
	Tags           *[]string   `json:"tags,omitempty"`
	Reason         string      `json:"reason,omitempty"`
	Revision       string      `json:"revision,omitempty"`
	NewID          string      `json:"new_id,omitempty"`
	Governance     *Governance `json:"governance,omitempty"`
	BridgeID       string      `json:"bridge_id,omitempty"`
	BridgeRevision string      `json:"bridge_revision,omitempty"`
	Approval       string      `json:"approval,omitempty"`
	provided       []string
}
type Preview struct {
	Plan         workstore.Plan      `json:"plan"`
	Owner        string              `json:"owner"`
	Scope        string              `json:"scope_id"`
	SkillID      string              `json:"skill_id,omitempty"`
	Findings     []inventory.Finding `json:"findings"`
	PendingApply bool                `json:"pending_native_apply"`
	Impact       *Impact             `json:"impact,omitempty"`
	Preflight    *Preflight          `json:"preflight,omitempty"`
}
type HistoryItem struct {
	ID         string `json:"id"`
	Action     string `json:"action"`
	State      string `json:"state"`
	Updated    string `json:"updated"`
	Restorable bool   `json:"restorable"`
}
type State struct {
	Root            string             `json:"root"`
	Index           Index              `json:"index"`
	Profile         map[string]any     `json:"profile"`
	History         []HistoryItem      `json:"history"`
	Pending         bool               `json:"recovery_required"`
	NativeApplied   bool               `json:"native_applied"`
	Warning         string             `json:"warning,omitempty"`
	Retirements     []Retirement       `json:"retirements"`
	Effective       []EffectiveSkill   `json:"effective_skills"`
	Inherited       []InheritedSkill   `json:"inherited_skills"`
	Library         []Candidate        `json:"library,omitempty"`
	LibraryRoot     string             `json:"library_root,omitempty"`
	LibraryError    string             `json:"library_error,omitempty"`
	External        []ExternalApproval `json:"external_skills"`
	ExternalHistory []ExternalApproval `json:"external_history"`
}
type Manager struct {
	Store            *workstore.Store
	Owner, Scope     string
	Library          *Manager
	planInputs       map[string]string
	BridgeFile, Copy string
}

var nativeName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func New(root, local, owner, scope string) (*Manager, error) {
	return newManager(root, local, owner, scope, false)
}
func NewGlobal(root, local string) (*Manager, error) {
	return newManager(root, local, "global", "global", true)
}
func newManager(root, local, owner, scope string, global bool) (*Manager, error) {
	r, e := filepath.Abs(root)
	if e != nil {
		return nil, e
	}
	l, e := filepath.Abs(local)
	if e != nil {
		return nil, e
	}
	if root == "" || local == "" || (!global && (filepath.Base(r) != ".aidock" || !uuid.MatchString(owner))) || strings.TrimSpace(scope) == "" {
		return nil, result.Fail("INVALID_SOURCE_BINDING", "源管理需要显式 .aidock 根、项目 UUID、作用域和 Local Root", 2)
	}
	if workstore.Within(r, l) || workstore.Within(l, r) {
		return nil, result.Fail("INVALID_ROOTS", "源与事务根不能重叠", 2)
	}
	if e = workstore.CheckLinks(r); e != nil {
		return nil, result.Fail("PATH_UNSAFE", e.Error(), 5)
	}
	if e = workstore.CheckLinks(l); e != nil {
		return nil, result.Fail("PATH_UNSAFE", e.Error(), 5)
	}
	m := &Manager{Store: &workstore.Store{Root: filepath.Clean(r), Local: filepath.Clean(l)}, Owner: owner, Scope: scope}
	if e = m.externalWriteBoundaries(); e != nil {
		return nil, e
	}
	return m, nil
}
func (m *Manager) identityFile() string {
	if m.Owner == "global" {
		return "library.yaml"
	}
	return "project.yaml"
}
func jsonBytes(v any) []byte { b, _ := json.MarshalIndent(v, "", "  "); return append(b, '\n') }
func (m *Manager) load(snapshot *workstore.Tree) (Index, map[string]any, error) {
	idx := Index{Version: 1, Owner: m.Owner, Scope: m.Scope, Skills: []Skill{}}
	profile := map[string]any{"schema_version": 2, "name": m.Scope, "skills": map[string]any{"enable": []any{}, "disable": []any{}, "pins": map[string]any{}}}
	read := func(name string) ([]byte, error) {
		if snapshot != nil {
			entry, ok := snapshot.Entries[name]
			if !ok {
				return nil, os.ErrNotExist
			}
			return m.Store.Data(entry)
		}
		return workstore.ReadFile(filepath.Join(m.Store.Root, name))
	}
	b, e := read(m.identityFile())
	if os.IsNotExist(e) {
		files, readErr := os.ReadDir(m.Store.Root)
		if snapshot != nil {
			if len(snapshot.Entries) > 0 {
				return idx, profile, result.Fail("OWNERSHIP_REQUIRED", "非空目录尚无项目身份，不能自动接管", 4)
			}
		} else if readErr == nil && len(files) > 0 {
			return idx, profile, result.Fail("OWNERSHIP_REQUIRED", "非空目录尚无项目身份，不能自动接管", 4)
		}
		return idx, profile, nil
	}
	if e != nil {
		return idx, profile, e
	}
	kind := "project"
	if m.Owner == "global" {
		kind = "skill-library"
	}
	v, e := config.Validate(kind, b)
	if e != nil {
		return idx, profile, e
	}
	identity := "id"
	if m.Owner == "global" {
		identity = "owner"
	}
	if v.(map[string]any)[identity] != m.Owner {
		return idx, profile, result.Fail("SOURCE_IDENTITY_MISMATCH", "项目身份不匹配", 3)
	}
	b, e = read("skills.yaml")
	if e != nil {
		return idx, profile, e
	}
	v, e = config.Validate("skills", b)
	if e != nil {
		return idx, profile, e
	}
	if e = json.Unmarshal(jsonBytes(v), &idx); e != nil {
		return idx, profile, e
	}
	if idx.Owner != m.Owner || idx.Scope != m.Scope {
		return idx, profile, result.Fail("SOURCE_IDENTITY_MISMATCH", "技能索引 owner/scope 不匹配", 3)
	}
	b, e = read("profile.yaml")
	if e != nil {
		return idx, profile, e
	}
	value, e := config.Parse(b)
	if e != nil {
		return idx, profile, e
	}
	kind = "profile"
	parsed, ok := value.(map[string]any)
	if !ok {
		return idx, profile, result.Fail("SOURCE_METADATA_INVALID", "Profile 必须为对象", 5)
	}
	if parsed["schema_version"] == 1 {
		kind = "profile-v1"
	}
	value, e = config.Validate(kind, b)
	if e != nil {
		return idx, profile, e
	}
	profile = value.(map[string]any)
	return idx, profile, nil
}
func (m *Manager) State() (State, error) {
	state := State{Root: m.Store.Root, History: []HistoryItem{}}
	e := m.Store.Locked(func() error {
		js, e := m.Store.Journals()
		if e != nil {
			return e
		}
		for _, j := range js {
			_, restorable := j.Plan.Before.Entries[m.identityFile()]
			state.History = append(state.History, HistoryItem{ID: j.Plan.ID, Action: j.Plan.Action, State: j.State, Updated: j.Updated, Restorable: restorable})
			if j.State == "prepared" {
				state.Pending = true
			}
		}
		idx, p, e := m.load(nil)
		state.Index, state.Profile = idx, p
		if e != nil {
			if !state.Pending {
				return e
			}
			state.Warning = "未完成事务中的源元数据不可用，先恢复: " + e.Error()
		}
		life, lifeErr := m.loadLifecycle(nil, idx)
		state.Retirements = life.Retirements
		if lifeErr != nil && !state.Pending {
			return lifeErr
		}
		if state.Pending {
			if lifeErr != nil {
				state.Warning = "未完成事务中的退役元数据不可用，先恢复: " + lifeErr.Error()
			}
		} else if e = validateRetiredProfile(life, p); e != nil {
			return e
		}
		if !state.Pending {
			var inheritance Inheritance
			inheritance, state.Effective, e = m.resolve(nil, idx, p)
			state.Inherited = inheritance.Skills
			if e != nil {
				return e
			}
			external, err := m.loadExternal(nil)
			if err != nil {
				return err
			}
			state.External = external.Skills
			state.ExternalHistory, e = m.externalHistory()
			if e != nil {
				return e
			}
		}
		if m.Library != nil {
			state.LibraryRoot = m.Library.Store.Root
			state.Library, e = m.Library.Catalog()
			if e != nil {
				state.LibraryError = e.Error()
			}
		}
		return nil
	})
	return state, e
}
func switches(profile map[string]any) map[string]any {
	skills, ok := profile["skills"].(map[string]any)
	if !ok {
		skills = map[string]any{}
		profile["skills"] = skills
	}
	for _, key := range []string{"enable", "disable"} {
		if _, ok = skills[key]; !ok {
			skills[key] = []any{}
		}
	}
	if _, ok = skills["pins"]; !ok {
		skills["pins"] = map[string]any{}
	}
	return skills
}
func setEnabled(profile map[string]any, id string, enabled bool) {
	s := switches(profile)
	for _, key := range []string{"enable", "disable"} {
		list := []any{}
		for _, v := range s[key].([]any) {
			if v != id {
				list = append(list, v)
			}
		}
		if enabled && key == "enable" || !enabled && key == "disable" {
			list = append(list, id)
		}
		s[key] = list
	}
}
func packageFiles(t workstore.Tree, prefix string) []inventory.File {
	files := []inventory.File{}
	for p, e := range t.Entries {
		if !e.Dir && strings.HasPrefix(p, prefix) {
			files = append(files, inventory.File{Path: strings.TrimPrefix(p, prefix), Bytes: e.Size, SHA256: e.Hash, Type: "binary"})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}
func (m *Manager) validatePackage(ctx context.Context, dir string) (workstore.Tree, inventory.Skill, error) {
	t, e := m.Store.Capture(dir)
	if e != nil {
		return t, inventory.Skill{}, e
	}
	if !t.Exists {
		return t, inventory.Skill{}, result.Fail("PACKAGE_INVALID", "包目录不存在", 5)
	}
	skill := inventory.AnalyzePackage(ctx, dir)
	if skill.StaticStatus == "FAIL" {
		return t, skill, result.Fail("PACKAGE_INVALID", "技能包静态检查失败", 5)
	}
	seen := 0
	for p, entry := range t.Entries {
		if entry.Dir {
			continue
		}
		if strings.HasPrefix(path.Base(p), ".env") || strings.HasPrefix(path.Base(p), ".aidock-tmp-") {
			return t, skill, result.Fail("PACKAGE_INVALID", "不能静默导入敏感文件/事务暂存文件", 5)
		}
		found := false
		for _, f := range skill.Files {
			if f.Path == p && f.SHA256 == entry.Hash && f.Bytes == entry.Size {
				found = true
				break
			}
		}
		if !found {
			return t, skill, result.Fail("PACKAGE_INVALID", "包包含被排除文件或盘点期间发生变化: "+p, 5)
		}
		seen++
	}
	if seen != len(skill.Files) {
		return t, skill, result.Fail("PLAN_STALE", "导入期间文件清单发生变化", 3)
	}
	if !nativeName.MatchString(skill.Name) {
		return t, skill, result.Fail("PACKAGE_INVALID", "原生名称需为小写字母、数字或连字符，最长 64", 5)
	}
	return t, skill, nil
}
func (m *Manager) Plan(ctx context.Context, req Request) (Preview, error) {
	out := Preview{Owner: m.Owner, Scope: m.Scope, Findings: []inventory.Finding{}, PendingApply: true}
	if req.Action == "delete" || req.Action == "remove" {
		return out, result.Fail("DELETE_SCOPE_UNKNOWN", "尚未登记完整项目/离线引用范围；仅可退役并保留完整包与修订", 4)
	}
	if e := validateRequest(req); e != nil {
		return out, e
	}
	// Check external read/write boundaries before creating a local lock or blobs.
	if req.Action == "external-adopt" {
		if _, _, e := m.externalRegistration(); e != nil {
			return out, e
		}
	}
	e := m.Store.Locked(func() error {
		m.planInputs = nil
		defer func() { m.planInputs = nil }()
		if e := m.Store.Pending(); e != nil {
			return e
		}
		before, e := m.Store.Capture(m.Store.Root)
		if e != nil {
			return e
		}
		idx, profile, e := m.load(&before)
		if e != nil {
			return e
		}
		life, e := m.loadLifecycle(&before, idx)
		if e != nil {
			return e
		}
		after := workstore.Clone(before)
		if req.Action == "rollback" {
			return m.rollback(&out, before, req.Snapshot)
		}
		inheritance, effective, e := m.resolve(&before, idx, profile)
		if e != nil {
			return e
		}
		if req.Action == "external-adopt" || req.Action == "external-select" {
			if profile["schema_version"] != 2 {
				return result.Fail("MIGRATION_REQUIRED", "先升级 Profile", 3)
			}
			return m.externalAction(ctx, req, &out, before, &after, idx, profile, effective)
		}
		for _, item := range effective {
			if item.Skill.ID == req.ID && item.Layer == "external" && req.Action != "enable" && req.Action != "disable" {
				return result.Fail("EXTERNAL_READ_ONLY", "外部批准包只读；修改原源后显式采纳新候选，或选择历史批准版本", 4)
			}
		}
		if req.Action == "inherit" || req.Action == "override" || req.Action == "restore-inheritance" || req.Action == "fork" {
			if profile["schema_version"] != 2 {
				return result.Fail("MIGRATION_REQUIRED", "先升级 Profile", 3)
			}
			return m.inheritanceAction(ctx, req, &out, before, &after, idx, profile, life, inheritance, effective)
		}
		if req.Action == "migrate" {
			entry, ok := before.Entries["profile.yaml"]
			if !ok {
				return result.Fail("MIGRATION_REQUIRED", "没有旧 Profile", 2)
			}
			data, e := m.Store.Data(entry)
			if e != nil {
				return e
			}
			names := map[string][]config.SkillBinding{}
			for _, s := range idx.Skills {
				names[s.Name] = append(names[s.Name], config.SkillBinding{ID: s.ID, Revision: s.Revision})
			}
			data, e = config.MigrateProfile(data, names)
			if e != nil {
				return result.Fail("MIGRATION_REFERENCE_UNRESOLVED", e.Error(), 3)
			}
			if e = m.Store.PutFile(&after, "profile.yaml", data, 0644); e != nil {
				return e
			}
			out.Plan, e = m.preview("migrate", before, after)
			return e
		}
		if profile["schema_version"] != 2 {
			return result.Fail("MIGRATION_REQUIRED", "Profile v1 需先生成并应用 migrate 计划", 3)
		}
		if e = validateRetiredProfile(life, profile); e != nil {
			return e
		}
		for _, s := range effective {
			if s.Skill.ID == req.ID && (s.Layer == "inherited" || s.Layer == "external") {
				if req.Action != "enable" && req.Action != "disable" {
					return result.Fail("INHERITED_READ_ONLY", "继承包为只读；先创建项目覆盖或独立分叉", 4)
				}
				setEnabled(profile, req.ID, req.Action == "enable")
				return m.finishPlan(&out, req.Action, before, after, idx, profile)
			}
		}
		at := -1
		for i, s := range idx.Skills {
			if s.ID == req.ID {
				at = i
			}
		}
		newSkill := req.Action == "create" || req.Action == "import"
		if newSkill {
			if req.ID == "" {
				req.ID = workstore.ID()
			}
			if at >= 0 {
				return result.Fail("SKILL_ID_COLLISION", "技能 id 已存在", 3)
			}
			for _, s := range effective {
				if s.Skill.ID == req.ID {
					return result.Fail("SKILL_ID_COLLISION", "独立资产不能复用继承 ID", 3)
				}
			}
		} else if at < 0 {
			return result.Fail("SKILL_NOT_FOUND", "必须引用现有稳定技能 id", 2)
		}
		out.SkillID = req.ID
		if !uuid.MatchString(req.ID) {
			return result.Fail("INVALID_INPUT", "技能 id 不是 UUID", 2)
		}
		if retired(life, req.ID) && req.Action != "reactivate" && req.Action != "disable" {
			return result.Fail("SKILL_RETIRED", "技能已退役；先恢复为可管理状态，或回退退役事务", 3)
		}
		if req.Action == "metadata" || req.Action == "retire" || req.Action == "reactivate" {
			out.Impact = m.impact(idx, profile, req.ID)
			if e = m.lifecycleAction(req, &idx, profile, &life, before, &after); e != nil {
				return e
			}
		} else if req.Action == "enable" || req.Action == "disable" {
			setEnabled(profile, req.ID, req.Action == "enable")
		} else {
			var skill Skill
			var pkg workstore.Tree
			if newSkill {
				skill = Skill{ID: req.ID, Source: Origin{Mode: "owned", Path: "skills/" + req.ID, Format: "native-skill"}, PackageFormat: map[string]string{"id": "native-skill", "version": "1"}, Dependencies: map[string]any{}, Compatibility: map[string]any{}, Requirements: map[string]any{}}
			} else {
				skill = idx.Skills[at]
				if skill.Source.Mode != "owned" {
					return result.Fail("OWNERSHIP_REQUIRED", "此版仅编辑 AIDock owned 包", 4)
				}
				if skill.Source.Path != "skills/"+skill.ID {
					return result.Fail("OWNERSHIP_REQUIRED", "非受支持源路径", 4)
				}
			}
			switch req.Action {
			case "create":
				if !nativeName.MatchString(req.Name) || strings.TrimSpace(req.Description) == "" {
					return result.Fail("INVALID_INPUT", "创建需要有效名称和描述", 2)
				}
				name, _ := json.Marshal(req.Name)
				desc, _ := json.Marshal(req.Description)
				document := []byte("---\nname: " + string(name) + "\ndescription: " + string(desc) + "\n---\n\n" + req.Document + "\n")
				pkg = workstore.Tree{Entries: map[string]workstore.Entry{}}
				if e = m.Store.PutFile(&pkg, "SKILL.md", document, 0644); e != nil {
					return e
				}
				skill.Name = req.Name
			case "import":
				if req.SourceRole != "local-original" {
					return result.Fail("SOURCE_ROLE_REQUIRED", "本地导入须明确 local-original；镜像/插件接管尚未开放", 4)
				}
				var scan inventory.Skill
				pkg, scan, e = m.validatePackage(ctx, req.PackagePath)
				if e != nil {
					return e
				}
				skill.Name = scan.Name
				out.Findings = scan.Findings
			case "edit", "rename", "governance":
				pkg = workstore.Tree{Exists: true, Entries: map[string]workstore.Entry{}}
				prefix := skill.Source.Path + "/"
				for p, v := range before.Entries {
					if strings.HasPrefix(p, prefix) {
						pkg.Entries[strings.TrimPrefix(p, prefix)] = v
					}
				}
				if req.Action == "governance" {
					if e = m.governanceAction(req, &skill, pkg, &after); e != nil {
						return e
					}
				}
				if req.Action == "rename" {
					if packageRevision(skill, pkg) != skill.Revision {
						return result.Fail("SOURCE_PACKAGE_DRIFT", "包与固定版本不一致；先保存源编辑，再改名", 3)
					}
					if !nativeName.MatchString(req.Name) || req.Name == skill.Name || len(req.Files) != 0 {
						return result.Fail("INVALID_INPUT", "改名需要不同的有效原生名称，不能夹带文件补丁", 2)
					}
					entry, ok := pkg.Entries["SKILL.md"]
					if !ok {
						return result.Fail("PACKAGE_INVALID", "缺少 SKILL.md", 5)
					}
					data, err := m.Store.Data(entry)
					if err != nil {
						return err
					}
					data, err = renameDocument(data, skill.Name, req.Name)
					if err != nil {
						return err
					}
					if e = m.Store.PutFile(&pkg, "SKILL.md", data, entry.Mode); e != nil {
						return e
					}
					out.Impact = m.impact(idx, profile, req.ID)
					out.Impact.NativeBefore, out.Impact.NativeAfter = skill.Name, req.Name
					out.Impact.NativeCleanup = "PENDING_NATIVE_APPLY"
					skill.Name = req.Name
				}
				if req.Action == "edit" && len(req.Files) == 0 {
					return result.Fail("INVALID_INPUT", "编辑需要文件补丁", 2)
				}
				seen := map[string]bool{}
				for _, patch := range req.Files {
					if e = workstore.SafeRelative(patch.Path); e != nil {
						return e
					}
					if strings.HasPrefix(path.Base(patch.Path), ".aidock-tmp-") {
						return result.Fail("INVALID_INPUT", "保留事务名称", 2)
					}
					k := strings.ToLower(patch.Path)
					if seen[k] {
						return result.Fail("INVALID_INPUT", "补丁路径重复", 2)
					}
					seen[k] = true
					if patch.ExpectedSHA256 != "" {
						old, ok := pkg.Entries[patch.Path]
						if !ok || old.Dir || old.Hash != patch.ExpectedSHA256 {
							return result.Fail("PLAN_STALE", "文件在加载后已变化，草稿保留，请重新核对: "+patch.Path, 3)
						}
					}
					if patch.Remove {
						if patch.Path == "SKILL.md" {
							return result.Fail("PACKAGE_INVALID", "不能删除 SKILL.md", 5)
						}
						old, ok := pkg.Entries[patch.Path]
						if !ok || old.Dir {
							return result.Fail("INVALID_INPUT", "删除补丁只接受现有文件", 2)
						}
						delete(pkg.Entries, patch.Path)
					} else {
						mode := uint32(0644)
						if old, ok := pkg.Entries[patch.Path]; ok {
							if old.Dir {
								return result.Fail("INVALID_INPUT", "文件槽位是目录", 2)
							}
							mode = old.Mode
							data, err := m.Store.Data(old)
							if err != nil {
								return err
							}
							if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
								return result.Fail("BINARY_EDIT_UNSUPPORTED", "二进制附件不接受文本补丁", 4)
							}
						}
						if e = m.Store.PutFile(&pkg, patch.Path, []byte(patch.Content), mode); e != nil {
							return e
						}
					}
				}
				doc, ok := pkg.Entries["SKILL.md"]
				if !ok {
					return result.Fail("PACKAGE_INVALID", "缺少 SKILL.md", 5)
				}
				data, e := m.Store.Data(doc)
				if e != nil {
					return e
				}
				name, e := documentName(data)
				if e != nil {
					return e
				}
				if name != skill.Name {
					return result.Fail("RENAME_ACTION_REQUIRED", "正文不能隐式改原生名；请使用 rename 计划", 3)
				}
			default:
				return result.Fail("FEATURE_NOT_IMPLEMENTED", "该源动作尚未开放", 4)
			}
			for _, other := range idx.Skills {
				if other.ID != skill.ID && strings.EqualFold(other.Name, skill.Name) {
					return result.Fail("NATIVE_NAME_COLLISION", "同一 owner/scope 原生名称重复", 3)
				}
			}
			for _, other := range effective {
				if other.Skill.ID != skill.ID && strings.EqualFold(other.Skill.Name, skill.Name) {
					return result.Fail("NATIVE_NAME_COLLISION", "名称与继承包冲突", 3)
				}
			}
			packageData := map[string][]byte{}
			for p, v := range pkg.Entries {
				if v.Dir {
					continue
				}
				data, e := m.Store.Data(v)
				if e != nil {
					return e
				}
				packageData[p] = data
			}
			name, findings, e := inventory.InspectPackage(packageData)
			if e != nil {
				return result.Fail("PACKAGE_INVALID", e.Error(), 5)
			}
			if name != skill.Name {
				return result.Fail("PACKAGE_INVALID", "原生名称与索引不一致", 5)
			}
			out.Findings = append(out.Findings, findings...)
			skill.Files = packageFiles(pkg, "")
			for i := range skill.Files {
				entry := pkg.Entries[skill.Files[i].Path]
				b, e := m.Store.Data(entry)
				if e != nil {
					return e
				}
				if utf8.Valid(b) && !strings.ContainsRune(string(b), 0) {
					skill.Files[i].Type = "text"
				}
			}
			if m.Owner == "global" || skill.RevisionFormat == "semantic-v1" {
				skill.RevisionFormat = "semantic-v1"
				skill.PackageDigest = portablePackageDigest(pkg)
			}
			skill.Revision = packageRevision(skill, pkg)
			if req.Action == "governance" {
				if e = m.recordGovernance(&after, skill); e != nil {
					return e
				}
			}
			revisionRoot := ".revisions/" + skill.ID + "/" + strings.TrimPrefix(skill.Revision, "sha256:")
			if _, exists := before.Entries[revisionRoot]; exists {
				stored := workstore.Tree{Exists: true, Entries: map[string]workstore.Entry{}}
				for p, v := range before.Entries {
					if strings.HasPrefix(p, revisionRoot+"/") {
						stored.Entries[strings.TrimPrefix(p, revisionRoot+"/")] = v
					}
				}
				if workstore.Digest(stored) != workstore.Digest(pkg) {
					return result.Fail("REVISION_CORRUPT", "已有完整修订与其摘要不匹配", 5)
				}
			}
			// Keep revisions immutable and complete, including executable/binary bytes.
			prefix := skill.Source.Path + "/"
			for p := range after.Entries {
				if p == skill.Source.Path || strings.HasPrefix(p, prefix) {
					delete(after.Entries, p)
				}
			}
			for p, v := range pkg.Entries {
				if v.Dir {
					for _, prefix := range []string{skill.Source.Path, ".revisions/" + skill.ID + "/" + strings.TrimPrefix(skill.Revision, "sha256:")} {
						after.Entries[prefix+"/"+p] = v
						for d := path.Dir(prefix + "/" + p); d != "."; d = path.Dir(d) {
							if _, ok := after.Entries[d]; !ok {
								after.Entries[d] = workstore.Directory()
							}
						}
					}
					continue
				}
				b, e := m.Store.Data(v)
				if e != nil {
					return e
				}
				if e = m.Store.PutFile(&after, skill.Source.Path+"/"+p, b, v.Mode); e != nil {
					return e
				}
				revPath := ".revisions/" + skill.ID + "/" + strings.TrimPrefix(skill.Revision, "sha256:") + "/" + p
				if old, ok := before.Entries[revPath]; ok && old.Hash != v.Hash {
					return result.Fail("REVISION_CORRUPT", "不可变修订已被修改", 5)
				}
				if e = m.Store.PutFile(&after, revPath, b, v.Mode); e != nil {
					return e
				}
			}
			if newSkill {
				idx.Skills = append(idx.Skills, skill)
			} else {
				idx.Skills[at] = skill
			}
			switches(profile)["pins"].(map[string]any)[skill.ID] = skill.Revision
		}
		return m.finishPlan(&out, req.Action, before, after, idx, profile)
	})
	return out, e
}
func (m *Manager) finishPlan(out *Preview, action string, before, after workstore.Tree, idx Index, profile map[string]any) error {
	if _, ok := before.Entries[m.identityFile()]; !ok {
		identity := map[string]any{"schema_version": 1, "id": m.Owner}
		if m.Owner == "global" {
			identity = map[string]any{"schema_version": 1, "owner": "global", "scope_id": "global"}
		}
		if e := m.Store.PutFile(&after, m.identityFile(), jsonBytes(identity), 0644); e != nil {
			return e
		}
	}
	sort.Slice(idx.Skills, func(i, j int) bool { return idx.Skills[i].ID < idx.Skills[j].ID })
	indexBytes := jsonBytes(idx)
	profileBytes := jsonBytes(profile)
	if _, e := config.Validate("skills", indexBytes); e != nil {
		return result.Fail("SOURCE_METADATA_INVALID", e.Error(), 5)
	}
	if _, e := config.Validate("profile", profileBytes); e != nil {
		return result.Fail("SOURCE_METADATA_INVALID", e.Error(), 5)
	}
	if e := m.Store.PutFile(&after, "skills.yaml", indexBytes, 0644); e != nil {
		return e
	}
	if e := m.Store.PutFile(&after, "profile.yaml", profileBytes, 0644); e != nil {
		return e
	}
	if m.Owner == "global" {
		if e := m.recordCandidates(&after, idx); e != nil {
			return e
		}
	}
	report, e := m.preflight(after, idx, profile)
	if e != nil {
		return e
	}
	report.Candidate = true
	out.Preflight = &report
	if action == "enable" {
		if e = blockEnabled(report); e != nil {
			return e
		}
	}
	p, e := m.preview(action, before, after)
	out.Plan = p
	return e
}
func documentName(data []byte) (string, error) {
	text := strings.TrimPrefix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\ufeff")
	lines := strings.Split(text, "\n")
	if len(lines) < 3 || lines[0] != "---" {
		return "", result.Fail("PACKAGE_INVALID", "缺少 frontmatter", 5)
	}
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			v, e := config.Parse([]byte(strings.Join(lines[1:i], "\n")))
			if e != nil {
				return "", e
			}
			m, ok := v.(map[string]any)
			if !ok {
				return "", fmt.Errorf("frontmatter 不是对象")
			}
			name, ok := m["name"].(string)
			desc, _ := m["description"].(string)
			if !ok || !nativeName.MatchString(name) || desc == "" {
				return "", result.Fail("PACKAGE_INVALID", "无效 name/description", 5)
			}
			return name, nil
		}
	}
	return "", result.Fail("PACKAGE_INVALID", "frontmatter 未闭合", 5)
}
func (m *Manager) rollback(out *Preview, current workstore.Tree, id string) error {
	js, e := m.Store.Journals()
	if e != nil {
		return e
	}
	for _, j := range js {
		if j.Plan.ID == id && j.State == "committed" {
			desired := workstore.Clone(j.Plan.Before)
			desired.Exists = true
			for p, v := range current.Entries {
				if p == ".revisions" || strings.HasPrefix(p, ".revisions/") || p == ".revision-records" || strings.HasPrefix(p, ".revision-records/") || p == ".governance-records" || strings.HasPrefix(p, ".governance-records/") || p == ".external-approvals" || strings.HasPrefix(p, ".external-approvals/") {
					desired.Entries[p] = v
				}
			}
			if _, ok := desired.Entries[m.identityFile()]; !ok {
				return result.Fail("SOURCE_ROLLBACK_UNSUPPORTED", "初始化前的空根不可作为项目恢复点；先创建第二个源版本", 4)
			}
			out.Plan, e = m.preview("rollback:"+id, current, desired)
			return e
		}
	}
	return result.Fail("SNAPSHOT_NOT_FOUND", "没有可恢复的已提交源事务", 2)
}

func (m *Manager) Apply(ctx context.Context, id string, expected ...string) (workstore.Journal, error) {
	m.Store.Validate = func(p workstore.Plan) error {
		if len(expected) > 0 && expected[0] != "" && expected[0] != p.Digest {
			return result.Fail("PLAN_STALE", "确认摘要已过期", 3)
		}
		idx, profile, e := m.load(&p.After)
		if e != nil {
			return e
		}
		if p.Action == "inherit" && len(p.Inputs) == 0 {
			return result.Fail("PLAN_STALE", "继承计划缺少来源绑定", 3)
		}
		if p.Action == "external-adopt" && p.Inputs["kind"] != "external-adopt" {
			return result.Fail("PLAN_STALE", "外部采纳计划缺少来源绑定", 3)
		}
		if e = m.validateInputs(ctx, p.Inputs); e != nil {
			return e
		}
		if _, _, e = m.resolve(&p.After, idx, profile); e != nil {
			return e
		}
		if profile["schema_version"] != 2 && !strings.HasPrefix(p.Action, "rollback:") {
			return result.Fail("MIGRATION_REQUIRED", "保存后的 Profile 必须为 v2", 3)
		}
		for _, s := range idx.Skills {
			if s.Source.Mode != "owned" || s.Source.Path != "skills/"+s.ID {
				return result.Fail("OWNERSHIP_REQUIRED", "该计划含未支持的非 owned 源", 4)
			}
		}
		life, e := m.loadLifecycle(&p.After, idx)
		if e != nil {
			return e
		}
		if e = validateRetiredProfile(life, profile); e != nil {
			return e
		}
		if p.Action == "enable" {
			report, err := m.preflight(p.After, idx, profile)
			if err != nil {
				return err
			}
			report.Candidate = true
			if err = blockEnabled(report); err != nil {
				return err
			}
		}
		beforeIdx, _, e := m.load(&p.Before)
		if e != nil {
			return e
		}
		previous, e := m.loadLifecycle(&p.Before, beforeIdx)
		if e != nil {
			return e
		}
		want, _ := json.Marshal(m.lifecycleEffects(previous, life))
		got, _ := json.Marshal(p.AssetEffects)
		if string(want) != string(got) {
			return result.Fail("PLAN_STALE", "退役记录与源树不一致", 3)
		}
		return nil
	}
	return m.Store.Apply(ctx, id)
}
func (m *Manager) Recover() (int, error) { return m.Store.Recover() }

func (m *Manager) File(id, relative string) (map[string]any, error) {
	var out map[string]any
	err := m.Store.Locked(func() error {
		if e := m.Store.Pending(); e != nil {
			return e
		}
		var e error
		out, e = m.file(id, relative)
		return e
	})
	return out, err
}
func (m *Manager) file(id, relative string) (map[string]any, error) {
	if e := workstore.SafeRelative(relative); e != nil {
		return nil, e
	}
	idx, profile, e := m.load(nil)
	if e != nil {
		return nil, e
	}
	_, effective, e := m.resolve(nil, idx, profile)
	if e != nil {
		return nil, e
	}
	for _, item := range effective {
		s := item.Skill
		if s.ID != id {
			continue
		}
		if item.Layer != "external" && (s.Source.Mode != "owned" || s.Source.Path != "skills/"+s.ID) {
			return nil, result.Fail("OWNERSHIP_REQUIRED", "不可读取非 owned 包", 4)
		}
		base := s.Source.Path
		if item.ReadOnly {
			base = revisionPath(s)
		}
		data, e := workstore.ReadFile(filepath.Join(m.Store.Root, base, filepath.FromSlash(relative)))
		if e != nil {
			return nil, e
		}
		if item.ReadOnly {
			matches := false
			for _, f := range s.Files {
				if f.Path == relative && f.SHA256 == workstore.Hash(data) && f.Bytes == int64(len(data)) {
					matches = true
				}
			}
			if !matches {
				return nil, result.Fail("REVISION_CORRUPT", "读取的固定文件与批准清单不符", 5)
			}
		}
		kind := "binary"
		content := ""
		if utf8.Valid(data) && !strings.ContainsRune(string(data), 0) {
			kind = "text"
			content = string(data)
		}
		return map[string]any{"path": relative, "type": kind, "bytes": len(data), "sha256": workstore.Hash(data), "content": content, "read_only": item.ReadOnly}, nil
	}
	return nil, result.Fail("SKILL_NOT_FOUND", "技能 id 不存在", 2)
}
