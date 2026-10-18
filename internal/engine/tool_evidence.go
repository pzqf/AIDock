package engine

import (
	"context"
	"errors"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/result"
)

type ToolEvidenceWarning struct {
	Area string `json:"area"`
	Code string `json:"code"`
}

type ToolEvidence struct {
	Version            int                                 `json:"schema_version"`
	Status             string                              `json:"status"`
	Current            native.CheckReport                  `json:"current"`
	Last               *native.CheckReport                 `json:"last"`
	LastStatus         string                              `json:"last_status"`
	Applied            *native.AppliedViews                `json:"applied"`
	ApplicationStatus  string                              `json:"application_status"`
	Publications       []replica.MemberPublicationEvidence `json:"publications"`
	PublicationsStatus string                              `json:"publications_status"`
	PublicationsTotal  *int                                `json:"publications_total"`
	Truncated          bool                                `json:"truncated"`
	Warnings           []ToolEvidenceWarning               `json:"warnings"`
	Boundary           string                              `json:"boundary"`
}

// NativeEvidence observes current files and reads existing evidence without
// manufacturing a stored check, application, qualification or authorization.
func (e *Engine) NativeEvidence(ctx context.Context) (ToolEvidence, error) {
	out := ToolEvidence{Version: 1, Status: "OBSERVED", LastStatus: "NONE", ApplicationStatus: "COPY_BINDING_REQUIRED", PublicationsStatus: "COPY_BINDING_REQUIRED", Publications: []replica.MemberPublicationEvidence{}, Warnings: []ToolEvidenceWarning{}, Boundary: "当前 L1 来自本次只读文件核对；最近独立检查重新判断过期，完整批次候选仅列历史。文件一致或历史发现不证明实际会话 L3/L4。读取不启动 AI 工具或 MCP；历史快照通过本机 Git 读取。不保存新的检查记录，不提供安装或应用资格；普通观察可能写本机缓存与锁。"}
	unavailable := func(area string, err error) {
		code := "EVIDENCE_UNAVAILABLE"
		var typed *result.Error
		if errors.As(err, &typed) {
			code = typed.Code
		}
		out.Status = "PARTIAL"
		out.Warnings = append(out.Warnings, ToolEvidenceWarning{Area: area, Code: code})
	}
	n, err := e.Native()
	if err != nil {
		return out, err
	}
	// Ordinary skill-file checks also support an explicitly bound source before
	// it has a copy identity. Do not invent a copy to retrieve application proof.
	if n.Source.Copy != "" {
		views, err := n.AppliedViews(ctx)
		if err != nil {
			out.ApplicationStatus = "UNAVAILABLE"
			unavailable("applications", err)
		} else {
			out.Applied, out.ApplicationStatus = &views, "OBSERVED"
		}
		ids, seen := []string{}, map[string]bool{}
		// A changed native file must not erase its original candidate history.
		// Snapshot references verify the member and original transaction, while
		// AppliedViews independently describes installed bytes and launchers.
		snapshots, err := n.ProjectSnapshots(ctx)
		if err != nil {
			out.PublicationsStatus = "UNAVAILABLE"
			unavailable("publications", err)
		} else {
			out.PublicationsStatus = "AVAILABLE"
			for _, snapshot := range snapshots.Items {
				if snapshot.TransactionState == "committed" && snapshot.Publication != "" && !seen[snapshot.Publication] {
					seen[snapshot.Publication] = true
					ids = append(ids, snapshot.Publication)
				}
			}
			total := len(ids)
			out.PublicationsTotal = &total
		}
		if len(ids) > 64 {
			ids, out.Truncated = ids[:64], true
		}
		if len(ids) > 0 {
			r, err := replica.OpenRecovery(n.Source)
			if err == nil {
				out.Publications, err = r.MemberPublicationChecks(ctx, ids)
			}
			if err != nil {
				out.Publications, out.PublicationsStatus = []replica.MemberPublicationEvidence{}, "UNAVAILABLE"
				unavailable("publications", err)
			}
		}
	}
	// Historical data cannot replace this final current observation.
	out.Current, err = n.InspectCurrent(ctx)
	if err != nil {
		return out, err
	}
	// Re-evaluate independent evidence after the file observation. Clients
	// also require exact context, source revision, target and tool bytes.
	out.Last, err = n.Latest(ctx)
	if err != nil {
		out.Last, out.LastStatus = nil, "UNAVAILABLE"
		unavailable("last", err)
	} else if out.Last != nil {
		out.LastStatus = "AVAILABLE"
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	return out, nil
}
