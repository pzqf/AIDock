// Package workstore provides recoverable transactions for explicitly selected
// owned source roots. It never launches tools or follows links into packages.
package workstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/result"
)

const MaxFile = 10 << 20
const MaxTree = 256 << 20

type Entry struct {
	Hash string `json:"sha256,omitempty"`
	Size int64  `json:"bytes"`
	Mode uint32 `json:"mode"`
	Dir  bool   `json:"directory,omitempty"`
}
type Tree struct {
	Exists  bool             `json:"exists"`
	Entries map[string]Entry `json:"entries"`
}

func Directory() Entry { return Entry{Dir: true, Mode: normalMode(0755, true)} }

type Change struct {
	Path   string `json:"path"`
	Before *Entry `json:"before,omitempty"`
	After  *Entry `json:"after,omitempty"`
}
type Plan struct {
	Version      int               `json:"schema_version"`
	ID           string            `json:"id"`
	Kind         string            `json:"kind"`
	Root         string            `json:"root"`
	Action       string            `json:"action"`
	Created      string            `json:"created"`
	Before       Tree              `json:"before"`
	After        Tree              `json:"after"`
	Changes      []Change          `json:"changes"`
	Digest       string            `json:"digest"`
	AssetEffects []AssetEffect     `json:"asset_effects,omitempty"`
	Inputs       map[string]string `json:"inputs,omitempty"`
}

// Asset effects live in the same durable plan/journal as the corresponding tree.
// They are descriptive records, never instructions to change other roots.
type AssetEffect struct {
	Kind       string `json:"kind"`
	Owner      string `json:"owner"`
	Scope      string `json:"scope_id"`
	ID         string `json:"id"`
	NativeName string `json:"native_name"`
	Revision   string `json:"revision"`
	Before     string `json:"before"`
	After      string `json:"after"`
}
type Journal struct {
	Plan    Plan   `json:"plan"`
	State   string `json:"state"`
	Updated string `json:"updated"`
}
type Store struct {
	Root, Local string
	Hook        func(string, int) error
	Validate    func(Plan) error
	active      string
}

func Hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func ID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func canonical(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("必须明确路径")
	}
	p, e := filepath.Abs(p)
	if e != nil {
		return "", e
	}
	return filepath.Clean(p), nil
}
func Within(parent, child string) bool {
	r, e := filepath.Rel(strings.ToLower(parent), strings.ToLower(child))
	return e == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r)
}
func SafeRelative(p string) error {
	if p == "" || p == "." || path.Clean(p) != p || path.IsAbs(p) || strings.ContainsAny(p, `\:<>"|?*`) {
		return fmt.Errorf("非法相对路径 %q", p)
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || strings.TrimRight(part, ". ") != part {
			return fmt.Errorf("非法路径 %q", p)
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "NUL" || base == "CON" || base == "AUX" || base == "PRN" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9') {
			return fmt.Errorf("设备名路径 %q", p)
		}
	}
	return nil
}
func CheckLinks(p string) error {
	p, e := canonical(p)
	if e != nil {
		return e
	}
	for {
		i, e := os.Lstat(p)
		if e == nil && isLink(i) {
			return fmt.Errorf("PATH_UNSAFE: 链接/reparse point %s", p)
		}
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		next := filepath.Dir(p)
		if next == p {
			return nil
		}
		p = next
	}
}
func (s *Store) normalize() error {
	root, e := canonical(s.Root)
	if e != nil {
		return e
	}
	local, e := canonical(s.Local)
	if e != nil {
		return e
	}
	if root != s.Root || local != s.Local {
		return result.Fail("INVALID_ROOTS", "事务根须为规范绝对路径", 2)
	}
	if Within(s.Root, s.Local) || Within(s.Local, s.Root) {
		return result.Fail("INVALID_ROOTS", "源根与事务根不能重叠", 2)
	}
	if e = CheckLinks(s.Root); e != nil {
		return result.Fail("PATH_UNSAFE", e.Error(), 5)
	}
	if e = CheckLinks(s.Local); e != nil {
		return result.Fail("PATH_UNSAFE", e.Error(), 5)
	}
	if i, e := os.Stat(filepath.Dir(s.Root)); e != nil || !i.IsDir() {
		return result.Fail("INVALID_ROOTS", "源根的父目录须已存在", 2)
	}
	return nil
}

// Lock lives beside the actual target root, so different Local Roots still
// contend on the same OS lock; kernel release also survives process crashes.
func (s *Store) Locked(fn func() error) error {
	if e := s.normalize(); e != nil {
		return e
	}
	release, e := lock(filepath.Join(filepath.Dir(s.Root), "."+filepath.Base(s.Root)+".aidock-lock"))
	if e != nil {
		return result.Fail("SOURCE_BUSY", e.Error(), 3)
	}
	defer release()
	return fn()
}
func (s *Store) bucket() string {
	return filepath.Join(s.Local, "source-transactions", Hash([]byte(strings.ToLower(s.Root))))
}
func (s *Store) put(b []byte) (Entry, error) {
	h := Hash(b)
	name := filepath.Join(s.bucket(), "blobs", h)
	if e := CheckLinks(name); e != nil {
		return Entry{}, e
	}
	if old, e := os.ReadFile(name); e == nil {
		if Hash(old) != h {
			return Entry{}, fmt.Errorf("备份摘要损坏")
		}
	} else if !os.IsNotExist(e) {
		return Entry{}, e
	} else if e = s.atomic(name, b, 0600); e != nil {
		return Entry{}, e
	}
	return Entry{Hash: h, Size: int64(len(b)), Mode: normalMode(0644, false)}, nil
}
func (s *Store) Data(e Entry) ([]byte, error) {
	if e.Dir {
		return nil, fmt.Errorf("目录没有正文")
	}
	if len(e.Hash) != 64 || strings.Trim(e.Hash, "0123456789abcdef") != "" || e.Size < 0 || e.Size > MaxFile {
		return nil, fmt.Errorf("无效备份清单")
	}
	b, err := ReadFile(filepath.Join(s.bucket(), "blobs", e.Hash))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) != e.Size || Hash(b) != e.Hash {
		return nil, fmt.Errorf("备份字节/摘要不匹配")
	}
	return b, nil
}
func ReadFile(p string) ([]byte, error) {
	if e := CheckLinks(p); e != nil {
		return nil, e
	}
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	i, e := f.Stat()
	if e != nil || !i.Mode().IsRegular() {
		return nil, fmt.Errorf("非普通文件")
	}
	b, e := io.ReadAll(io.LimitReader(f, MaxFile+1))
	if e == nil && len(b) > MaxFile {
		e = fmt.Errorf("文件超过 10 MB")
	}
	return b, e
}

// Capture stores every regular byte, including binary attachments and drafts.
// No silent omission of links, secret-like files, caches or empty directories.
func (s *Store) Capture(p string) (Tree, error) {
	t := Tree{Entries: map[string]Entry{}}
	if e := CheckLinks(p); e != nil {
		return t, e
	}
	if _, e := os.Stat(p); os.IsNotExist(e) {
		return t, nil
	} else if e != nil {
		return t, e
	}
	r, e := os.OpenRoot(p)
	if e != nil {
		return t, e
	}
	defer r.Close()
	t.Exists = true
	total := int64(0)
	seen := map[string]bool{}
	e = fs.WalkDir(r.FS(), ".", func(rel string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if rel == "." {
			return nil
		}
		if e = SafeRelative(rel); e != nil {
			return e
		}
		i, e := r.Lstat(rel)
		if e != nil {
			return e
		}
		if isLink(i) {
			return fmt.Errorf("PATH_UNSAFE: %s", rel)
		}
		key := strings.ToLower(rel)
		if seen[key] {
			return fmt.Errorf("CASE_COLLISION: %s", rel)
		}
		seen[key] = true
		if len(t.Entries) >= 20000 {
			return fmt.Errorf("树超过 20000 项")
		}
		if i.IsDir() {
			t.Entries[rel] = Entry{Dir: true, Mode: normalMode(uint32(i.Mode().Perm()), true)}
			return nil
		}
		if !i.Mode().IsRegular() {
			return fmt.Errorf("非普通文件: %s", rel)
		}
		f, e := r.Open(rel)
		if e != nil {
			return e
		}
		b, e := io.ReadAll(io.LimitReader(f, MaxFile+1))
		f.Close()
		if e != nil {
			return e
		}
		total += int64(len(b))
		if len(b) > MaxFile || total > MaxTree {
			return fmt.Errorf("源树超出大小限制")
		}
		entry, e := s.put(b)
		if e != nil {
			return e
		}
		entry.Mode = normalMode(uint32(i.Mode().Perm()), false)
		t.Entries[rel] = entry
		return nil
	})
	return t, e
}
func Digest(t Tree) string { b, _ := json.Marshal(t); return Hash(b) }
func Clone(t Tree) Tree {
	n := Tree{Exists: t.Exists, Entries: map[string]Entry{}}
	for p, e := range t.Entries {
		n.Entries[p] = e
	}
	return n
}
func (s *Store) PutFile(t *Tree, p string, b []byte, mode uint32) error {
	if e := SafeRelative(p); e != nil {
		return e
	}
	if len(b) > MaxFile {
		return fmt.Errorf("文件超过 10 MB")
	}
	entry, e := s.put(b)
	if e != nil {
		return e
	}
	entry.Mode = normalMode(mode, false)
	t.Exists = true
	t.Entries[p] = entry
	for dir := path.Dir(p); dir != "."; dir = path.Dir(dir) {
		if old, ok := t.Entries[dir]; ok && !old.Dir {
			return fmt.Errorf("目录槽位被文件占用")
		}
		if _, ok := t.Entries[dir]; !ok {
			t.Entries[dir] = Entry{Dir: true, Mode: normalMode(0755, true)}
		}
	}
	return nil
}
func Changes(a, b Tree) []Change {
	keys := map[string]bool{}
	for p := range a.Entries {
		keys[p] = true
	}
	for p := range b.Entries {
		keys[p] = true
	}
	changes := []Change{}
	for p := range keys {
		x, xok := a.Entries[p]
		y, yok := b.Entries[p]
		if xok && yok && x == y {
			continue
		}
		c := Change{Path: p}
		if xok {
			c.Before = &x
		}
		if yok {
			c.After = &y
		}
		changes = append(changes, c)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}
func planDigest(p Plan) string { p.Digest = ""; b, _ := json.Marshal(p); return Hash(b) }
func (s *Store) Preview(action string, before, after Tree, effects ...AssetEffect) (Plan, error) {
	return s.PreviewBound(action, before, after, nil, effects...)
}
func (s *Store) PreviewBound(action string, before, after Tree, inputs map[string]string, effects ...AssetEffect) (Plan, error) {
	if e := validateTree(before); e != nil {
		return Plan{}, e
	}
	if e := validateTree(after); e != nil {
		return Plan{}, e
	}
	if e := s.validateAssets(before, after); e != nil {
		return Plan{}, e
	}
	p := Plan{Version: 1, ID: ID(), Kind: "skill-source", Root: s.Root, Action: action, Created: time.Now().UTC().Format(time.RFC3339), Before: before, After: after, Changes: Changes(before, after)}
	p.AssetEffects = effects
	p.Inputs = inputs
	p.Digest = planDigest(p)
	b, _ := json.Marshal(p)
	e := s.atomic(filepath.Join(s.bucket(), "plans", p.ID+".json"), b, 0600)
	return p, e
}
func (s *Store) validateAssets(trees ...Tree) error {
	for _, t := range trees {
		for _, v := range t.Entries {
			if !v.Dir {
				if _, e := s.Data(v); e != nil {
					return result.Fail("BACKUP_INVALID", "恢复资产不可用，未开始写入: "+e.Error(), 5)
				}
			}
		}
	}
	return nil
}
func validID(id string) bool {
	return len(id) == 36 && strings.Count(id, "-") == 4 && strings.Trim(id, "0123456789abcdef-") == ""
}
func (s *Store) LoadPlan(id string) (Plan, error) {
	var p Plan
	if !validID(id) {
		return p, result.Fail("INVALID_INPUT", "无效计划 id", 2)
	}
	b, e := ReadFile(filepath.Join(s.bucket(), "plans", id+".json"))
	if e != nil {
		return p, e
	}
	if e = json.Unmarshal(b, &p); e != nil {
		return p, e
	}
	if p.Kind != "skill-source" {
		return p, result.Fail("PLAN_KIND_MISMATCH", "只接受技能源计划", 2)
	}
	if p.Version != 1 || p.ID != id || p.Root != s.Root || p.Digest != planDigest(p) {
		return p, result.Fail("PLAN_STALE", "计划身份/摘要不匹配", 3)
	}
	if e = validateTree(p.Before); e != nil {
		return p, e
	}
	if e = validateTree(p.After); e != nil {
		return p, e
	}
	expected, _ := json.Marshal(Changes(p.Before, p.After))
	actual, _ := json.Marshal(p.Changes)
	if string(expected) != string(actual) {
		return p, result.Fail("PLAN_STALE", "差异清单已变更", 3)
	}
	return p, nil
}
func validateTree(t Tree) error {
	total := int64(0)
	seen := map[string]bool{}
	if !t.Exists && len(t.Entries) > 0 {
		return fmt.Errorf("不存在的根包含条目")
	}
	if len(t.Entries) > 20000 {
		return fmt.Errorf("条目超限")
	}
	for p, e := range t.Entries {
		if err := SafeRelative(p); err != nil {
			return err
		}
		k := strings.ToLower(p)
		if seen[k] {
			return fmt.Errorf("CASE_COLLISION")
		}
		seen[k] = true
		if e.Mode&^0777 != 0 || e.Size < 0 || e.Size > MaxFile {
			return fmt.Errorf("无效文件元数据")
		}
		if e.Dir && (e.Hash != "" || e.Size != 0) {
			return fmt.Errorf("目录不能包含文件摘要/正文大小")
		}
		if !e.Dir && (len(e.Hash) != 64 || strings.Trim(e.Hash, "0123456789abcdef") != "") {
			return fmt.Errorf("无效摘要")
		}
		total += e.Size
		for d := path.Dir(p); d != "."; d = path.Dir(d) {
			parent, ok := t.Entries[d]
			if !ok || !parent.Dir {
				return fmt.Errorf("缺少目录 %s", d)
			}
		}
	}
	if total > MaxTree {
		return fmt.Errorf("树大小超限")
	}
	return nil
}
func (s *Store) save(j Journal) error {
	j.Updated = time.Now().UTC().Format(time.RFC3339Nano)
	b, _ := json.Marshal(j)
	return s.atomic(filepath.Join(s.bucket(), "transactions", j.Plan.ID+".json"), b, 0600)
}
func (s *Store) Journals() ([]Journal, error) {
	items := []Journal{}
	dir := filepath.Join(s.bucket(), "transactions")
	if e := CheckLinks(dir); e != nil {
		return nil, e
	}
	files, e := os.ReadDir(dir)
	if os.IsNotExist(e) {
		return items, nil
	}
	if e != nil {
		return nil, e
	}
	for _, f := range files {
		if strings.HasPrefix(f.Name(), ".aidock-tmp-") {
			continue
		}
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			return nil, fmt.Errorf("未知事务文件")
		}
		b, e := ReadFile(filepath.Join(dir, f.Name()))
		if e != nil {
			return nil, e
		}
		var j Journal
		if e = json.Unmarshal(b, &j); e != nil {
			return nil, e
		}
		p, e := s.LoadPlan(j.Plan.ID)
		if e != nil || p.Digest != j.Plan.Digest || planDigest(j.Plan) != p.Digest || f.Name() != p.ID+".json" {
			return nil, result.Fail("RECOVERY_CONFLICT", "事务计划损坏；保留记录等待修复", 6)
		}
		switch j.State {
		case "prepared", "committed", "recovered":
		default:
			return nil, result.Fail("RECOVERY_CONFLICT", "未知事务状态", 6)
		}
		items = append(items, j)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Updated > items[j].Updated })
	return items, nil
}
func (s *Store) Pending() error {
	// Native batches protect the source from new saves until every tool target
	// has recovered. The marker is outside the source and survives Local changes.
	if filepath.Base(s.Root) == ".aidock" {
		marker := filepath.Join(filepath.Dir(s.Root), NativeBarrier)
		if e := CheckLinks(marker); e != nil {
			return recoveryError("", e.Error())
		}
		if _, e := os.Lstat(marker); e == nil {
			return result.Fail("NATIVE_RECOVERY_REQUIRED", "存在未完成的工具事务；请先执行 tool recover", 6)
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	js, e := s.Journals()
	if e != nil {
		return e
	}
	for _, j := range js {
		if j.State == "prepared" {
			return recoveryError(j.Plan.ID, "存在未完成事务，请先执行 skill recover")
		}
	}
	return nil
}
func recoveryError(id, msg string) *result.Error {
	return &result.Error{Code: "RECOVERY_CONFLICT", Message: msg, Exit: 6, Recovery: map[string]string{"transaction": id, "command": "skill recover"}}
}
func (s *Store) Apply(ctx context.Context, id string) (Journal, error) {
	var j Journal
	e := s.Locked(func() error {
		if e := s.Pending(); e != nil {
			return e
		}
		p, e := s.LoadPlan(id)
		if e != nil {
			return e
		}
		if s.Validate != nil {
			if e = s.Validate(p); e != nil {
				return e
			}
		}
		if _, e = os.Stat(filepath.Join(s.bucket(), "transactions", id+".json")); e == nil {
			return result.Fail("PLAN_CONSUMED", "计划已使用，请重新预览", 3)
		}
		now, e := s.Capture(s.Root)
		if e != nil {
			return e
		}
		if Digest(now) != Digest(p.Before) {
			return result.Fail("PLAN_STALE", "源文件/草稿已在预览后变化；没有覆盖外部编辑", 3)
		}
		if e = s.validateAssets(p.Before, p.After); e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		j = Journal{Plan: p, State: "prepared"}
		if e = s.save(j); e != nil {
			return e
		}
		s.active = p.ID
		defer func() { s.active = "" }()
		e = s.transition(ctx, p.Before, p.After)
		if e == nil {
			j.State = "committed"
			e = s.save(j)
			if e == nil {
				return nil
			}
		}
		cause := e
		if e = s.recover(&j); e != nil {
			return e
		}
		return result.Fail("SOURCE_APPLY_FAILED", "保存失败，已恢复本次变更: "+cause.Error(), 5)
	})
	return j, e
}
func (s *Store) Recover() (int, error) {
	n := 0
	e := s.Locked(func() error {
		js, e := s.Journals()
		if e != nil {
			return e
		}
		for _, j := range js {
			if j.State == "prepared" {
				if e = s.recover(&j); e != nil {
					return e
				}
				n++
			}
		}
		return nil
	})
	return n, e
}
func (s *Store) recover(j *Journal) error {
	s.active = j.Plan.ID
	defer func() { s.active = "" }()
	now, desired, e := s.recoveryTrees(j.Plan)
	if e != nil {
		return recoveryError(j.Plan.ID, e.Error())
	}
	hook := s.Hook
	s.Hook = nil
	e = s.transition(context.Background(), now, desired)
	s.Hook = hook
	if e != nil {
		return recoveryError(j.Plan.ID, e.Error())
	}
	j.State = "recovered"
	if e = s.save(*j); e != nil {
		return recoveryError(j.Plan.ID, e.Error())
	}
	return nil
}

// Compute recovery without replacing any target bytes. Batch recovery checks
// every target first, so a conflict in the second target cannot undo the first.
func (s *Store) recoveryTrees(p Plan) (Tree, Tree, error) {
	fail := func(e error) (Tree, Tree, error) { return Tree{}, Tree{}, e }
	// Interrupted atomic writes may leave a partial temporary file. Only the
	// journal-derived name and a prefix of the approved bytes can be removed.
	for _, c := range p.Changes {
		if (c.After == nil || c.After.Dir) && (c.Before == nil || c.Before.Dir) {
			continue
		}
		tmp := filepath.Join(s.Root, filepath.FromSlash(path.Dir(c.Path)), s.targetTemp(path.Base(c.Path)))
		data, e := ReadFile(tmp)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return fail(e)
		}
		approved := false
		for _, entry := range []*Entry{c.Before, c.After} {
			if entry == nil || entry.Dir {
				continue
			}
			expected, err := s.Data(*entry)
			if err != nil {
				return fail(err)
			}
			approved = approved || bytes.HasPrefix(expected, data)
		}
		if !approved {
			return fail(fmt.Errorf("暂存文件已被外部修改"))
		}
		if e = os.Remove(tmp); e != nil {
			return fail(e)
		}
	}
	now, e := s.Capture(s.Root)
	if e != nil {
		return fail(e)
	}
	desired := Clone(now)
	for _, c := range p.Changes {
		current, exists := now.Entries[c.Path]
		same := func(x *Entry) bool {
			if x == nil {
				return !exists
			}
			return exists && current == *x
		}
		if !same(c.Before) && !same(c.After) {
			return fail(fmt.Errorf("恢复目标被外部修改: %s", c.Path))
		}
		if c.Before == nil {
			delete(desired.Entries, c.Path)
		} else {
			desired.Entries[c.Path] = *c.Before
		}
	}
	desired.Exists = p.Before.Exists || len(desired.Entries) > 0
	if e = validateTree(desired); e != nil {
		return fail(fmt.Errorf("恢复目录含外部内容: %w", e))
	}
	return now, desired, nil
}

// Each file is replaced atomically. The batch may be observed between files;
// its durable prepared record is what makes interrupted batches recoverable.
func (s *Store) transition(ctx context.Context, before, after Tree) error {
	if e := validateTree(after); e != nil {
		return e
	}
	changes := Changes(before, after)
	for _, c := range changes {
		if c.After != nil && !c.After.Dir {
			if _, e := s.Data(*c.After); e != nil {
				return e
			}
		}
	}
	if after.Exists {
		if e := CheckLinks(s.Root); e != nil {
			return e
		}
		if e := os.MkdirAll(s.Root, 0755); e != nil {
			return e
		}
	}
	dirs := []string{}
	for p, e := range after.Entries {
		if e.Dir {
			dirs = append(dirs, p)
		}
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		if old, ok := before.Entries[d]; ok && old == after.Entries[d] {
			continue
		}
		if e := CheckLinks(filepath.Join(s.Root, filepath.FromSlash(d))); e != nil {
			return e
		}
		r, e := os.OpenRoot(s.Root)
		if e != nil {
			return e
		}
		e = r.MkdirAll(d, os.FileMode(after.Entries[d].Mode))
		if e == nil {
			var f *os.File
			f, e = r.Open(d)
			if e == nil {
				e = f.Chmod(os.FileMode(after.Entries[d].Mode))
				f.Close()
			}
		}
		r.Close()
		if e != nil {
			return e
		}
	}
	count := 0
	for _, c := range changes {
		if (c.Before == nil || c.Before.Dir) && (c.After == nil || c.After.Dir) {
			continue
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		current, e := s.Capture(s.Root)
		if e != nil {
			return e
		}
		v, ok := current.Entries[c.Path]
		if (c.Before == nil && ok) || (c.Before != nil && (!ok || v != *c.Before)) {
			return result.Fail("PLAN_STALE", "写入前文件已变化: "+c.Path, 3)
		}
		target := filepath.Join(s.Root, filepath.FromSlash(c.Path))
		if c.After == nil {
			r, e := os.OpenRoot(s.Root)
			if e != nil {
				return e
			}
			e = r.Remove(c.Path)
			r.Close()
			if e != nil {
				return e
			}
		} else {
			data, e := s.Data(*c.After)
			if e != nil {
				return e
			}
			if e = s.atomic(target, data, os.FileMode(c.After.Mode)); e != nil {
				return e
			}
		}
		count++
		if s.Hook != nil {
			if e = s.Hook("replaced", count); e != nil {
				return e
			}
		}
	}
	removed := []string{}
	for _, c := range changes {
		if c.Before != nil && c.Before.Dir && c.After == nil {
			removed = append(removed, c.Path)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(removed)))
	for _, d := range removed {
		r, e := os.OpenRoot(s.Root)
		if e != nil {
			return e
		}
		e = r.Remove(d)
		r.Close()
		if e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	if !after.Exists {
		if e := os.Remove(s.Root); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	now, e := s.Capture(s.Root)
	if e != nil {
		return e
	}
	if Digest(now) != Digest(after) {
		return fmt.Errorf("事务结束时源树变化")
	}
	return nil
}
func (s *Store) atomic(p string, b []byte, mode os.FileMode) error {
	if e := CheckLinks(p); e != nil {
		return e
	}
	dir := filepath.Dir(p)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	if e := CheckLinks(dir); e != nil {
		return e
	}
	r, e := os.OpenRoot(dir)
	if e != nil {
		return e
	}
	defer r.Close()
	tmp := ".aidock-tmp-" + ID()
	if s.active != "" && Within(s.Root, p) {
		tmp = s.targetTemp(filepath.Base(p))
	}
	f, e := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if e != nil {
		return e
	}
	defer r.Remove(tmp)
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	if e == nil {
		e = f.Chmod(mode)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if s.active != "" && Within(s.Root, p) && s.Hook != nil {
		if e = s.Hook("staged", 0); e != nil {
			return e
		}
	}
	return durableRename(r, tmp, filepath.Base(p))
}

func (s *Store) targetTemp(base string) string {
	return ".aidock-tmp-" + s.active + "-" + Hash([]byte(base))[:16]
}
