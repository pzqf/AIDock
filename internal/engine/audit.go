package engine

import (
	"context"
	"time"

	"github.com/pzqf/AIDock/internal/auditlog"
	"github.com/pzqf/AIDock/internal/result"
)

type AuditReport struct {
	Version  int              `json:"schema_version"`
	Observed string           `json:"observed"`
	Project  string           `json:"project"`
	Scope    string           `json:"scope"`
	Copy     string           `json:"copy"`
	Root     string           `json:"root"`
	Status   string           `json:"status"`
	Chains   []auditlog.Chain `json:"chains"`
	auditlog.Page
	Boundary string `json:"boundary"`
}

func (e *Engine) Audit(ctx context.Context, q auditlog.Query) (result.Result, error) {
	if err := q.Validate(); err != nil {
		return result.Result{}, err
	}
	m, err := e.Sources()
	if err != nil {
		return result.Result{}, err
	}
	chains := m.AuditChains(ctx)
	if err := ctx.Err(); err != nil {
		return result.Result{}, err
	}
	r := AuditReport{Version: 1, Observed: time.Now().UTC().Format(time.RFC3339Nano), Project: m.Owner, Scope: m.Scope, Copy: m.Copy, Root: m.Store.Root, Status: "PASS", Chains: chains, Page: auditlog.Paginate(chains, q), Boundary: "只读当前绑定的来源证据、安装登记、项目权限、运行时核验及 MCP 会话五条独立审计链。source 范围按项目/作用域/源目录绑定，copy 范围另含当前副本。凭据审计、源/原生文件/副本回退记录仍在各自管理页；本报告不是全部产品事件。完整性检查复用现有链及本机事务锚点，无外部可信锚点，不能证明未被同用户整体重写或删除。不显示用途/风险理由、权限值、工具参数/返回正文或密钥；不会执行、授权、恢复或改写历史。各链依次读取，刷新或翻页时可能出现新记录。"}
	for _, c := range chains {
		if c.Status == "FAIL" {
			r.Status = "FAIL"
		} else if c.Status == "RECOVERY_REQUIRED" && r.Status != "FAIL" {
			r.Status = "RECOVERY_REQUIRED"
		}
	}
	v := result.New("audit", r)
	v.Summary = "安全审计查询；五类记录独立核对，本机链不等于外部可信存证"
	if r.Status != "PASS" {
		v.OK, v.Code = false, "STATIC_CHECK_FAILED"
	}
	return v, nil
}
