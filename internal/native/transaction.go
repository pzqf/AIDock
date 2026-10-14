package native

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

func (m *Manager) batch() *workstore.Batch {
	paths := []string{}
	for _, a := range Adapters() {
		paths = append(paths, a.Directory)
	}
	return &workstore.Batch{Boundary: m.Root, Local: m.Source.Store.Local, Identity: m.Source.Owner + "/" + m.Source.Scope + "/" + m.Source.Copy, Paths: paths, Blobs: m.Source.Store}
}
func batchTargets(p Plan) []workstore.BatchTarget {
	out := []workstore.BatchTarget{}
	for _, t := range p.Targets {
		out = append(out, workstore.BatchTarget{Path: t.Adapter.Directory, Before: t.Before, After: t.After})
	}
	return out
}
func (m *Manager) applyNative(ctx context.Context, p Plan) error {
	b := m.batch()
	tx, err := b.Preview("apply", batchTargets(p))
	if err != nil {
		return err
	}
	return m.Source.Store.Locked(func() error {
		if err := m.Source.Store.Pending(); err != nil {
			return err
		}
		now, err := m.Source.Store.Capture(m.Source.Store.Root)
		if err != nil {
			return err
		}
		if workstore.Digest(now) != p.Source.Snapshot {
			return result.Fail("PLAN_STALE", "开始工具事务前源或草稿已变化", 3)
		}
		_, err = b.Apply(ctx, tx.ID, tx.Digest)
		return err
	})
}
func (m *Manager) History() ([]workstore.BatchJournal, error) { return m.batch().History() }
func (m *Manager) Recover() (int, error)                      { return m.batch().Recover() }

// Rehearsal uses the production batch writer on independent copies of observed
// tool trees. It never changes the bound tool targets or initializes a tool.
type rehearsalRecord struct {
	Version    int    `json:"schema_version"`
	ID         string `json:"id"`
	Created    string `json:"created"`
	NativePlan string `json:"native_plan"`
	Root       string `json:"root"`
	Owner      string `json:"owner"`
	Scope      string `json:"scope_id"`
	Copy       string `json:"copy,omitempty"`
	Digest     string `json:"digest"`
}
type TransactionSummary struct {
	ID        string `json:"id"`
	Action    string `json:"action"`
	Reference string `json:"reference,omitempty"`
	State     string `json:"state"`
	Updated   string `json:"updated"`
	Targets   int    `json:"targets"`
	Changes   int    `json:"changes"`
}
type Rehearsal struct {
	ID           string               `json:"id"`
	Created      string               `json:"created"`
	NativePlan   string               `json:"native_plan"`
	Root         string               `json:"root"`
	Pending      bool                 `json:"pending"`
	Status       string               `json:"status"`
	Transactions []TransactionSummary `json:"transactions"`
	Boundary     string               `json:"boundary"`
}

func latestCommitted(items []TransactionSummary) *TransactionSummary {
	for _, item := range items {
		if item.State == "committed" {
			return &item
		}
	}
	return nil
}

func (m *Manager) rehearsalsDir() string {
	return filepath.Join(m.Source.Store.Local, "native-rehearsals", workstore.Hash([]byte(strings.ToLower(m.Root)+"\n"+m.Source.Owner+"/"+m.Source.Scope+"/"+m.Source.Copy)))
}
func (m *Manager) rehearsalBatch(id string) *workstore.Batch {
	run := filepath.Join(m.rehearsalsDir(), "runs", id)
	b := m.batch()
	b.Boundary = filepath.Join(run, "project")
	b.Local = filepath.Join(run, "local")
	b.Identity = "rehearsal/" + id
	return b
}
func recordDigest(v rehearsalRecord) string { v.Digest = ""; return workstore.Hash(jsonData(v)) }
func (m *Manager) loadRehearsal(id string) (rehearsalRecord, error) {
	var v rehearsalRecord
	if !validID.MatchString(id) {
		return v, result.Fail("INVALID_INPUT", "需要当前绑定的预演 UUID", 2)
	}
	data, err := workstore.ReadFile(filepath.Join(m.rehearsalsDir(), "sessions", id+".json"))
	if err != nil {
		return v, err
	}
	if err = strict(data, &v); err != nil {
		return v, err
	}
	if v.Version != 1 || v.ID != id || v.Root != m.Root || v.Owner != m.Source.Owner || v.Scope != m.Source.Scope || v.Copy != m.Source.Copy || v.Digest != recordDigest(v) {
		return v, result.Fail("BACKUP_INVALID", "预演身份或摘要不匹配", 5)
	}
	return v, nil
}
func (m *Manager) rehearsalReport(v rehearsalRecord) (Rehearsal, error) {
	b := m.rehearsalBatch(v.ID)
	js, err := b.History()
	if err != nil {
		return Rehearsal{}, err
	}
	r := Rehearsal{ID: v.ID, Created: v.Created, NativePlan: v.NativePlan, Root: b.Boundary, Status: "READY", Transactions: []TransactionSummary{}, Boundary: "仅在独立预演项目写入完整技能包；项目技能源、已绑定工具目标及页面草稿均保留。不启动工具或执行技能，不能作为实际会话生效证据。"}
	for _, j := range js {
		if j.State == "prepared" {
			r.Pending = true
		}
		if j.Plan.Action == "seed" {
			continue
		}
		x := TransactionSummary{ID: j.Plan.ID, Action: j.Plan.Action, Reference: j.Plan.Reference, State: j.State, Updated: j.Updated, Targets: len(j.Plan.Targets)}
		for _, t := range j.Plan.Targets {
			x.Changes += len(workstore.Changes(t.Before, t.After))
		}
		r.Transactions = append(r.Transactions, x)
	}
	if err := b.Pending(); err != nil {
		r.Pending = true
	}
	if r.Pending {
		r.Status = "PENDING"
	} else if len(r.Transactions) > 0 {
		t := r.Transactions[0]
		if t.State == "recovered" {
			r.Status = "RECOVERED"
		} else {
			switch t.Action {
			case "apply":
				r.Status = "APPLIED"
			case "rollback":
				r.Status = "ROLLED_BACK"
			case "undo-rollback":
				r.Status = "UNDONE"
			}
		}
	}
	return r, nil
}
func (m *Manager) Rehearse(ctx context.Context, id, digest string) (Rehearsal, error) {
	latest, err := m.RehearsalLatest()
	if err != nil {
		return Rehearsal{}, err
	}
	if latest != nil && latest.Pending {
		return Rehearsal{}, result.Fail("NATIVE_RECOVERY_REQUIRED", "先恢复未完成的预演，再开始新预演", 6)
	}
	p, err := m.Diff(ctx, id, digest)
	if err != nil {
		return Rehearsal{}, err
	}
	if p.Blocked {
		return Rehearsal{}, result.Fail("NATIVE_PREFLIGHT_FAILED", "存在目标冲突或静态阻断项，不能预演", 5)
	}
	if err = ctx.Err(); err != nil {
		return Rehearsal{}, err
	}
	v := rehearsalRecord{Version: 1, ID: workstore.ID(), Created: time.Now().UTC().Format(time.RFC3339Nano), NativePlan: p.ID, Root: m.Root, Owner: m.Source.Owner, Scope: m.Source.Scope, Copy: m.Source.Copy}
	v.Digest = recordDigest(v)
	b := m.rehearsalBatch(v.ID)
	if err = workstore.CheckLinks(b.Boundary); err != nil {
		return Rehearsal{}, err
	}
	if err = os.MkdirAll(b.Boundary, 0700); err != nil {
		return Rehearsal{}, err
	}
	if err = save(filepath.Join(m.rehearsalsDir(), "sessions", v.ID+".json"), jsonData(v)); err != nil {
		return Rehearsal{}, err
	}
	// Copy observed original trees first, including existing handwritten items.
	seed := []workstore.BatchTarget{}
	for _, t := range p.Targets {
		seed = append(seed, workstore.BatchTarget{Path: t.Adapter.Directory, Before: workstore.Tree{Entries: map[string]workstore.Entry{}}, After: t.Before})
	}
	baseline, err := b.Preview("seed", seed)
	if err != nil {
		return Rehearsal{}, err
	}
	if _, err = b.Apply(ctx, baseline.ID, baseline.Digest); err != nil {
		return Rehearsal{}, err
	}
	apply, err := b.Preview("apply", batchTargets(p))
	if err != nil {
		return Rehearsal{}, err
	}
	if _, err = b.Apply(ctx, apply.ID, apply.Digest); err != nil {
		return Rehearsal{}, err
	}
	return m.rehearsalReport(v)
}
func (m *Manager) RehearsalLatest() (*Rehearsal, error) {
	dir := filepath.Join(m.rehearsalsDir(), "sessions")
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
	vs := []rehearsalRecord{}
	for _, f := range files {
		if strings.HasPrefix(f.Name(), ".preview-") {
			continue
		}
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			return nil, result.Fail("BACKUP_INVALID", "未知预演记录", 5)
		}
		v, err := m.loadRehearsal(strings.TrimSuffix(f.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		vs = append(vs, v)
	}
	if len(vs) == 0 {
		return nil, nil
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].Created > vs[j].Created })
	var latest *Rehearsal
	for i, record := range vs {
		r, err := m.rehearsalReport(record)
		if err != nil {
			return nil, err
		}
		if r.Pending {
			return &r, nil
		}
		if i == 0 {
			latest = &r
		}
	}
	return latest, nil
}
func (m *Manager) RehearsalReverse(session, transaction string) (workstore.BatchPlan, error) {
	v, err := m.loadRehearsal(session)
	if err != nil {
		return workstore.BatchPlan{}, err
	}
	report, err := m.rehearsalReport(v)
	if err != nil {
		return workstore.BatchPlan{}, err
	}
	if report.Pending {
		return workstore.BatchPlan{}, result.Fail("NATIVE_RECOVERY_REQUIRED", "预演事务尚未恢复", 6)
	}
	current := latestCommitted(report.Transactions)
	if current == nil || current.ID != transaction {
		return workstore.BatchPlan{}, result.Fail("TRANSACTION_NOT_FOUND", "请选择最新成功的预演事务", 2)
	}
	return m.rehearsalBatch(session).Reverse(transaction)
}
func (m *Manager) RehearsalApply(ctx context.Context, session, id, digest string) (Rehearsal, error) {
	v, err := m.loadRehearsal(session)
	if err != nil {
		return Rehearsal{}, err
	}
	// Reject arbitrary apply/seed plans: this endpoint only confirms a reverse
	// of the latest successful transaction from this exact rehearsal session.
	b := m.rehearsalBatch(session)
	js, err := b.History()
	if err != nil {
		return Rehearsal{}, err
	}
	plan, err := b.LoadPlan(id)
	if err != nil {
		return Rehearsal{}, err
	}
	current := ""
	for _, j := range js {
		if j.State == "committed" {
			current = j.Plan.ID
			break
		}
	}
	if current == "" || plan.Reference != current || (plan.Action != "rollback" && plan.Action != "undo-rollback") {
		return Rehearsal{}, result.Fail("PLAN_KIND_MISMATCH", "仅接受最新预演的回退/撤销计划", 2)
	}
	if _, err = b.Apply(ctx, id, digest); err != nil {
		return Rehearsal{}, err
	}
	return m.rehearsalReport(v)
}
func (m *Manager) RehearsalRecover(session string) (Rehearsal, error) {
	v, err := m.loadRehearsal(session)
	if err != nil {
		return Rehearsal{}, err
	}
	if _, err = m.rehearsalBatch(session).Recover(); err != nil {
		return Rehearsal{}, err
	}
	return m.rehearsalReport(v)
}
