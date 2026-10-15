package pluginorigin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/pluginlock"
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
type Report struct {
	SubjectSHA256 string `json:"subject_sha256"`
	KeyID         string `json:"key_id"`
	Publisher     string `json:"publisher"`
	ExpiresAt     string `json:"expires_at"`
}
type State struct {
	SHA256         string              `json:"sha256"`
	Keys           Keys                `json:"keys"`
	Subjects       []Subject           `json:"subjects"`
	Reports        []Report            `json:"reports"`
	Results        []Check             `json:"results"`
	Checks         []inventory.Finding `json:"checks"`
	History        []History           `json:"history"`
	Recovery       bool                `json:"recovery_required"`
	AuditCount     int                 `json:"audit_count"`
	AuditHead      string              `json:"audit_head"`
	Root           string              `json:"root"`
	Boundary       string              `json:"boundary"`
	ExecutionReady bool                `json:"execution_ready"`
}
type Edit struct {
	Action         string `json:"action"`
	ExpectedSHA256 string `json:"expected_sha256"`
	Keys           *Keys  `json:"keys,omitempty"`
	Evidence       string `json:"evidence,omitempty"`
	SubjectSHA256  string `json:"subject_sha256,omitempty"`
	Snapshot       string `json:"snapshot,omitempty"`
}
type Preview struct {
	Plan           workstore.Plan `json:"plan"`
	Candidate      State          `json:"candidate"`
	Before         State          `json:"before"`
	EvidenceBefore string         `json:"evidence_before,omitempty"`
	EvidenceAfter  string         `json:"evidence_after,omitempty"`
}
type audit struct {
	Version   int    `json:"schema_version"`
	Sequence  int    `json:"sequence"`
	ID        string `json:"id"`
	Action    string `json:"action"`
	CreatedAt string `json:"created_at"`
	Previous  string `json:"previous"`
	Before    string `json:"before"`
	After     string `json:"after"`
}

func New(local string, binding Binding) (*Manager, error) {
	if !filepath.IsAbs(local) || filepath.Clean(local) != local || !filepath.IsAbs(binding.SourceRoot) || filepath.Clean(binding.SourceRoot) != binding.SourceRoot || binding.Project == "" || binding.Scope == "" || binding.Version != 1 {
		return nil, result.Fail("INVALID_ORIGIN_BINDING", "来源证据需要明确的本机根、项目、作用域和源目录", 2)
	}
	key := workstore.Hash(bytesOf(binding))
	s := &workstore.Store{Root: filepath.Join(local, "plugin-origin-"+key), Local: filepath.Join(local, "plugin-origin-transactions"), PlanKind: "plugin-origin"}
	if err := workstore.CheckLinks(s.Root); err != nil {
		return nil, err
	}
	if err := workstore.CheckLinks(s.Local); err != nil {
		return nil, err
	}
	return &Manager{Store: s, Binding: binding, Now: time.Now}, nil
}
func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}
func authority(t workstore.Tree) string {
	v := map[string]workstore.Entry{}
	for p, e := range t.Entries {
		if p != "audit" && !strings.HasPrefix(p, "audit/") {
			v[p] = e
		}
	}
	return workstore.Hash(bytesOf(v))
}
func decode(b []byte, v any) error {
	parsed, err := config.Parse(b)
	if err != nil {
		return err
	}
	d := json.NewDecoder(strings.NewReader(string(bytesOf(parsed))))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func (m *Manager) binding(t workstore.Tree) error {
	if len(t.Entries) == 0 {
		return nil
	}
	e, ok := t.Entries["binding.json"]
	if !ok || e.Dir {
		return result.Fail("ORIGIN_OWNERSHIP_REQUIRED", "非空来源证据目录缺少绑定，不能接管", 4)
	}
	b, err := m.Store.Data(e)
	if err != nil {
		return err
	}
	var binding Binding
	if err = decode(b, &binding); err != nil || binding != m.Binding {
		return result.Fail("ORIGIN_BINDING_MISMATCH", "来源证据属于另一个项目、作用域或源目录", 3)
	}
	for p, e := range t.Entries {
		valid := p == "binding.json" || p == "keys.json" || p == "reports" && e.Dir || p == "audit" && e.Dir || strings.HasPrefix(p, "reports/") && !e.Dir && digest(strings.TrimSuffix(strings.TrimPrefix(p, "reports/"), ".json")) && strings.HasSuffix(p, ".json") || strings.HasPrefix(p, "audit/") && !e.Dir && strings.HasSuffix(p, ".json")
		if !valid {
			return result.Fail("ORIGIN_CONTENT_UNKNOWN", "来源证据目录包含未知内容，保留并阻断: "+p, 5)
		}
	}
	return nil
}
func (m *Manager) audits(t workstore.Tree) ([]audit, string, error) {
	paths := []string{}
	for p := range t.Entries {
		if strings.HasPrefix(p, "audit/") {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	items := []audit{}
	head := ""
	for i, p := range paths {
		b, err := m.Store.Data(t.Entries[p])
		if err != nil {
			return items, head, err
		}
		var a audit
		if err = decode(b, &a); err != nil {
			return items, head, err
		}
		if a.Version != 1 || a.Sequence != i+1 || a.Previous != head || a.ID == "" || !originAction(a.Action) || !digest(a.Before) || !digest(a.After) || p != fmt.Sprintf("audit/%08d-%s.json", a.Sequence, a.ID) {
			return items, head, result.Fail("ORIGIN_AUDIT_INVALID", "来源证据审计链断裂或记录不匹配", 5)
		}
		if i > 0 && a.Action != "rollback" && a.Before != items[i-1].After {
			return items, head, result.Fail("ORIGIN_AUDIT_INVALID", "审计前后状态不连续", 5)
		}
		if _, err = time.Parse(time.RFC3339Nano, a.CreatedAt); err != nil {
			return items, head, result.Fail("ORIGIN_AUDIT_INVALID", "审计时间无效", 5)
		}
		items = append(items, a)
		head = workstore.Hash(b)
	}
	if len(items) == 0 && len(t.Entries) > 0 {
		return items, head, result.Fail("ORIGIN_AUDIT_INVALID", "已初始化来源目录缺少审计链", 5)
	}
	// Committed journals are a separate local anchor. Replacing the authority
	// tree with an older otherwise-valid prefix must not erase later audit events.
	js, err := m.Store.Journals()
	if err != nil {
		return items, head, err
	}
	for _, j := range js {
		if j.State != "committed" {
			continue
		}
		for p, e := range j.Plan.After.Entries {
			if strings.HasPrefix(p, "audit/") && t.Entries[p] != e {
				return items, head, result.Fail("ORIGIN_AUDIT_INVALID", "审计记录与已提交事务锚点不符；保留现状等待人工核对", 5)
			}
		}
	}
	if len(items) > 0 && items[len(items)-1].After != authority(t) {
		return items, head, result.Fail("ORIGIN_AUTHORITY_DRIFT", "密钥或证据在审计记录之外发生变化；需核对后从已保存记录恢复", 5)
	}
	return items, head, nil
}
func (m *Manager) read(t workstore.Tree) (Keys, map[string]Envelope, error) {
	keys := Keys{Version: 1, Keys: []Key{}}
	reports := map[string]Envelope{}
	if err := m.binding(t); err != nil {
		return keys, reports, err
	}
	if e, ok := t.Entries["keys.json"]; ok {
		b, err := m.Store.Data(e)
		if err != nil {
			return keys, reports, err
		}
		keys, err = ParseKeys(b)
		if err != nil {
			return keys, reports, result.Fail("ORIGIN_KEYS_INVALID", err.Error(), 5)
		}
	} else if len(t.Entries) > 0 {
		return keys, reports, result.Fail("ORIGIN_KEYS_INVALID", "来源证据缺少密钥表", 5)
	}
	for p, e := range t.Entries {
		if !strings.HasPrefix(p, "reports/") {
			continue
		}
		b, err := m.Store.Data(e)
		if err != nil {
			return keys, reports, err
		}
		v, err := ParseEnvelope(b)
		if err != nil || p != "reports/"+v.Payload.SubjectSHA256+".json" {
			return keys, reports, result.Fail("ORIGIN_EVIDENCE_INVALID", "来源声明格式或文件绑定不符: "+p, 5)
		}
		reports[v.Payload.SubjectSHA256] = v
	}
	return keys, reports, nil
}
func (m *Manager) state(t workstore.Tree, plugins []pluginlock.Plugin) State {
	out := State{SHA256: workstore.Digest(t), Keys: Keys{Version: 1, Keys: []Key{}}, Subjects: []Subject{}, Reports: []Report{}, Results: []Check{}, Checks: []inventory.Finding{}, History: []History{}, Root: m.Store.Root, Boundary: "密钥只在此项目、作用域与本机源目录生效，不随共享源传播。签名证明登记密钥签署了特定产物声明；安全评估与执行授权分别判断。"}
	keys, reports, err := m.read(t)
	if err == nil {
		var audits []audit
		audits, out.AuditHead, err = m.audits(t)
		out.AuditCount = len(audits)
	}
	if err != nil {
		out.Checks = append(out.Checks, inventory.Finding{Status: "FAIL", Code: "MCP_ORIGIN_STORE_INVALID", Detail: err.Error()})
	} else {
		out.Keys = keys
	}
	for _, p := range plugins {
		sub := For(p)
		out.Subjects = append(out.Subjects, sub)
		var e *Envelope
		if v, ok := reports[sub.SHA256]; ok {
			e = &v
		}
		check := Verify(p, keys, e, m.now())
		if err != nil {
			check.Status = "FAIL"
			check.RuntimeVersionVerified = false
			check.Checks = []inventory.Finding{{Status: "FAIL", Code: "MCP_ORIGIN_STORE_INVALID", Detail: err.Error()}}
		}
		out.Results = append(out.Results, check)
	}
	ids := []string{}
	for id := range reports {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e := reports[id]
		out.Reports = append(out.Reports, Report{SubjectSHA256: id, KeyID: e.KeyID, Publisher: e.Payload.Publisher, ExpiresAt: e.Payload.ExpiresAt})
	}
	return out
}
func (m *Manager) State(plugins []pluginlock.Plugin) (State, error) {
	var out State
	err := m.Store.Locked(func() error {
		t, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		out = m.state(t, plugins)
		if err = m.Store.Pending(); err != nil {
			err = originRecovery(err)
			out.Recovery = true
			out.Checks = append(out.Checks, inventory.Finding{Status: "FAIL", Code: "MCP_ORIGIN_RECOVERY_REQUIRED", Detail: err.Error()})
			for i := range out.Results {
				out.Results[i].Status = "FAIL"
				out.Results[i].RuntimeVersionVerified = false
				out.Results[i].Checks = []inventory.Finding{{Status: "FAIL", Code: "MCP_ORIGIN_RECOVERY_REQUIRED", Detail: err.Error()}}
			}
		}
		js, err := m.Store.Journals()
		if err != nil {
			return err
		}
		for _, j := range js {
			out.History = append(out.History, History{ID: j.Plan.ID, Action: j.Plan.Action, State: j.State, Updated: j.Updated})
		}
		return nil
	})
	return out, originRecovery(err)
}
func (m *Manager) init(t *workstore.Tree) error {
	if err := m.binding(*t); err != nil {
		return err
	}
	if len(t.Entries) == 0 {
		if err := m.Store.PutFile(t, "binding.json", JSON(m.Binding), 0600); err != nil {
			return err
		}
		return m.Store.PutFile(t, "keys.json", JSON(Keys{Version: 1, Keys: []Key{}}), 0600)
	}
	return nil
}
func evidenceDigest(s State) string { return workstore.Hash(bytesOf(s.Results)) }
func originAction(s string) bool {
	return s == "keys" || s == "evidence" || s == "remove" || s == "rollback"
}
func authorityDrift(err error) bool {
	var e *result.Error
	return errors.As(err, &e) && e.Code == "ORIGIN_AUTHORITY_DRIFT"
}
func (m *Manager) Plan(ctx context.Context, req Edit, plugins []pluginlock.Plugin, sourceSHA256 string) (Preview, error) {
	var out Preview
	if req.ExpectedSHA256 == "" || !digest(sourceSHA256) {
		return out, result.Fail("INVALID_INPUT", "需要已读取的本机证据摘要和项目源绑定", 2)
	}
	valid := req.Action == "keys" && req.Keys != nil && req.Evidence == "" && req.SubjectSHA256 == "" && req.Snapshot == "" || req.Action == "evidence" && req.Keys == nil && req.Evidence != "" && req.SubjectSHA256 == "" && req.Snapshot == "" || req.Action == "remove" && req.Keys == nil && req.Evidence == "" && digest(req.SubjectSHA256) && req.Snapshot == "" || req.Action == "rollback" && req.Keys == nil && req.Evidence == "" && req.SubjectSHA256 == "" && req.Snapshot != ""
	if !valid {
		return out, result.Fail("INVALID_INPUT", "动作与来源证据输入不匹配", 2)
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
		if workstore.Digest(before) != req.ExpectedSHA256 {
			return result.Fail("PLAN_STALE", "本机来源证据已变化；保留草稿并重新读取", 3)
		}
		if err = m.binding(before); err != nil {
			return err
		}
		audits, head, auditErr := m.audits(before)
		// Explicit history restoration may repair authority bytes changed outside
		// the chain; every existing audit record still has to validate.
		if auditErr != nil && (req.Action != "rollback" || !authorityDrift(auditErr)) {
			return auditErr
		}
		after := workstore.Clone(before)
		if err = m.init(&after); err != nil {
			return err
		}
		switch req.Action {
		case "keys":
			keys, err := ParseKeys(JSON(req.Keys))
			if err != nil {
				return result.Fail("ORIGIN_KEYS_INVALID", err.Error(), 5)
			}
			if err = m.Store.PutFile(&after, "keys.json", JSON(keys), 0600); err != nil {
				return err
			}
		case "evidence":
			e, err := ParseEnvelope([]byte(req.Evidence))
			if err != nil {
				return result.Fail("ORIGIN_EVIDENCE_INVALID", err.Error(), 5)
			}
			if err = m.Store.PutFile(&after, "reports/"+e.Payload.SubjectSHA256+".json", []byte(req.Evidence), 0600); err != nil {
				return err
			}
		case "remove":
			p := "reports/" + req.SubjectSHA256 + ".json"
			if _, ok := after.Entries[p]; !ok {
				return result.Fail("ORIGIN_EVIDENCE_NOT_FOUND", "没有此候选的缓存声明", 2)
			}
			delete(after.Entries, p)
		case "rollback":
			js, err := m.Store.Journals()
			if err != nil {
				return err
			}
			found := false
			for _, j := range js {
				if j.Plan.ID != req.Snapshot || j.State != "committed" {
					continue
				}
				found = true
				for p := range after.Entries {
					if p != "binding.json" && p != "audit" && !strings.HasPrefix(p, "audit/") {
						delete(after.Entries, p)
					}
				}
				for p, e := range j.Plan.Before.Entries {
					if p != "binding.json" && p != "audit" && !strings.HasPrefix(p, "audit/") {
						after.Entries[p] = e
					}
				}
				if _, ok := after.Entries["keys.json"]; !ok {
					if err = m.Store.PutFile(&after, "keys.json", JSON(Keys{Version: 1, Keys: []Key{}}), 0600); err != nil {
						return err
					}
				}
				break
			}
			if !found {
				return result.Fail("SNAPSHOT_NOT_FOUND", "没有此来源证据的已提交恢复点", 2)
			}
		}
		if _, _, err = m.read(after); err != nil {
			return err
		}
		a := audit{Version: 1, Sequence: len(audits) + 1, ID: workstore.ID(), Action: req.Action, CreatedAt: m.now().Format(time.RFC3339Nano), Previous: head, Before: authority(before), After: authority(after)}
		if err = m.Store.PutFile(&after, fmt.Sprintf("audit/%08d-%s.json", a.Sequence, a.ID), JSON(a), 0600); err != nil {
			return err
		}
		out.Before = m.state(before, plugins)
		out.Candidate = m.state(after, plugins)
		target := ""
		if req.Action == "keys" {
			target = "keys.json"
		}
		if req.Action == "evidence" {
			e, _ := ParseEnvelope([]byte(req.Evidence))
			target = "reports/" + e.Payload.SubjectSHA256 + ".json"
		}
		if req.Action == "remove" {
			target = "reports/" + req.SubjectSHA256 + ".json"
		}
		if strings.HasPrefix(target, "reports/") {
			if e, ok := before.Entries[target]; ok {
				b, err := m.Store.Data(e)
				if err != nil {
					return err
				}
				out.EvidenceBefore = string(b)
			}
			if e, ok := after.Entries[target]; ok {
				b, err := m.Store.Data(e)
				if err != nil {
					return err
				}
				out.EvidenceAfter = string(b)
			}
		}
		out.Plan, err = m.Store.PreviewBound("origin-"+req.Action, before, after, map[string]string{"source_sha256": sourceSHA256, "evidence_state": evidenceDigest(out.Candidate), "target": target, "snapshot": req.Snapshot})
		return err
	})
	return out, originRecovery(err)
}
func (m *Manager) Apply(ctx context.Context, id, digestValue string, plugins []pluginlock.Plugin, sourceSHA256 string) (workstore.Journal, error) {
	if digestValue == "" {
		return workstore.Journal{}, result.Fail("INVALID_INPUT", "需要确认摘要", 2)
	}
	m.Store.Validate = func(p workstore.Plan) error {
		if p.Digest != digestValue || p.Inputs["source_sha256"] != sourceSHA256 {
			return result.Fail("PLAN_STALE", "确认摘要或项目源已变化", 3)
		}
		if p.Action != "origin-keys" && p.Action != "origin-evidence" && p.Action != "origin-remove" && p.Action != "origin-rollback" {
			return result.Fail("INVALID_INPUT", "来源确认不接受其它事务种类", 2)
		}
		if _, _, err := m.read(p.After); err != nil {
			return err
		}
		aa, _, err := m.audits(p.After)
		if err != nil {
			return err
		}
		ba, _, beforeErr := m.audits(p.Before)
		if beforeErr != nil && (p.Action != "origin-rollback" || !authorityDrift(beforeErr)) {
			return beforeErr
		}
		if len(aa) != len(ba)+1 {
			return result.Fail("ORIGIN_AUDIT_INVALID", "每次来源更改必须追加一条审计记录", 5)
		}
		last := aa[len(aa)-1]
		if last.Before != authority(p.Before) || last.Action != strings.TrimPrefix(p.Action, "origin-") {
			return result.Fail("ORIGIN_AUDIT_INVALID", "审计记录与候选动作不匹配", 5)
		}
		for path, e := range p.Before.Entries {
			if strings.HasPrefix(path, "audit/") && p.After.Entries[path] != e {
				return result.Fail("ORIGIN_AUDIT_INVALID", "不能重写已有审计记录", 5)
			}
		}
		if err = m.validateChanges(p); err != nil {
			return err
		}
		if state := m.state(p.After, plugins); evidenceDigest(state) != p.Inputs["evidence_state"] {
			return result.Fail("PLAN_STALE", "签名证据或密钥有效性在预览后发生变化；重新预览", 3)
		}
		return nil
	}
	j, err := m.Store.Apply(ctx, id)
	return j, originRecovery(err)
}
func (m *Manager) Recover() (int, error) { n, err := m.Store.Recover(); return n, originRecovery(err) }

func originRecovery(err error) error {
	var e *result.Error
	if errors.As(err, &e) && e.Code == "RECOVERY_CONFLICT" {
		return &result.Error{Code: e.Code, Message: "来源证据事务需要恢复或人工核对: " + strings.ReplaceAll(e.Message, "skill recover", "mcp origin recover"), Exit: e.Exit, Recovery: map[string]string{"command": "mcp origin recover"}}
	}
	return err
}

// Validate a persisted plan's ownership and exact mutation surface independently
// of its digest. A different action cannot smuggle a key or second report edit.
func (m *Manager) validateChanges(p workstore.Plan) error {
	if err := m.binding(p.Before); err != nil {
		return err
	}
	if len(p.Inputs) != 4 {
		return result.Fail("INVALID_ORIGIN_PLAN", "来源计划输入不匹配", 5)
	}
	if p.Action == "origin-rollback" {
		js, err := m.Store.Journals()
		if err != nil {
			return err
		}
		found := false
		for _, j := range js {
			if j.Plan.ID != p.Inputs["snapshot"] || j.State != "committed" {
				continue
			}
			found = true
			expected := workstore.Clone(p.Before)
			if err = m.init(&expected); err != nil {
				return err
			}
			for path := range expected.Entries {
				if path != "binding.json" && path != "audit" && !strings.HasPrefix(path, "audit/") {
					delete(expected.Entries, path)
				}
			}
			for path, e := range j.Plan.Before.Entries {
				if path != "binding.json" && path != "audit" && !strings.HasPrefix(path, "audit/") {
					expected.Entries[path] = e
				}
			}
			if _, ok := expected.Entries["keys.json"]; !ok {
				if err = m.Store.PutFile(&expected, "keys.json", JSON(Keys{Version: 1, Keys: []Key{}}), 0600); err != nil {
					return err
				}
			}
			if authority(expected) != authority(p.After) {
				return result.Fail("INVALID_ORIGIN_PLAN", "恢复候选与已提交历史不匹配", 5)
			}
			break
		}
		if !found {
			return result.Fail("SNAPSHOT_NOT_FOUND", "恢复点不可用", 5)
		}
	} else {
		target := p.Inputs["target"]
		if p.Inputs["snapshot"] != "" || p.Action == "origin-keys" && target != "keys.json" || p.Action != "origin-keys" && (!strings.HasPrefix(target, "reports/") || !strings.HasSuffix(target, ".json") || !digest(strings.TrimSuffix(strings.TrimPrefix(target, "reports/"), ".json"))) {
			return result.Fail("INVALID_ORIGIN_PLAN", "来源动作与目标路径不匹配", 5)
		}
		base := workstore.Clone(p.Before)
		if err := m.init(&base); err != nil {
			return err
		}
		for _, c := range workstore.Changes(base, p.After) {
			if c.Path != target && c.Path != "reports" && c.Path != "audit" && !strings.HasPrefix(c.Path, "audit/") {
				return result.Fail("INVALID_ORIGIN_PLAN", "来源动作不能修改其它文件: "+c.Path, 5)
			}
		}
		_, exists := p.After.Entries[target]
		if p.Action == "origin-remove" && exists || p.Action != "origin-remove" && !exists {
			return result.Fail("INVALID_ORIGIN_PLAN", "来源动作的文件结果不匹配", 5)
		}
	}
	beforeBinding, exists := p.Before.Entries["binding.json"]
	if exists && beforeBinding != p.After.Entries["binding.json"] {
		return result.Fail("ORIGIN_BINDING_MISMATCH", "不能更改来源证据绑定", 5)
	}
	return nil
}

func (m *Manager) Show(subject string) (string, error) {
	if !digest(subject) {
		return "", result.Fail("INVALID_INPUT", "需要来源声明 subject SHA-256", 2)
	}
	var out string
	err := m.Store.Locked(func() error {
		t, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		if err = m.binding(t); err != nil {
			return err
		}
		e, ok := t.Entries["reports/"+subject+".json"]
		if !ok {
			return result.Fail("ORIGIN_EVIDENCE_NOT_FOUND", "没有此缓存声明", 2)
		}
		b, err := m.Store.Data(e)
		out = string(b)
		return err
	})
	return out, err
}
