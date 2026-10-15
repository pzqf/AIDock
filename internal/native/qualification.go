package native

import (
	"os"
	"path/filepath"
	"time"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Qualification authorizes only reversible installation of checked native files.
// It is not evidence of user-home precedence, session loading or execution.
type Qualification struct {
	CheckID     string          `json:"check_id"`
	CheckDigest string          `json:"check_digest"`
	Expires     string          `json:"expires"`
	Tools       []QualifiedTool `json:"tools"`
}
type QualifiedTool struct {
	Tool         string `json:"tool"`
	Version      string `json:"version"`
	Executable   string `json:"executable"`
	BinaryDigest string `json:"binary_digest"`
}

func (m *Manager) loadCheck(id string) (CheckReport, error) {
	var r CheckReport
	if !validID.MatchString(id) {
		return r, result.Fail("INVALID_INPUT", "需要当前绑定的工具检查 UUID", 2)
	}
	b, err := workstore.ReadFile(filepath.Join(m.checksDir(), "reports", id+".json"))
	if os.IsNotExist(err) {
		return r, result.Fail("CHECK_NOT_FOUND", "当前绑定没有此工具检查", 2)
	}
	if err != nil {
		return r, err
	}
	if err = strict(b, &r); err != nil || r.Version != 1 || r.ID != id || r.Root != m.Root || r.Owner != m.Source.Owner || r.Scope != m.Source.Scope || r.Copy != m.Source.Copy || r.Digest != checkDigest(r) {
		return r, result.Fail("CHECK_CORRUPT", "工具检查身份或摘要不一致", 5)
	}
	return r, nil
}

func (m *Manager) qualify(p *Plan, id string) error {
	r, err := m.loadCheck(id)
	if err != nil {
		return err
	}
	expires, err := time.Parse(time.RFC3339Nano, r.Expires)
	created, ce := time.Parse(time.RFC3339Nano, r.Created)
	if err != nil || ce != nil || created.After(time.Now().UTC()) || !time.Now().Before(expires) || expires.Sub(created) > 24*time.Hour {
		return result.Fail("CHECK_STALE", "工具检查时间已过期或无效，请重新验证工具发现", 3)
	}
	if r.InputDigest != p.ContentDigest || r.SourceSnapshot != p.Source.Snapshot {
		return result.Fail("CHECK_STALE", "技能源、工具目标、工具选择或适配器已变化，请重新检查", 3)
	}
	if !r.Probe || r.Status == "FAIL" || p.Blocked || len(r.Tools) != len(p.Targets) {
		return result.Fail("NATIVE_CHECK_REQUIRED", "需要当前版本通过工具发现检查；文件静态检查不能开放写入", 4)
	}
	q := &Qualification{CheckID: r.ID, CheckDigest: r.Digest, Expires: r.Expires, Tools: []QualifiedTool{}}
	seen := map[string]bool{}
	for _, target := range p.Targets {
		var t *ToolCheck
		for i := range r.Tools {
			if r.Tools[i].Tool == target.Adapter.ID && !seen[r.Tools[i].Tool] {
				t = &r.Tools[i]
				seen[t.Tool] = true
				break
			}
		}
		if t == nil || !t.Installed || !hasVersion(target.Adapter.Verified, t.Version) || (t.L2 != "PASS" && !(len(target.Packages) == 0 && t.L2 == "SKIPPED")) {
			return result.Fail("NATIVE_CHECK_REQUIRED", "所选工具缺少受支持精确版本的发现证据", 4)
		}
		for _, f := range target.Findings {
			if f.Code == "RULES_SESSION_UNVERIFIED" && r.RuleProbe && t.Rules != nil && t.Rules.Status == "PASS" && t.Rules.Code == "RULES_ISOLATED_LOAD" && target.Rule != nil && t.Rules.Path == target.Rule.Path && t.Rules.SHA256 == target.Rule.After.Entries[workstore.FileContent].Hash && hasVersion(target.Adapter.RuleVersions, t.Version) {
				continue
			}
			if f.Status != "PASS" && f.Code != "TOOL_VERSION_UNVERIFIED" && f.Code != "DISCOVERY_SCOPE_UNVERIFIED" {
				return result.Fail("NATIVE_SEMANTICS_UNVERIFIED", "当前包包含尚未通过原生语义验收的条件："+f.Code+"；保留预览和预演", 4)
			}
		}
		binary, err := m.probes().resolve(t.Tool)
		if err != nil || binary.Path != t.Executable || binary.Digest != t.BinaryDigest {
			return result.Fail("CHECK_STALE", "工具程序已变化，请重新检查", 3)
		}
		q.Tools = append(q.Tools, QualifiedTool{t.Tool, t.Version, t.Executable, t.BinaryDigest})
	}
	p.Qualification, p.WriteSupported = q, true
	p.Evidence["L2"] = "PASS_ISOLATED_DISCOVERY"
	p.Boundary = "此计划仅安装已保存、启用且通过当前精确版本发现检查的完整技能包；包含备份、归属检查与失败恢复。不启动工具或执行技能。原生客户端之后执行技能属于 external，不受 AIDock 隔离治理；实际项目配置优先级、旧会话、正文加载与模型使用仍未验证。"
	for _, target := range p.Targets {
		if target.Rule != nil {
			p.Evidence["rules"] = "PASS_ISOLATED_LOAD"
			p.Boundary += " 同时安装通过精确版本隔离临时会话来源核对的规则文件托管区块，保留手写区；规则证据仅覆盖空配置独立候选，不提升实际项目的 L3/L4。"
			break
		}
	}
	return nil
}
