// Package runtimecheck records explicit runtime version checks. A completed
// check never grants permission to start a plugin or claims OS isolation.
package runtimecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type Binding = pluginpermission.Binding
type Input struct {
	Name                string                           `json:"name"`
	AuthorizationID     string                           `json:"authorization_id"`
	AuthorizationSHA256 string                           `json:"authorization_sha256"`
	InputSHA256         string                           `json:"input_sha256"`
	Runtime             pluginpermission.UnisolatedInput `json:"runtime"`
}
type Event struct {
	Version         int    `json:"schema_version"`
	Sequence        int    `json:"sequence"`
	ID              string `json:"id"`
	Attempt         string `json:"attempt"`
	CreatedAt       string `json:"created_at"`
	Previous        string `json:"previous"`
	Input           Input  `json:"input"`
	Phase           string `json:"phase"`
	Code            string `json:"code"`
	ObservedVersion string `json:"observed_version,omitempty"`
	Actor           string `json:"actor"`
}
type Entry struct {
	Event   Event  `json:"event"`
	Status  string `json:"status"`
	Current bool   `json:"current"`
}
type State struct {
	Binding        Binding `json:"binding"`
	Entries        []Entry `json:"entries"`
	AuditCount     int     `json:"audit_count"`
	AuditHead      string  `json:"audit_head"`
	Recovery       bool    `json:"recovery_required"`
	ExecutionReady bool    `json:"execution_ready"`
	Boundary       string  `json:"boundary"`
}
type Manager struct {
	Store     *workstore.Store
	operation *workstore.Store
	Binding   Binding
}

func bytesOf(v any) []byte { b, _ := json.Marshal(v); return b }
func hash(s string) bool   { return len(s) == 64 && strings.Trim(s, "0123456789abcdef") == "" }
func uuid(s string) bool {
	return len(s) == 36 && strings.Count(s, "-") == 4 && strings.Trim(s, "0123456789abcdef-") == ""
}

var versionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)

func New(local string, binding Binding) (*Manager, error) {
	if !filepath.IsAbs(local) || filepath.Clean(local) != local || !filepath.IsAbs(binding.SourceRoot) || filepath.Clean(binding.SourceRoot) != binding.SourceRoot || binding.Version != 1 || binding.Project == "" || binding.Scope == "" {
		return nil, result.Fail("RUNTIME_CHECK_BINDING_INVALID", "运行时核验需要明确项目副本与本机根", 2)
	}
	id := workstore.Hash(bytesOf(binding))
	m := &Manager{Store: &workstore.Store{Root: filepath.Join(local, "runtime-check-"+id), Local: filepath.Join(local, "runtime-check-transactions"), PlanKind: "runtime-check"}, operation: &workstore.Store{Root: filepath.Join(local, "runtime-operation-"+id), Local: filepath.Join(local, "runtime-check-transactions")}, Binding: binding}
	for _, p := range []string{m.Store.Root, m.Store.Local, m.operation.Root} {
		if err := workstore.CheckLinks(p); err != nil {
			return nil, err
		}
	}
	return m, nil
}
func validInput(i Input) bool {
	u := i.Runtime
	return i.Name != "" && len(i.Name) <= 64 && uuid(i.AuthorizationID) && hash(i.AuthorizationSHA256) && hash(i.InputSHA256) && u.SourceType == "local" && u.Kind == "node" && len(u.Version) <= 128 && versionPattern.MatchString(u.Version) && workstore.SafeRelative(u.Executable) == nil && hash(u.SHA256)
}
func From(r pluginpermission.Record) (Input, error) {
	if !r.OwnTrusted || r.Input.Tier != 0 || r.Input.Unisolated == nil {
		return Input{}, result.Fail("RUNTIME_CHECK_TRUST_REQUIRED", "先单独确认自有可信本地插件及锁定运行时的非隔离选择", 4)
	}
	i := Input{Name: r.Input.Name, AuthorizationID: r.ID, AuthorizationSHA256: workstore.Hash(bytesOf(r)), InputSHA256: workstore.Hash(bytesOf(r.Input)), Runtime: *r.Input.Unisolated}
	if !validInput(i) {
		return Input{}, result.Fail("RUNTIME_CHECK_UNSUPPORTED", "当前版本核验仅支持自有本地插件内的锁定 Node 稳定版本", 4)
	}
	return i, nil
}
func validEvent(e Event) bool {
	if e.Version != 1 || e.Sequence < 1 || !uuid(e.ID) || !uuid(e.Attempt) || !validInput(e.Input) || e.Actor == "" || len(e.Actor) > 512 || e.Sequence > 1 && !hash(e.Previous) {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, e.CreatedAt); err != nil {
		return false
	}
	switch e.Phase {
	case "INTENT":
		return e.Code == "RUNTIME_CHECK_INTENT" && e.ObservedVersion == ""
	case "COMPLETE":
		switch e.Code {
		case "RUNTIME_VERSION_MATCH":
			return versionPattern.MatchString(e.ObservedVersion) && strings.TrimPrefix(e.ObservedVersion, "v") == strings.TrimPrefix(e.Input.Runtime.Version, "v")
		case "RUNTIME_VERSION_MISMATCH":
			return versionPattern.MatchString(e.ObservedVersion) && strings.TrimPrefix(e.ObservedVersion, "v") != strings.TrimPrefix(e.Input.Runtime.Version, "v")
		case "RUNTIME_CHECK_FAILED", "RUNTIME_CHECK_TIMEOUT", "RUNTIME_CHECK_CANCELED", "RUNTIME_ARTIFACT_CHANGED", "RUNTIME_CHECK_OUTPUT_INVALID", "RUNTIME_CHECK_START_FAILED", "RUNTIME_CHECK_CLEANUP_FAILED":
			return e.ObservedVersion == ""
		}
	}
	return false
}
func invalid() error {
	return result.Fail("RUNTIME_CHECK_AUDIT_INVALID", "运行时核验审计、绑定或事务锚点不符；保留记录并阻断执行", 5)
}
func (m *Manager) read(t workstore.Tree) ([]Event, error) {
	events := []Event{}
	if len(t.Entries) == 0 {
		return events, nil
	}
	b, ok := t.Entries["binding.json"]
	if !ok || b.Dir {
		return nil, invalid()
	}
	raw, err := m.Store.Data(b)
	if err != nil {
		return nil, err
	}
	if string(raw) != string(bytesOf(m.Binding)) {
		return nil, invalid()
	}
	paths := []string{}
	for p, e := range t.Entries {
		if p == "binding.json" || p == "audit" && e.Dir {
			continue
		}
		if !strings.HasPrefix(p, "audit/") || e.Dir || !strings.HasSuffix(p, ".json") {
			return nil, invalid()
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	head := ""
	attempts := map[string]Event{}
	for n, p := range paths {
		raw, err = m.Store.Data(t.Entries[p])
		if err != nil {
			return nil, err
		}
		var e Event
		if json.Unmarshal(raw, &e) != nil || string(raw) != string(bytesOf(e)) || !validEvent(e) || e.Sequence != n+1 || e.Previous != head || p != fmt.Sprintf("audit/%08d-%s.json", e.Sequence, e.ID) {
			return nil, invalid()
		}
		old, exists := attempts[e.Attempt]
		if e.Phase == "INTENT" && exists || e.Phase == "COMPLETE" && (!exists || old.Phase != "INTENT" || string(bytesOf(old.Input)) != string(bytesOf(e.Input))) {
			return nil, invalid()
		}
		attempts[e.Attempt] = e
		events = append(events, e)
		head = workstore.Hash(raw)
	}
	if len(events) == 0 {
		return nil, invalid()
	}
	js, err := m.Store.Journals()
	if err != nil {
		return nil, err
	}
	for _, j := range js {
		if j.State == "committed" {
			for p, e := range j.Plan.After.Entries {
				if t.Entries[p] != e {
					return nil, invalid()
				}
			}
		}
	}
	return events, nil
}
func (m *Manager) State(current []Input) (State, error) {
	out := State{Binding: m.Binding, Entries: []Entry{}, Boundary: "仅核验明确选择的自有可信本地 Node 运行时版本。INTENT 只证明已记录执行意图；无完成回执可能仍在运行或已中断，不重放。匹配结果不证明插件启动、文件或网络隔离。审计完整性锚点在本机事务内，不能防御同用户同时改写全部记录。"}
	err := m.Store.Locked(func() error {
		if err := m.Store.Pending(); err != nil {
			out.Recovery = true
			return nil
		}
		t, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		events, err := m.read(t)
		if err != nil {
			return err
		}
		latest := map[string]Event{}
		order := []string{}
		for _, e := range events {
			out.AuditCount++
			out.AuditHead = workstore.Hash(bytesOf(e))
			if _, ok := latest[e.Attempt]; !ok {
				order = append(order, e.Attempt)
			}
			latest[e.Attempt] = e
		}
		for n := len(order) - 1; n >= 0; n-- {
			e := latest[order[n]]
			entry := Entry{Event: e, Status: "INCOMPLETE"}
			if e.Phase == "COMPLETE" {
				entry.Status = "FAIL"
				if e.Code == "RUNTIME_VERSION_MATCH" {
					entry.Status = "PASS"
				}
			}
			for _, i := range current {
				if string(bytesOf(e.Input)) == string(bytesOf(i)) {
					entry.Current = true
				}
			}
			created, _ := time.Parse(time.RFC3339Nano, e.CreatedAt)
			if time.Since(created) < 0 || time.Since(created) > 24*time.Hour {
				entry.Current = false
			}
			if out.Recovery {
				entry.Current = false
			}
			out.Entries = append(out.Entries, entry)
		}
		return nil
	})
	return out, err
}
func (m *Manager) append(ctx context.Context, input Input, attempt, phase, code, observed string) (Event, error) {
	account, err := user.Current()
	if err != nil || account.Uid == "" || account.Username == "" {
		return Event{}, result.Fail("RUNTIME_CHECK_ACTOR_UNAVAILABLE", "无法确定运行时核验账户；未执行", 4)
	}
	e := Event{Version: 1, ID: workstore.ID(), Attempt: attempt, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Input: input, Phase: phase, Code: code, ObservedVersion: observed}
	e.Actor = account.Uid + ":" + account.Username
	var plan workstore.Plan
	err = m.Store.Locked(func() error {
		if err := m.Store.Pending(); err != nil {
			return err
		}
		before, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		events, err := m.read(before)
		if err != nil {
			return err
		}
		if len(events) >= 10000 {
			return result.Fail("RUNTIME_CHECK_AUDIT_FULL", "运行时核验审计已达到容量限制", 4)
		}
		e.Sequence = len(events) + 1
		if len(events) > 0 {
			e.Previous = workstore.Hash(bytesOf(events[len(events)-1]))
		}
		if !validEvent(e) {
			return invalid()
		}
		after := workstore.Clone(before)
		if len(after.Entries) == 0 {
			if err = m.Store.PutFile(&after, "binding.json", bytesOf(m.Binding), 0600); err != nil {
				return err
			}
		}
		if err = m.Store.PutFile(&after, fmt.Sprintf("audit/%08d-%s.json", e.Sequence, e.ID), bytesOf(e), 0600); err != nil {
			return err
		}
		if _, err = m.read(after); err != nil {
			return err
		}
		plan, err = m.Store.Preview("runtime-check-"+strings.ToLower(phase), before, after)
		return err
	})
	if err == nil {
		m.Store.Validate = func(p workstore.Plan) error {
			if p.Digest != plan.Digest {
				return invalid()
			}
			_, err := m.read(p.Before)
			return err
		}
		_, err = m.Store.Apply(ctx, plan.ID)
		m.Store.Validate = nil
	}
	return e, recoveryError(err)
}

// Probe is called only while the current permission and source/artifact locks
// are held. This independent operation lock spans both durable events and the
// execution; audit transactions use their own lock. An interrupted intent is
// retained, never replayed or converted to success by recovery.
func (m *Manager) Probe(ctx context.Context, input Input, fn func() (string, string)) (Event, error) {
	if !validInput(input) || fn == nil {
		return Event{}, invalid()
	}
	var out Event
	err := m.operation.Locked(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		intent, err := m.append(ctx, input, workstore.ID(), "INTENT", "RUNTIME_CHECK_INTENT", "")
		if err != nil {
			return err
		}
		out = intent
		code, version := "RUNTIME_CHECK_FAILED", ""
		func() {
			defer func() {
				if recover() != nil {
					code, version = "RUNTIME_CHECK_FAILED", ""
				}
			}()
			code, version = fn()
		}()
		if !validEvent(Event{Version: 1, Sequence: 1, ID: workstore.ID(), Attempt: intent.Attempt, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Input: input, Phase: "COMPLETE", Code: code, ObservedVersion: version, Actor: intent.Actor}) {
			code, version = "RUNTIME_CHECK_FAILED", ""
		}
		finish, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err = m.append(finish, input, intent.Attempt, "COMPLETE", code, version)
		return err
	})
	return out, err
}
func (m *Manager) Recover() (int, error) {
	n := 0
	err := m.operation.Locked(func() error {
		var err error
		n, err = m.Store.RecoverGuarded(func(p workstore.Plan) error {
			if p.Action != "runtime-check-intent" && p.Action != "runtime-check-complete" {
				return invalid()
			}
			_, err := m.read(p.Before)
			return err
		})
		return err
	})
	return n, recoveryError(err)
}

func recoveryError(err error) error {
	var e *result.Error
	if errors.As(err, &e) && e.Code == "RECOVERY_CONFLICT" {
		return &result.Error{Code: e.Code, Message: "运行时审计事务需要恢复或人工核对；请执行 mcp runtime-recover", Exit: e.Exit, Recovery: map[string]string{"command": "mcp runtime-recover"}}
	}
	return err
}
