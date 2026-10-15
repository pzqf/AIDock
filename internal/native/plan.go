package native

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

const ownershipFile = ".aidock-ownership.json"

var validID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
var validDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var validRevision = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type Manager struct {
	Source *source.Manager
	Root   string
	probe  *probeOps
}
type Request struct {
	Tools           []string         `json:"tools"`
	CheckID         string           `json:"check_id,omitempty"`
	RuleResolutions []RuleResolution `json:"rule_resolutions,omitempty"`
}
type Ownership struct {
	Version    int            `json:"schema_version"`
	Owner      string         `json:"owner"`
	Scope      string         `json:"scope_id"`
	SourceRoot string         `json:"source_root"`
	Tool       string         `json:"tool"`
	Packages   []OwnedPackage `json:"packages"`
	Rule       *OwnedRule     `json:"rule,omitempty"`
}
type OwnedPackage struct {
	ID       string `json:"id"`
	Name     string `json:"native_name"`
	Revision string `json:"revision"`
	Digest   string `json:"tree_digest"`
}
type Package struct {
	OwnedPackage
	Layer string         `json:"layer"`
	Tree  workstore.Tree `json:"tree"`
}
type Target struct {
	Adapter  Adapter             `json:"adapter"`
	Root     string              `json:"root"`
	Before   workstore.Tree      `json:"before"`
	After    workstore.Tree      `json:"after"`
	Changes  []workstore.Change  `json:"changes"`
	Findings []inventory.Finding `json:"findings"`
	Packages []Package           `json:"packages"`
	Status   string              `json:"status"`
	Rule     *RuleTarget         `json:"rule,omitempty"`
}
type Plan struct {
	Version         int               `json:"schema_version"`
	ID              string            `json:"id"`
	Kind            string            `json:"kind"`
	Created         string            `json:"created"`
	Root            string            `json:"root"`
	Source          source.Export     `json:"source"`
	Targets         []Target          `json:"targets"`
	Digest          string            `json:"digest"`
	ContentDigest   string            `json:"content_digest"`
	Blocked         bool              `json:"blocked"`
	WriteSupported  bool              `json:"write_supported"`
	Evidence        map[string]string `json:"evidence"`
	Boundary        string            `json:"boundary"`
	Qualification   *Qualification    `json:"qualification,omitempty"`
	RuleResolutions []RuleResolution  `json:"rule_resolutions,omitempty"`
}

func New(m *source.Manager, root string) (*Manager, error) {
	if root == "" || m.Owner == "global" {
		return nil, result.Fail("NATIVE_BINDING_REQUIRED", "项目预览需显式 --native-root，与项目 .aidock 的父目录一致", 4)
	}
	r, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	r = filepath.Clean(r)
	if !strings.EqualFold(r, filepath.Dir(m.Store.Root)) {
		return nil, result.Fail("NATIVE_BINDING_MISMATCH", "工具目标必须绑定当前项目 .aidock 的父目录", 2)
	}
	if err := workstore.CheckLinks(r); err != nil {
		return nil, result.Fail("PATH_UNSAFE", err.Error(), 5)
	}
	for _, a := range Adapters() {
		t := filepath.Join(r, filepath.FromSlash(a.Directory))
		protectedRoots := []string{m.Store.Root, m.Store.Local}
		if m.Library != nil {
			protectedRoots = append(protectedRoots, m.Library.Store.Root)
		}
		for _, protected := range protectedRoots {
			if overlaps(t, protected) {
				return nil, result.Fail("INVALID_ROOTS", "工具目标不能与项目源、只读全局库或事务根重叠", 2)
			}
		}
	}
	return &Manager{Source: m, Root: r}, nil
}
func overlaps(a, b string) bool { return workstore.Within(a, b) || workstore.Within(b, a) }

// Include the generated wrapper directory's mode, which is outside the source
// package tree, so chmod on an obsolete output cannot silently be overwritten.
func packageDigest(pkg workstore.Tree, root workstore.Entry) string {
	return workstore.Hash(jsonData(struct {
		Root workstore.Entry `json:"root"`
		Tree workstore.Tree  `json:"tree"`
	}{root, pkg}))
}
func targetPackageDigest(t workstore.Tree, name string) string {
	return packageDigest(subtree(t, name), t.Entries[name])
}
func subtree(t workstore.Tree, prefix string) workstore.Tree {
	out := workstore.Tree{Entries: map[string]workstore.Entry{}}
	if e, ok := t.Entries[prefix]; ok && e.Dir {
		out.Exists = true
	}
	for p, e := range t.Entries {
		if strings.HasPrefix(p, prefix+"/") {
			out.Entries[strings.TrimPrefix(p, prefix+"/")] = e
		}
	}
	return out
}
func remove(t *workstore.Tree, prefix string) {
	for p := range t.Entries {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			delete(t.Entries, p)
		}
	}
}
func strict(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var tail any
	if err := d.Decode(&tail); err != io.EOF {
		return fmt.Errorf("需要单个 JSON 对象")
	}
	return nil
}
func jsonData(v any) []byte { b, _ := json.MarshalIndent(v, "", "  "); return append(b, '\n') }

func (m *Manager) compile(ctx context.Context, req Request) (Plan, error) {
	if err := m.batch().Pending(); err != nil {
		return Plan{}, err
	}
	as, err := selectAdapters(req.Tools)
	if err != nil {
		return Plan{}, err
	}
	decisions, err := validateRuleResolutions(req.RuleResolutions, as)
	if err != nil {
		return Plan{}, err
	}
	bundle, err := m.Source.Export()
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Version: 1, Kind: "native-config", Root: m.Root, Source: bundle, Targets: []Target{},
		Evidence: map[string]string{"L1": "PREVIEW_ONLY", "L2": "UNKNOWN", "L3": "UNKNOWN", "L4": "UNKNOWN"},
		Boundary: "仅编译已保存且启用的完整包，备份观察字节与预览存于 Local Root；源、工具目标未修改。精确版本兼容、工具发现、新会话加载与能力探测均未验证，原生写入保持关闭。"}
	err = m.Source.Store.Locked(func() error {
		if err := m.Source.Store.Pending(); err != nil {
			return err
		}
		current, err := m.Source.Store.Capture(m.Source.Store.Root)
		if err != nil {
			return err
		}
		if workstore.Digest(current) != bundle.Snapshot {
			return result.Fail("PLAN_STALE", "源在编译期间变化，请重新预览", 3)
		}
		for _, a := range as {
			if err := ctx.Err(); err != nil {
				return err
			}
			var resolution *RuleResolution
			for i := range decisions {
				if decisions[i].Tool == a.ID {
					resolution = &decisions[i]
				}
			}
			t, err := m.target(a, bundle, resolution)
			if err != nil {
				return err
			}
			p.Targets = append(p.Targets, t)
			if t.Status == "FAIL" {
				p.Blocked = true
			}
		}
		return nil
	})
	if err != nil {
		return Plan{}, err
	}
	p.RuleResolutions = decisions
	p.ContentDigest = contentDigest(p)
	return p, nil
}

func (m *Manager) target(a Adapter, bundle source.Export, resolutions ...*RuleResolution) (Target, error) {
	root := filepath.Join(m.Root, filepath.FromSlash(a.Directory))
	for _, authority := range bundle.Authorities {
		if overlaps(root, authority) {
			return Target{}, result.Fail("EXTERNAL_TARGET_OVERLAP", "工具目标与只读外部权威源重叠："+root, 4)
		}
	}
	before, err := m.Source.Store.Capture(root)
	if err != nil {
		return Target{}, result.Fail("NATIVE_TARGET_UNSAFE", err.Error(), 5)
	}
	t, err := m.draftTargetWithRules(a, bundle, before, nil, true)
	if err != nil || t.Status == "FAIL" {
		return t, err
	}
	return m.targetRule(t, bundle, resolutions...)
}

func (m *Manager) draftTarget(a Adapter, bundle source.Export, before workstore.Tree) (Target, error) {
	return m.draftTargetPreserving(a, bundle, before, nil)
}

func (m *Manager) draftTargetPreserving(a Adapter, bundle source.Export, before workstore.Tree, keep *Preservation) (Target, error) {
	return m.draftTargetWithRules(a, bundle, before, keep, false)
}
func (m *Manager) draftTargetWithRules(a Adapter, bundle source.Export, before workstore.Tree, keep *Preservation, rules bool) (Target, error) {
	t := Target{Adapter: a, Root: filepath.Join(m.Root, filepath.FromSlash(a.Directory)), Findings: []inventory.Finding{}, Packages: []Package{}, Status: "PASS"}
	add := func(status, code, detail string) {
		t.Findings = append(t.Findings, inventory.Finding{Status: status, Code: code, Detail: detail})
		if status == "FAIL" {
			t.Status = "FAIL"
		} else if t.Status != "FAIL" && status == "UNKNOWN" {
			t.Status = "UNKNOWN"
		}
	}
	for _, check := range bundle.Preflight.ProfileChecks {
		if rules && check.Code == "PROFILE_RULES_PENDING" {
			continue
		}
		// This adapter only writes complete skill packages. Never pretend that
		// an unresolved Profile or uncompiled rules/MCP settings were applied.
		add("FAIL", check.Code, check.Detail)
	}
	if !rules && len(bundle.Rules) > 0 {
		add("FAIL", "RULES_TARGET_REQUIRED", "技能目录暂存未包含规则文件，不能忽略共享规则")
	}
	for _, root := range bundle.Authorities {
		if overlaps(t.Root, root) {
			return t, result.Fail("EXTERNAL_TARGET_OVERLAP", "工具目标与只读外部权威源重叠："+t.Root, 4)
		}
	}
	t.Before = before
	t.After = workstore.Clone(t.Before)
	own := Ownership{Version: 1, Owner: bundle.Owner, Scope: bundle.Scope, SourceRoot: bundle.Root, Tool: a.ID, Packages: []OwnedPackage{}}
	previous := map[string]OwnedPackage{}
	if e, ok := t.Before.Entries[ownershipFile]; ok {
		b, err := m.Source.Store.Data(e)
		if err != nil {
			return t, err
		}
		if err := strict(b, &own); err != nil || own.Version != 1 || own.Owner != bundle.Owner || own.Scope != bundle.Scope || own.SourceRoot != bundle.Root || own.Tool != a.ID {
			add("FAIL", "NATIVE_OWNERSHIP_INVALID", "所有权声明无效或不属于当前项目/作用域/工具；不接管目标")
			return t, nil
		}
		if !rules && own.Rule != nil {
			add("FAIL", "RULES_TARGET_REQUIRED", "目标含规则归属；仅技能目录的暂存不能协调规则文件")
		}
		ids := map[string]bool{}
		for _, item := range own.Packages {
			if !validID.MatchString(item.ID) || !validName.MatchString(item.Name) || !validRevision.MatchString(item.Revision) || !validDigest.MatchString(item.Digest) || previous[item.Name].ID != "" || ids[item.ID] {
				add("FAIL", "NATIVE_OWNERSHIP_INVALID", "所有权清单含无效或重复包身份")
				return t, nil
			}
			ids[item.ID] = true
			previous[item.Name] = item
		}
	}
	// Excluding a previous native name protects its identity too, even when the
	// saved source has since been renamed. Otherwise one ID could get two names
	// in the next ownership manifest while the excluded old name is preserved.
	protectedIDs := map[string]bool{}
	for name, item := range previous {
		if keep.Matches(item.ID, name) {
			protectedIDs[item.ID] = true
		}
	}
	protected := func(id, name string) bool { return keep.Matches(id, name) || protectedIDs[id] }
	// Every recorded package is checked before replacements or removals. Empty
	// dirs, binary bytes and executable modes are included in the tree digest.
	for _, name := range sortedKeys(previous) {
		item := previous[name]
		if protected(item.ID, name) {
			add("UNKNOWN", "LOCAL_EXCLUDED_PRESERVED", name+" 属于本机排除项；现有目录与原归属记录保留，不修复人工漂移")
			continue
		}
		pkg := subtree(t.Before, name)
		if packageDigest(pkg, t.Before.Entries[name]) != item.Digest {
			add("FAIL", "NATIVE_TARGET_DRIFT", name+" 已与上次归属字节/目录/模式不一致；保留人工修改")
		} else {
			remove(&t.After, name)
		}
	}
	checks := map[string]source.SkillCheck{}
	for _, c := range bundle.Preflight.Skills {
		checks[c.ID] = c
	}
	newOwnership := own
	newOwnership.Packages = []OwnedPackage{}
	for _, name := range sortedKeys(previous) {
		if item := previous[name]; protected(item.ID, name) {
			newOwnership.Packages = append(newOwnership.Packages, item)
		}
	}
	for _, pkg := range bundle.Packages {
		s := pkg.Skill
		if protected(s.ID, s.Name) {
			if previous[s.Name].ID == "" {
				add("UNKNOWN", "LOCAL_EXCLUDED_PRESERVED", s.Name+" 属于本机排除项；不创建或接管工具包")
			}
			continue
		}
		item := OwnedPackage{ID: s.ID, Name: s.Name, Revision: s.Revision, Digest: packageDigest(pkg.Tree, workstore.Directory())}
		t.Packages = append(t.Packages, Package{OwnedPackage: item, Layer: pkg.Layer, Tree: pkg.Tree})
		newOwnership.Packages = append(newOwnership.Packages, item)
		if c := checks[s.ID]; c.Status == "FAIL" {
			add("FAIL", "SKILL_PREFLIGHT_FAILED", s.Name+" 的已保存包/依赖/兼容有静态阻断项")
		} else {
			for _, f := range c.Checks {
				if f.Status != "PASS" {
					add(f.Status, f.Code, s.Name+"："+f.Detail)
				}
			}
		}
		if reserved(a, s.Name) {
			add("FAIL", "NATIVE_RESERVED_NAME", s.Name+" 是该工具保留名称")
		}
		if doc, ok := pkg.Tree.Entries["SKILL.md"]; ok && !doc.Dir {
			b, err := m.Source.Store.Data(doc)
			if err != nil {
				return t, err
			}
			parts := strings.SplitN(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n---", 2)
			if len(parts) != 2 {
				add("FAIL", "NATIVE_FRONTMATTER_INVALID", s.Name+" 缺少原生元数据")
			} else {
				v, err := config.Parse([]byte(strings.TrimPrefix(parts[0], "---\n")))
				front, ok := v.(map[string]any)
				if err != nil || !ok {
					add("FAIL", "NATIVE_FRONTMATTER_INVALID", s.Name+" 元数据格式无效")
				} else {
					for _, key := range a.Required {
						value, _ := front[key].(string)
						if strings.TrimSpace(value) == "" {
							add("FAIL", "NATIVE_FRONTMATTER_INVALID", s.Name+" 缺少 "+key)
						}
					}
					keys := []string{}
					for key := range front {
						if key != "name" && key != "description" {
							keys = append(keys, key)
						}
					}
					sort.Strings(keys)
					if len(keys) > 0 {
						add("UNKNOWN", "NATIVE_METADATA_SEMANTICS_UNVERIFIED", s.Name+" 保留原生字段 "+strings.Join(keys, ", ")+"；跨工具语义未验证")
					}
				}
			}
		}
		if _, ok := pkg.Tree.Entries[".claude-plugin/plugin.json"]; ok {
			add("FAIL", "NATIVE_PLUGIN_PACKAGE_UNSUPPORTED", s.Name+" 含插件入口；技能预览不批准插件/钩子/MCP")
		}
		// Existing unowned entries are never adopted, even when bytes match.
		collision := false
		for p := range t.Before.Entries {
			if strings.EqualFold(strings.SplitN(p, "/", 2)[0], s.Name) && previous[s.Name].ID == "" {
				collision = true
			}
		}
		if old := previous[s.Name]; old.ID != "" && old.ID != s.ID {
			collision = true
		}
		if collision {
			add("FAIL", "NATIVE_TARGET_COLLISION", s.Name+" 的目标名称已被未归属或不同身份资产占用")
			continue
		}
		if old := previous[s.Name]; old.ID != "" && targetPackageDigest(t.Before, s.Name) != old.Digest {
			continue
		}
		t.After.Exists = true
		t.After.Entries[s.Name] = workstore.Directory()
		for p, e := range pkg.Tree.Entries {
			t.After.Entries[s.Name+"/"+p] = e
		}
	}
	// A blocked target has no actionable candidate changes. Diagnostics and
	// complete desired package manifests remain available for review.
	if t.Status == "FAIL" {
		t.After = workstore.Clone(t.Before)
	} else if len(previous) > 0 || len(newOwnership.Packages) > 0 {
		sort.Slice(newOwnership.Packages, func(i, j int) bool { return newOwnership.Packages[i].Name < newOwnership.Packages[j].Name })
		if _, exists := t.Before.Entries[ownershipFile]; !exists || !bytes.Equal(jsonData(own), jsonData(newOwnership)) {
			if err := m.Source.Store.PutFile(&t.After, ownershipFile, jsonData(newOwnership), 0644); err != nil {
				return t, err
			}
		}
	}
	t.Changes = workstore.Changes(t.Before, t.After)
	for _, c := range t.Changes {
		if c.Before != nil && c.After != nil && c.Before.Dir != c.After.Dir {
			add("FAIL", "NATIVE_STRUCTURE_CHANGE_UNSUPPORTED", c.Path+" 的文件/目录类型互换尚未通过恢复验收；目标保留")
		}
	}
	if t.Status == "FAIL" {
		t.After = workstore.Clone(t.Before)
		t.Changes = []workstore.Change{}
	}
	add("UNKNOWN", "TOOL_VERSION_UNVERIFIED", "文件契约已有精确版本验收；当前预览需显式绑定工具发现检查，不能以观察版本替代当前证据")
	add("UNKNOWN", "DISCOVERY_SCOPE_UNVERIFIED", a.Limitations)
	return t, nil
}
func sortedKeys(v map[string]OwnedPackage) []string {
	out := []string{}
	for k := range v {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func contentDigest(p Plan) string {
	p.ID = ""
	p.Created = ""
	p.Digest = ""
	p.ContentDigest = ""
	return workstore.Hash(jsonData(p))
}
func planDigest(p Plan) string { p.Digest = ""; return workstore.Hash(jsonData(p)) }
func (m *Manager) file(id string) string {
	return filepath.Join(m.Source.Store.Local, "native-previews", workstore.Hash([]byte(strings.ToLower(m.Source.Store.Root))), "plans", id+".json")
}

func (m *Manager) Plan(ctx context.Context, req Request) (Plan, error) {
	p, err := m.compile(ctx, req)
	if err != nil {
		return p, err
	}
	if req.CheckID != "" {
		if err = m.qualify(&p, req.CheckID); err != nil {
			return p, err
		}
		p.ContentDigest = contentDigest(p)
	}
	p.ID = workstore.ID()
	p.Created = time.Now().UTC().Format(time.RFC3339)
	p.Digest = planDigest(p)
	err = m.Source.Store.Locked(func() error { return save(m.file(p.ID), jsonData(p)) })
	return p, err
}
func save(p string, b []byte) error {
	if err := workstore.CheckLinks(p); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".preview-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), p)
}
func (m *Manager) load(id, digest string) (Plan, error) {
	var p Plan
	if !validID.MatchString(id) {
		return p, result.Fail("INVALID_INPUT", "需要当前绑定的计划 UUID", 2)
	}
	b, err := workstore.ReadFile(m.file(id))
	if os.IsNotExist(err) {
		if sourcePlan, e := m.Source.Store.LoadPlan(id); e == nil && sourcePlan.Kind != "native-config" {
			return p, result.Fail("PLAN_KIND_MISMATCH", "此命令只接受 native-config 计划", 2)
		}
		return p, result.Fail("PLAN_NOT_FOUND", "当前绑定未找到此原生预览", 2)
	}
	if err != nil {
		return p, err
	}
	if err = strict(b, &p); err != nil {
		return p, result.Fail("PLAN_CORRUPT", err.Error(), 5)
	}
	if p.Kind != "native-config" {
		return p, result.Fail("PLAN_KIND_MISMATCH", "此命令只接受 native-config 计划", 2)
	}
	if p.Version != 1 || p.ID != id || p.Root != m.Root || p.Source.Root != m.Source.Store.Root || p.Source.Owner != m.Source.Owner || p.Source.Scope != m.Source.Scope || p.Source.Copy != m.Source.Copy || p.Digest != planDigest(p) || p.ContentDigest != contentDigest(p) {
		return p, result.Fail("PLAN_CORRUPT", "计划身份或摘要不匹配", 5)
	}
	if digest == "" || digest != p.Digest {
		return p, result.Fail("PLAN_CONFIRMATION_REQUIRED", "需要预览的完整 digest", 4)
	}
	return p, nil
}

// Diff reobserves saved inputs; it never applies, even when the preview is fresh.
func (m *Manager) Diff(ctx context.Context, id, digest string) (Plan, error) {
	p, err := m.load(id, digest)
	if err != nil {
		return p, err
	}
	ids := []string{}
	for _, t := range p.Targets {
		ids = append(ids, t.Adapter.ID)
	}
	current, err := m.compile(ctx, Request{Tools: ids, RuleResolutions: p.RuleResolutions})
	if err != nil {
		return p, err
	}
	if p.Qualification != nil {
		if err = m.qualify(&current, p.Qualification.CheckID); err != nil {
			return p, err
		}
		current.ContentDigest = contentDigest(current)
	}
	if current.ContentDigest != p.ContentDigest {
		return p, result.Fail("PLAN_STALE", "源、工具目标、外部登记或适配器证据已变化，请重新预览", 3)
	}
	return p, nil
}
func (m *Manager) Apply(ctx context.Context, id, digest string) error {
	p, err := m.Diff(ctx, id, digest)
	if err != nil {
		return err
	}
	if p.Blocked {
		return result.Fail("NATIVE_PREFLIGHT_FAILED", "存在目标冲突或静态阻断项", 5)
	}
	if !p.WriteSupported {
		return result.Fail("NATIVE_WRITE_UNAVAILABLE", "请先显式完成当前保存版本的工具发现检查，再用 --check 生成可应用预览；只安装文件，不批准脚本执行", 4)
	}
	return m.applyNative(ctx, p)
}
