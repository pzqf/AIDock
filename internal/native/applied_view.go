package native

import (
	"context"
	"errors"
	"path/filepath"
	"sort"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

type AppliedTargetState struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}
type AppliedLauncherState struct {
	Tool   string `json:"tool"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Code   string `json:"code,omitempty"`
}
type AppliedViewItem struct {
	Binding            string                 `json:"applied_binding,omitempty"`
	Snapshot           string                 `json:"snapshot_id,omitempty"`
	Transaction        string                 `json:"transaction,omitempty"`
	Publication        string                 `json:"publication_id,omitempty"`
	PublicationMembers int                    `json:"publication_members,omitempty"`
	Mode               string                 `json:"mode"`
	Source             string                 `json:"source_digest,omitempty"`
	SourceDifference   string                 `json:"source_difference"`
	LocalDifference    string                 `json:"local_difference"`
	Targets            []AppliedTargetState   `json:"targets"`
	Launchers          []AppliedLauncherState `json:"launchers"`
}
type AppliedViews struct {
	Project          string            `json:"project"`
	Scope            string            `json:"scope"`
	Copy             string            `json:"copy"`
	Root             string            `json:"root"`
	DraftObservation string            `json:"draft_observation"`
	DraftSource      string            `json:"draft_source_digest,omitempty"`
	DraftLocal       string            `json:"draft_local_digest,omitempty"`
	Recovery         bool              `json:"recovery_required"`
	Items            []AppliedViewItem `json:"views"`
	Boundary         string            `json:"boundary"`
}

func difference(a, b string) string {
	if a == "" {
		return "UNKNOWN"
	}
	if a == b {
		return "SAME"
	}
	return "DIFFERENT"
}

// This read distinguishes committed installed bytes from today's saved draft.
// SAME compares source/Local bytes only, not today's global/session semantics.
// No source bodies, native fields, arguments, purposes or vault values escape.
func (m *Manager) AppliedViews(ctx context.Context) (AppliedViews, error) {
	out := AppliedViews{Project: m.Source.Owner, Scope: m.Source.Scope, Copy: m.Source.Copy, Root: m.Root, DraftObservation: "UNKNOWN", Items: []AppliedViewItem{}, Boundary: "已应用视图由原成功事务及当前工具目标或托管字段核对；已保存源/本机覆盖与应用版本分别展示。字节相同不代表今天的全局/会话层或实际技能 L3/L4 已生效。读取不启动 AI 工具、MCP 或读取密钥；新绑定的连接继续验证当前授权、安装、运行时及产物。旧安装保持原启动门槛，不自动转为冻结视图。"}
	local := &workstore.Store{Root: source.LocalProfileRoot(m.Source.Store.Local, m.Source.Owner, m.Source.Scope, m.Source.Copy), Local: filepath.Join(m.Source.Store.Local, "profile-override-transactions"), PlanKind: "profile-local"}
	err := m.Source.Store.Locked(func() error {
		if err := m.Source.Store.Pending(); err != nil {
			return err
		}
		return local.Locked(func() error {
			if err := local.Pending(); err != nil {
				return err
			}
			s, err := m.Source.Store.Capture(m.Source.Store.Root)
			if err != nil {
				return err
			}
			l, err := local.Capture(local.Root)
			if err != nil {
				return err
			}
			out.DraftSource, out.DraftLocal = workstore.Digest(s), workstore.Digest(l)
			out.DraftObservation = "OBSERVED"
			return nil
		})
	})
	if err != nil {
		out.DraftObservation = "UNAVAILABLE"
	}
	if err := m.profileApplicationBatch().Pending(); err != nil {
		var e *result.Error
		if errors.As(err, &e) && (e.Code == "NATIVE_RECOVERY_REQUIRED" || e.Code == "COORDINATED_RECOVERY_REQUIRED") {
			out.Recovery = true
		} else {
			return out, err
		}
	}
	_, records, journals, err := m.projectSnapshotRecords(ctx)
	if err != nil {
		return out, err
	}
	live, err := m.connectionLedger()
	if err != nil {
		return out, err
	}
	keys := map[string]bool{}
	for _, tool := range live.Tools {
		for _, field := range tool.Fields {
			if field.AppliedKey != "" {
				keys[field.AppliedKey] = true
			}
		}
	}
	seen := map[string]bool{}
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		state, err := snapshotState(record, journals)
		if err != nil {
			return out, err
		}
		if state != "committed" {
			continue
		}
		key := record.ActiveKey
		if seen[key] && key != "" {
			continue
		}
		item := AppliedViewItem{Binding: key, Snapshot: record.ID, Transaction: record.Transaction.ID, Mode: "FROZEN", Source: record.Effective.Snapshot, SourceDifference: difference(out.DraftSource, record.Effective.Snapshot), LocalDifference: "UNKNOWN", Targets: []AppliedTargetState{}, Launchers: []AppliedLauncherState{}}
		if record.Publication != nil {
			item.Publication, item.PublicationMembers = record.Publication.Publication, len(record.Publication.Members)
		}
		if record.Configuration != nil {
			item.LocalDifference = difference(out.DraftLocal, workstore.Digest(record.Configuration.LocalTree))
		}
		allMatch := true
		for _, t := range record.Transaction.Targets {
			var current workstore.Tree
			if t.File {
				current, err = m.connectionStore().CaptureFile(filepath.Join(m.Root, filepath.FromSlash(t.Path)))
			} else {
				current, err = m.connectionStore().Capture(filepath.Join(m.Root, filepath.FromSlash(t.Path)))
			}
			if err != nil {
				return out, err
			}
			match := workstore.Digest(current) == workstore.Digest(t.After)
			status := "DIFFERENT"
			if match {
				status = "MATCH"
			}
			allMatch = allMatch && match
			item.Targets = append(item.Targets, AppliedTargetState{t.Path, status})
		}
		if !keys[key] && !allMatch {
			continue
		}
		if key == "" {
			item.Mode = "LEGACY_CURRENT_SOURCE"
			if !allMatch {
				continue
			}
			out.Items = append(out.Items, item)
			continue
		}
		seen[key] = true
		a, err := m.loadAppliedRecord(ctx, key)
		if err != nil {
			return out, err
		}
		for tool, fields := range live.Tools {
			for _, field := range fields.Fields {
				if field.AppliedKey != key {
					continue
				}
				v := AppliedLauncherState{Tool: tool, Name: field.Plugin, Status: "CURRENT"}
				err := m.Source.WithFrozenMCPClientConfigs(ctx, a.Frozen, []source.MCPSessionRequest{field.Session}, field.Executable, field.Home, func([]source.MCPClientConfiguration) error {
					return m.withAppliedNativeTool(ctx, a, field.Session, tool, func() error { return nil })
				})
				if err != nil {
					v.Status, v.Code = "BLOCKED", "APPLIED_VIEW_UNVERIFIED"
					var e *result.Error
					if errors.As(err, &e) {
						v.Code = e.Code
					}
				}
				item.Launchers = append(item.Launchers, v)
			}
		}
		sort.Slice(item.Launchers, func(i, j int) bool {
			a, b := item.Launchers[i], item.Launchers[j]
			if a.Tool != b.Tool {
				return a.Tool < b.Tool
			}
			return a.Name < b.Name
		})
		out.Items = append(out.Items, item)
	}
	unknown := []string{}
	for key := range keys {
		if !seen[key] {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	for _, key := range unknown {
		if !seen[key] {
			out.Items = append(out.Items, AppliedViewItem{Binding: key, Mode: "UNPROVEN", SourceDifference: "UNKNOWN", LocalDifference: "UNKNOWN", Targets: []AppliedTargetState{}, Launchers: []AppliedLauncherState{}})
		}
	}
	return out, nil
}
