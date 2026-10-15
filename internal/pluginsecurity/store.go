package pluginsecurity

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type Binding struct {
	Version    int    `json:"schema_version"`
	Project    string `json:"project"`
	Scope      string `json:"scope"`
	SourceRoot string `json:"source_root"`
}
type Manager struct {
	Store   *workstore.Store
	Binding Binding
	Now     func() time.Time
}
type History struct {
	ID      string `json:"id"`
	Action  string `json:"action"`
	State   string `json:"state"`
	Updated string `json:"updated"`
}
type State struct {
	SHA256         string              `json:"sha256"`
	Root           string              `json:"root"`
	Results        []Check             `json:"results"`
	Subjects       []Snapshot          `json:"subjects"`
	Documents      map[string]string   `json:"documents"`
	Checks         []inventory.Finding `json:"checks"`
	History        []History           `json:"history"`
	Recovery       bool                `json:"recovery_required"`
	Boundary       string              `json:"boundary"`
	ExecutionReady bool                `json:"execution_ready"`
}
type Edit struct {
	Action         string `json:"action"`
	ExpectedSHA256 string `json:"expected_sha256"`
	Document       string `json:"document,omitempty"`
	SubjectSHA256  string `json:"subject_sha256,omitempty"`
	Snapshot       string `json:"snapshot,omitempty"`
}
type Preview struct {
	Plan      workstore.Plan `json:"plan"`
	Before    State          `json:"before"`
	Candidate State          `json:"candidate"`
}

func New(local string, b Binding) (*Manager, error) {
	if b.Version != 1 || b.Project == "" || b.Scope == "" || !filepath.IsAbs(local) || filepath.Clean(local) != local || !filepath.IsAbs(b.SourceRoot) || filepath.Clean(b.SourceRoot) != b.SourceRoot {
		return nil, result.Fail("INVALID_SECURITY_BINDING", "安全快照需要明确本机根、项目、作用域和物理源目录", 2)
	}
	s := &workstore.Store{Root: filepath.Join(local, "plugin-security-"+workstore.Hash(bytesOf(b))), Local: filepath.Join(local, "plugin-security-transactions"), PlanKind: "plugin-security"}
	for _, p := range []string{s.Root, s.Local} {
		if err := workstore.CheckLinks(p); err != nil {
			return nil, err
		}
	}
	return &Manager{Store: s, Binding: b, Now: time.Now}, nil
}
func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

// A locally edited reference cannot silently remove a previously recorded risk.
// Journals are a local integrity anchor, not an external signature authority.
func (m *Manager) integrity(t workstore.Tree) error {
	js, err := m.Store.Journals()
	if err != nil {
		return err
	}
	for _, j := range js {
		if j.State == "prepared" {
			return nil
		}
		want := j.Plan.After
		if j.State == "recovered" {
			want = j.Plan.Before
		}
		if workstore.Digest(t) != workstore.Digest(want) {
			return result.Fail("SECURITY_CACHE_CHANGED", "缓存不符合最近本机事务；保留原件，可明确预览历史回退修复", 5)
		}
		return nil
	}
	if len(t.Entries) > 0 {
		return result.Fail("SECURITY_CACHE_CHANGED", "安全快照缺少本机导入事务，不能作为已确认缓存使用", 5)
	}
	return nil
}
func (m *Manager) read(t workstore.Tree) (map[string]Snapshot, map[string]string, error) {
	values, docs := map[string]Snapshot{}, map[string]string{}
	if len(t.Entries) == 0 {
		return values, docs, nil
	}
	e, ok := t.Entries["binding.json"]
	if !ok || e.Dir {
		return values, docs, result.Fail("SECURITY_OWNERSHIP_REQUIRED", "非空安全快照目录缺少绑定，保留并阻断", 5)
	}
	b, err := m.Store.Data(e)
	if err != nil {
		return values, docs, err
	}
	var bound Binding
	if err = decode(b, &bound); err != nil || bound != m.Binding {
		return values, docs, result.Fail("SECURITY_BINDING_MISMATCH", "安全快照属于其它绑定", 5)
	}
	for p, e := range t.Entries {
		if p == "binding.json" || p == "reports" && e.Dir {
			continue
		}
		key := strings.TrimSuffix(strings.TrimPrefix(p, "reports/"), ".json")
		if !strings.HasPrefix(p, "reports/") || !strings.HasSuffix(p, ".json") || !hash(key) || e.Dir {
			return values, docs, result.Fail("SECURITY_CONTENT_UNKNOWN", "安全快照含未知内容: "+p, 5)
		}
		b, err := m.Store.Data(e)
		if err != nil {
			return values, docs, err
		}
		s, err := Parse(b)
		if err != nil || s.SubjectSHA256 != key {
			return values, docs, result.Fail("SECURITY_REFERENCE_INVALID", "缓存快照格式或文件绑定错误", 5)
		}
		values[key] = s
		docs[key] = string(b)
	}
	return values, docs, nil
}
func (m *Manager) state(t workstore.Tree, candidates []Candidate) State {
	out := State{SHA256: workstore.Digest(t), Root: m.Store.Root, Results: []Check{}, Subjects: []Snapshot{}, Documents: map[string]string{}, Checks: []inventory.Finding{}, History: []History{}, Boundary: "本地导入的当前版本与直接声明依赖漏洞参考；不抓取网络、不做完整传递依赖或恶意代码分析、不提升 L0。快照来源内容未独立认证，空结果不能证明安全。"}
	values, docs, err := m.read(t)
	out.Documents = docs
	if err != nil {
		out.Checks = append(out.Checks, inventory.Finding{Status: "FAIL", Code: "MCP_SECURITY_STORE_INVALID", Detail: err.Error()})
		return out
	}
	for _, c := range candidates {
		template := Template(c, m.now())
		out.Subjects = append(out.Subjects, template)
		var snapshot *Snapshot
		if v, ok := values[template.SubjectSHA256]; ok {
			snapshot = &v
		}
		out.Results = append(out.Results, Verify(c, snapshot, m.now()))
	}
	return out
}
func (m *Manager) State(c []Candidate) (State, error) {
	var out State
	err := m.Store.Locked(func() error {
		t, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		out = m.state(t, c)
		if err = m.integrity(t); err != nil {
			out.Results = []Check{}
			out.Checks = append(out.Checks, inventory.Finding{Status: "FAIL", Code: "MCP_SECURITY_STORE_INVALID", Detail: err.Error()})
		}
		if err = m.Store.Pending(); err != nil {
			out.Recovery = true
			out.Checks = append(out.Checks, inventory.Finding{Status: "FAIL", Code: "MCP_SECURITY_RECOVERY_REQUIRED", Detail: recovery(err).Error()})
		}
		js, err := m.Store.Journals()
		if err != nil {
			return err
		}
		for _, j := range js {
			out.History = append(out.History, History{j.Plan.ID, j.Plan.Action, j.State, j.Updated})
		}
		return nil
	})
	return out, recovery(err)
}
func valid(r Edit) bool {
	return hash(r.ExpectedSHA256) && (r.Action == "import" && r.Document != "" && r.SubjectSHA256 == "" && r.Snapshot == "" || r.Action == "remove" && hash(r.SubjectSHA256) && r.Document == "" && r.Snapshot == "" || r.Action == "rollback" && r.Snapshot != "" && r.SubjectSHA256 == "" && r.Document == "")
}
func (m *Manager) build(t workstore.Tree, r Edit, c []Candidate) (workstore.Tree, error) {
	after := workstore.Clone(t)
	if _, _, err := m.read(t); err != nil {
		return after, err
	}
	if len(t.Entries) == 0 {
		if err := m.Store.PutFile(&after, "binding.json", JSON(m.Binding), 0600); err != nil {
			return after, err
		}
	}
	switch r.Action {
	case "import":
		s, err := Parse([]byte(r.Document))
		if err != nil {
			return after, result.Fail("SECURITY_REFERENCE_INVALID", err.Error(), 5)
		}
		found := false
		for _, candidate := range c {
			if Template(candidate, m.now()).SubjectSHA256 == s.SubjectSHA256 {
				if err = Bound(s, candidate); err != nil {
					return after, result.Fail("SECURITY_REFERENCE_UNBOUND", err.Error(), 5)
				}
				found = true
			}
		}
		if !found {
			return after, result.Fail("SECURITY_REFERENCE_UNBOUND", "快照不属于当前锁定产物", 5)
		}
		if err = m.Store.PutFile(&after, "reports/"+s.SubjectSHA256+".json", []byte(r.Document), 0600); err != nil {
			return after, err
		}
	case "remove":
		p := "reports/" + r.SubjectSHA256 + ".json"
		if _, ok := after.Entries[p]; !ok {
			return after, result.Fail("SECURITY_REFERENCE_NOT_FOUND", "没有此缓存快照", 4)
		}
		delete(after.Entries, p)
	case "rollback":
		js, err := m.Store.Journals()
		if err != nil {
			return after, err
		}
		found := false
		for _, j := range js {
			if j.Plan.ID == r.Snapshot && j.State == "committed" {
				after = workstore.Clone(j.Plan.Before)
				found = true
				break
			}
		}
		if !found {
			return after, result.Fail("SNAPSHOT_NOT_FOUND", "没有此安全快照恢复点", 4)
		}
	default:
		return after, fmt.Errorf("未知动作")
	}
	if _, _, err := m.read(after); err != nil {
		return after, err
	}
	return after, nil
}
func fingerprint(s State) string {
	return workstore.Hash(bytesOf(struct {
		Results []Check
		Checks  []inventory.Finding
	}{s.Results, s.Checks}))
}
func (m *Manager) Plan(ctx context.Context, r Edit, c []Candidate, sourceHash string) (Preview, error) {
	var out Preview
	if !valid(r) || !hash(sourceHash) {
		return out, result.Fail("INVALID_INPUT", "安全快照动作和已读摘要不匹配", 2)
	}
	err := m.Store.Locked(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.Store.Pending(); err != nil {
			return err
		}
		before, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		if workstore.Digest(before) != r.ExpectedSHA256 {
			return result.Fail("PLAN_STALE", "安全快照已变化，保留草稿并重读", 3)
		}
		if err = m.integrity(before); err != nil && r.Action != "rollback" {
			return err
		}
		after, err := m.build(before, r, c)
		if err != nil {
			return err
		}
		out.Before = m.state(before, c)
		out.Candidate = m.state(after, c)
		out.Plan, err = m.Store.PreviewBound("security-"+r.Action, before, after, map[string]string{"request": string(bytesOf(r)), "source_sha256": sourceHash, "review_sha256": fingerprint(out.Candidate)})
		return err
	})
	return out, recovery(err)
}
func (m *Manager) Apply(ctx context.Context, id, digest string, c []Candidate, sourceHash string) (workstore.Journal, error) {
	if digest == "" {
		return workstore.Journal{}, result.Fail("INVALID_INPUT", "需要确认摘要", 2)
	}
	m.Store.Validate = func(p workstore.Plan) error {
		js, err := m.Store.Journals()
		if err != nil {
			return err
		}
		for _, j := range js {
			if j.Plan.ID == p.ID {
				return result.Fail("PLAN_CONSUMED", "计划已使用，请重新预览", 3)
			}
		}
		if len(p.Inputs) != 3 || p.Digest != digest || p.Inputs["source_sha256"] != sourceHash {
			return result.Fail("PLAN_STALE", "确认摘要或源已变化", 3)
		}
		var r Edit
		if err := decode([]byte(p.Inputs["request"]), &r); err != nil || !valid(r) || p.Action != "security-"+r.Action || r.ExpectedSHA256 != workstore.Digest(p.Before) {
			return result.Fail("INVALID_SECURITY_PLAN", "快照计划动作和输入不一致", 5)
		}
		if err := m.integrity(p.Before); err != nil && r.Action != "rollback" {
			return err
		}
		after, err := m.build(p.Before, r, c)
		if err != nil {
			return err
		}
		if workstore.Digest(after) != workstore.Digest(p.After) {
			return result.Fail("INVALID_SECURITY_PLAN", "完整快照候选与请求或历史不符", 5)
		}
		if fingerprint(m.state(after, c)) != p.Inputs["review_sha256"] {
			return result.Fail("PLAN_STALE", "产物、权限、快照覆盖或有效期已变化", 3)
		}
		return nil
	}
	j, err := m.Store.Apply(ctx, id)
	return j, recovery(err)
}
func (m *Manager) Recover() (int, error) { n, err := m.Store.Recover(); return n, recovery(err) }
func recovery(err error) error {
	var e *result.Error
	if errors.As(err, &e) && e.Code == "RECOVERY_CONFLICT" {
		return &result.Error{Code: e.Code, Message: strings.ReplaceAll(e.Message, "skill recover", "mcp security recover"), Exit: e.Exit, Recovery: map[string]string{"command": "mcp security recover"}}
	}
	return err
}
