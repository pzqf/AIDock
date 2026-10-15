package native

import (
	"context"
	"path/filepath"
	"time"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// InspectDraft inspects compiler-produced immutable targets without saving a
// native installation qualification. The caller revalidates candidate inputs
// and observed roots before and after this operation. Only an explicit probe
// starts tools, against fresh Local projects with empty tool homes.
func (m *Manager) InspectDraft(ctx context.Context, targets []Target, snapshot string, probe bool, ruleProbes ...bool) (CheckReport, error) {
	ruleProbe := len(ruleProbes) > 0 && ruleProbes[0]
	if ruleProbe && !probe {
		return CheckReport{}, result.Fail("INVALID_INPUT", "规则来源检查需同时显式启用工具探测", 2)
	}
	if len(targets) < 1 || len(targets) > len(Adapters()) || !validDigest.MatchString(snapshot) {
		return CheckReport{}, result.Fail("GENERATOR_MANIFEST_INVALID", "候选工具集合或源摘要无效", 5)
	}
	seen := map[string]bool{}
	for _, t := range targets {
		if t.Rule != nil {
			if err := ValidateDraftRule(t); err != nil {
				return CheckReport{}, err
			}
		}
		matched := false
		for _, a := range Adapters() {
			if t.Adapter.ID == a.ID && string(jsonData(t.Adapter)) == string(jsonData(a)) && t.Root == filepath.Join(m.Root, filepath.FromSlash(a.Directory)) {
				matched = true
			}
		}
		if !matched || seen[t.Adapter.ID] || string(jsonData(t.Changes)) != string(jsonData(workstore.Changes(t.Before, t.After))) {
			return CheckReport{}, result.Fail("GENERATOR_MANIFEST_INVALID", "候选工具目录或适配器绑定无效", 5)
		}
		seen[t.Adapter.ID] = true
		names := map[string]bool{}
		for _, pkg := range t.Packages {
			if !validID.MatchString(pkg.ID) || !validName.MatchString(pkg.Name) || !validRevision.MatchString(pkg.Revision) || pkg.Digest != packageDigest(pkg.Tree, workstore.Directory()) || names[pkg.Name] {
				return CheckReport{}, result.Fail("GENERATOR_MANIFEST_INVALID", "候选包身份、名称或字节摘要无效", 5)
			}
			names[pkg.Name] = true
			for path, entry := range pkg.Tree.Entries {
				if err := workstore.SafeRelative(path); err != nil {
					return CheckReport{}, err
				}
				if t.Status != "FAIL" && entry != t.After.Entries[pkg.Name+"/"+path] {
					return CheckReport{}, result.Fail("GENERATOR_MANIFEST_INVALID", "候选包与生成目录字节不一致", 5)
				}
			}
		}
	}
	p := Plan{Targets: targets}
	p.Source.Snapshot = snapshot
	p.ContentDigest = workstore.Hash(jsonData(struct {
		Snapshot string
		Targets  []Target
	}{snapshot, targets}))
	r, err := m.inspectPlan(ctx, p, probe, ruleProbe)
	if err != nil {
		return r, err
	}
	r.Boundary = "检查暂存候选，不是原生安装资格。L1 核对原始工具目录、规则文件与候选；L2 验证独立临时项目的待生成完整包发现。规则来源仅在另行明确选择后验证，包含保留手写正文；本机排除技能不进入探测。L3/L4、实际配置优先级和正文使用仍未验证；不执行技能，不写入登记项目。"
	r.Digest = checkDigest(r)
	return r, m.InspectionFresh(r)
}

// InspectionFresh reads executable bytes only. It does not start processes or
// compile the bound source, which may differ from an immutable candidate.
func (m *Manager) InspectionFresh(r CheckReport) error {
	if r.Version != 1 || !validID.MatchString(r.ID) || r.Digest != checkDigest(r) || r.Root != m.Root || r.Owner != m.Source.Owner || r.Scope != m.Source.Scope || r.Copy != m.Source.Copy || len(r.Tools) == 0 {
		return result.Fail("CHECK_CORRUPT", "候选工具检查身份或摘要不一致", 5)
	}
	created, ce := time.Parse(time.RFC3339Nano, r.Created)
	expires, ee := time.Parse(time.RFC3339Nano, r.Expires)
	if ce != nil || ee != nil || created.After(time.Now().UTC()) || !time.Now().Before(expires) || !expires.After(created) || expires.Sub(created) > 24*time.Hour {
		return result.Fail("CHECK_STALE", "候选工具检查时间已过期或无效", 3)
	}
	for _, t := range r.Tools {
		b, err := m.probes().resolve(t.Tool)
		if t.Installed && (err != nil || b.Path != t.Executable || b.Digest != t.BinaryDigest) || !t.Installed && err == nil {
			return result.Fail("CHECK_STALE", "工具程序内容或安装状态已变化", 3)
		}
	}
	return nil
}

// DraftSemantics applies the checked installation semantics to packages that
// would be written. Excluded packages are outside this inspection's coverage;
// preserving them is not an installation. It never grants a qualification.
func DraftSemantics(t Target) []string {
	var codes []string
	for _, f := range t.Findings {
		// These files have no compiler ownership and are deliberately preserved.
		// Match the ordinary native installation gate; FAIL remains blocking.
		if f.Code == "RULE_FILES_UNMANAGED" && f.Status == "WARN" {
			continue
		}
		if f.Status != "PASS" && f.Code != "TOOL_VERSION_UNVERIFIED" && f.Code != "DISCOVERY_SCOPE_UNVERIFIED" && !(f.Code == "RULES_SESSION_UNVERIFIED" && t.Rule != nil && f.Status == "UNKNOWN") && !(f.Code == "LOCAL_EXCLUDED_PRESERVED" && f.Status == "UNKNOWN") {
			codes = append(codes, f.Code)
		}
	}
	return codes
}

// ValidateDraftRule checks the fixed adapter path and single-file shape; the
// replica compiler separately re-derives exact bytes before accepting a draft.
func ValidateDraftRule(t Target) error {
	r := t.Rule
	bad := func() error {
		return result.Fail("GENERATOR_MANIFEST_INVALID", "候选规则路径、观察或文件快照无效", 5)
	}
	if r == nil {
		return nil
	}
	if r.Path != rulePath(t.Adapter.ID) || r.ObservedDigest != workstore.Digest(r.Before) || string(jsonData(r.Changes)) != string(jsonData(workstore.Changes(r.Before, r.After))) {
		return bad()
	}
	trees := []workstore.Tree{r.Before, r.After}
	if r.Override != nil {
		if t.Adapter.ID != "codex-cli" || r.Override.Path != "AGENTS.override.md" {
			return bad()
		}
		trees = append(trees, r.Override.Before)
	}
	for _, tree := range trees {
		if !tree.Exists && len(tree.Entries) == 0 {
			continue
		}
		entry, ok := tree.Entries[workstore.FileContent]
		if !tree.Exists || len(tree.Entries) != 1 || !ok || entry.Dir || !validDigest.MatchString(entry.Hash) {
			return bad()
		}
	}
	return nil
}
