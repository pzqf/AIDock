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
	Version    int         `json:"schema_version"`
	ID         string      `json:"id"`
	Plan       string      `json:"resolution_plan"`
	PlanDigest string      `json:"resolution_digest"`
	Owner      string      `json:"owner"`
	Scope      string      `json:"scope_id"`
	Created    string      `json:"created"`
	Digest     string      `json:"digest"`
	Generation *Generation `json:"generation,omitempty"`
}
type Transaction struct {
	ID        string `json:"id"`
	Action    string `json:"action"`
	Reference string `json:"reference,omitempty"`
	State     string `json:"state"`
	Updated   string `json:"updated"`
	Targets   int    `json:"targets"`
	Changes   int    `json:"changes"`
	Generated int    `json:"generated_roots,omitempty"`
}
type Rehearsal struct {
	ID           string                  `json:"id"`
	Plan         string                  `json:"resolution_plan"`
	Root         string                  `json:"root"`
	Created      string                  `json:"created"`
	Status       string                  `json:"status"`
	Pending      bool                    `json:"pending"`
	Transactions []Transaction           `json:"transactions"`
	Boundary     string                  `json:"boundary"`
	Coordination string                  `json:"coordination,omitempty"`
	Roots        []workstore.RootBinding `json:"bound_roots,omitempty"`
	Warning      string                  `json:"warning,omitempty"`
	Generation   *Generation             `json:"generation,omitempty"`
}

type rehearsalWriter interface {
	Preview(string, []workstore.BatchTarget) (workstore.BatchPlan, error)
	LoadPlan(string) (workstore.BatchPlan, error)
	Apply(context.Context, string, string) (workstore.BatchJournal, error)
	Pending() error
	History() ([]workstore.BatchJournal, error)
	Reverse(string) (workstore.BatchPlan, error)
	Recover() (int, error)
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
	if (record.Version < 1 || record.Version > 3) || record.ID != id || record.Owner != m.Source.Owner || record.Scope != m.Source.Scope || record.Digest != rehearsalDigest(record) || (record.Version == 3) != (record.Generation != nil) {
		return record, Resolution{}, result.Fail("BACKUP_INVALID", "副本预演身份或摘要损坏", 5)
	}
	p, err := m.loadResolution(record.Plan, record.PlanDigest)
	if err == nil && record.Generation != nil {
		err = validateGeneration(*record.Generation, p)
	}
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
func (m *Manager) rehearsalCoordinator(v rehearsalRecord, p Resolution) *workstore.Coordinator {
	run := filepath.Join(m.resolutionDir(), "runs", v.ID)
	roots := []workstore.RootBinding{{Path: "control", Root: filepath.Join(run, "control-project", "baseline")}}
	for _, target := range p.Targets {
		roots = append(roots, workstore.RootBinding{Path: "copies/" + target.Copy.ID + "/.aidock", Root: filepath.Join(run, "copies", target.Copy.ID, "project", ".aidock")})
	}
	if v.Generation != nil {
		for _, target := range v.Generation.Targets {
			for _, output := range target.Outputs {
				roots = append(roots, workstore.RootBinding{Path: generatedPath(target.Copy, output.Adapter.ID), Root: filepath.Join(run, "copies", target.Copy, "project", filepath.FromSlash(output.Adapter.Directory))})
			}
		}
	}
	return &workstore.Coordinator{Local: filepath.Join(run, "local"), Identity: resolutionIdentity(m.Source.Owner, m.Source.Scope, v.ID), Roots: roots, Blobs: m.Control, Hook: m.RehearsalHook}
}
func (m *Manager) rehearsalWriter(v rehearsalRecord, p Resolution) rehearsalWriter {
	if v.Version >= 2 {
		return m.rehearsalCoordinator(v, p)
	}
	b := m.rehearsalBatch(v, p)
	b.Hook = m.RehearsalHook
	return b
}
func (m *Manager) rehearsalReport(v rehearsalRecord, p Resolution) (Rehearsal, error) {
	b := m.rehearsalWriter(v, p)
	js, err := b.History()
	if err != nil {
		return Rehearsal{}, err
	}
	out := Rehearsal{ID: v.ID, Plan: v.Plan, Root: m.rehearsalBatch(v, p).Boundary, Created: v.Created, Status: "READY", Transactions: []Transaction{}, Boundary: "应用、共同基线推进、回退与撤销均发生在独立预演副本。已登记项目、真实共同基线、原生工具目录及页面草稿不修改，不执行生成器/技能代码；不能作为工具会话生效或真实跨项目提交证据。"}
	if v.Version >= 2 {
		out.Root = filepath.Join(m.resolutionDir(), "runs", v.ID)
		out.Coordination = "fixed-roots"
		out.Roots = m.rehearsalCoordinator(v, p).Roots
		out.Boundary += " 本次每个目标与基线目录独立绑定，共同加锁并设持久恢复标记；未收口时禁止单目录继续写入。"
	}
	if v.Generation != nil {
		out.Generation = v.Generation
		out.Boundary = "本次在独立副本联合应用/恢复/回退/撤销技能源、内置编译器生成的工具完整包与共同基线。清单记录源版本、原始输出观察、完整生成字节和归属。已登记项目、真实工具目录与真实基线不写；不执行第三方生成器或技能脚本，不能证明真实工具会话生效。每个根共同加锁并设持久恢复标记。"
		if v.Generation.Repair {
			out.Boundary = "本次是纯生成物修复：按每个目标自己已保存的源编译，源和共同基线保留。工具包的修复、回退与撤销只在独立预演目录发生；本机排除项及手写包保留。不执行第三方脚本，不证明会话生效，已登记目录不修改。"
		}
		if v.Generation.Generator == externalMirrorGenerator {
			out.Boundary = "本次在独立副本联合预演与恢复技能源、通过离线校验的外部返回完整包和共同基线；本机排除与手写项保留。清单绑定外部暂存、校验记录和完整输出摘要。未运行外部生成器，不证明工具会话生效，已登记项目、真实工具目录和真实基线未修改。已创建的恢复点不依赖暂存目录继续存在。"
		}
	}
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
			if strings.Contains(target.Path, "/generated/") {
				item.Generated++
			} else if target.Path != "control" {
				item.Targets++
			}
		}
		out.Transactions = append(out.Transactions, item)
	}
	if err = b.Pending(); err != nil {
		out.Pending = true
		out.Warning = err.Error()
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
	if p.Repair {
		return Rehearsal{}, result.Fail("GENERATOR_PLAN_REQUIRED", "纯生成修复需确认完整生成清单", 4)
	}
	if err = m.revalidate(ctx, p); err != nil {
		return Rehearsal{}, err
	}
	return m.rehearse(ctx, p, nil)
}

func (m *Manager) rehearse(ctx context.Context, p Resolution, generated *Generation) (Rehearsal, error) {
	// Also check older pending sessions when the generated entry point is used.
	latest, err := m.RehearsalLatest()
	if err != nil {
		return Rehearsal{}, err
	}
	if latest != nil && latest.Pending {
		return Rehearsal{}, result.Fail("REPLICA_RECOVERY_REQUIRED", "先恢复未完成副本预演", 6)
	}
	if err = ctx.Err(); err != nil {
		return Rehearsal{}, err
	}
	v := rehearsalRecord{Version: 2, ID: workstore.ID(), Plan: p.ID, PlanDigest: p.Digest, Owner: m.Source.Owner, Scope: m.Source.Scope, Created: time.Now().UTC().Format(time.RFC3339Nano)}
	if generated != nil {
		v.Version = 3
		v.Generation = generated
	}
	v.Digest = rehearsalDigest(v)
	c := m.rehearsalCoordinator(v, p)
	for _, root := range c.Roots {
		if err = workstore.CheckLinks(root.Root); err != nil {
			return Rehearsal{}, err
		}
		if err = os.MkdirAll(filepath.Dir(root.Root), 0700); err != nil {
			return Rehearsal{}, err
		}
	}
	// Seeding is also durable, but apply-failure hooks start only after seed.
	c.Hook = nil
	var b rehearsalWriter = c
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
	if generated != nil {
		for _, target := range generated.Targets {
			for _, output := range target.Outputs {
				path := generatedPath(target.Copy, output.Adapter.ID)
				seed = append(seed, workstore.BatchTarget{Path: path, Before: workstore.Tree{Entries: map[string]workstore.Entry{}}, After: output.Before})
				apply = append(apply, workstore.BatchTarget{Path: path, Before: output.Before, After: output.After})
			}
		}
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
	c.Hook = m.RehearsalHook
	if _, err = b.Apply(ctx, tx.ID, tx.Digest); err != nil {
		return Rehearsal{}, err
	}
	return m.rehearsalReport(v, p)
}
func (m *Manager) RehearsalHistory() ([]Rehearsal, error) {
	dir := filepath.Join(m.resolutionDir(), "sessions")
	if err := workstore.CheckLinks(dir); err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []Rehearsal{}, nil
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
	history := []Rehearsal{}
	for _, v := range items {
		_, p, err := m.loadRehearsal(v.ID)
		if err != nil {
			return nil, err
		}
		r, err := m.rehearsalReport(v, p)
		if err != nil {
			return nil, err
		}
		history = append(history, r)
	}
	return history, nil
}
func (m *Manager) RehearsalLatest() (*Rehearsal, error) {
	history, err := m.RehearsalHistory()
	if err != nil {
		return nil, err
	}
	for _, item := range history {
		if item.Pending {
			v := item
			return &v, nil
		}
	}
	if len(history) == 0 {
		return nil, nil
	}
	return &history[0], nil
}
func (m *Manager) RehearsalShow(session string) (Rehearsal, error) {
	v, p, err := m.loadRehearsal(session)
	if err != nil {
		return Rehearsal{}, err
	}
	return m.rehearsalReport(v, p)
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
	return m.rehearsalWriter(v, p).Reverse(transaction)
}
func (m *Manager) RehearsalApply(ctx context.Context, session, id, digest string) (Rehearsal, error) {
	v, p, err := m.loadRehearsal(session)
	if err != nil {
		return Rehearsal{}, err
	}
	b := m.rehearsalWriter(v, p)
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
	if _, err = m.rehearsalWriter(v, p).Recover(); err != nil {
		return Rehearsal{}, err
	}
	return m.rehearsalReport(v, p)
}
