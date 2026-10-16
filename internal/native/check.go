package native

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/privatefs"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

type probeOps struct {
	resolve     func(string) (toolBinary, error)
	version     func(context.Context, toolBinary, string, string, string) (string, error)
	discover    func(context.Context, toolBinary, string, string, string) (discovery, error)
	rules       func(context.Context, toolBinary, string, string) ([]string, error)
	claudeRules func(context.Context, toolBinary, string, string) ([]string, error)
}

func (m *Manager) probes() probeOps {
	if m.probe != nil {
		return *m.probe
	}
	return probeOps{resolve: resolveBinary, version: toolVersion, discover: probeProcess, rules: probeRuleProcess, claudeRules: probeClaudeRuleProcess}
}

type CheckRequest struct {
	ApplicationScope string           `json:"application_scope,omitempty"`
	Tools            []string         `json:"tools"`
	Probe            bool             `json:"probe"`
	RuleProbe        bool             `json:"rule_probe,omitempty"`
	RuleResolutions  []RuleResolution `json:"rule_resolutions,omitempty"`
}
type SkillDiscovery struct {
	ID         string `json:"id"`
	Name       string `json:"native_name"`
	Revision   string `json:"revision"`
	Status     string `json:"status"`
	Detail     string `json:"detail"`
	L1         string `json:"L1,omitempty"`
	L2         string `json:"L2,omitempty"`
	L3         string `json:"L3,omitempty"`
	L4         string `json:"L4,omitempty"`
	FileDetail string `json:"file_detail,omitempty"`
}
type ToolCheck struct {
	Tool         string              `json:"tool"`
	Target       string              `json:"target"`
	Executable   string              `json:"executable,omitempty"`
	BinaryDigest string              `json:"binary_digest,omitempty"`
	Version      string              `json:"version,omitempty"`
	Installed    bool                `json:"installed"`
	L1           string              `json:"L1"`
	L2           string              `json:"L2"`
	L3           string              `json:"L3"`
	L4           string              `json:"L4"`
	Skills       []SkillDiscovery    `json:"skills"`
	Findings     []inventory.Finding `json:"findings"`
	Rules        *RuleLoad           `json:"rules,omitempty"`
}
type CheckReport struct {
	ApplicationScope string              `json:"application_scope,omitempty"`
	ExcludedChecks   []inventory.Finding `json:"excluded_checks,omitempty"`
	Version          int                 `json:"schema_version"`
	ID               string              `json:"id"`
	Created          string              `json:"created"`
	Expires          string              `json:"expires"`
	Root             string              `json:"root"`
	Owner            string              `json:"owner"`
	Scope            string              `json:"scope_id"`
	Copy             string              `json:"copy,omitempty"`
	InputDigest      string              `json:"input_digest"`
	SourceSnapshot   string              `json:"source_snapshot"`
	Probe            bool                `json:"probe"`
	RuleProbe        bool                `json:"rule_probe,omitempty"`
	RuleResolutions  []RuleResolution    `json:"rule_resolutions,omitempty"`
	Status           string              `json:"status"`
	Tools            []ToolCheck         `json:"tools"`
	Digest           string              `json:"digest"`
	Stale            bool                `json:"stale"`
	StaleReason      string              `json:"stale_reason,omitempty"`
	Boundary         string              `json:"boundary"`
}

// InspectCurrent reads both supported tools' current skill/rule files without
// starting processes or saving a new check record. It does not replace the last
// explicit probe. Local observation blobs and locks use the ordinary compiler.
func (m *Manager) InspectCurrent(ctx context.Context) (CheckReport, error) {
	req := Request{Tools: []string{"claude-code", "codex-cli"}, ApplicationScope: SkillsRulesScope}
	p, err := m.compile(ctx, req)
	if err != nil {
		return CheckReport{}, err
	}
	r, err := m.inspectPlan(ctx, p, false)
	if err != nil {
		return r, err
	}
	now, err := m.compile(ctx, req)
	if err != nil || now.ContentDigest != p.ContentDigest {
		r.Stale, r.StaleReason = true, "读取期间项目输入发生变化或无法重新核对，请刷新诊断"
	}
	return r, nil
}

func checkDigest(r CheckReport) string {
	r.Digest, r.StaleReason, r.Stale = "", "", false
	return workstore.Hash(jsonData(r))
}
func (m *Manager) checksDir() string {
	identity := m.Source.Owner + "\x00" + m.Source.Scope + "\x00" + m.Source.Copy
	return filepath.Join(m.Source.Store.Local, "native-checks", workstore.Hash([]byte(strings.ToLower(m.Source.Store.Root))), workstore.Hash([]byte(identity)))
}

// Check's default path starts no processes. The explicit probe copies only
// approved, enabled package blobs to a fresh project with empty tool homes.
// It never initializes a tool against the user's bound project directory.
func (m *Manager) Check(ctx context.Context, req CheckRequest) (CheckReport, error) {
	if req.RuleProbe && !req.Probe {
		return CheckReport{}, result.Fail("INVALID_INPUT", "规则加载验证须同时显式启用 --probe", 2)
	}
	ops := m.probes()
	p, err := m.compile(ctx, Request{Tools: req.Tools, RuleResolutions: req.RuleResolutions, ApplicationScope: req.ApplicationScope})
	if err != nil {
		return CheckReport{}, err
	}
	r, err := m.inspectPlan(ctx, p, req.Probe, req.RuleProbe)
	if err != nil {
		return r, err
	}
	// A change during a probe isn't accepted as evidence for the current state.
	current, err := m.compile(ctx, Request{Tools: req.Tools, RuleResolutions: req.RuleResolutions, ApplicationScope: req.ApplicationScope})
	if err != nil {
		return r, err
	}
	if current.ContentDigest != r.InputDigest {
		return r, result.Fail("PLAN_STALE", "检查期间源或目标已变化，请重新检查", 3)
	}
	for _, t := range r.Tools {
		if t.Installed {
			b, err := ops.resolve(t.Tool)
			if err != nil || b.Path != t.Executable || b.Digest != t.BinaryDigest {
				return r, result.Fail("PLAN_STALE", "检查期间工具程序已变化，请重新检查", 3)
			}
		}
	}
	r.Digest = checkDigest(r)
	err = m.Source.Store.Locked(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.Source.Store.Pending(); err != nil {
			return err
		}
		saved, err := m.Source.Store.Capture(m.Source.Store.Root)
		if err != nil {
			return err
		}
		if workstore.Digest(saved) != r.SourceSnapshot {
			return result.Fail("PLAN_STALE", "保存检查记录前技能源已变化", 3)
		}
		layers, err := m.Source.ConfigurationDigest()
		if err != nil {
			return err
		}
		if layers != current.Source.ConfigurationDigest {
			return result.Fail("PLAN_STALE", "保存检查记录前配置层已变化", 3)
		}
		for _, target := range current.Targets {
			tree, err := m.Source.Store.Capture(target.Root)
			if err != nil {
				return err
			}
			if workstore.Digest(tree) != workstore.Digest(target.Before) {
				return result.Fail("PLAN_STALE", "保存检查记录前工具目标已变化", 3)
			}
			if target.Rule != nil {
				tree, err := m.Source.Store.CaptureFile(filepath.Join(m.Root, target.Rule.Path))
				if err != nil {
					return err
				}
				if workstore.Digest(tree) != workstore.Digest(target.Rule.Before) {
					return result.Fail("PLAN_STALE", "保存检查记录前规则目标已变化", 3)
				}
				if target.Rule.Override != nil {
					tree, err := m.Source.Store.CaptureFile(filepath.Join(m.Root, target.Rule.Override.Path))
					if err != nil {
						return err
					}
					if workstore.Digest(tree) != workstore.Digest(target.Rule.Override.Before) {
						return result.Fail("PLAN_STALE", "保存检查前规则优先级输入已变化", 3)
					}
				}
			}
		}
		return save(filepath.Join(m.checksDir(), "reports", r.ID+".json"), jsonData(r))
	})
	return r, err
}

func (m *Manager) inspectPlan(ctx context.Context, p Plan, probe bool, ruleProbes ...bool) (CheckReport, error) {
	ops := m.probes()
	var err error
	now := time.Now().UTC()
	r := CheckReport{Version: 1, ID: workstore.ID(), Created: now.Format(time.RFC3339Nano), Expires: now.Add(24 * time.Hour).Format(time.RFC3339Nano), Root: m.Root, Owner: m.Source.Owner, Scope: m.Source.Scope, Copy: m.Source.Copy, InputDigest: p.ContentDigest, SourceSnapshot: p.Source.Snapshot, Probe: probe, Status: "UNKNOWN", Tools: []ToolCheck{}, Boundary: "L1 核对当前项目工具文件；L2 只验证隔离项目中的完整包发现。探测使用空工具配置，不代表实际项目配置、用户同名技能、旧会话或模型已使用正文。L3/L4 保持 UNKNOWN；不执行技能/脚本，不写入项目源或工具目标。检查记录 24 小时后过期，源、目标、适配器或工具程序变化也使记录过期。"}
	r.RuleProbe = len(ruleProbes) > 0 && ruleProbes[0]
	r.RuleResolutions = p.RuleResolutions
	r.ApplicationScope, r.ExcludedChecks = p.ApplicationScope, p.ExcludedChecks
	r.Boundary = applicationBoundary(p.ApplicationScope) + r.Boundary
	if r.RuleProbe {
		r.Boundary += " 规则检查另在空配置的独立项目建立临时会话，仅核对工具返回的指令来源路径和候选文件字节；不发送模型请求，不证明模型遵从、实际项目优先级或既有会话加载。"
	}
	var run string
	if probe {
		// CreateProcess on Windows rejects some long cwd paths even when Go's
		// filesystem APIs accept them. Keep disposable projects separate from
		// the hashed persistent identity directories.
		parent := filepath.Join(m.Source.Store.Local, "native-probes")
		if m.probeLocal != "" {
			parent = filepath.Join(m.probeLocal, "native-probes")
			if err = privatefs.Ensure(parent); err != nil {
				return r, err
			}
		}
		if err = workstore.CheckLinks(parent); err != nil {
			return r, err
		}
		if err = os.MkdirAll(parent, 0700); err != nil {
			return r, err
		}
		run, err = os.MkdirTemp(parent, "probe-")
		if err != nil {
			return r, err
		}
		defer os.RemoveAll(run) // generated child of the checked Local Root
	}
	for _, target := range p.Targets {
		if err = ctx.Err(); err != nil {
			return r, err
		}
		t := ToolCheck{Tool: target.Adapter.ID, Target: target.Root, L1: "NOT_APPLIED", L2: "UNKNOWN", L3: "UNKNOWN", L4: "UNKNOWN", Skills: []SkillDiscovery{}, Findings: []inventory.Finding{}}
		if target.Status == "FAIL" {
			t.L1, r.Status = "FAIL", "FAIL"
		} else if workstore.Digest(target.Before) == workstore.Digest(target.After) && (target.Rule == nil || workstore.Digest(target.Rule.Before) == workstore.Digest(target.Rule.After)) {
			t.L1 = "PASS"
		}
		for _, finding := range target.Findings {
			if finding.Code != "TOOL_VERSION_UNVERIFIED" && finding.Code != "DISCOVERY_SCOPE_UNVERIFIED" {
				t.Findings = append(t.Findings, finding)
			}
		}
		binary, e := ops.resolve(t.Tool)
		if e == nil {
			t.Installed, t.Executable, t.BinaryDigest = true, binary.Path, binary.Digest
		}
		for _, pkg := range target.Packages {
			l1, detail := m.skillFileStatus(target, pkg)
			t.Skills = append(t.Skills, SkillDiscovery{ID: pkg.ID, Name: pkg.Name, Revision: pkg.Revision, Status: "UNKNOWN", Detail: "尚未执行隔离发现检查", L1: l1, L2: "UNKNOWN", L3: "UNKNOWN", L4: "UNKNOWN", FileDetail: detail})
		}
		if e != nil {
			t.Findings = append(t.Findings, inventory.Finding{Status: "UNKNOWN", Code: "TOOL_NOT_AVAILABLE", Detail: "未找到可直接运行的工具程序；未启动 shell 包装器"})
		} else if probe && target.Status != "FAIL" {
			project, home := filepath.Join(run, t.Tool, "project"), filepath.Join(run, t.Tool, "home")
			if e = m.prepareProbe(project, home, target); e != nil {
				return r, e
			}
			t.Version, e = ops.version(ctx, binary, t.Tool, project, home)
			if e != nil {
				t.Findings = append(t.Findings, inventory.Finding{Status: "UNKNOWN", Code: "TOOL_VERSION_FAILED", Detail: "版本探测失败或超时；没有运行发现协议"})
			} else if !hasVersion(target.Adapter.ProbeVersions, t.Version) {
				t.Findings = append(t.Findings, inventory.Finding{Status: "UNKNOWN", Code: "TOOL_VERSION_UNSUPPORTED", Detail: "版本 " + t.Version + " 尚未验证本地发现协议；没有运行初始化"})
			} else if len(target.Packages) == 0 {
				t.L2 = "SKIPPED"
				t.Findings = append(t.Findings, inventory.Finding{Status: "PASS", Code: "NO_ENABLED_SKILLS", Detail: "版本已核对；没有启用的技能，未运行发现初始化。仅可预览移除已归属输出"})
			} else {
				d, e := ops.discover(ctx, binary, t.Tool, project, home)
				if e != nil {
					t.Findings = append(t.Findings, inventory.Finding{Status: "UNKNOWN", Code: "TOOL_DISCOVERY_FAILED", Detail: probeFailure(e) + "；请重试隔离发现检查。没有发送模型提示"})
				} else {
					m.compareDiscovery(&t, target, d, project)
					if t.L2 == "FAIL" {
						r.Status = "FAIL"
					}
				}
			}
		} else if probe && len(target.Packages) == 0 {
			t.L2 = "SKIPPED"
			t.Findings = append(t.Findings, inventory.Finding{Status: "UNKNOWN", Code: "NO_ENABLED_SKILLS", Detail: "没有启用的技能，未启动工具进程"})
		} else if probe {
			t.Findings = append(t.Findings, inventory.Finding{Status: "FAIL", Code: "PROBE_PREFLIGHT_BLOCKED", Detail: "静态预览有阻断项，未启动工具进程"})
		}
		if target.Rule != nil {
			t.Rules = m.inspectRuleLoad(ctx, target, t, run, r.RuleProbe)
			if t.Rules.Status == "FAIL" {
				r.Status = "FAIL"
			}
		}
		for i := range t.Skills {
			t.Skills[i].L2 = t.Skills[i].Status
		}
		r.Tools = append(r.Tools, t)
	}
	r.Digest = checkDigest(r)
	return r, nil
}

// File evidence belongs to one owned package. A conflict in another package or
// a rule file must not mark this package as failed, nor does matching unowned
// content establish that AIDock installed it.
func (m *Manager) skillFileStatus(target Target, pkg Package) (string, string) {
	var ownership Ownership
	if entry, ok := target.Before.Entries[ownershipFile]; ok {
		data, err := m.Source.Store.Data(entry)
		if err != nil || strict(data, &ownership) != nil || ownership.Version != 1 || ownership.Owner != m.Source.Owner || ownership.Scope != m.Source.Scope || ownership.SourceRoot != m.Source.Store.Root || ownership.Tool != target.Adapter.ID {
			return "UNKNOWN", "工具所有权记录不可用，无法核对技能文件归属"
		}
	}
	for _, owned := range ownership.Packages {
		if owned.Name != pkg.Name {
			continue
		}
		if owned.ID != pkg.ID {
			return "FAIL", "同名目标归属于另一个技能身份"
		}
		actual := targetPackageDigest(target.Before, pkg.Name)
		if actual != owned.Digest {
			return "FAIL", "已归属的技能文件、目录或模式发生人工漂移"
		}
		if owned.Revision == pkg.Revision && actual == pkg.Digest {
			return "PASS", "当前完整包与固定修订一致，且归属于此技能身份"
		}
		return "NOT_APPLIED", "工具仍保存此技能的旧版本，需要重新预览应用"
	}
	if _, exists := target.Before.Entries[pkg.Name]; exists || len(subtree(target.Before, pkg.Name).Entries) != 0 {
		return "FAIL", "同名目标未归属于此技能，保留现有内容"
	}
	return "NOT_APPLIED", "此技能尚未安装到工具项目目录"
}

func hasVersion(versions []string, v string) bool {
	for _, known := range versions {
		if known == v {
			return true
		}
	}
	return false
}

func (m *Manager) prepareProbe(project, home string, target Target) error {
	if err := os.MkdirAll(home, 0700); err != nil {
		return err
	}
	// A local repository boundary prevents parent project discovery. No git
	// command, hooks, config, checkout or network access is involved.
	if err := os.MkdirAll(filepath.Join(project, ".git", "objects"), 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(project, ".git", "refs"), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(project, ".git", "HEAD"), []byte("ref: refs/heads/aidock-probe\n"), 0600); err != nil {
		return err
	}
	return m.materializeProbe(project, target)
}

func (m *Manager) compareDiscovery(t *ToolCheck, target Target, d discovery, project string) {
	t.L2 = "PASS"
	if d.Errors > 0 {
		t.L2 = "FAIL"
		t.Findings = append(t.Findings, inventory.Finding{Status: "FAIL", Code: "TOOL_REJECTED_SKILL", Detail: fmt.Sprintf("工具返回 %d 项技能解析错误", d.Errors)})
	}
	for i, pkg := range target.Packages {
		bytes, err := m.Source.Store.Data(pkg.Tree.Entries["SKILL.md"])
		if err != nil {
			t.Skills[i].Status, t.Skills[i].Detail, t.L2 = "FAIL", "技能备份不可用", "FAIL"
			continue
		}
		parts := strings.SplitN(strings.ReplaceAll(string(bytes), "\r\n", "\n"), "\n---", 2)
		front, err := config.Parse([]byte(strings.TrimPrefix(parts[0], "---\n")))
		if err != nil {
			t.Skills[i].Status, t.Skills[i].Detail, t.L2 = "FAIL", "元数据解析失败", "FAIL"
			continue
		}
		metadata, ok := front.(map[string]any)
		if !ok {
			t.Skills[i].Status, t.Skills[i].Detail, t.L2 = "FAIL", "元数据格式不支持", "FAIL"
			continue
		}
		description, _ := metadata["description"].(string)
		hits := 0
		for _, observed := range d.Skills {
			if observed.Name != pkg.Name {
				continue
			}
			if t.Tool == "codex-cli" {
				wanted := filepath.Join(project, filepath.FromSlash(target.Adapter.Directory), pkg.Name, "SKILL.md")
				if !samePath(observed.Path, wanted) || !observed.Enabled || strings.TrimSpace(observed.Description) != strings.TrimSpace(description) {
					continue
				}
			} else if strings.TrimSpace(strings.TrimSuffix(observed.Description, " (project)")) != strings.TrimSpace(description) {
				continue
			}
			hits++
		}
		if hits == 1 {
			t.Skills[i].Status, t.Skills[i].Detail = "PASS", "工具在隔离项目初始化时发现此完整包的名称和描述；尚未读取/使用正文"
		} else if visible, ok := metadata["user-invocable"].(bool); t.Tool == "claude-code" && ok && !visible {
			t.Skills[i].Status, t.Skills[i].Detail = "UNKNOWN", "技能声明 user-invocable: false，初始化命令列表不能证明其模型发现状态"
			if t.L2 != "FAIL" {
				t.L2 = "UNKNOWN"
			}
		} else {
			t.Skills[i].Status, t.Skills[i].Detail, t.L2 = "FAIL", "未发现唯一且元数据一致的项目技能；请检查 frontmatter 或同名命令", "FAIL"
		}
	}
}

// Latest returns a stored observation, with freshness recomputed from current
// inputs and executable bytes. It never invokes --version or starts a tool.
func (m *Manager) Latest(ctx context.Context) (*CheckReport, error) {
	ops := m.probes()
	dir := filepath.Join(m.checksDir(), "reports")
	if err := workstore.CheckLinks(dir); err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	reports := []CheckReport{}
	for _, file := range files {
		if strings.HasPrefix(file.Name(), ".preview-") {
			continue
		}
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			return nil, result.Fail("CHECK_CORRUPT", "检查目录含未知记录", 5)
		}
		bytes, err := workstore.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			return nil, err
		}
		var r CheckReport
		if err = strict(bytes, &r); err != nil || r.Version != 1 || !validID.MatchString(r.ID) || file.Name() != r.ID+".json" || r.Digest != checkDigest(r) || r.Root != m.Root || r.Owner != m.Source.Owner || r.Scope != m.Source.Scope || r.Copy != m.Source.Copy || len(r.Tools) == 0 {
			return nil, result.Fail("CHECK_CORRUPT", "检查记录身份或摘要不一致", 5)
		}
		if _, err := time.Parse(time.RFC3339Nano, r.Created); err != nil {
			return nil, result.Fail("CHECK_CORRUPT", "检查时间无效", 5)
		}
		reports = append(reports, r)
	}
	if len(reports) == 0 {
		return nil, nil
	}
	sort.Slice(reports, func(i, j int) bool {
		if reports[i].Created == reports[j].Created {
			return reports[i].ID > reports[j].ID
		}
		a, _ := time.Parse(time.RFC3339Nano, reports[i].Created)
		b, _ := time.Parse(time.RFC3339Nano, reports[j].Created)
		return a.After(b)
	})
	r := reports[0]
	stale := func(reason string) { r.Stale, r.StaleReason = true, reason }
	expires, err := time.Parse(time.RFC3339Nano, r.Expires)
	if err != nil || !time.Now().Before(expires) {
		stale("检查已超过 24 小时有效期")
	}
	ids := []string{}
	for _, t := range r.Tools {
		ids = append(ids, t.Tool)
		binary, err := ops.resolve(t.Tool)
		if t.Installed && (err != nil || binary.Path != t.Executable || binary.Digest != t.BinaryDigest) || !t.Installed && err == nil {
			stale("工具安装或程序内容已变化")
		}
	}
	p, err := m.compile(ctx, Request{Tools: ids, RuleResolutions: r.RuleResolutions, ApplicationScope: r.ApplicationScope})
	if err != nil {
		stale("无法重新核对当前输入；" + err.Error())
	} else if p.ContentDigest != r.InputDigest {
		stale("源、目标、外部绑定或适配器已变化")
	}
	return &r, nil
}
