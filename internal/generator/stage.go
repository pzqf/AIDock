package generator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/workstore"
)

type Stage struct {
	Version int     `json:"schema_version"`
	Kind    string  `json:"kind"`
	ID      string  `json:"id"`
	Created string  `json:"created"`
	Root    string  `json:"root"`
	Preview Preview `json:"preview"`
	Digest  string  `json:"digest"`
}
type State struct {
	StageID     string `json:"stage_id"`
	StageDigest string `json:"stage_digest"`
	Status      string `json:"status"`
}
type Observation struct {
	Stage             Stage       `json:"stage"`
	State             string      `json:"state"`
	Stale             bool        `json:"stale"`
	Reason            string      `json:"stale_reason,omitempty"`
	ObservationDigest string      `json:"observation_digest"`
	Files             int         `json:"files"`
	Directories       int         `json:"directories"`
	CleanupSafe       bool        `json:"cleanup_safe"`
	CleanupReason     string      `json:"cleanup_reason,omitempty"`
	Validation        *Validation `json:"validation,omitempty"`
}
type Receipt struct {
	Version       int      `json:"schema_version"`
	StageID       string   `json:"stage_id"`
	StageDigest   string   `json:"stage_digest"`
	AdapterHash   string   `json:"adapter_sha256"`
	RuntimeDigest string   `json:"runtime_digest"`
	InputDigest   string   `json:"input_digest"`
	Tools         []string `json:"tools"`
}
type Package struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision string `json:"revision"`
}
type Exchange struct {
	Receipt
	Protocol string    `json:"protocol"`
	Packages []Package `json:"packages"`
	Network  string    `json:"network"`
	Children string    `json:"child_processes"`
}

func (m *Manager) binding() string {
	return workstore.Hash(encode([]string{strings.ToLower(m.Source.Store.Root), m.Source.Owner, m.Source.Scope}))
}
func (m *Manager) base() string {
	return filepath.Join(m.Source.Store.Local, "external-generators", m.binding())
}
func (m *Manager) area() string {
	return filepath.Join(m.Source.Store.Local, "generator-stages", m.binding())
}
func (m *Manager) root(id string) string      { return filepath.Join(m.area(), "stage-"+id) }
func (m *Manager) planFile(id string) string  { return filepath.Join(m.base(), "plans", id+".json") }
func (m *Manager) stateFile(id string) string { return filepath.Join(m.base(), "states", id+".json") }
func stageDigest(s Stage) string              { s.Digest = ""; return workstore.Hash(encode(s)) }
func (m *Manager) locked(fn func() error) error {
	if err := workstore.CheckLinks(m.Source.Store.Local); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.area()), 0700); err != nil {
		return err
	}
	s := &workstore.Store{Root: m.area(), Local: m.base()}
	return s.Locked(fn)
}
func save(file string, v any, replace bool) error {
	if err := workstore.CheckLinks(file); err != nil {
		return err
	}
	if !replace {
		if _, err := os.Lstat(file); err == nil {
			return fmt.Errorf("记录已存在")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(file), ".record-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(encode(v))
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), file)
}
func (m *Manager) setState(s Stage, status string) error {
	return save(m.stateFile(s.ID), State{s.ID, s.Digest, status}, true)
}
func materialize(ctx context.Context, store *workstore.Store, root string, t workstore.Tree) error {
	if err := workstore.CheckLinks(root); err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	keys := make([]string, 0, len(t.Entries))
	for key := range t.Entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := workstore.SafeRelative(key); err != nil {
			return err
		}
		file := filepath.Join(root, filepath.FromSlash(key))
		if err := workstore.CheckLinks(file); err != nil {
			return err
		}
		e := t.Entries[key]
		if e.Dir {
			if err := os.MkdirAll(file, 0755); err != nil {
				return err
			}
			continue
		}
		b, err := store.Data(e)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			return err
		}
		f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(e.Mode))
		if err != nil {
			return err
		}
		_, err = f.Write(b)
		if err == nil {
			err = f.Sync()
		}
		ce := f.Close()
		if err == nil {
			err = ce
		}
		if err != nil {
			return err
		}
	}
	// Restore exact permissions after creation (including file modes masked by
	// the host's umask); directory permissions must wait for their descendants.
	for i := len(keys) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		e := t.Entries[keys[i]]
		file := filepath.Join(root, filepath.FromSlash(keys[i]))
		if err := workstore.CheckLinks(file); err != nil {
			return err
		}
		if err := os.Chmod(file, os.FileMode(e.Mode)); err != nil {
			return err
		}
	}
	return nil
}
func receipt(s Stage) Receipt {
	return Receipt{1, s.ID, s.Digest, s.Preview.AdapterHash, workstore.Digest(s.Preview.Runtime), workstore.Digest(s.Preview.Input), s.Preview.Contract.Tools}
}
func exchange(s Stage) Exchange {
	x := Exchange{Receipt: receipt(s), Protocol: Protocol, Packages: []Package{}, Network: "deny", Children: "deny"}
	for _, pkg := range s.Preview.Source.Packages {
		x.Packages = append(x.Packages, Package{pkg.Skill.ID, pkg.Skill.Name, pkg.Skill.Revision})
	}
	return x
}

// Stage binds the user's reviewed preview and records recovery identity before
// materialization. It only writes its own Local staging root.
func (m *Manager) Stage(ctx context.Context, adapter, reviewed string) (Observation, error) {
	var out Observation
	err := m.locked(func() error {
		p, err := m.Inspect(ctx, adapter)
		if err != nil {
			return err
		}
		if reviewed == "" || p.Digest != reviewed {
			return fail("GENERATOR_STAGE_STALE", "预览摘要不一致；重新检查当前源、程序及全部依赖", 3)
		}
		pending, err := m.pending()
		if err != nil {
			return err
		}
		if len(pending) != 0 {
			return fail("GENERATOR_CLEANUP_REQUIRED", "有未完成的暂存；先查看历史并清理，不覆盖旧目录", 6)
		}
		s := Stage{Version: 1, Kind: "external-generator-stage", ID: workstore.ID(), Created: time.Now().UTC().Format(time.RFC3339Nano), Preview: p}
		s.Root = m.root(s.ID)
		s.Digest = stageDigest(s)
		if err = save(m.planFile(s.ID), s, false); err != nil {
			return err
		}
		if err = m.setState(s, "PREPARING"); err != nil {
			return err
		}
		if err = os.MkdirAll(m.area(), 0700); err != nil {
			return err
		}
		if err = workstore.CheckLinks(s.Root); err != nil {
			return err
		}
		if err = os.Mkdir(s.Root, 0700); err != nil {
			return err
		}
		if err = save(filepath.Join(s.Root, ".aidock-generator.json"), State{s.ID, s.Digest, "OWNED"}, false); err != nil {
			return err
		}
		if m.hook != nil {
			if err = m.hook("marked"); err != nil {
				return err
			}
		}
		if err = materialize(ctx, m.Source.Store, filepath.Join(s.Root, "runtime"), p.Runtime); err != nil {
			return err
		}
		if err = materialize(ctx, m.Source.Store, filepath.Join(s.Root, "input"), p.Input); err != nil {
			return err
		}
		if err = save(filepath.Join(s.Root, "input", "request.json"), exchange(s), false); err != nil {
			return err
		}
		for _, dir := range []string{"output", "scratch"} {
			if err = os.Mkdir(filepath.Join(s.Root, dir), 0700); err != nil {
				return err
			}
		}
		if m.hook != nil {
			if err = m.hook("materialized"); err != nil {
				return err
			}
		}
		after, err := m.Inspect(ctx, adapter)
		if err != nil {
			return err
		}
		if after.Digest != p.Digest {
			return fail("GENERATOR_STAGE_STALE", "源或依赖在暂存期间变化；旧暂存保留待清理", 3)
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = m.setState(s, "STAGED"); err != nil {
			return err
		}
		out, err = m.observe(ctx, s, true)
		return err
	})
	return out, err
}
func (m *Manager) load(id string) (Stage, State, error) {
	var s Stage
	var state State
	if !uuid.MatchString(id) {
		return s, state, fail("INVALID_INPUT", "无效暂存 id", 2)
	}
	b, err := workstore.ReadFile(m.planFile(id))
	if err != nil {
		return s, state, err
	}
	if strict(b, &s) != nil || s.Version != 1 || s.Kind != "external-generator-stage" || s.ID != id || s.Root != m.root(id) || s.Digest != stageDigest(s) || s.Preview.Digest != previewDigest(s.Preview) || s.Preview.Policy != Policy || s.Preview.ExecutionReady || s.Preview.PublicationReady || s.Preview.Source.Root != m.Source.Store.Root || s.Preview.Source.Owner != m.Source.Owner || s.Preview.Source.Scope != m.Source.Scope {
		return s, state, fail("GENERATOR_RECORD_INVALID", "暂存记录身份、绑定或摘要无效", 5)
	}
	if err = m.validContract(s.Preview.Contract); err != nil {
		return s, state, err
	}
	b, err = workstore.ReadFile(m.stateFile(id))
	if os.IsNotExist(err) {
		return s, State{s.ID, s.Digest, "PREPARING"}, nil
	}
	if err != nil {
		return s, state, err
	}
	if strict(b, &state) != nil || state.StageID != id || state.StageDigest != s.Digest || state.Status != "PREPARING" && state.Status != "STAGED" && state.Status != "CLEANING" && state.Status != "CLEANED" {
		return s, state, fail("GENERATOR_RECORD_INVALID", "暂存恢复状态无效", 6)
	}
	return s, state, nil
}
func (m *Manager) ids() ([]string, error) {
	dir := filepath.Join(m.base(), "plans")
	if err := workstore.CheckLinks(dir); err != nil {
		return nil, err
	}
	es, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, e := range es {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			ids = append(ids, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	return ids, nil
}
func (m *Manager) pending() ([]Stage, error) {
	ids, err := m.ids()
	if err != nil {
		return nil, err
	}
	out := []Stage{}
	for _, id := range ids {
		s, state, err := m.load(id)
		if err != nil {
			return nil, err
		}
		if state.Status == "PREPARING" || state.Status == "CLEANING" {
			out = append(out, s)
		}
	}
	// Never adopt an unregistered directory, even one resembling our UUID names.
	if err = workstore.CheckLinks(m.area()); err != nil {
		return nil, err
	}
	es, err := os.ReadDir(m.area())
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	registered := map[string]bool{}
	for _, id := range ids {
		registered["stage-"+id] = true
	}
	for _, e := range es {
		if !registered[e.Name()] {
			return nil, fail("GENERATOR_RECOVERY_CONFLICT", "有未识别的暂存目录，保留内容："+filepath.Join(m.area(), e.Name()), 6)
		}
	}
	return out, nil
}
func (m *Manager) Show(ctx context.Context, id string) (Observation, error) {
	s, _, err := m.load(id)
	if err != nil {
		return Observation{}, err
	}
	return m.observe(ctx, s, true)
}
func (m *Manager) History(ctx context.Context) ([]Observation, error) {
	ids, err := m.ids()
	if err != nil {
		return nil, err
	}
	out := []Observation{}
	for _, id := range ids {
		o, err := m.Show(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Stage.Created > out[j].Stage.Created })
	return out, nil
}
func (m *Manager) observe(ctx context.Context, s Stage, fresh bool) (Observation, error) {
	_, state, err := m.load(s.ID)
	if err != nil {
		return Observation{}, err
	}
	o := Observation{Stage: s, State: state.Status, CleanupSafe: true}
	t, err := m.Source.Store.Capture(s.Root)
	if err != nil {
		o.CleanupSafe = false
		o.CleanupReason = err.Error()
		return o, nil
	}
	if t.Exists {
		b, err := workstore.ReadFile(filepath.Join(s.Root, ".aidock-generator.json"))
		var marker State
		if (err != nil || strict(b, &marker) != nil || marker != (State{s.ID, s.Digest, "OWNED"})) && !((state.Status == "PREPARING" || state.Status == "CLEANING") && len(t.Entries) == 0) {
			o.CleanupSafe = false
			o.CleanupReason = "恢复标记缺失或不一致，不能自动清理"
		}
		allowed := map[string]bool{".aidock-generator.json": true, "runtime": true, "input": true, "output": true, "scratch": true}
		for key, e := range t.Entries {
			if !allowed[strings.Split(key, "/")[0]] {
				o.CleanupSafe = false
				o.CleanupReason = "顶层有未声明内容，不能自动清理"
			}
			if e.Dir {
				o.Directories++
			} else {
				o.Files++
			}
		}
		if state.Status == "CLEANED" {
			o.CleanupSafe = false
			o.CleanupReason = "已清理目录被重新占用，不能自动接管"
		}
	} else if state.Status == "STAGED" {
		o.CleanupSafe = false
		o.CleanupReason = "完整暂存目录已被移走；保留记录，不自动重建"
	}
	o.ObservationDigest = workstore.Hash(encode(struct {
		State State
		Tree  workstore.Tree
	}{state, t}))
	if fresh {
		p, err := m.Inspect(ctx, s.Preview.AdapterPath)
		if err != nil {
			o.Stale = true
			o.Reason = "当前源或依赖无法核验：" + err.Error()
		} else if p.Digest != s.Preview.Digest {
			o.Stale = true
			o.Reason = "源、适配文件、程序或依赖发生变化；历史暂存不可复用"
		}
	}
	if (state.Status == "STAGED" || state.Status == "CLEANED") && fresh {
		o.Validation, err = m.latest(s)
		if err != nil {
			o.Validation = nil
			o.Stale = true
			o.Reason = "历史校验记录无效；不能复用通过结论，可清理此暂存：" + err.Error()
			return o, ctx.Err()
		}
		if o.Validation != nil && (o.Validation.ObservationDigest != o.ObservationDigest || o.Stale) {
			o.Validation.Stale = true
		}
	}
	return o, ctx.Err()
}
