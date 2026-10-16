package native

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

const ProfileApplicationKind = "profile"

type ApplicationJournal struct {
	workstore.BatchJournal
	ApplicationKind string `json:"application_kind,omitempty"`
}

type ApplicationReversePlan struct {
	workstore.BatchPlan
	ApplicationKind string `json:"application_kind,omitempty"`
}

func (m *Manager) applicationKind(id string) (string, error) {
	if !validID.MatchString(id) {
		return "", result.Fail("INVALID_INPUT", "需要当前绑定的应用计划 UUID", 2)
	}
	found, kind := false, ""
	for _, item := range []struct{ kind, path string }{{"", m.file(id)}, {ProfileApplicationKind, filepath.Join(m.connectionArea(), "profile-plans", id+".json")}} {
		if err := workstore.CheckLinks(item.path); err != nil {
			return "", err
		}
		_, err := os.Lstat(item.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if found {
			return "", result.Fail("PLAN_KIND_MISMATCH", "应用计划身份存在歧义，请重新预览", 5)
		}
		found, kind = true, item.kind
	}
	// Preserve the legacy missing-plan error and kind checks when no joint
	// plan exists. Looking up an ID cannot choose another project or root.
	return kind, nil
}

func (m *Manager) DiffApplication(ctx context.Context, id, digest string) (any, error) {
	kind, err := m.applicationKind(id)
	if err != nil {
		return nil, err
	}
	if kind == ProfileApplicationKind {
		return m.DiffProfileApplication(ctx, id, digest)
	}
	return m.Diff(ctx, id, digest)
}

func (m *Manager) ApplyApplication(ctx context.Context, id, digest string) error {
	kind, err := m.applicationKind(id)
	if err != nil {
		return err
	}
	if kind == ProfileApplicationKind {
		_, err = m.ApplyProfileApplication(ctx, id, digest)
		return err
	}
	return m.Apply(ctx, id, digest)
}

func (m *Manager) ApplicationHistory() ([]ApplicationJournal, error) {
	out := []ApplicationJournal{}
	for _, item := range []struct {
		kind string
		read func() ([]workstore.BatchJournal, error)
	}{{"", m.History}, {ProfileApplicationKind, m.ProfileApplicationHistory}, {"mcp", m.MCPConnectionHistory}} {
		rows, err := item.read()
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, ApplicationJournal{BatchJournal: row, ApplicationKind: item.kind})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, out[i].Updated)
		b, _ := time.Parse(time.RFC3339Nano, out[j].Updated)
		if !a.Equal(b) {
			return a.After(b)
		}
		return out[i].Plan.ID > out[j].Plan.ID
	})
	return out, nil
}

func (m *Manager) ReverseApplication(ctx context.Context, transaction string) (ApplicationReversePlan, error) {
	var out ApplicationReversePlan
	if !validID.MatchString(transaction) {
		return out, result.Fail("INVALID_INPUT", "需要最新成功的应用事务 UUID", 2)
	}
	rows, err := m.ApplicationHistory()
	if err != nil {
		return out, err
	}
	found := false
	for _, row := range rows {
		if row.Plan.ID != transaction {
			continue
		}
		if found {
			return out, result.Fail("PLAN_KIND_MISMATCH", "应用历史身份存在歧义", 5)
		}
		out.ApplicationKind, found = row.ApplicationKind, true
	}
	if !found {
		return out, result.Fail("TRANSACTION_NOT_FOUND", "当前绑定没有此应用事务", 2)
	}
	switch out.ApplicationKind {
	case ProfileApplicationKind:
		out.BatchPlan, err = m.ReverseProfileApplication(ctx, transaction)
	case "mcp":
		out.BatchPlan, err = m.ReverseMCPConnection(ctx, transaction)
	default:
		out.BatchPlan, err = m.Reverse(transaction)
	}
	return out, err
}

// The reversed plan is looked up in fixed per-binding stores and validated by
// its original batch. An API/CLI caller cannot select or broaden its targets.
func (m *Manager) ApplyApplicationReverse(ctx context.Context, id, digest string) (workstore.BatchJournal, error) {
	var out workstore.BatchJournal
	if !validID.MatchString(id) || !validDigest.MatchString(digest) {
		return out, result.Fail("INVALID_INPUT", "需要回退计划 UUID 与完整摘要", 2)
	}
	kind, found := "", false
	for _, item := range []struct {
		kind  string
		batch *workstore.Batch
	}{{"", m.batch()}, {ProfileApplicationKind, m.profileApplicationBatch()}, {"mcp", m.connectionBatch()}} {
		_, err := item.batch.LoadPlan(id)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return out, err
		}
		if found {
			return out, result.Fail("PLAN_KIND_MISMATCH", "回退计划身份存在歧义", 5)
		}
		found, kind = true, item.kind
	}
	if !found {
		return out, result.Fail("PLAN_NOT_FOUND", "当前绑定没有此回退计划", 2)
	}
	switch kind {
	case ProfileApplicationKind:
		return m.ApplyProfileApplicationReverse(ctx, id, digest)
	case "mcp":
		return m.ApplyMCPConnectionReverse(ctx, id, digest)
	default:
		return m.ApplyReverse(ctx, id, digest)
	}
}

func (m *Manager) RecoverApplication() (int, error) {
	rows, err := m.ApplicationHistory()
	if err != nil {
		return 0, err
	}
	kind, found := "", false
	for _, row := range rows {
		if row.State != "prepared" {
			continue
		}
		if found && kind != row.ApplicationKind {
			return 0, result.Fail("RECOVERY_CONFLICT", "存在不同恢复范围的待处理事务，请到各自原绑定核对", 6)
		}
		kind, found = row.ApplicationKind, true
	}
	switch kind {
	case ProfileApplicationKind:
		return m.RecoverProfileApplication()
	case "mcp":
		return m.RecoverMCPConnections()
	default:
		return m.Recover()
	}
}
