package workstore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/result"
)

const NativeBarrier = ".aidock-native-pending.json"

// Batch coordinates fixed tool roots under one explicitly bound project. Blob
// backups are durable before the prepared journal or any target replacement.
// Visibility is per-file; the journal provides recovery, not atomic visibility.
type Batch struct {
	Boundary, Local, Identity string
	Paths                     []string
	FilePaths                 []string
	Blobs                     *Store
	Hook                      func(string, int) error
}
type BatchTarget struct {
	Path   string `json:"path"`
	File   bool   `json:"file,omitempty"`
	Before Tree   `json:"before"`
	After  Tree   `json:"after"`
}
type BatchPlan struct {
	Version       int           `json:"schema_version"`
	Kind          string        `json:"kind"`
	ID            string        `json:"id"`
	Root          string        `json:"root"`
	Identity      string        `json:"identity"`
	Action        string        `json:"action"`
	Reference     string        `json:"reference,omitempty"`
	Created       string        `json:"created"`
	Targets       []BatchTarget `json:"targets"`
	Parents       []string      `json:"created_parents"`
	RemoveParents []string      `json:"remove_empty_parents,omitempty"`
	Digest        string        `json:"digest"`
	Roots         []RootBinding `json:"bound_roots,omitempty"`
}
type BatchJournal struct {
	Plan    BatchPlan `json:"plan"`
	State   string    `json:"state"`
	Updated string    `json:"updated"`
}
type batchBarrier struct {
	ID       string `json:"id"`
	Local    string `json:"local"`
	Identity string `json:"identity"`
	Digest   string `json:"digest"`
}

func batchDigest(p BatchPlan) string { p.Digest = ""; b, _ := json.Marshal(p); return Hash(b) }
func (b *Batch) anchor() *Store      { return &Store{Root: b.Boundary, Local: b.Local} }
func (b *Batch) dir() string {
	return filepath.Join(b.Local, "native-transactions", Hash([]byte(strings.ToLower(b.Boundary)+"\n"+b.Identity)))
}
func (b *Batch) store(p string) *Store {
	return &Store{Root: filepath.Join(b.Boundary, filepath.FromSlash(p)), Local: b.Local}
}
func (b *Batch) marker() string { return filepath.Join(b.Boundary, NativeBarrier) }
func (b *Batch) locked(fn func() error) error {
	if b.Identity == "" || len(b.Paths) == 0 {
		return result.Fail("INVALID_INPUT", "事务身份与固定目标不可为空", 2)
	}
	for _, p := range b.FilePaths {
		found := false
		for _, q := range b.Paths {
			found = found || p == q
		}
		if !found {
			return fmt.Errorf("单文件目标未登记")
		}
	}
	for i, p := range b.Paths {
		if err := SafeRelative(p); err != nil {
			return err
		}
		for j, q := range b.Paths {
			if i != j && (p == q || strings.HasPrefix(p, q+"/")) {
				return fmt.Errorf("事务目标重叠")
			}
		}
		if Within(b.store(p).Root, b.Local) || Within(b.Local, b.store(p).Root) {
			return fmt.Errorf("目标与 Local Root 重叠")
		}
	}
	boundary, err := canonical(b.Boundary)
	if err != nil {
		return err
	}
	local, err := canonical(b.Local)
	if err != nil {
		return err
	}
	if boundary != b.Boundary || local != b.Local || Within(local, boundary) {
		return result.Fail("INVALID_ROOTS", "工具事务边界和 Local Root 无效", 2)
	}
	for _, p := range []string{boundary, local} {
		if err := CheckLinks(p); err != nil {
			return err
		}
	}
	if i, err := os.Stat(boundary); err != nil || !i.IsDir() {
		return result.Fail("INVALID_ROOTS", "工具项目根须已存在", 2)
	}
	release, err := lock(filepath.Join(filepath.Dir(boundary), "."+filepath.Base(boundary)+".aidock-native-lock"))
	if err != nil {
		return result.Fail("NATIVE_BUSY", err.Error(), 3)
	}
	defer release()
	return fn()
}
func (b *Batch) validate(p BatchPlan) error {
	if p.Version != 1 || p.Kind != "native-targets" || !validID(p.ID) || p.Root != b.Boundary || p.Identity != b.Identity || p.Digest != batchDigest(p) || len(p.Targets) == 0 {
		return result.Fail("BACKUP_INVALID", "工具事务身份或摘要损坏", 5)
	}
	switch p.Action {
	case "seed", "apply", "rollback", "undo-rollback":
	default:
		return result.Fail("BACKUP_INVALID", "未知工具事务动作", 5)
	}
	seen := map[string]bool{}
	for _, t := range p.Targets {
		if err := SafeRelative(t.Path); err != nil {
			return err
		}
		allowed := false
		for _, path := range b.Paths {
			allowed = allowed || path == t.Path
		}
		if !allowed || seen[t.Path] {
			return result.Fail("BACKUP_INVALID", "工具事务含未绑定或重复目标", 5)
		}
		if t.File != b.filePath(t.Path) {
			return result.Fail("BACKUP_INVALID", "目标文件/目录类型与绑定不符", 5)
		}
		seen[t.Path] = true
		for _, tree := range []Tree{t.Before, t.After} {
			if err := validateTree(tree); err != nil {
				return err
			}
			if t.File {
				if err := validateFileTree(tree); err != nil {
					return err
				}
			}
		}
		for _, c := range Changes(t.Before, t.After) {
			if c.Before != nil && c.After != nil && c.Before.Dir != c.After.Dir {
				return result.Fail("NATIVE_STRUCTURE_CHANGE_UNSUPPORTED", "文件与目录类型互换尚未通过恢复验收: "+t.Path+"/"+c.Path, 4)
			}
		}
	}
	parentSeen := map[string]bool{}
	for _, parent := range append(append([]string{}, p.Parents...), p.RemoveParents...) {
		if err := SafeRelative(parent); err != nil {
			return err
		}
		allowed := false
		for _, t := range p.Targets {
			allowed = allowed || strings.HasPrefix(t.Path, parent+"/")
		}
		if !allowed || parentSeen[parent] {
			return fmt.Errorf("无效父目录清单")
		}
		parentSeen[parent] = true
	}
	return nil
}
func (b *Batch) assets(p BatchPlan) error {
	for _, t := range p.Targets {
		if err := b.store(t.Path).validateAssets(t.Before, t.After); err != nil {
			return err
		}
	}
	return nil
}
func (b *Batch) writeJSON(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return b.anchor().atomic(path, data, 0600)
}
func (b *Batch) load(id string) (BatchPlan, error) {
	var p BatchPlan
	if !validID(id) {
		return p, result.Fail("INVALID_INPUT", "需要事务计划 UUID", 2)
	}
	data, err := ReadFile(filepath.Join(b.dir(), "plans", id+".json"))
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(data, &p); err != nil {
		return p, err
	}
	return p, b.validate(p)
}
func (b *Batch) LoadPlan(id string) (BatchPlan, error) {
	var p BatchPlan
	err := b.locked(func() error { var err error; p, err = b.load(id); return err })
	return p, err
}
func (b *Batch) journals() ([]BatchJournal, error) {
	out := []BatchJournal{}
	dir := filepath.Join(b.dir(), "journals")
	if err := CheckLinks(dir); err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if strings.HasPrefix(f.Name(), ".aidock-tmp-") {
			continue
		}
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			return nil, fmt.Errorf("未知工具事务记录")
		}
		data, err := ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			return nil, err
		}
		var j BatchJournal
		if err = json.Unmarshal(data, &j); err != nil {
			return nil, err
		}
		p, err := b.load(j.Plan.ID)
		if err != nil || p.Digest != j.Plan.Digest || batchDigest(j.Plan) != p.Digest || f.Name() != p.ID+".json" {
			return nil, result.Fail("RECOVERY_CONFLICT", "工具事务记录损坏", 6)
		}
		if j.State != "prepared" && j.State != "committed" && j.State != "recovered" {
			return nil, result.Fail("RECOVERY_CONFLICT", "未知工具事务状态", 6)
		}
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated > out[j].Updated })
	return out, nil
}
func (b *Batch) barrier() (*batchBarrier, error) {
	data, err := ReadFile(b.marker())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v batchBarrier
	if err = json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	if v.Local != b.Local || v.Identity != b.Identity || !validID(v.ID) {
		return nil, result.Fail("RECOVERY_CONFLICT", "工具恢复点属于另一个 Local Root 或作用域；请使用原绑定恢复", 6)
	}
	return &v, nil
}
func (b *Batch) coordinatedPending() error {
	// A formal replica transaction can bind a tool container to preserve its
	// first creation/removal. Its barriers must also block ordinary native
	// rollback and recovery, after the original process released its locks.
	for _, path := range b.Paths {
		for root := b.store(path).Root; root != b.Boundary; root = filepath.Dir(root) {
			if err := CoordinatedPending(root); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *Batch) pending() error {
	if err := b.coordinatedPending(); err != nil {
		return err
	}
	v, err := b.barrier()
	if err != nil {
		return err
	}
	if v != nil {
		return result.Fail("NATIVE_RECOVERY_REQUIRED", "工具事务尚未收口，请先执行 tool recover", 6)
	}
	js, err := b.journals()
	if err != nil {
		return err
	}
	for _, j := range js {
		if j.State == "prepared" {
			return result.Fail("NATIVE_RECOVERY_REQUIRED", "工具事务尚未恢复", 6)
		}
	}
	return nil
}
func (b *Batch) Pending() error { return b.locked(b.pending) }
func (b *Batch) History() ([]BatchJournal, error) {
	var js []BatchJournal
	err := b.locked(func() error { var err error; js, err = b.journals(); return err })
	return js, err
}
func (b *Batch) preview(action, reference string, targets []BatchTarget) (BatchPlan, error) {
	p := BatchPlan{Version: 1, Kind: "native-targets", ID: ID(), Root: b.Boundary, Identity: b.Identity, Action: action, Reference: reference, Created: time.Now().UTC().Format(time.RFC3339Nano), Targets: targets, Parents: []string{}}
	parents := map[string]bool{}
	for _, t := range targets {
		for parent := filepath.Dir(filepath.FromSlash(t.Path)); parent != "."; parent = filepath.Dir(parent) {
			full := filepath.Join(b.Boundary, parent)
			if err := CheckLinks(full); err != nil {
				return p, err
			}
			if i, err := os.Stat(full); os.IsNotExist(err) {
				parents[filepath.ToSlash(parent)] = true
			} else if err != nil {
				return p, err
			} else if !i.IsDir() {
				return p, fmt.Errorf("父目录槽位被文件占用")
			}
		}
	}
	for parent := range parents {
		p.Parents = append(p.Parents, parent)
	}
	sort.Strings(p.Parents)
	p.Digest = batchDigest(p)
	if err := b.validate(p); err != nil {
		return p, err
	}
	if err := b.assets(p); err != nil {
		return p, err
	}
	return p, b.writeJSON(filepath.Join(b.dir(), "plans", p.ID+".json"), p)
}
func (b *Batch) Preview(action string, targets []BatchTarget) (BatchPlan, error) {
	var p BatchPlan
	err := b.locked(func() error {
		if err := b.pending(); err != nil {
			return err
		}
		// Transfer the complete approved immutable assets into each target's
		// backup bucket before recording the batch. Never read live source paths.
		for _, t := range targets {
			for _, tree := range []Tree{t.Before, t.After} {
				for _, e := range tree.Entries {
					if e.Dir {
						continue
					}
					if b.Blobs == nil {
						return fmt.Errorf("缺少完整包备份源")
					}
					data, err := b.Blobs.Data(e)
					if err != nil {
						return err
					}
					if _, err = b.store(t.Path).put(data); err != nil {
						return err
					}
				}
			}
		}
		var err error
		p, err = b.preview(action, "", targets)
		return err
	})
	return p, err
}
func (b *Batch) saveJournal(j *BatchJournal) error {
	j.Updated = time.Now().UTC().Format(time.RFC3339Nano)
	return b.writeJSON(filepath.Join(b.dir(), "journals", j.Plan.ID+".json"), j)
}
func (b *Batch) clearMarker(id string) error {
	v, err := b.barrier()
	if err != nil {
		return err
	}
	if v == nil {
		return nil
	}
	if v.ID != id {
		return fmt.Errorf("恢复标记身份不匹配")
	}
	return os.Remove(b.marker())
}
func (b *Batch) cleanupParents(p BatchPlan) error {
	parents := append([]string{}, p.Parents...)
	sort.Sort(sort.Reverse(sort.StringSlice(parents)))
	for _, parent := range parents {
		full := filepath.Join(b.Boundary, filepath.FromSlash(parent))
		if err := CheckLinks(full); err != nil {
			return err
		}
		entries, err := os.ReadDir(full)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			if err = os.Remove(full); err != nil {
				return err
			}
		}
	}
	return nil
}
func (b *Batch) Apply(ctx context.Context, id, digest string) (BatchJournal, error) {
	var j BatchJournal
	err := b.locked(func() error {
		if err := b.pending(); err != nil {
			return err
		}
		p, err := b.load(id)
		if err != nil {
			return err
		}
		if digest == "" || digest != p.Digest {
			return result.Fail("PLAN_CONFIRMATION_REQUIRED", "需要完整工具事务计划摘要", 4)
		}
		if p.Reference != "" {
			js, err := b.journals()
			if err != nil {
				return err
			}
			current := ""
			for _, j := range js {
				if j.State == "committed" {
					current = j.Plan.ID
					break
				}
			}
			if current != p.Reference {
				return result.Fail("PLAN_STALE", "回退引用已变化，请重新预览", 3)
			}
		}
		if _, err = os.Stat(filepath.Join(b.dir(), "journals", id+".json")); err == nil {
			return result.Fail("PLAN_CONSUMED", "计划已使用，请重新预览", 3)
		} else if !os.IsNotExist(err) {
			return err
		}
		for _, t := range p.Targets {
			now, err := b.capture(t)
			if err != nil {
				return err
			}
			if Digest(now) != Digest(t.Before) {
				return result.Fail("PLAN_STALE", "工具目标在预览后变化", 3)
			}
		}
		for _, parent := range p.Parents {
			if _, err := os.Stat(filepath.Join(b.Boundary, filepath.FromSlash(parent))); !os.IsNotExist(err) {
				return result.Fail("PLAN_STALE", "拟创建的工具父目录已变化", 3)
			}
		}
		if err = b.assets(p); err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		j = BatchJournal{Plan: p, State: "prepared"}
		if err = b.saveJournal(&j); err != nil {
			return err
		}
		err = b.writeJSON(b.marker(), batchBarrier{ID: p.ID, Local: b.Local, Identity: b.Identity, Digest: p.Digest})
		if err == nil && b.Hook != nil {
			err = b.Hook("prepared", 0)
		}
		for i, t := range p.Targets {
			if err != nil {
				break
			}
			if b.Hook != nil {
				err = b.Hook("target", i)
			}
			if err != nil {
				break
			}
			s := b.store(t.Path)
			s.active = p.ID
			s.Hook = func(stage string, n int) error {
				if b.Hook != nil {
					return b.Hook(stage, n)
				}
				return nil
			}
			if t.File {
				err = s.transitionFile(ctx, t.Before, t.After)
			} else {
				err = s.transition(ctx, t.Before, t.After)
			}
			if err == nil && b.Hook != nil {
				err = b.Hook("target-complete", i)
			}
		}
		if err == nil && len(p.RemoveParents) > 0 {
			cleanup := p
			cleanup.Parents = p.RemoveParents
			err = b.cleanupParents(cleanup)
		}
		if err == nil {
			j.State = "committed"
			err = b.saveJournal(&j)
			if err == nil {
				if b.Hook != nil {
					if err := b.Hook("committed", 0); err != nil {
						return result.Fail("NATIVE_RECOVERY_REQUIRED", "提交记录已保存，恢复标记尚未清理", 6)
					}
				}
				if err := b.clearMarker(p.ID); err != nil {
					return result.Fail("RECOVERY_CONFLICT", "提交已保存，请恢复标记: "+err.Error(), 6)
				}
				return nil
			}
		}
		cause := err
		j.State = "prepared" // An unpersisted commit is still recoverable.
		if err = b.recover(&j); err != nil {
			return err
		}
		return result.Fail("NATIVE_APPLY_FAILED", "工具应用失败，全部目标已恢复: "+cause.Error(), 5)
	})
	return j, err
}
func (b *Batch) recover(j *BatchJournal) error {
	conflict := func(err error) error {
		return &result.Error{Code: "RECOVERY_CONFLICT", Message: err.Error(), Exit: 6, Recovery: map[string]string{"transaction": j.Plan.ID, "command": "tool recover"}}
	}
	if err := b.assets(j.Plan); err != nil {
		return conflict(err)
	}
	type recovery struct {
		s            *Store
		now, desired Tree
		file         bool
	}
	items := []recovery{}
	for _, t := range j.Plan.Targets {
		s := b.store(t.Path)
		s.active = j.Plan.ID
		p := Plan{ID: j.Plan.ID, Before: t.Before, After: t.After, Changes: Changes(t.Before, t.After)}
		var now, desired Tree
		var err error
		if t.File {
			now, desired, err = s.fileRecoveryTrees(p)
		} else {
			now, desired, err = s.recoveryTrees(p)
		}
		if err != nil {
			return conflict(err)
		}
		items = append(items, recovery{s, now, desired, t.File})
	}
	for i, v := range items {
		var err error
		if v.file {
			err = v.s.transitionFile(context.Background(), v.now, v.desired)
		} else {
			err = v.s.transition(context.Background(), v.now, v.desired)
		}
		if err != nil {
			return conflict(err)
		}
		if b.Hook != nil {
			if err := b.Hook("recovered-target", i); err != nil {
				return conflict(err)
			}
		}
	}
	if err := b.cleanupParents(j.Plan); err != nil {
		return conflict(err)
	}
	j.State = "recovered"
	if err := b.saveJournal(j); err != nil {
		return conflict(err)
	}
	return b.clearMarker(j.Plan.ID)
}
func (b *Batch) Recover() (int, error) {
	n := 0
	err := b.locked(func() error {
		if err := b.coordinatedPending(); err != nil {
			return err
		}
		barrier, err := b.barrier()
		if err != nil {
			return err
		}
		js, err := b.journals()
		if err != nil {
			return err
		}
		if barrier != nil {
			found := false
			for _, j := range js {
				if j.Plan.ID == barrier.ID && j.Plan.Digest == barrier.Digest {
					found = true
				}
			}
			if !found {
				return result.Fail("RECOVERY_CONFLICT", "恢复标记没有匹配的完整事务备份", 6)
			}
		}
		for _, j := range js {
			if j.State == "prepared" {
				if err = b.recover(&j); err != nil {
					return err
				}
				n++
			} else if barrier != nil && barrier.ID == j.Plan.ID {
				if err = b.clearMarker(j.Plan.ID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return n, err
}

// Reverse restores only this transaction's changed paths. Unrelated new files
// survive; a changed managed path blocks the entire preview instead of guessing.
func (b *Batch) Reverse(id string) (BatchPlan, error) {
	var p BatchPlan
	err := b.locked(func() error {
		if err := b.pending(); err != nil {
			return err
		}
		js, err := b.journals()
		if err != nil {
			return err
		}
		var original *BatchPlan
		for _, j := range js {
			if j.Plan.ID == id && j.State == "committed" {
				copy := j.Plan
				original = &copy
			}
		}
		if original == nil {
			return result.Fail("TRANSACTION_NOT_FOUND", "需要已成功提交的工具事务", 2)
		}
		targets := []BatchTarget{}
		for _, t := range original.Targets {
			now, err := b.capture(t)
			if err != nil {
				return err
			}
			desired := Clone(now)
			for _, c := range Changes(t.Before, t.After) {
				v, exists := now.Entries[c.Path]
				if c.After == nil && exists || c.After != nil && (!exists || v != *c.After) {
					return result.Fail("RECOVERY_CONFLICT", "回退目标已被外部修改: "+c.Path, 6)
				}
				if c.Before == nil {
					delete(desired.Entries, c.Path)
				} else {
					desired.Entries[c.Path] = *c.Before
				}
			}
			desired.Exists = t.Before.Exists || len(desired.Entries) > 0
			if err = validateTree(desired); err != nil {
				return result.Fail("RECOVERY_CONFLICT", err.Error(), 6)
			}
			targets = append(targets, BatchTarget{Path: t.Path, File: t.File, Before: now, After: desired})
		}
		action := "rollback"
		if original.Action == "rollback" {
			action = "undo-rollback"
		}
		p, err = b.preview(action, id, targets)
		// The original apply may have created parents. Remove only empty owned
		// parents after a successful rollback; never delete external contents.
		if err == nil && action == "rollback" {
			p.RemoveParents = append([]string{}, original.Parents...)
			p.Digest = batchDigest(p)
			err = b.writeJSON(filepath.Join(b.dir(), "plans", p.ID+".json"), p)
		}
		return err
	})
	return p, err
}
