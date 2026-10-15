package native

import (
	"context"
	"errors"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/result"
)

// PrepareReport joins observation and preview, never installation. READY means
// only that the existing reversible native-file transaction can be confirmed.
type PrepareReport struct {
	Status   string              `json:"status"`
	Check    CheckReport         `json:"check"`
	Plan     Plan                `json:"plan"`
	Gates    []inventory.Finding `json:"gates"`
	Boundary string              `json:"boundary"`
}

// Prepare uses the same checks and qualification as separate CLI commands.
// Probe is opt-in; a static request cannot start a process or authorize writes.
func (m *Manager) Prepare(ctx context.Context, req CheckRequest) (PrepareReport, error) {
	out := PrepareReport{Status: "NEEDS_CHECK", Gates: []inventory.Finding{}, Boundary: "准备应用只检查已保存版本并生成文件预览，不写入项目源或工具目标。READY 仅表示可另行确认文件安装，不证明实际项目的新会话正文加载或能力使用，L3/L4 仍待验证。"}
	r, err := m.Check(ctx, req)
	if err != nil {
		return out, err
	}
	out.Check = r
	p, err := m.compile(ctx, Request{Tools: req.Tools, RuleResolutions: req.RuleResolutions})
	if err != nil {
		return out, err
	}
	if p.ContentDigest != r.InputDigest || p.Source.Snapshot != r.SourceSnapshot {
		return out, result.Fail("CHECK_STALE", "检查完成后源、工具目标或配置已变化，请重新准备应用", 3)
	}
	if p.Blocked {
		out.Status = "BLOCKED"
		out.Gates = append(out.Gates, inventory.Finding{Status: "FAIL", Code: "NATIVE_PREFLIGHT_FAILED", Detail: "源或目标存在冲突，请处理预览中标出的阻断项后重新准备应用"})
	} else if !req.Probe {
		out.Gates = append(out.Gates, inventory.Finding{Status: "UNKNOWN", Code: "NATIVE_CHECK_REQUIRED", Detail: "文件预览已生成；需明确启动隔离工具发现和规则加载检查，才可开放安装确认"})
	} else if err = m.qualify(&p, r.ID); err != nil {
		var gate *result.Error
		if !errors.As(err, &gate) || (gate.Code != "NATIVE_CHECK_REQUIRED" && gate.Code != "NATIVE_SEMANTICS_UNVERIFIED") {
			return out, err
		}
		out.Status = "NEEDS_ATTENTION"
		out.Gates = append(out.Gates, inventory.Finding{Status: "UNKNOWN", Code: gate.Code, Detail: gate.Message})
	} else {
		out.Status = "READY"
		p.ContentDigest = contentDigest(p)
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out.Plan, err = m.savePreview(p)
	return out, err
}
