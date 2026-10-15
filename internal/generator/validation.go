package generator

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/workstore"
)

type Validation struct {
	Version           int            `json:"schema_version"`
	ID                string         `json:"id"`
	StageID           string         `json:"stage_id"`
	StageDigest       string         `json:"stage_digest"`
	Created           string         `json:"created"`
	Status            string         `json:"status"`
	ObservationDigest string         `json:"observation_digest"`
	Runtime           workstore.Tree `json:"runtime"`
	Input             workstore.Tree `json:"input"`
	Output            workstore.Tree `json:"output"`
	Issues            []string       `json:"issues"`
	ExecutionReady    bool           `json:"execution_ready"`
	PublicationReady  bool           `json:"publication_ready"`
	Stale             bool           `json:"stale"`
	Digest            string         `json:"digest"`
	Boundary          string         `json:"boundary"`
}

// ValidatedOutput is the only handoff surface exposed to another subsystem.
// It requires a current PASS record and a byte-for-byte observation of the
// stage output. The receipt still binds inputs only; it is not an execution
// attestation.
type ValidatedOutput struct {
	Stage      Stage
	Validation Validation
	Output     workstore.Tree
}

func validationDigest(v Validation) string {
	v.Digest = ""
	v.Stale = false
	return workstore.Hash(encode(v))
}
func (m *Manager) latest(s Stage) (*Validation, error) {
	dir := filepath.Join(m.base(), "validations", s.ID)
	if err := workstore.CheckLinks(dir); err != nil {
		return nil, err
	}
	es, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	records := []Validation{}
	for _, e := range es {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := workstore.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var v Validation
		if strict(b, &v) != nil || v.Version != 1 || !uuid.MatchString(v.ID) || e.Name() != v.ID+".json" || v.StageID != s.ID || v.StageDigest != s.Digest || v.Digest != validationDigest(v) || v.ExecutionReady || v.PublicationReady || v.Status != "PASS" && v.Status != "FAIL" {
			return nil, fail("GENERATOR_RECORD_INVALID", "输出校验记录无效", 5)
		}
		if v.Status == "PASS" {
			valid, err := m.consistentAcceptance(s, v)
			if err != nil {
				return nil, err
			}
			if !valid {
				return nil, fail("GENERATOR_RECORD_INVALID", "记录通过结论与锁定输入/输出不一致；重新计算摘要不能采纳错误内容", 5)
			}
		}
		records = append(records, v)
	}
	if len(records) == 0 {
		return nil, nil
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Created > records[j].Created })
	return &records[0], nil
}
func (m *Manager) consistentAcceptance(s Stage, v Validation) (bool, error) {
	expectedInput := workstore.Clone(s.Preview.Input)
	if err := m.Source.Store.PutFile(&expectedInput, "request.json", encode(exchange(s)), 0600); err != nil {
		return false, err
	}
	if len(v.Issues) != 0 || !bytesEqual(v.Runtime, s.Preview.Runtime) || !bytesEqual(v.Input, expectedInput) {
		return false, nil
	}
	packages := workstore.Clone(v.Output)
	delete(packages.Entries, "result.json")
	if !bytesEqual(packages, s.Preview.Expected) {
		return false, nil
	}
	e, ok := v.Output.Entries["result.json"]
	if !ok || e.Dir {
		return false, nil
	}
	b, err := m.Source.Store.Data(e)
	if err != nil {
		return false, err
	}
	var r Receipt
	if strict(b, &r) != nil || string(encode(r)) != string(encode(receipt(s))) {
		return false, nil
	}
	for _, tree := range []workstore.Tree{v.Runtime, v.Input, v.Output} {
		for key, e := range tree.Entries {
			if err := workstore.SafeRelative(key); err != nil {
				return false, err
			}
			if !e.Dir {
				if _, err := m.Source.Store.Data(e); err != nil {
					return false, err
				}
			}
		}
	}
	return true, nil
}
func bytesEqual(a, b workstore.Tree) bool {
	if a.Exists != b.Exists || len(a.Entries) != len(b.Entries) {
		return false
	}
	for key, e := range a.Entries {
		v, ok := b.Entries[key]
		if !ok || e.Dir != v.Dir || e.Hash != v.Hash || e.Size != v.Size || e.Mode != v.Mode {
			return false
		}
	}
	return true
}

// Validate ingests only the stage's predetermined output subtree. The receipt
// binds inputs but is NOT an attestation that an external program executed.
func (m *Manager) Validate(ctx context.Context, id, digest string) (Validation, error) {
	var v Validation
	err := m.locked(func() error {
		s, state, err := m.load(id)
		if err != nil {
			return err
		}
		if digest == "" || digest != s.Digest {
			return fail("GENERATOR_STAGE_STALE", "暂存摘要不一致", 3)
		}
		o, err := m.observe(ctx, s, true)
		if err != nil {
			return err
		}
		if state.Status != "STAGED" || !o.CleanupSafe {
			return fail("GENERATOR_STAGE_INVALID", "暂存未完成、目录缺失或存在不安全路径", 5)
		}
		if o.Stale {
			return fail("GENERATOR_STAGE_STALE", o.Reason, 3)
		}
		v = Validation{Version: 1, ID: workstore.ID(), StageID: id, StageDigest: s.Digest, Created: time.Now().UTC().Format(time.RFC3339Nano), Status: "PASS", ObservationDigest: o.ObservationDigest, Issues: []string{}, Boundary: Boundary}
		add := func(msg string) {
			v.Status = "FAIL"
			if len(v.Issues) < 100 {
				v.Issues = append(v.Issues, msg)
			}
		}
		runtime, err := m.Source.Store.Capture(filepath.Join(s.Root, "runtime"))
		if err != nil {
			return err
		}
		if workstore.Digest(runtime) != workstore.Digest(s.Preview.Runtime) {
			add("暂存程序或依赖被修改")
		}
		input, err := m.Source.Store.Capture(filepath.Join(s.Root, "input"))
		if err != nil {
			return err
		}
		v.Runtime, v.Input = runtime, input
		expectedInput := workstore.Clone(s.Preview.Input)
		if err = m.Source.Store.PutFile(&expectedInput, "request.json", encode(exchange(s)), 0600); err != nil {
			return err
		}
		if workstore.Digest(input) != workstore.Digest(expectedInput) {
			add("暂存输入、二进制附件、空目录或请求清单被修改")
		}
		output, err := m.Source.Store.Capture(filepath.Join(s.Root, "output"))
		if err != nil {
			return err
		}
		v.Output = output
		entry, ok := output.Entries["result.json"]
		var returned Receipt
		if !ok || entry.Dir {
			add("缺少 result.json 输入绑定回执")
		} else {
			b, err := m.Source.Store.Data(entry)
			if err != nil {
				return err
			}
			if len(b) > 64<<10 || strict(b, &returned) != nil || string(encode(returned)) != string(encode(receipt(s))) {
				add("回执含未知字段，或源/依赖/工具/暂存摘要不一致")
			}
		}
		packages := workstore.Clone(output)
		delete(packages.Entries, "result.json")
		if !bytesEqual(packages, s.Preview.Expected) {
			keys := map[string]bool{}
			for key := range packages.Entries {
				keys[key] = true
			}
			for key := range s.Preview.Expected.Entries {
				keys[key] = true
			}
			ordered := []string{}
			for key := range keys {
				ordered = append(ordered, key)
			}
			sort.Strings(ordered)
			for _, key := range ordered {
				got, gok := packages.Entries[key]
				want, wok := s.Preview.Expected.Entries[key]
				switch {
				case !wok:
					add("未声明输出：" + key)
				case !gok:
					add("缺少完整包内容：" + key)
				case got.Dir != want.Dir || got.Hash != want.Hash || got.Size != want.Size || got.Mode != want.Mode:
					add("输出字节、权限或目录类型不同：" + key)
				}
			}
		}
		if m.hook != nil {
			if err = m.hook("validated"); err != nil {
				return err
			}
		}
		after, err := m.observe(ctx, s, true)
		if err != nil {
			return err
		}
		if after.Stale || after.ObservationDigest != o.ObservationDigest {
			return fail("GENERATOR_STAGE_STALE", "源、依赖或暂存内容在校验期间变化；未保存校验结果", 3)
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		v.Digest = validationDigest(v)
		return save(filepath.Join(m.base(), "validations", id, v.ID+".json"), v, false)
	})
	return v, err
}

// LoadValidated returns an immutable, current external output for a read-only
// replica rehearsal. It never launches the declared program and never writes
// a project or tool directory.
func (m *Manager) LoadValidated(ctx context.Context, id, digest string) (ValidatedOutput, error) {
	var out ValidatedOutput
	err := m.locked(func() error {
		var err error
		out, err = m.loadValidated(ctx, id, digest)
		return err
	})
	return out, err
}

func (m *Manager) loadValidated(ctx context.Context, id, digest string) (ValidatedOutput, error) {
	var out ValidatedOutput
	s, state, err := m.load(id)
	if err != nil {
		return out, err
	}
	if digest == "" || digest != s.Digest {
		return out, fail("GENERATOR_STAGE_STALE", "暂存摘要不一致；重新查看当前暂存", 3)
	}
	if state.Status != "STAGED" {
		return out, fail("GENERATOR_OUTPUT_UNAVAILABLE", "暂存尚未处于可交换状态", 4)
	}
	o, err := m.observe(ctx, s, true)
	if err != nil {
		return out, err
	}
	if o.Stale {
		return out, fail("GENERATOR_STAGE_STALE", o.Reason, 3)
	}
	if !o.CleanupSafe {
		return out, fail("GENERATOR_OUTPUT_UNAVAILABLE", o.CleanupReason, 4)
	}
	if o.Validation != nil && (o.Validation.Stale || o.Validation.ObservationDigest != o.ObservationDigest) {
		return out, fail("GENERATOR_STAGE_STALE", "源、依赖或返回目录发生变化；请重新检查并生成", 3)
	}
	if o.Validation == nil || o.Validation.Status != "PASS" {
		return out, fail("GENERATOR_OUTPUT_UNAVAILABLE", "需要当前暂存的 PASS 返回包校验记录；校验记录过期或缺失", 4)
	}
	output, err := m.Source.Store.Capture(filepath.Join(s.Root, "output"))
	if err != nil {
		return out, err
	}
	if !bytesEqual(output, o.Validation.Output) {
		return out, fail("GENERATOR_STAGE_STALE", "返回目录在读取期间变化；未接收外部输出", 3)
	}
	after, err := m.observe(ctx, s, true)
	if err != nil {
		return out, err
	}
	if after.State != "STAGED" || !after.CleanupSafe || after.Stale || after.ObservationDigest != o.ObservationDigest {
		return out, fail("GENERATOR_STAGE_STALE", "读取期间源、依赖或暂存内容变化；未接收外部输出", 3)
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	out = ValidatedOutput{Stage: s, Validation: *o.Validation, Output: output}
	return out, nil
}

// Cleanup is an explicit, digest-bound deletion of owned temporary resources.
// Immutable plans, byte snapshots and validation history are retained.
func (m *Manager) Cleanup(ctx context.Context, id, reviewed string) (Observation, error) {
	var out Observation
	err := m.locked(func() error {
		s, state, err := m.load(id)
		if err != nil {
			return err
		}
		o, err := m.observe(ctx, s, false)
		if err != nil {
			return err
		}
		if reviewed == "" || reviewed != o.ObservationDigest {
			return fail("GENERATOR_STAGE_STALE", "清理预览后内容发生变化；重新查看并确认当前文件", 3)
		}
		if !o.CleanupSafe {
			return fail("GENERATOR_RECOVERY_CONFLICT", o.CleanupReason, 6)
		}
		if state.Status == "CLEANED" {
			out = o
			return nil
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = m.setState(s, "CLEANING"); err != nil {
			return err
		}
		if m.hook != nil {
			if err = m.hook("cleaning"); err != nil {
				return err
			}
		}
		tree, err := m.Source.Store.Capture(s.Root)
		if err != nil {
			return fail("GENERATOR_RECOVERY_CONFLICT", err.Error(), 6)
		}
		observed := workstore.Hash(encode(struct {
			State State
			Tree  workstore.Tree
		}{state, tree}))
		if observed != o.ObservationDigest {
			return fail("GENERATOR_STAGE_STALE", "内容在开始清理前变化；未删除新增内容，重新查看后再确认", 3)
		}
		// All paths were checked before touching any contents. Marker is removed last.
		entries, err := os.ReadDir(s.Root)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		for _, entry := range entries {
			if entry.Name() == ".aidock-generator.json" {
				continue
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			if err = workstore.CheckLinks(filepath.Join(s.Root, entry.Name())); err != nil {
				return fail("GENERATOR_RECOVERY_CONFLICT", err.Error(), 6)
			}
			if err = makeDeletable(ctx, filepath.Join(s.Root, entry.Name())); err != nil {
				return fail("GENERATOR_CLEANUP_REQUIRED", err.Error(), 6)
			}
			if err = os.RemoveAll(filepath.Join(s.Root, entry.Name())); err != nil {
				return fail("GENERATOR_CLEANUP_REQUIRED", err.Error(), 6)
			}
		}
		if _, exists := os.Lstat(s.Root); exists == nil {
			if err = os.Remove(filepath.Join(s.Root, ".aidock-generator.json")); err != nil && !os.IsNotExist(err) {
				return fail("GENERATOR_CLEANUP_REQUIRED", err.Error(), 6)
			}
			if err = os.Remove(s.Root); err != nil {
				_ = save(filepath.Join(s.Root, ".aidock-generator.json"), State{s.ID, s.Digest, "OWNED"}, false)
				return fail("GENERATOR_CLEANUP_REQUIRED", err.Error(), 6)
			}
		}
		if err = m.setState(s, "CLEANED"); err != nil {
			return err
		}
		out, err = m.observe(ctx, s, false)
		return err
	})
	return out, err
}

// Adjust only approved temporary copies, never their original dependencies.
func makeDeletable(ctx context.Context, root string) error {
	return filepath.WalkDir(root, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = workstore.CheckLinks(file); err != nil {
			return err
		}
		mode := os.FileMode(0600)
		if d.IsDir() {
			mode = 0700
		}
		return os.Chmod(file, mode)
	})
}
