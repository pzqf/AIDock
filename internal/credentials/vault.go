// Package credentials keeps values in the OS vault. Its disk journal contains
// names and random revisions only; it is deliberately outside source rollback.
package credentials

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

const MaxValue = 2400 // Windows generic-credential blob limit, in bytes.

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var revisionPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var errMissing = errors.New("credential missing")

type Entry struct {
	Name     string `json:"name"`
	Revision string `json:"revision"`
	Updated  string `json:"updated"`
}
type Event struct {
	Sequence int    `json:"sequence"`
	ID       string `json:"id"`
	At       string `json:"at"`
	Action   string `json:"action"`
	Name     string `json:"name"`
	Before   string `json:"before,omitempty"`
	After    string `json:"after,omitempty"`
	Outcome  string `json:"outcome"`
	Previous string `json:"previous,omitempty"`
}
type State struct {
	Available bool    `json:"available"`
	Provider  string  `json:"provider"`
	Namespace string  `json:"namespace"`
	Entries   []Entry `json:"entries"`
	History   []Event `json:"history"`
	Pending   []Event `json:"pending"`
	Boundary  string  `json:"boundary"`
}
type Edit struct {
	Action             string `json:"action"`
	Name               string `json:"name"`
	ExpectedRevision   string `json:"expected_revision,omitempty"`
	Value              string `json:"value,omitempty"`
	Confirm            bool   `json:"confirm"`
	ReferencesReviewed bool   `json:"references_reviewed,omitempty"`
	ExpectedReferences string `json:"expected_references,omitempty"`
}
type backend interface {
	available() bool
	locked(context.Context, func() error) error
	list() ([]Entry, error)
	read(string) (Entry, []byte, error)
	write(Entry, []byte) error
	remove(string) error
}
type Manager struct {
	local, namespace string
	store            backend
	// hook is a crash/failure seam, never configured by CLI or HTTP inputs.
	hook func(string) error
}

func New(local string) (*Manager, error) {
	if !filepath.IsAbs(local) || filepath.Clean(local) != local {
		return nil, fail("INVALID_ROOTS", "密钥管理需要规范的本机数据根")
	}
	if err := workstore.CheckLinks(local); err != nil {
		return nil, fail("PATH_UNSAFE", "密钥审计路径含链接或不可访问")
	}
	sum := sha256.Sum256([]byte(strings.ToLower(local)))
	ns := hex.EncodeToString(sum[:])
	return &Manager{local: local, namespace: ns, store: newBackend(ns)}, nil
}
func fail(code, message string) error { return result.Fail(code, message, 4) }
func ValidName(name string) bool      { return namePattern.MatchString(name) }
func wipe(b []byte)                   { clear(b) }
func randomID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func (m *Manager) directory() string {
	return filepath.Join(m.local, "credential-audit-v1", m.namespace)
}

// ParseEdit rejects duplicate keys, case aliases and unknown fields without
// reflecting attacker-controlled values into diagnostics.
func ParseEdit(data []byte) (Edit, error) {
	var out Edit
	d := json.NewDecoder(strings.NewReader(string(data)))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return out, fail("INVALID_INPUT", "密钥请求须为一个 JSON 对象")
	}
	seen := map[string]bool{}
	for d.More() {
		t, e = d.Token()
		if e != nil {
			return Edit{}, fail("INVALID_INPUT", "密钥请求无效")
		}
		key, ok := t.(string)
		if !ok || seen[key] {
			return Edit{}, fail("INVALID_INPUT", "密钥请求含重复字段")
		}
		seen[key] = true
		var target any
		switch key {
		case "action":
			target = &out.Action
		case "name":
			target = &out.Name
		case "expected_revision":
			target = &out.ExpectedRevision
		case "value":
			target = &out.Value
		case "confirm":
			target = &out.Confirm
		case "references_reviewed":
			target = &out.ReferencesReviewed
		case "expected_references":
			target = &out.ExpectedReferences
		default:
			return Edit{}, fail("INVALID_INPUT", "密钥请求含未知字段")
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil || string(raw) == "null" || json.Unmarshal(raw, target) != nil {
			return Edit{}, fail("INVALID_INPUT", "密钥请求字段类型无效")
		}
	}
	if _, e = d.Token(); e != nil {
		return Edit{}, fail("INVALID_INPUT", "密钥请求不完整")
	}
	var tail json.RawMessage
	if d.Decode(&tail) == nil || d.InputOffset() != int64(len(strings.TrimRight(string(data), " \r\n\t"))) {
		return Edit{}, fail("INVALID_INPUT", "密钥请求须为单个对象")
	}
	return out, nil
}

func (m *Manager) audit() ([]Event, error) {
	dir := m.directory()
	if workstore.CheckLinks(dir) != nil {
		return nil, fail("CREDENTIAL_AUDIT_INVALID", "密钥审计目录不安全")
	}
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []Event{}, nil
	}
	if err != nil || len(files) > 10000 {
		return nil, fail("CREDENTIAL_AUDIT_INVALID", "密钥审计不可读或超过条数限制")
	}
	out := []Event{}
	head := ""
	for i, f := range files {
		if f.Name() != fmt.Sprintf("%08d.json", i+1) || f.IsDir() {
			return nil, fail("CREDENTIAL_AUDIT_INVALID", "密钥审计顺序或内容异常；保留现场")
		}
		b, e := workstore.ReadFile(filepath.Join(dir, f.Name()))
		if e != nil || len(b) > 4096 {
			return nil, fail("CREDENTIAL_AUDIT_INVALID", "密钥审计记录不可读")
		}
		var a Event
		if json.Unmarshal(b, &a) != nil || a.Sequence != i+1 || a.Previous != head || !revisionPattern.MatchString(a.ID) || !ValidName(a.Name) || a.Before != "" && !revisionPattern.MatchString(a.Before) || a.After != "" && !revisionPattern.MatchString(a.After) {
			return nil, fail("CREDENTIAL_AUDIT_INVALID", "密钥审计链无效")
		}
		if a.Action != "add" && a.Action != "rotate" && a.Action != "delete" {
			return nil, fail("CREDENTIAL_AUDIT_INVALID", "密钥审计动作无效")
		}
		if _, e = time.Parse(time.RFC3339Nano, a.At); e != nil {
			return nil, fail("CREDENTIAL_AUDIT_INVALID", "密钥审计时间无效")
		}
		if a.Outcome != "INTENT" && a.Outcome != "APPLIED" && a.Outcome != "NOT_APPLIED" {
			return nil, fail("CREDENTIAL_AUDIT_INVALID", "密钥审计状态无效")
		}
		out = append(out, a)
		head = digest(b)
	}
	// Every outcome must close exactly one matching intent.
	open := map[string]Event{}
	for _, a := range out {
		if a.Outcome == "INTENT" {
			if _, ok := open[a.ID]; ok {
				return nil, fail("CREDENTIAL_AUDIT_INVALID", "密钥审计意图重复")
			}
			open[a.ID] = a
		} else {
			p, ok := open[a.ID]
			if !ok || p.Action != a.Action || p.Name != a.Name || p.Before != a.Before || p.After != a.After {
				return nil, fail("CREDENTIAL_AUDIT_INVALID", "密钥审计结果未绑定意图")
			}
			delete(open, a.ID)
		}
	}
	return out, nil
}
func pending(events []Event) []Event {
	open := map[string]Event{}
	for _, a := range events {
		if a.Outcome == "INTENT" {
			open[a.ID] = a
		} else {
			delete(open, a.ID)
		}
	}
	out := []Event{}
	for _, a := range open {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
	return out
}
func (m *Manager) append(events []Event, a Event) error {
	if len(events) >= 10000 {
		return fail("CREDENTIAL_AUDIT_UNAVAILABLE", "密钥审计已达当前容量限制；保留记录并阻断")
	}
	dir := m.directory()
	if workstore.CheckLinks(dir) != nil {
		return fail("CREDENTIAL_AUDIT_INVALID", "审计路径不安全")
	}
	if os.MkdirAll(dir, 0700) != nil {
		return fail("CREDENTIAL_AUDIT_UNAVAILABLE", "无法创建密钥审计目录；未继续操作")
	}
	a.Sequence = len(events) + 1
	a.At = time.Now().UTC().Format(time.RFC3339Nano)
	if len(events) > 0 {
		b, e := workstore.ReadFile(filepath.Join(dir, fmt.Sprintf("%08d.json", len(events))))
		if e != nil {
			return fail("CREDENTIAL_AUDIT_UNAVAILABLE", "无法读取密钥审计头")
		}
		a.Previous = digest(b)
	}
	b, _ := json.Marshal(a)
	b = append(b, '\n')
	f, e := os.OpenFile(filepath.Join(dir, fmt.Sprintf("%08d.json", a.Sequence)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return fail("CREDENTIAL_AUDIT_UNAVAILABLE", "无法追加密钥审计")
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	closed := f.Close()
	if e != nil || closed != nil {
		return fail("CREDENTIAL_AUDIT_UNAVAILABLE", "密钥审计未完整写入；保留现场并阻断后续操作")
	}
	return nil
}
func (m *Manager) State(ctx context.Context) (State, error) {
	out := State{Available: m.store.available(), Provider: "Windows Credential Manager", Namespace: m.namespace, Entries: []Entry{}, History: []Event{}, Pending: []Event{}, Boundary: "值仅存当前 Windows 用户的系统凭据库；本机数据根隔离名称空间。普通同步、快照和回退不包含值；轮换与删除不能从 AIDock 恢复旧值。"}
	if !out.Available {
		out.Provider = "unavailable"
		return out, nil
	}
	err := m.store.locked(ctx, func() error {
		var e error
		out.Entries, e = m.store.list()
		if e != nil {
			return e
		}
		out.History, e = m.audit()
		out.Pending = pending(out.History)
		return e
	})
	return out, err
}
func (m *Manager) Change(ctx context.Context, req Edit) (Entry, error) {
	var out Entry
	if !ValidName(req.Name) || !req.Confirm {
		return out, fail("INVALID_INPUT", "需要有效名称和明确确认；名称使用小写字母开头及字母、数字、下划线，最多 64 字符")
	}
	if req.Action != "add" && req.Action != "rotate" && req.Action != "delete" {
		return out, fail("INVALID_INPUT", "密钥动作只支持新增、轮换或删除")
	}
	if req.Action == "delete" {
		if req.Value != "" || !req.ReferencesReviewed {
			return out, fail("CREDENTIAL_REFERENCES_REVIEW_REQUIRED", "删除前须检查引用并确认影响；删除不能回退")
		}
	} else if len(req.Value) == 0 || len(req.Value) > MaxValue || !utf8.ValidString(req.Value) || strings.ContainsRune(req.Value, 0) {
		return out, fail("INVALID_INPUT", "密钥值须为有效 UTF-8 文本，非空、不含 NUL，最多 2400 字节")
	}
	if req.Action == "add" && req.ExpectedRevision != "" || req.Action != "add" && !revisionPattern.MatchString(req.ExpectedRevision) {
		return out, fail("INVALID_INPUT", "轮换或删除需要当前修订；新增不能指定修订")
	}
	if !m.store.available() {
		return out, fail("CREDENTIAL_VAULT_UNAVAILABLE", "当前系统没有受支持的 OS 凭据库；不会降级写文件")
	}
	err := m.store.locked(ctx, func() error {
		events, e := m.audit()
		if e != nil {
			return e
		}
		if len(events) > 9998 {
			return fail("CREDENTIAL_AUDIT_UNAVAILABLE", "密钥审计容量不足以记录完整操作")
		}
		if len(pending(events)) > 0 {
			return fail("CREDENTIAL_RECOVERY_REQUIRED", "存在结果未记录的密钥操作；先核对当前系统状态")
		}
		old, value, e := m.store.read(req.Name)
		wipe(value)
		if req.Action == "add" {
			if e == nil {
				return fail("CREDENTIAL_EXISTS", "名称已存在；请使用轮换")
			}
			if !errors.Is(e, errMissing) {
				return e
			}
		} else {
			if errors.Is(e, errMissing) {
				return fail("CREDENTIAL_MISSING", "密钥不存在")
			}
			if e != nil {
				return e
			}
			if old.Revision != req.ExpectedRevision {
				return fail("CREDENTIAL_STALE", "密钥修订已变化；刷新后重新确认")
			}
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		a := Event{ID: randomID(), Action: req.Action, Name: req.Name, Before: old.Revision, Outcome: "INTENT"}
		out = Entry{Name: req.Name, Revision: randomID(), Updated: time.Now().UTC().Format(time.RFC3339Nano)}
		if req.Action != "delete" {
			a.After = out.Revision
		}
		if e = m.append(events, a); e != nil {
			return e
		}
		if m.hook != nil {
			if e = m.hook("intent"); e != nil {
				return e
			}
		}
		if req.Action == "delete" {
			e = m.store.remove(req.Name)
		} else {
			secret := []byte(req.Value)
			e = m.store.write(out, secret)
			wipe(secret)
		}
		if e != nil {
			return fail("CREDENTIAL_OUTCOME_UNRECORDED", "系统凭据操作未完成；请核对当前状态后重试")
		}
		if m.hook != nil {
			if e = m.hook("vault"); e != nil {
				return e
			}
		}
		events, e = m.audit()
		if e != nil {
			return e
		}
		a.Outcome = "APPLIED"
		if e = m.append(events, a); e != nil {
			return fail("CREDENTIAL_OUTCOME_UNRECORDED", "系统凭据已改变，但结果审计未完成；先核对当前状态")
		}
		if req.Action == "delete" {
			out = Entry{Name: req.Name}
		}
		return nil
	})
	return out, err
}

// Reconcile observes random revisions; it never repeats a write or recovers an
// old value. Conflicting external changes remain blocked for manual review.
func (m *Manager) Reconcile(ctx context.Context) error {
	if !m.store.available() {
		return fail("CREDENTIAL_VAULT_UNAVAILABLE", "系统凭据库不可用")
	}
	return m.store.locked(ctx, func() error {
		events, e := m.audit()
		if e != nil {
			return e
		}
		for _, a := range pending(events) {
			current, value, err := m.store.read(a.Name)
			wipe(value)
			if err != nil && !errors.Is(err, errMissing) {
				return err
			}
			rev := current.Revision
			if rev == a.After {
				a.Outcome = "APPLIED"
			} else if rev == a.Before {
				a.Outcome = "NOT_APPLIED"
			} else {
				return fail("CREDENTIAL_RECOVERY_CONFLICT", "系统密钥与操作前后修订均不匹配；保留审计并阻断")
			}
			if e = m.append(events, a); e != nil {
				return e
			}
			events, e = m.audit()
			if e != nil {
				return e
			}
		}
		return nil
	})
}
