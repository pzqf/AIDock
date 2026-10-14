package replica

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

type rehearsalRecord struct {
	Version    int    `json:"schema_version"`
	ID         string `json:"id"`
	Plan       string `json:"resolution_plan"`
	PlanDigest string `json:"resolution_digest"`
	Owner      string `json:"owner"`
	Scope      string `json:"scope_id"`
	Created    string `json:"created"`
	Digest     string `json:"digest"`
}
type Transaction struct {
	ID        string `json:"id"`
	Action    string `json:"action"`
	Reference string `json:"reference,omitempty"`
	State     string `json:"state"`
	Updated   string `json:"updated"`
	Targets   int    `json:"targets"`
	Changes   int    `json:"changes"`
}
type Rehearsal struct {
	ID           string        `json:"id"`
	Plan         string        `json:"resolution_plan"`
	Root         string        `json:"root"`
	Created      string        `json:"created"`
	Status       string        `json:"status"`
	Pending      bool          `json:"pending"`
	Transactions []Transaction `json:"transactions"`
	Boundary     string        `json:"boundary"`
}

func rehearsalDigest(v rehearsalRecord) string { v.Digest = ""; return workstore.Hash(data(v)) }
func (m *Manager) loadRehearsal(id string) (rehearsalRecord, Resolution, error) {
	var record rehearsalRecord
	if !uuid.MatchString(id) {
		return record, Resolution{}, result.Fail("INVALID_INPUT", "需要当前项目的副本预演 UUID", 2)
	}
	b, err := workstore.ReadFile(filepath.Join(m.resolutionDir(), "sessions", id+".json"))
	if err != nil {
		return record, Resolution{}, err
	}
	if err = decode(b, &record); err != nil {
		return record, Resolution{}, err
	}
	if record.Version != 1 || record.ID != id || record.Owner != m.Source.Owner || record.Scope != m.Source.Scope || record.Digest != rehearsalDigest(record) {
		return record, Resolution{}, result.Fail("BACKUP_INVALID", "副本预演身份或摘要损坏", 5)
	}
	p, err := m.loadResolution(record.Plan, record.PlanDigest)
	return record, p, err
}
func (m *Manager) rehearsalBatch(v rehearsalRecord, p Resolution) *workstore.Batch {
	paths := []string{"control"}
	for _, target := range p.Targets {
		paths = append(paths, "copies/"+target.Copy.ID+"/.aidock")
	}
	sort.Strings(paths)
	run := filepath.Join(m.resolutionDir(), "runs", v.ID)
	return &workstore.Batch{Boundary: filepath.Join(run, "project"), Local: filepath.Join(run, "local"), Identity: resolutionIdentity(m.Source.Owner, m.Source.Scope, v.ID), Paths: paths, Blobs: m.Control}
}
func (m *Manager) rehearsalReport(v rehearsalRecord, p Resolution) (Rehearsal, error) {
	b := m.rehearsalBatch(v, p)
	js, err := b.History()
	if err != nil {
		return Rehearsal{}, err
	}
	out := Rehearsal{ID: v.ID, Plan: v.Plan, Root: b.Boundary, Created: v.Created, Status: "READY", Transactions: []Transaction{}, Boundary: "应用、共同基线推进、回退与撤销均发生在独立预演副本。已登记项目、真实共同基线、原生工具目录及页面草稿不修改，不执行生成器/技能代码；不能作为工具会话生效或真实跨项目提交证据。"}
	for _, j := range js {
		if j.State == "prepared" {
			out.Pending = true
		}
		if j.Plan.Action == "seed" {
			continue
		}
		item := Transaction{ID: j.Plan.ID, Action: j.Plan.Action, Reference: j.Plan.Reference, State: j.State, Updated: j.Updated}
		for _, target := range j.Plan.Targets {
			item.Changes += len(workstore.Changes(target.Before, target.After))
			if target.Path != "control" {
				item.Targets++
			}
		}
		out.Transactions = append(out.Transactions, item)
	}
	if err = b.Pending(); err != nil {
		out.Pending = true
	}
	if out.Pending {
		out.Status = "PENDING"
	} else if len(out.Transactions) > 0 {
		j := out.Transactions[0]
		if j.State == "recovered" {
			out.Status = "RECOVERED"
		} else {
			switch j.Action {
			case "apply":
				out.Status = "APPLIED"
			case "rollback":
				out.Status = "ROLLED_BACK"
			case "undo-rollback":
				out.Status = "UNDONE"
			}
		}
	}
	return out, nil
}
func (m *Manager) Rehearse(ctx context.Context, id, digest string) (Rehearsal, error) {
	latest, err := m.RehearsalLatest()
	if err != nil {
		return Rehearsal{}, err
	}
	if latest != nil && latest.Pending {
		return Rehearsal{}, result.Fail("REPLICA_RECOVERY_REQUIRED", "先恢复未完成副本预演", 6)
	}
	p, err := m.loadResolution(id, digest)
	if err != nil {
		return Rehearsal{}, err
	}
	if err = m.revalidate(ctx, p); err != nil {
		return Rehearsal{}, err
	}
	if err = ctx.Err(); err != nil {
		return Rehearsal{}, err
	}
	v := rehearsalRecord{Version: 1, ID: workstore.ID(), Plan: p.ID, PlanDigest: p.Digest, Owner: m.Source.Owner, Scope: m.Source.Scope, Created: time.Now().UTC().Format(time.RFC3339Nano)}
	v.Digest = rehearsalDigest(v)
	b := m.rehearsalBatch(v, p)
	if err = workstore.CheckLinks(b.Boundary); err != nil {
		return Rehearsal{}, err
	}
	if err = os.MkdirAll(b.Boundary, 0700); err != nil {
		return Rehearsal{}, err
	}
	if err = saveLocal(filepath.Join(m.resolutionDir(), "sessions", v.ID+".json"), v); err != nil {
		return Rehearsal{}, err
	}
	seed := []workstore.BatchTarget{{Path: "control", Before: workstore.Tree{Entries: map[string]workstore.Entry{}}, After: p.ControlBefore}}
	apply := []workstore.BatchTarget{{Path: "control", Before: p.ControlBefore, After: p.ControlAfter}}
	for _, t := range p.Targets {
		path := "copies/" + t.Copy.ID + "/.aidock"
		seed = append(seed, workstore.BatchTarget{Path: path, Before: workstore.Tree{Entries: map[string]workstore.Entry{}}, After: t.Before})
		apply = append(apply, workstore.BatchTarget{Path: path, Before: t.Before, After: t.After})
	}
	baseline, err := b.Preview("seed", seed)
	if err != nil {
		return Rehearsal{}, err
	}
	if _, err = b.Apply(ctx, baseline.ID, baseline.Digest); err != nil {
		return Rehearsal{}, err
	}
	tx, err := b.Preview("apply", apply)
	if err != nil {
		return Rehearsal{}, err
	}
	b.Hook = m.RehearsalHook
	if _, err = b.Apply(ctx, tx.ID, tx.Digest); err != nil {
		return Rehearsal{}, err
	}
	return m.rehearsalReport(v, p)
}
func (m *Manager) RehearsalLatest() (*Rehearsal, error) {
	dir := filepath.Join(m.resolutionDir(), "sessions")
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
	items := []rehearsalRecord{}
	for _, f := range files {
		if strings.HasPrefix(f.Name(), ".resolution-") {
			continue
		}
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			return nil, result.Fail("BACKUP_INVALID", "未知副本预演记录", 5)
		}
		v, _, err := m.loadRehearsal(strings.TrimSuffix(f.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Created > items[j].Created })
	var latest *Rehearsal
	for i, v := range items {
		_, p, err := m.loadRehearsal(v.ID)
		if err != nil {
			return nil, err
		}
		r, err := m.rehearsalReport(v, p)
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
	v, p, err := m.loadRehearsal(session)
	if err != nil {
		return workstore.BatchPlan{}, err
	}
	r, err := m.rehearsalReport(v, p)
	if err != nil {
		return workstore.BatchPlan{}, err
	}
	if r.Pending {
		return workstore.BatchPlan{}, result.Fail("REPLICA_RECOVERY_REQUIRED", "副本预演尚未恢复", 6)
	}
	current := ""
	for _, t := range r.Transactions {
		if t.State == "committed" {
			current = t.ID
			break
		}
	}
	if current == "" || current != transaction {
		return workstore.BatchPlan{}, result.Fail("TRANSACTION_NOT_FOUND", "选择最新成功的副本预演事务", 2)
	}
	return m.rehearsalBatch(v, p).Reverse(transaction)
}
func (m *Manager) RehearsalApply(ctx context.Context, session, id, digest string) (Rehearsal, error) {
	v, p, err := m.loadRehearsal(session)
	if err != nil {
		return Rehearsal{}, err
	}
	b := m.rehearsalBatch(v, p)
	plan, err := b.LoadPlan(id)
	if err != nil {
		return Rehearsal{}, err
	}
	js, err := b.History()
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
	if current == "" || plan.Reference != current || plan.Action != "rollback" && plan.Action != "undo-rollback" {
		return Rehearsal{}, result.Fail("PLAN_KIND_MISMATCH", "仅确认最新副本预演的回退或撤销", 2)
	}
	if _, err = b.Apply(ctx, id, digest); err != nil {
		return Rehearsal{}, err
	}
	return m.rehearsalReport(v, p)
}
func (m *Manager) RehearsalRecover(session string) (Rehearsal, error) {
	v, p, err := m.loadRehearsal(session)
	if err != nil {
		return Rehearsal{}, err
	}
	if _, err = m.rehearsalBatch(v, p).Recover(); err != nil {
		return Rehearsal{}, err
	}
	return m.rehearsalReport(v, p)
}
