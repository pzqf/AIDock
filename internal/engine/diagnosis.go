package engine

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/profile"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type DiagnosisSection struct {
	ID       string              `json:"id"`
	Status   string              `json:"status"`
	Summary  string              `json:"summary"`
	Findings []inventory.Finding `json:"findings"`
}

// Only provenance is exposed here. Profile values, rule text, package bodies,
// raw native configuration and backup trees never enter the diagnostic report.
type DiagnosisOrigin struct {
	Path     string `json:"path"`
	Layer    string `json:"layer"`
	Previous string `json:"previous_layer,omitempty"`
	Action   string `json:"action"`
}

type DiagnosisEvidence struct {
	ID      string `json:"id"`
	Created string `json:"created"`
	Scope   string `json:"application_scope"`
	Stale   bool   `json:"stale"`
	Reason  string `json:"stale_reason,omitempty"`
	L1      string `json:"L1"`
	L2      string `json:"L2"`
	L3      string `json:"L3"`
	L4      string `json:"L4"`
}

type DiagnosisTool struct {
	Tool       string              `json:"tool"`
	Target     string              `json:"target"`
	Installed  bool                `json:"installed"`
	Version    string              `json:"version,omitempty"`
	Files      string              `json:"files"`
	Discovery  string              `json:"discovery"`
	Session    string              `json:"session"`
	Capability string              `json:"capability"`
	Rules      string              `json:"rules"`
	Evidence   *DiagnosisEvidence  `json:"evidence,omitempty"`
	Findings   []inventory.Finding `json:"findings"`
}

type DiagnosisRecovery struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type Diagnosis struct {
	Version    int                 `json:"schema_version"`
	Observed   string              `json:"observed"`
	Project    string              `json:"project"`
	Scope      string              `json:"scope_id"`
	Copy       string              `json:"copy,omitempty"`
	Root       string              `json:"root"`
	Status     string              `json:"status"`
	Sections   []DiagnosisSection  `json:"sections"`
	Layers     []profile.Layer     `json:"layers"`
	Local      []DiagnosisOrigin   `json:"local_origins"`
	Tools      []DiagnosisTool     `json:"tools"`
	Recoveries []DiagnosisRecovery `json:"recoveries"`
	Copies     *int                `json:"registered_copies"`
	Boundary   string              `json:"boundary"`
}

func diagnosisFailure(section *DiagnosisSection, err error) {
	code := "READ_UNAVAILABLE"
	var classified *result.Error
	if errors.As(err, &classified) {
		code = classified.Code
	}
	section.Status, section.Summary = "FAIL", "当前检查未完成，请到对应管理页核对并刷新诊断"
	// Parser errors may contain source values. Return an error code only.
	section.Findings = append(section.Findings, inventory.Finding{Status: "FAIL", Code: code, Detail: section.Summary})
}

func diagnosisStaleReason(reason string) string {
	switch reason {
	case "检查已超过 24 小时有效期", "工具安装或程序内容已变化", "源、目标、外部绑定或适配器已变化":
		return reason
	default:
		// A compiler/parser error in Latest can contain raw configuration values.
		return "无法核对已有检查与当前输入，请到 AI 工具页重新检查"
	}
}

// Diagnose is a read-only overview of the explicitly bound project. Independent
// sections keep reporting when one source/history is damaged. It neither scans
// neighboring projects, probes tools, grants permission nor performs recovery.
// Sections are sequential observations, not an installation qualification.
func (e *Engine) Diagnose(ctx context.Context) (Diagnosis, error) {
	m, err := e.Sources()
	if err != nil {
		return Diagnosis{}, err
	}
	r := Diagnosis{Version: 1, Observed: time.Now().UTC().Format(time.RFC3339Nano), Project: m.Owner, Scope: m.Scope, Copy: m.Copy, Root: m.Store.Root, Status: "INCOMPLETE", Sections: []DiagnosisSection{}, Layers: []profile.Layer{}, Local: []DiagnosisOrigin{}, Tools: []DiagnosisTool{}, Recoveries: []DiagnosisRecovery{}, Boundary: "只读取当前绑定项目的配置、文件归属与已有检查记录；不启动工具、不安装、不恢复。各项为本次依次观察，输入变化后请刷新。读取可使用本机锁与观察缓存；项目源和工具文件保留。文件一致、隔离发现、实际会话与能力分别判断，历史证据不作为当前通过结论。"}
	sourceSection := DiagnosisSection{ID: "source", Status: "UNKNOWN", Summary: "尚未完成技能源检查", Findings: []inventory.Finding{}}
	profileSection := DiagnosisSection{ID: "profile", Status: "UNKNOWN", Summary: "尚未解析配置来源", Findings: []inventory.Finding{}}
	recoverySection := DiagnosisSection{ID: "recovery", Status: "PASS", Summary: "所检查记录中没有待恢复事务", Findings: []inventory.Finding{}}
	state, stateErr := m.State()
	if stateErr != nil {
		diagnosisFailure(&sourceSection, stateErr)
		diagnosisFailure(&recoverySection, stateErr)
	} else if state.Pending {
		sourceSection.Status, sourceSection.Summary = "RECOVERY_REQUIRED", "技能源受未完成事务保护，先查看恢复记录"
		if workstore.CoordinatedPending(m.Store.Root) != nil {
			sourceSection.Summary = "当前目录受多目录事务保护，请切换原发起副本恢复整批事务"
			r.Recoveries = append(r.Recoveries, DiagnosisRecovery{Kind: "coordinated"})
		}
		for _, h := range state.History {
			if h.State == "prepared" {
				r.Recoveries = append(r.Recoveries, DiagnosisRecovery{"source", h.ID})
			}
		}
	}
	if stateErr == nil && !state.Pending {
		check, err := m.Check()
		if err != nil {
			diagnosisFailure(&sourceSection, err)
		} else {
			sourceSection.Status, sourceSection.Summary = check.Status, "完整包、声明依赖及配置合理性已检查"
			sourceSection.Findings = append(sourceSection.Findings, check.ProfileChecks...)
			for _, skill := range check.Skills {
				for _, f := range skill.Checks {
					if f.Status != "PASS" {
						f.Detail = skill.Name + "：" + f.Detail
						sourceSection.Findings = append(sourceSection.Findings, f)
					}
				}
			}
		}
		if state.ProfileReport != nil {
			r.Layers = state.ProfileReport.Layers
			last := map[string]profile.Event{}
			for _, event := range state.ProfileReport.Trace {
				last[event.Path] = event
			}
			for _, event := range last {
				if event.Layer == "local" {
					r.Local = append(r.Local, DiagnosisOrigin{event.Path, event.Layer, event.Previous, event.Action})
				}
			}
			sort.Slice(r.Local, func(i, j int) bool { return r.Local[i].Path < r.Local[j].Path })
			profileSection.Status, profileSection.Summary = "PASS", "已解析全局、继承、共享与本机来源链"
			if len(r.Local) > 0 {
				profileSection.Status, profileSection.Summary = "WARN", "存在本机配置，部分共享设置会由本机层决定"
			}
		} else {
			profileSection.Summary = "配置来源尚未解析，检查 Profile 版本与引用"
			p, err := m.Profile()
			if p.Layers.Recovery {
				profileSection.Status, profileSection.Summary = "RECOVERY_REQUIRED", "本机 Profile 有未完成事务，请先恢复"
				r.Recoveries = append(r.Recoveries, DiagnosisRecovery{Kind: "profile"})
			} else if err != nil {
				diagnosisFailure(&profileSection, err)
			}
		}
	}
	r.Sections = append(r.Sections, sourceSection, profileSection)
	toolsSection := DiagnosisSection{ID: "tools", Status: "UNKNOWN", Summary: "未绑定工具项目目录", Findings: []inventory.Finding{}}
	if e.NativeRoot != "" {
		n, err := e.Native()
		if err != nil {
			diagnosisFailure(&toolsSection, err)
		} else {
			current, currentErr := n.InspectCurrent(ctx)
			last, lastErr := n.Latest(ctx)
			if currentErr != nil {
				diagnosisFailure(&toolsSection, currentErr)
			} else {
				toolsSection.Summary = "当前技能与规则文件已核对，发现及会话证据单独显示"
				for _, t := range current.Tools {
					row := DiagnosisTool{Tool: t.Tool, Target: t.Target, Installed: t.Installed, Files: t.L1, Discovery: "UNKNOWN", Session: "UNKNOWN", Capability: "UNKNOWN", Rules: "UNKNOWN", Findings: t.Findings}
					if current.Stale {
						row.Files = "STALE"
					}
					if last != nil {
						for _, old := range last.Tools {
							if old.Tool != t.Tool {
								continue
							}
							row.Version = old.Version
							reason := ""
							if last.Stale {
								reason = diagnosisStaleReason(last.StaleReason)
							}
							row.Evidence = &DiagnosisEvidence{last.ID, last.Created, last.ApplicationScope, last.Stale, reason, old.L1, old.L2, old.L3, old.L4}
							if !last.Stale {
								row.Discovery, row.Session, row.Capability = old.L2, old.L3, old.L4
								if old.Rules != nil {
									row.Rules = old.Rules.Status
								}
							}
						}
					}
					r.Tools = append(r.Tools, row)
				}
			}
			if lastErr != nil {
				diagnosisFailure(&toolsSection, lastErr)
			}
			for _, category := range []struct {
				kind string
				read func() ([]workstore.BatchJournal, error)
			}{{"native", n.History}, {"mcp", n.MCPConnectionHistory}, {"profile-application", n.ProfileApplicationHistory}} {
				rows, err := category.read()
				if err != nil {
					diagnosisFailure(&recoverySection, err)
					continue
				}
				for _, row := range rows {
					if row.State == "prepared" {
						r.Recoveries = append(r.Recoveries, DiagnosisRecovery{category.kind, row.Plan.ID})
					}
				}
			}
		}
	}
	r.Sections = append(r.Sections, toolsSection)
	if m.Owner != "global" {
		copiesSection := DiagnosisSection{ID: "replica", Status: "UNKNOWN", Summary: "尚未登记副本", Findings: []inventory.Finding{}}
		copies, err := e.Replicas()
		if err != nil {
			diagnosisFailure(&copiesSection, err)
		} else {
			registry, err := copies.List()
			if err != nil {
				diagnosisFailure(&copiesSection, err)
			} else {
				count := len(registry.Copies)
				r.Copies = &count
				if count > 0 {
					copiesSection.Status, copiesSection.Summary = "PASS", "已读取当前项目副本登记；本次不比较或同步其它目录"
				}
			}
			history, err := copies.PublicationHistory()
			if err != nil {
				diagnosisFailure(&recoverySection, err)
			} else {
				for _, row := range history {
					if row.Pending {
						r.Recoveries = append(r.Recoveries, DiagnosisRecovery{"replica", row.ID})
					}
				}
			}
		}
		r.Sections = append(r.Sections, copiesSection)
	}
	if len(r.Recoveries) > 0 {
		recoverySection.Status, recoverySection.Summary = "RECOVERY_REQUIRED", "发现待恢复事务，请先到对应页面核对恢复条件"
	}
	r.Sections = append(r.Sections, recoverySection)
	for _, s := range r.Sections {
		if s.Status == "FAIL" || s.Status == "RECOVERY_REQUIRED" {
			r.Status = "ACTION_REQUIRED"
		}
	}
	for _, t := range r.Tools {
		if t.Files == "FAIL" || t.Files == "NOT_APPLIED" || t.Discovery == "FAIL" || t.Session == "FAIL" || t.Capability == "FAIL" || t.Rules == "FAIL" {
			r.Status = "ACTION_REQUIRED"
		}
	}
	return r, ctx.Err()
}
