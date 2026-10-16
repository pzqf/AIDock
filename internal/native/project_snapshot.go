package native

import (
	"context"
	"sort"
	"time"

	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

type ProjectSnapshot struct {
	ID                  string                  `json:"id"`
	Commit              string                  `json:"commit"`
	At                  string                  `json:"at"`
	Transaction         string                  `json:"transaction"`
	TransactionState    string                  `json:"transaction_state"`
	SourceDigest        string                  `json:"source_digest"`
	PreviousApplied     string                  `json:"previous_applied,omitempty"`
	Packages            int                     `json:"packages"`
	ReferencePackages   int                     `json:"reference_packages"`
	SourceFiles         int                     `json:"source_files"`
	Targets             int                     `json:"targets"`
	Adapters            map[string]string       `json:"adapter_versions"`
	Summary             projectsnapshot.Summary `json:"summary"`
	Integrity           string                  `json:"integrity"`
	ConfigurationInputs string                  `json:"configuration_inputs"`
	Replay              string                  `json:"replay"`
}

type ProjectSnapshots struct {
	Project  string            `json:"project"`
	Scope    string            `json:"scope_id"`
	Copy     string            `json:"copy"`
	Items    []ProjectSnapshot `json:"snapshots"`
	Boundary string            `json:"boundary"`
}

func (m *Manager) projectSnapshotArchive() (*projectsnapshot.Archive, error) {
	return projectsnapshot.New(m.Source.Store.Local, projectsnapshot.Binding{Project: m.Source.Owner, Scope: m.Source.Scope, Copy: m.Source.Copy, SourceRoot: m.Source.Store.Root, NativeRoot: m.Root})
}

func (m *Manager) projectSnapshotRecords(ctx context.Context) (*projectsnapshot.Archive, []projectsnapshot.Record, []workstore.BatchJournal, error) {
	a, err := m.projectSnapshotArchive()
	if err != nil {
		return nil, nil, nil, err
	}
	ids, err := a.References(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	records := []projectsnapshot.Record{}
	for _, id := range ids {
		r, e := a.Metadata(ctx, id)
		if e != nil {
			return nil, nil, nil, e
		}
		records = append(records, r)
	}
	journals, err := m.ProfileApplicationHistory()
	if err != nil {
		return nil, nil, nil, err
	}
	sort.Slice(records, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, records[i].At)
		b, _ := time.Parse(time.RFC3339Nano, records[j].At)
		if !a.Equal(b) {
			return a.After(b)
		}
		return records[i].ID > records[j].ID
	})
	return a, records, journals, nil
}

func snapshotState(r projectsnapshot.Record, journals []workstore.BatchJournal) (string, error) {
	for _, j := range journals {
		if j.Plan.ID != r.Transaction.ID {
			continue
		}
		if j.Plan.Digest != r.Transaction.Digest || workstore.Hash(jsonData(j.Plan)) != workstore.Hash(jsonData(r.Transaction)) {
			return "", result.Fail("SNAPSHOT_TRANSACTION_MISMATCH", "项目快照与原应用事务摘要不一致", 5)
		}
		return j.State, nil
	}
	return "unknown", nil
}

func snapshotSummary(r projectsnapshot.Record, state, integrity string) ProjectSnapshot {
	inputs := "LEGACY_MISSING"
	references := 0
	if r.Configuration != nil {
		inputs = "CAPTURED_ORIGINAL_LAYERS"
		references = len(r.Configuration.ReferencePackages)
	}
	files := 0
	for _, e := range r.Source.Entries {
		if !e.Dir {
			files++
		}
	}
	return ProjectSnapshot{ID: r.ID, Commit: r.Commit, At: r.At, Transaction: r.Transaction.ID, TransactionState: state, SourceDigest: r.Effective.Snapshot, PreviousApplied: r.PreviousApplied, Packages: len(r.Effective.Packages), ReferencePackages: references, SourceFiles: files, Targets: len(r.Transaction.Targets), Adapters: r.Adapters, Summary: r.Summary, Integrity: integrity, ConfigurationInputs: inputs, Replay: "NOT_IMPLEMENTED"}
}

func (m *Manager) ProjectSnapshots(ctx context.Context) (ProjectSnapshots, error) {
	out := ProjectSnapshots{Project: m.Source.Owner, Scope: m.Source.Scope, Copy: m.Source.Copy, Items: []ProjectSnapshot{}, Boundary: "仅列本版本起的 Profile 联合应用快照；不补造旧历史。完整规范源及有效技能包正文保存在受限本机 Git，原生备份另存私有区。事务状态由原应用记录核对，仅表示当次提交；列表未核对正文完整性或当前策略。查看与核验不修改项目，不运行工具；恢复为已保存草稿需另行预览并确认，原生工具应用仍须单独准备。完整项目 replay 写入与依赖恢复尚未开放。旧独立技能规则、MCP 和副本批次仍使用原恢复点。"}
	_, records, journals, err := m.projectSnapshotRecords(ctx)
	if err != nil {
		return out, err
	}
	for _, r := range records {
		state, e := snapshotState(r, journals)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, snapshotSummary(r, state, "NOT_CHECKED"))
	}
	return out, nil
}

func (m *Manager) CheckProjectSnapshot(ctx context.Context, id string) (ProjectSnapshot, error) {
	var out ProjectSnapshot
	a, err := m.projectSnapshotArchive()
	if err != nil {
		return out, err
	}
	r, err := a.Load(ctx, id)
	if err != nil {
		return out, err
	}
	journals, err := m.ProfileApplicationHistory()
	if err != nil {
		return out, err
	}
	state, err := snapshotState(r, journals)
	if err != nil {
		return out, err
	}
	return snapshotSummary(r, state, "PASS_ARCHIVED_BYTES"), nil
}

// Snapshot assembly happens before the final permission/target lock section.
// The existing joint validator rechecks every input and expiry afterward;
// an archived intent never substitutes for a committed transaction.
func (m *Manager) captureProfileSnapshot(ctx context.Context, plan ProfileApplicationPlan) error {
	if plan.Changes == 0 {
		return result.Fail("PROFILE_APPLICATION_CURRENT", "技能、规则与有效 MCP 连接均已一致，无需重复安装", 3)
	}
	p, err := m.profileApplicationNative(ctx, plan.Request)
	if err != nil {
		return err
	}
	a, records, journals, err := m.projectSnapshotRecords(ctx)
	if err != nil {
		return err
	}
	previous := ""
	for _, r := range records {
		state, e := snapshotState(r, journals)
		if e != nil {
			return e
		}
		if state != "committed" || len(r.Transaction.Targets) != len(plan.Transaction.Targets) {
			continue
		}
		after := map[string]string{}
		for _, t := range r.Transaction.Targets {
			after[t.Path] = workstore.Digest(t.After)
		}
		same := true
		for _, t := range plan.Transaction.Targets {
			if after[t.Path] != workstore.Digest(t.Before) {
				same = false
				break
			}
		}
		if same {
			previous = r.ID
			break
		}
	}
	var saved workstore.Tree
	var configuration source.SnapshotConfiguration
	err = m.Source.WithSnapshotInputs(func(now source.Export, tree workstore.Tree, inputs source.SnapshotConfiguration) error {
		if workstore.Hash(jsonData(now)) != workstore.Hash(jsonData(p.Source)) {
			return result.Fail("PLAN_STALE", "保存应用快照前规范源已变化", 3)
		}
		saved, configuration = tree, inputs
		return nil
	})
	if err != nil {
		return err
	}
	adapters := map[string]string{}
	for _, t := range p.Targets {
		adapters[t.Adapter.ID] = t.Adapter.Version
	}
	_, err = a.Capture(ctx, projectsnapshot.Content{Trigger: "aidock apply", Source: saved, Effective: p.Source, Transaction: plan.Transaction, Adapters: adapters, PreviousApplied: previous, Configuration: &configuration}, m.Source.Store, m.connectionStore())
	return err
}
