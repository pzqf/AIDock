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

// RootBinding is an exact root, never a shared ancestor or a path supplied by
// a generator. Path is an opaque key used by BatchTarget and the review UI.
type RootBinding struct {
	Path          string `json:"path"`
	Root          string `json:"root"`
	File          bool   `json:"file,omitempty"`
	NativeProject string `json:"native_project,omitempty"`
}

// Coordinator writes a fixed set of disjoint roots. All roots use the same OS
// locks as Store. A durable journal precedes all per-root barriers; every
// barrier precedes the first replacement. No cross-root atomic visibility is
// claimed. Recovery always restores all changed roots and control data.
type Coordinator struct {
	Local, Identity string
	Roots           []RootBinding
	Blobs           *Store
	Hook            func(string, int) error
	// Validate runs under every root lock, before a journal or barrier is
	// written. Recovery deliberately does not depend on current policy gates.
	Validate func(BatchPlan) error
	// BeforeCommit rechecks read-only inputs after all replacements and exact
	// after-tree checks, while every lock is still held. Failure restores the
	// whole batch. Recovery never calls this policy/input callback.
	BeforeCommit    func(BatchPlan) error
	RecoveryCommand string
}

type coordinatedBarrier struct {
	Version  int    `json:"schema_version"`
	Root     string `json:"root"`
	Local    string `json:"local"`
	Identity string `json:"identity"`
	ID       string `json:"id"`
	Digest   string `json:"digest"`
}

func CoordinatedMarker(root string) string {
	return filepath.Join(filepath.Dir(root), "."+filepath.Base(root)+".aidock-coordinated-pending.json")
}

// CoordinatedPending also protects a root opened from a different Local Root.
// Even an unreadable/corrupt marker blocks writes rather than being ignored.
func CoordinatedPending(root string) error {
	path := CoordinatedMarker(root)
	if err := CheckLinks(path); err != nil {
		return result.Fail("COORDINATED_RECOVERY_REQUIRED", err.Error(), 6)
	}
	if _, err := os.Lstat(path); err == nil {
		return result.Fail("COORDINATED_RECOVERY_REQUIRED", "此目录属于未收口的多目录事务；使用原绑定的正式同步或预演恢复入口，不可单独恢复或继续保存", 6)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

// MarkersPending reports barriers for this exact saved batch, including the
// post-commit cleanup window. Barriers for a later batch are not its recovery.
func (c *Coordinator) MarkersPending(id string) (bool, error) {
	if _, err := c.LoadPlan(id); err != nil {
		return false, err
	}
	for _, root := range c.Roots {
		b, err := ReadFile(CoordinatedMarker(root.Root))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		var v coordinatedBarrier
		if err = json.Unmarshal(b, &v); err != nil {
			return false, result.Fail("RECOVERY_CONFLICT", "多目录恢复标记损坏", 6)
		}
		if v.ID == id {
			if _, err = c.barrier(root); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}

func (c *Coordinator) bindings() []RootBinding {
	bound := append([]RootBinding{}, c.Roots...)
	sort.Slice(bound, func(i, j int) bool { return bound[i].Path < bound[j].Path })
	return bound
}
func (c *Coordinator) dir() string {
	b, _ := json.Marshal(c.bindings())
	return filepath.Join(c.Local, "coordinated-transactions", Hash(append([]byte(c.Identity+"\n"), b...)))
}
func (c *Coordinator) store(key string) *Store {
	for _, b := range c.Roots {
		if b.Path == key {
			return &Store{Root: b.Root, Local: c.Local}
		}
	}
	return nil
}
func (c *Coordinator) validateRoots() error {
	local, err := canonical(c.Local)
	// 32 copies, each with one source and two explicitly selected generated
	// directories and two rule files, plus the control baseline.
	directories, files := 0, 0
	for _, root := range c.Roots {
		if root.File {
			files++
		} else {
			directories++
		}
	}
	if err != nil || local != c.Local || c.Identity == "" || len(c.Roots) == 0 || directories > 97 || files > 64 {
		return result.Fail("INVALID_ROOTS", "需要明确事务目录、身份，最多 97 个独立目录与 64 个单文件", 2)
	}
	seen := map[string]bool{}
	for i, b := range c.Roots {
		if err := SafeRelative(b.Path); err != nil || seen[b.Path] {
			return result.Fail("INVALID_ROOTS", "多目录事务键无效或重复", 2)
		}
		seen[b.Path] = true
		root, err := canonical(b.Root)
		if err != nil || root != b.Root || Within(root, local) || Within(local, root) || filepath.Dir(root) == root {
			return result.Fail("INVALID_ROOTS", "事务根须为独立规范目录且不能包含事务目录", 2)
		}
		for j := 0; j < i; j++ {
			if Within(b.Root, c.Roots[j].Root) || Within(c.Roots[j].Root, b.Root) {
				return result.Fail("INVALID_ROOTS", "多目录事务不允许重叠或重复根", 2)
			}
		}
		if b.NativeProject != "" {
			project, err := canonical(b.NativeProject)
			if err != nil || project != b.NativeProject || project == b.Root || !Within(project, b.Root) || Within(project, local) || Within(local, project) {
				return result.Fail("INVALID_ROOTS", "原生项目锁须绑定包含目标且不包含事务目录的规范项目目录", 2)
			}
			if err = CheckLinks(project); err != nil {
				return err
			}
		}
	}
	return CheckLinks(local)
}
func (c *Coordinator) locked(fn func() error) error {
	if err := c.validateRoots(); err != nil {
		return err
	}
	for _, root := range c.Roots {
		if err := c.store(root.Path).normalize(); err != nil {
			return err
		}
	}
	// Deterministic lock order prevents cycles. Immediate failures release all
	// earlier locks; callers never wait while holding a partial set of roots.
	locks := map[string]string{}
	for _, b := range c.Roots {
		path := filepath.Join(filepath.Dir(b.Root), "."+filepath.Base(b.Root)+".aidock-lock")
		locks[strings.ToLower(path)] = path
		if b.NativeProject != "" {
			if st, err := os.Stat(b.NativeProject); err != nil || !st.IsDir() {
				return result.Fail("INVALID_ROOTS", "原生项目根须已存在", 2)
			}
			path = filepath.Join(filepath.Dir(b.NativeProject), "."+filepath.Base(b.NativeProject)+".aidock-native-lock")
			locks[strings.ToLower(path)] = path
		}
	}
	keys := make([]string, 0, len(locks))
	for key := range locks {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	releases := []func(){}
	defer func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}()
	for _, key := range keys {
		release, err := lock(locks[key])
		if err != nil {
			return result.Fail("SOURCE_BUSY", "无法取得全部目录锁: "+err.Error(), 3)
		}
		releases = append(releases, release)
	}
	return fn()
}
func (c *Coordinator) validate(p BatchPlan) error {
	if p.Version != 1 || p.Kind != "coordinated-roots" || p.Root != c.Local || p.Identity != c.Identity || !validID(p.ID) || p.Digest != batchDigest(p) || string(jsonBytes(p.Roots)) != string(jsonBytes(c.bindings())) || len(p.Parents)+len(p.RemoveParents) != 0 || len(p.Targets) != len(c.Roots) {
		return result.Fail("BACKUP_INVALID", "多目录事务身份、绑定或摘要损坏", 5)
	}
	switch p.Action {
	case "seed", "apply", "rollback", "undo-rollback":
	default:
		return result.Fail("BACKUP_INVALID", "未知多目录事务动作", 5)
	}
	if p.Reference != "" && !validID(p.Reference) {
		return result.Fail("BACKUP_INVALID", "多目录回退引用无效", 5)
	}
	seen := map[string]bool{}
	for _, t := range p.Targets {
		if c.store(t.Path) == nil || seen[t.Path] || t.File != c.filePath(t.Path) {
			return result.Fail("BACKUP_INVALID", "事务含未绑定或重复目标", 5)
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
		for _, change := range Changes(t.Before, t.After) {
			if change.Before != nil && change.After != nil && change.Before.Dir != change.After.Dir {
				return result.Fail("COORDINATED_STRUCTURE_CHANGE_UNSUPPORTED", "文件与目录类型互换尚未通过多目录恢复验收", 4)
			}
		}
	}
	return nil
}
func jsonBytes(v any) []byte { b, _ := json.Marshal(v); return b }
func (c *Coordinator) writeJSON(path string, value any) error {
	bytes, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(bytes) > MaxFile {
		return result.Fail("PLAN_TOO_LARGE", "事务计划超过 10 MB", 4)
	}
	return c.store(c.Roots[0].Path).atomic(path, bytes, 0600)
}
func (c *Coordinator) load(id string) (BatchPlan, error) {
	var p BatchPlan
	if !validID(id) {
		return p, result.Fail("INVALID_INPUT", "需要多目录事务 UUID", 2)
	}
	bytes, err := ReadFile(filepath.Join(c.dir(), "plans", id+".json"))
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(bytes, &p); err != nil {
		return p, err
	}
	return p, c.validate(p)
}
func (c *Coordinator) LoadPlan(id string) (BatchPlan, error) {
	if err := c.validateRoots(); err != nil {
		return BatchPlan{}, err
	}
	return c.load(id)
}
func (c *Coordinator) journals() ([]BatchJournal, error) {
	js := []BatchJournal{}
	dir := filepath.Join(c.dir(), "journals")
	if err := CheckLinks(dir); err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return js, nil
	}
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if strings.HasPrefix(file.Name(), ".aidock-tmp-") {
			continue
		}
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			return nil, result.Fail("BACKUP_INVALID", "未知多目录事务记录", 5)
		}
		bytes, err := ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			return nil, err
		}
		var j BatchJournal
		if err = json.Unmarshal(bytes, &j); err != nil {
			return nil, err
		}
		p, err := c.load(j.Plan.ID)
		if err != nil || p.Digest != j.Plan.Digest || batchDigest(j.Plan) != p.Digest || file.Name() != p.ID+".json" {
			return nil, result.Fail("BACKUP_INVALID", "多目录事务记录与计划不匹配", 5)
		}
		if j.State != "prepared" && j.State != "committed" && j.State != "recovered" {
			return nil, result.Fail("BACKUP_INVALID", "未知多目录事务状态", 5)
		}
		js = append(js, j)
	}
	sort.Slice(js, func(i, j int) bool { return js[i].Updated > js[j].Updated })
	return js, nil
}
func (c *Coordinator) barrier(b RootBinding) (*coordinatedBarrier, error) {
	bytes, err := ReadFile(CoordinatedMarker(b.Root))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v coordinatedBarrier
	if err = json.Unmarshal(bytes, &v); err != nil {
		return nil, result.Fail("RECOVERY_CONFLICT", "多目录恢复标记损坏", 6)
	}
	if v.Version != 1 || v.Root != b.Root || v.Local != c.Local || v.Identity != c.Identity || !validID(v.ID) || len(v.Digest) != 64 {
		return nil, result.Fail("RECOVERY_CONFLICT", "目录由另一个多目录事务绑定保护；使用原事务目录恢复", 6)
	}
	return &v, nil
}
func (c *Coordinator) pending() error {
	for _, root := range c.Roots {
		if err := c.store(root.Path).Pending(); err != nil {
			return err
		}
	}
	js, err := c.journals()
	if err != nil {
		return err
	}
	for _, j := range js {
		if j.State == "prepared" {
			return result.Fail("COORDINATED_RECOVERY_REQUIRED", "多目录事务尚未恢复", 6)
		}
	}
	return nil
}
func (c *Coordinator) Pending() error { return c.locked(c.pending) }
func (c *Coordinator) History() ([]BatchJournal, error) {
	// Journal files are atomically replaced. Reading Local records must remain
	// possible when a drive or parent directory of a target is unavailable.
	if err := c.validateRoots(); err != nil {
		return nil, err
	}
	return c.journals()
}
func (c *Coordinator) assets(p BatchPlan) error {
	for _, t := range p.Targets {
		if err := c.store(t.Path).validateAssets(t.Before, t.After); err != nil {
			return err
		}
	}
	return nil
}
func (c *Coordinator) preview(action, reference string, targets []BatchTarget) (BatchPlan, error) {
	p := BatchPlan{Version: 1, Kind: "coordinated-roots", ID: ID(), Root: c.Local, Identity: c.Identity, Action: action, Reference: reference, Created: time.Now().UTC().Format(time.RFC3339Nano), Targets: targets, Roots: c.bindings(), Parents: []string{}}
	p.Digest = batchDigest(p)
	if err := c.validate(p); err != nil {
		return p, err
	}
	if err := c.assets(p); err != nil {
		return p, err
	}
	return p, c.writeJSON(filepath.Join(c.dir(), "plans", p.ID+".json"), p)
}
func (c *Coordinator) Preview(action string, targets []BatchTarget) (BatchPlan, error) {
	var p BatchPlan
	err := c.locked(func() error {
		var err error
		p, err = c.draft(action, targets)
		return err
	})
	return p, err
}

// PreviewObserved saves a reviewable proposal in Local without creating lock
// sidecars in target projects. Observations are checked before and after the
// backup; Apply still takes every root lock and checks the exact before trees.
func (c *Coordinator) PreviewObserved(action string, targets []BatchTarget) (BatchPlan, error) {
	if err := c.validateRoots(); err != nil {
		return BatchPlan{}, err
	}
	check := func() error {
		if err := c.pending(); err != nil {
			return err
		}
		for _, t := range targets {
			s := c.store(t.Path)
			if s == nil {
				return result.Fail("INVALID_ROOTS", "观察目标缺少明确根绑定", 2)
			}
			if err := s.normalize(); err != nil {
				return err
			}
			now, err := c.capture(t)
			if err != nil {
				return err
			}
			if Digest(now) != Digest(t.Before) {
				return result.Fail("PLAN_STALE", "预览期间目录变化: "+t.Path, 3)
			}
		}
		return nil
	}
	if err := check(); err != nil {
		return BatchPlan{}, err
	}
	p, err := c.draft(action, targets)
	if err == nil {
		err = check()
	}
	return p, err
}

func (c *Coordinator) draft(action string, targets []BatchTarget) (BatchPlan, error) {
	if err := c.pending(); err != nil {
		return BatchPlan{}, err
	}
	// Validate bindings before writing any target-associated backup bucket.
	probe := BatchPlan{Version: 1, Kind: "coordinated-roots", ID: ID(), Root: c.Local, Identity: c.Identity, Action: action, Targets: targets, Roots: c.bindings()}
	probe.Digest = batchDigest(probe)
	if err := c.validate(probe); err != nil {
		return BatchPlan{}, err
	}
	for _, t := range targets {
		for _, tree := range []Tree{t.Before, t.After} {
			for _, entry := range tree.Entries {
				if entry.Dir {
					continue
				}
				if c.Blobs == nil {
					return BatchPlan{}, fmt.Errorf("缺少完整包备份源")
				}
				bytes, err := c.Blobs.Data(entry)
				if err != nil {
					return BatchPlan{}, err
				}
				if _, err = c.store(t.Path).put(bytes); err != nil {
					return BatchPlan{}, err
				}
			}
		}
	}
	return c.preview(action, "", targets)
}
func (c *Coordinator) save(j *BatchJournal) error {
	j.Updated = time.Now().UTC().Format(time.RFC3339Nano)
	return c.writeJSON(filepath.Join(c.dir(), "journals", j.Plan.ID+".json"), j)
}
func (c *Coordinator) clear(p BatchPlan) error {
	// Validate all markers first; never remove another transaction's barrier.
	for _, root := range c.Roots {
		v, err := c.barrier(root)
		if err != nil {
			return err
		}
		if v != nil && (v.ID != p.ID || v.Digest != p.Digest) {
			return result.Fail("RECOVERY_CONFLICT", "恢复标记与待收口事务不匹配", 6)
		}
	}
	for i, root := range c.Roots {
		if err := durableRemove(CoordinatedMarker(root.Root)); err != nil {
			return err
		}
		if c.Hook != nil {
			if err := c.Hook("cleared-barrier", i); err != nil {
				return err
			}
		}
	}
	return nil
}
func (c *Coordinator) Apply(ctx context.Context, id, digest string) (BatchJournal, error) {
	var j BatchJournal
	err := c.locked(func() error {
		if err := c.pending(); err != nil {
			return err
		}
		p, err := c.load(id)
		if err != nil {
			return err
		}
		if digest == "" || digest != p.Digest {
			return result.Fail("PLAN_CONFIRMATION_REQUIRED", "需要多目录事务完整摘要", 4)
		}
		js, err := c.journals()
		if err != nil {
			return err
		}
		current := ""
		for _, record := range js {
			if record.Plan.ID == p.ID {
				return result.Fail("PLAN_CONSUMED", "多目录计划已使用", 3)
			}
			if current == "" && record.State == "committed" {
				current = record.Plan.ID
			}
		}
		if p.Reference != "" && p.Reference != current {
			return result.Fail("PLAN_STALE", "最新成功事务已变化，重新预览回退", 3)
		}
		for _, t := range p.Targets {
			now, err := c.capture(t)
			if err != nil {
				return err
			}
			if Digest(now) != Digest(t.Before) {
				return result.Fail("PLAN_STALE", "目录在预览后变化: "+t.Path, 3)
			}
		}
		if err = c.assets(p); err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if c.Validate != nil {
			if err = c.Validate(p); err != nil {
				return err
			}
		}
		j = BatchJournal{Plan: p, State: "prepared"}
		if err = c.save(&j); err != nil {
			return err
		}
		if c.Hook != nil {
			err = c.Hook("prepared", 0)
		}
		for i, root := range c.Roots {
			if err != nil {
				break
			}
			err = c.writeJSON(CoordinatedMarker(root.Root), coordinatedBarrier{Version: 1, Root: root.Root, Local: c.Local, Identity: c.Identity, ID: p.ID, Digest: p.Digest})
			if err == nil && c.Hook != nil {
				err = c.Hook("barrier", i)
			}
		}
		for i, t := range p.Targets {
			if err != nil {
				break
			}
			if c.Hook != nil {
				err = c.Hook("target", i)
			}
			if err != nil {
				break
			}
			s := c.store(t.Path)
			s.active, s.Hook = p.ID, c.Hook
			err = coordinatedTransition(s, ctx, t.File, t.Before, t.After)
			if err == nil && c.Hook != nil {
				err = c.Hook("target-complete", i)
			}
		}
		if err == nil {
			// Recheck every root after the last write, before publishing commit.
			for _, t := range p.Targets {
				now, readErr := c.capture(t)
				if readErr != nil {
					err = readErr
					break
				}
				if Digest(now) != Digest(t.After) {
					err = result.Fail("PLAN_STALE", "提交前目录被外部修改: "+t.Path, 3)
					break
				}
			}
		}
		if err == nil && c.BeforeCommit != nil {
			err = c.BeforeCommit(p)
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			j.State = "committed"
			err = c.save(&j)
			if err == nil {
				if c.Hook != nil {
					if err = c.Hook("committed", 0); err != nil {
						return result.Fail("COORDINATED_RECOVERY_REQUIRED", "提交已持久保存，恢复标记待收口", 6)
					}
				}
				if err = c.clear(p); err != nil {
					return result.Fail("COORDINATED_RECOVERY_REQUIRED", "提交已保存，恢复标记待收口: "+err.Error(), 6)
				}
				return nil
			}
		}
		cause := err
		j.State = "prepared"
		if err = c.recover(&j); err != nil {
			return err
		}
		return result.Fail("COORDINATED_APPLY_FAILED", "多目录应用失败，全部修改已恢复: "+cause.Error(), 5)
	})
	return j, err
}
func (c *Coordinator) recover(j *BatchJournal) error {
	conflict := func(err error) error {
		command := c.RecoveryCommand
		if command == "" {
			command = "replica rehearsal recover"
		}
		return &result.Error{Code: "RECOVERY_CONFLICT", Message: err.Error(), Exit: 6, Recovery: map[string]string{"transaction": j.Plan.ID, "command": command}}
	}
	if err := c.assets(j.Plan); err != nil {
		return conflict(err)
	}
	// Reinstall missing barriers under all locks before recovery replacements.
	// A crash during barrier creation never allows a new multi-root write.
	for _, root := range c.Roots {
		v, err := c.barrier(root)
		if err != nil {
			return conflict(err)
		}
		if v != nil && (v.ID != j.Plan.ID || v.Digest != j.Plan.Digest) {
			return conflict(fmt.Errorf("恢复标记不匹配"))
		}
	}
	for _, root := range c.Roots {
		if err := c.writeJSON(CoordinatedMarker(root.Root), coordinatedBarrier{Version: 1, Root: root.Root, Local: c.Local, Identity: c.Identity, ID: j.Plan.ID, Digest: j.Plan.Digest}); err != nil {
			return conflict(err)
		}
	}
	type item struct {
		store        *Store
		now, desired Tree
		file         bool
	}
	items := []item{}
	for _, t := range j.Plan.Targets {
		s := c.store(t.Path)
		s.active = j.Plan.ID
		plan := Plan{ID: j.Plan.ID, Before: t.Before, After: t.After, Changes: Changes(t.Before, t.After)}
		recoverTrees := s.recoveryTrees
		if t.File {
			recoverTrees = s.fileRecoveryTrees
		}
		now, desired, err := recoverTrees(plan)
		if err != nil {
			return conflict(err)
		}
		if t.Before.Exists && t.After.Exists && !now.Exists {
			return conflict(fmt.Errorf("目录不可用，恢复原目录后重试: %s", t.Path))
		}
		items = append(items, item{s, now, desired, t.File})
	}
	for i, v := range items {
		if err := coordinatedTransition(v.store, context.Background(), v.file, v.now, v.desired); err != nil {
			return conflict(err)
		}
		if c.Hook != nil {
			if err := c.Hook("recovered-target", i); err != nil {
				return conflict(err)
			}
		}
	}
	j.State = "recovered"
	if err := c.save(j); err != nil {
		return conflict(err)
	}
	return c.clear(j.Plan)
}
func (c *Coordinator) Recover() (int, error) {
	n := 0
	err := c.locked(func() error {
		js, err := c.journals()
		if err != nil {
			return err
		}
		for _, root := range c.Roots {
			v, err := c.barrier(root)
			if err != nil {
				return err
			}
			if v == nil {
				continue
			}
			found := false
			for _, j := range js {
				found = found || j.Plan.ID == v.ID && j.Plan.Digest == v.Digest
			}
			if !found {
				return result.Fail("RECOVERY_CONFLICT", "恢复标记缺少匹配的完整备份", 6)
			}
		}
		for _, j := range js {
			if j.State == "prepared" {
				if err = c.recover(&j); err != nil {
					return err
				}
				n++
			} else {
				for _, root := range c.Roots {
					v, err := c.barrier(root)
					if err != nil {
						return err
					}
					if v != nil && v.ID == j.Plan.ID {
						if err = c.clear(j.Plan); err != nil {
							return err
						}
						break
					}
				}
			}
		}
		return nil
	})
	return n, err
}
func (c *Coordinator) Reverse(id string) (BatchPlan, error) {
	var p BatchPlan
	err := c.locked(func() error {
		if err := c.pending(); err != nil {
			return err
		}
		js, err := c.journals()
		if err != nil {
			return err
		}
		var original *BatchPlan
		for _, j := range js {
			if j.State == "committed" {
				v := j.Plan
				original = &v
				break
			}
		}
		if original == nil || original.ID != id || original.Action == "seed" {
			return result.Fail("TRANSACTION_NOT_FOUND", "选择最新成功的多目录应用/回退事务", 2)
		}
		targets := []BatchTarget{}
		for _, t := range original.Targets {
			now, err := c.capture(t)
			if err != nil {
				return err
			}
			if t.After.Exists && !now.Exists {
				return result.Fail("RECOVERY_CONFLICT", "回退目录不可用: "+t.Path, 6)
			}
			desired := Clone(now)
			for _, change := range Changes(t.Before, t.After) {
				v, exists := now.Entries[change.Path]
				if change.After == nil && exists || change.After != nil && (!exists || v != *change.After) {
					return result.Fail("RECOVERY_CONFLICT", "回退目标被外部修改: "+t.Path+"/"+change.Path, 6)
				}
				if change.Before == nil {
					delete(desired.Entries, change.Path)
				} else {
					desired.Entries[change.Path] = *change.Before
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
		p, err = c.preview(action, id, targets)
		return err
	})
	return p, err
}
