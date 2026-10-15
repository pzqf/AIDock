package source

import (
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type SkillCheck struct {
	ID       string              `json:"id"`
	Name     string              `json:"native_name"`
	Revision string              `json:"revision"`
	Enabled  bool                `json:"enabled"`
	Status   string              `json:"status"`
	Checks   []inventory.Finding `json:"checks"`
}
type Preflight struct {
	Owner         string              `json:"owner"`
	Scope         string              `json:"scope_id"`
	Platform      string              `json:"platform"`
	Status        string              `json:"status"`
	Skills        []SkillCheck        `json:"skills"`
	Counts        map[string]int      `json:"counts"`
	NativeReady   bool                `json:"native_ready"`
	Candidate     bool                `json:"candidate"`
	Boundary      string              `json:"boundary"`
	ProfileChecks []inventory.Finding `json:"profile_checks,omitempty"`
}

// Check never executes a version command, skill script or generator, and never
// searches another workspace. Local observation blobs and the lock are allowed;
// the selected Config Root remains byte-for-byte unchanged.
func (m *Manager) Check() (Preflight, error) {
	var report Preflight
	err := m.Store.Locked(func() error {
		if err := m.Store.Pending(); err != nil {
			return err
		}
		t, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		idx, profile, err := m.load(&t)
		if err != nil {
			return err
		}
		report, err = m.preflight(t, idx, profile)
		return err
	})
	return report, err
}

func statusRank(s string) int {
	switch s {
	case "FAIL":
		return 3
	case "UNKNOWN":
		return 2
	case "WARN":
		return 1
	}
	return 0
}
func assetKey(owner, scope, id string) string { return owner + "/" + scope + "/" + id }

func (m *Manager) preflight(t workstore.Tree, idx Index, profile map[string]any) (Preflight, error) {
	r := Preflight{Owner: m.Owner, Scope: m.Scope, Platform: runtime.GOOS, Status: "PASS", Skills: []SkillCheck{}, Counts: map[string]int{"PASS": 0, "WARN": 0, "UNKNOWN": 0, "FAIL": 0},
		Boundary: "仅检查当前绑定的完整包、声明依赖与平台；不执行脚本、不读取其他项目。工具版本、插件安装锁、项目条件、会话加载和执行隔离尚未验证。"}
	_, effective, err := m.resolve(&t, idx, profile)
	if err != nil {
		return r, err
	}
	texts, err := m.sourceRules(t, profile)
	if err != nil {
		return r, err
	}
	resolved := profileWithRules(profile, texts)
	r.ProfileChecks = profileChecks(resolved, effective)
	_, unmanaged, err := m.ruleLibrary(t)
	if err != nil {
		return r, err
	}
	if len(unmanaged) > 0 {
		r.ProfileChecks = append(r.ProfileChecks, inventory.Finding{Status: "WARN", Code: "RULE_FILES_UNMANAGED", Detail: "未登记规则文件不会进入工具编译：" + strings.Join(unmanaged, "、")})
	}
	for _, check := range r.ProfileChecks {
		if statusRank(check.Status) > statusRank(r.Status) {
			r.Status = check.Status
		}
	}
	life, err := m.loadLifecycle(&t, idx)
	if err != nil {
		return r, err
	}
	sort.Slice(effective, func(i, j int) bool { return effective[i].Skill.ID < effective[j].Skill.ID })
	byKey := map[string]int{}
	enabled := map[string]bool{}
	for _, id := range switches(profile)["enable"].([]any) {
		enabled[id.(string)] = true
	}
	for _, id := range switches(profile)["disable"].([]any) {
		enabled[id.(string)] = false
	}
	for i, item := range effective {
		byKey[assetKey(item.Owner, item.Scope, item.Skill.ID)] = i
		r.Skills = append(r.Skills, SkillCheck{ID: item.Skill.ID, Name: item.Skill.Name, Revision: item.Skill.Revision, Enabled: enabled[item.Skill.ID], Status: "PASS", Checks: []inventory.Finding{}})
	}
	add := func(i int, status, code, detail string) {
		r.Skills[i].Checks = append(r.Skills[i].Checks, inventory.Finding{Status: status, Code: code, Detail: detail})
	}
	edges := make([][]int, len(effective))
	for i, item := range effective {
		s := item.Skill
		pkg := subtree(t, s.Source.Path)
		if item.ReadOnly {
			pkg = subtree(t, revisionPath(s))
		}
		if err := m.checkPackage(s, pkg); err != nil {
			add(i, "FAIL", "SOURCE_PACKAGE_DRIFT", err.Error())
		} else {
			files := map[string][]byte{}
			for p, e := range pkg.Entries {
				if e.Dir {
					continue
				}
				data, err := m.Store.Data(e)
				if err != nil {
					return r, err
				}
				files[p] = data
			}
			_, findings, err := inventory.InspectPackage(files)
			if err != nil {
				add(i, "FAIL", "PACKAGE_INVALID", err.Error())
			} else {
				add(i, "PASS", "PACKAGE_CLOSURE", "完整包字节、文件清单与固定修订一致")
				for _, f := range findings {
					add(i, f.Status, f.Code, f.Detail)
				}
			}
		}
		if pins := switches(profile)["pins"].(map[string]any); pins[s.ID] != nil && pins[s.ID] != s.Revision {
			add(i, "FAIL", "PROFILE_PIN_MISMATCH", "Profile pin 与当前有效包的固定修订不同")
		}
		if item.Status == "BASE_STALE" {
			add(i, "FAIL", "BASE_STALE", "上层已选 pin 与覆盖基础不同，需协调后应用")
		}
		if retired(life, s.ID) {
			add(i, "WARN", "SKILL_RETIRED", "已退役，保留完整包供恢复，不能用于新应用")
		}
		seen := map[string]bool{}
		for _, v := range array(s.Dependencies["skills"]) {
			d := v.(map[string]any)
			key := assetKey(d["owner"].(string), d["scope_id"].(string), d["id"].(string))
			required := d["required"].(bool)
			status := "FAIL"
			if !required {
				status = "WARN"
			}
			if seen[key] {
				add(i, "FAIL", "DEPENDENCY_DUPLICATE", "重复的技能依赖身份："+key)
				continue
			}
			seen[key] = true
			at, ok := byKey[key]
			if !ok {
				localScope := d["owner"] == m.Owner && d["scope_id"] == m.Scope || d["owner"] == "global" && d["scope_id"] == "global"
				if !localScope && required {
					add(i, "UNKNOWN", "DEPENDENCY_SCOPE_UNREGISTERED", "依赖范围未登记："+key)
				} else {
					add(i, status, "DEPENDENCY_MISSING", "当前有效集合中缺少"+dependencyKind(required)+"依赖："+key)
				}
				continue
			}
			if required {
				edges[i] = append(edges[i], at)
			}
			target := effective[at].Skill
			if target.Revision != d["revision"] {
				add(i, status, "DEPENDENCY_REVISION_MISMATCH", key+"：需要 "+d["revision"].(string)+"，实际 "+target.Revision)
			} else if retired(life, target.ID) {
				add(i, status, "DEPENDENCY_RETIRED", key+" 已退役")
			} else if enabled[s.ID] && !enabled[target.ID] {
				add(i, status, "DEPENDENCY_DISABLED", key+" 在本层未启用；不会自动启用依赖")
			} else {
				add(i, "PASS", "DEPENDENCY_PINNED", key+" @ "+target.Revision)
			}
		}
		for _, kind := range []string{"plugins", "runtime", "project_files"} {
			for _, v := range array(s.Dependencies[kind]) {
				d := v.(map[string]any)
				status := "UNKNOWN"
				if !d["required"].(bool) {
					status = "WARN"
				}
				add(i, status, "DEPENDENCY_"+strings.ToUpper(kind)+"_UNVERIFIED", fmt.Sprintf("%s 依赖尚无绑定证据：%v；未安装或执行", kind, d))
			}
		}
		if platforms := array(s.Compatibility["platforms"]); len(platforms) > 0 {
			ok := false
			for _, p := range platforms {
				if p == runtime.GOOS {
					ok = true
				}
			}
			if ok {
				add(i, "PASS", "PLATFORM_COMPATIBLE", runtime.GOOS)
			} else {
				add(i, "FAIL", "PLATFORM_INCOMPATIBLE", fmt.Sprint("当前平台 ", runtime.GOOS, " 不在声明中：", platforms))
			}
		}
		if tools, ok := s.Compatibility["tools"].(map[string]any); ok {
			keys := []string{}
			for k := range tools {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				add(i, "UNKNOWN", "TOOL_VERSION_UNVERIFIED", fmt.Sprint(k, " 要求 ", tools[k], "；工具版本与支持白名单尚未验证"))
			}
		}
		for _, kind := range []string{"stack", "project_files"} {
			if len(array(s.Compatibility[kind])) > 0 {
				add(i, "UNKNOWN", "PROJECT_COMPATIBILITY_UNVERIFIED", fmt.Sprintf("项目 %s 条件未绑定证据：%v", kind, s.Compatibility[kind]))
			}
		}
		for _, script := range array(s.Requirements["scripts"]) {
			p := script.(string)
			if e, ok := pkg.Entries[p]; !ok || e.Dir {
				add(i, "FAIL", "SCRIPT_MISSING", "声明的包内脚本不存在："+p)
			} else {
				add(i, "PASS", "SCRIPT_PRESENT", "已保留脚本原字节，未执行："+p)
			}
			if s.Requirements["exec"] != true {
				add(i, "FAIL", "SCRIPT_EXEC_UNDECLARED", "声明脚本需要明确 exec: true："+p)
			}
		}
		for _, kind := range []string{"fs", "net", "exec", "secrets"} {
			v, ok := s.Requirements[kind]
			if !ok || kind == "exec" && v == false || kind == "secrets" && len(array(v)) == 0 {
				continue
			}
			add(i, "UNKNOWN", "EXECUTION_REQUIREMENT_UNVERIFIED", kind+" 需求尚未验证授权/隔离；声明不是执行许可")
		}
		for p, e := range pkg.Entries {
			if !e.Dir && strings.HasPrefix(p, "scripts/") {
				add(i, "UNKNOWN", "DYNAMIC_DEPENDENCIES_UNVERIFIED", "包内脚本的动态依赖尚未验证，未执行")
				break
			}
		}
	}
	// Detect cycles even when a declared edge also has a stale pin. A present
	// optional dependency never becomes a required edge or enables another asset.
	for start := range edges {
		seen := map[int]bool{}
		var returns func(int) bool
		returns = func(at int) bool {
			for _, next := range edges[at] {
				if next == start {
					return true
				}
				if !seen[next] {
					seen[next] = true
					if returns(next) {
						return true
					}
				}
			}
			return false
		}
		if returns(start) {
			add(start, "FAIL", "DEPENDENCY_CYCLE", "必需技能依赖形成环，需显式修改声明")
		}
	}
	for i := range r.Skills {
		s := &r.Skills[i]
		sort.Slice(s.Checks, func(a, b int) bool {
			x, y := s.Checks[a], s.Checks[b]
			if x.Code != y.Code {
				return x.Code < y.Code
			}
			return x.Detail < y.Detail
		})
		for _, f := range s.Checks {
			if statusRank(f.Status) > statusRank(s.Status) {
				s.Status = f.Status
			}
		}
		r.Counts[s.Status]++
		if statusRank(s.Status) > statusRank(r.Status) {
			r.Status = s.Status
		}
	}
	return r, nil
}
func array(v any) []any { a, _ := v.([]any); return a }
func dependencyKind(required bool) string {
	if required {
		return "必需"
	}
	return "可选"
}
func blockEnabled(r Preflight) error {
	for _, check := range r.ProfileChecks {
		if check.Status == "FAIL" {
			return result.Fail("PROFILE_RESOLUTION_FAILED", check.Detail, 5)
		}
	}
	for _, s := range r.Skills {
		if s.Enabled && s.Status == "FAIL" {
			e := result.Fail("SKILL_PREFLIGHT_FAILED", "启用集合存在静态阻断项；先保存修正声明或协调依赖，再预览启用", 5)
			e.Recovery = r
			return e
		}
	}
	return nil
}
