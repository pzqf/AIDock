// Package mcpsession records explicitly requested, bounded project MCP handshakes.
// A successful handshake does not grant persistent execution or prove OS isolation.
package mcpsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/mcprelay"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/runtimecheck"
	"github.com/pzqf/AIDock/internal/workstore"
)

type Binding = pluginpermission.Binding
type Input struct {
	RuntimeInput   runtimecheck.Input `json:"runtime_input"`
	Entrypoint     string             `json:"entrypoint"`
	PolicySHA256   string             `json:"policy_sha256"`
	RuntimeAttempt string             `json:"runtime_attempt"`
}
type Event struct {
	Version   int             `json:"schema_version"`
	Sequence  int             `json:"sequence"`
	ID        string          `json:"id"`
	Attempt   string          `json:"attempt"`
	CreatedAt string          `json:"created_at"`
	Previous  string          `json:"previous"`
	Input     Input           `json:"input"`
	Phase     string          `json:"phase"`
	Code      string          `json:"code"`
	ToolCount int             `json:"tool_count"`
	Relay     *mcprelay.Event `json:"relay,omitempty"`
	Actor     string          `json:"actor"`
}
type Entry struct {
	Event   Event  `json:"event"`
	Status  string `json:"status"`
	Current bool   `json:"current"`
}
type State struct {
	Binding        Binding `json:"binding"`
	Entries        []Entry `json:"entries"`
	Candidates     []Input `json:"candidates"`
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

func New(local string, binding Binding) (*Manager, error) {
	if !filepath.IsAbs(local) || filepath.Clean(local) != local || !filepath.IsAbs(binding.SourceRoot) || filepath.Clean(binding.SourceRoot) != binding.SourceRoot || binding.Version != 1 || binding.Project == "" || binding.Scope == "" || binding.Copy == "" {
		return nil, result.Fail("MCP_SESSION_BINDING_INVALID", "会话握手需要明确项目副本与本机根", 2)
	}
	id := workstore.Hash(bytesOf(binding))
	m := &Manager{Store: &workstore.Store{Root: filepath.Join(local, "mcp-session-"+id), Local: filepath.Join(local, "mcp-session-transactions"), PlanKind: "mcp-session"}, operation: &workstore.Store{Root: filepath.Join(local, "mcp-session-operation-"+id), Local: filepath.Join(local, "mcp-session-transactions")}, Binding: binding}
	for _, p := range []string{m.Store.Root, m.Store.Local, m.operation.Root} {
		if err := workstore.CheckLinks(p); err != nil {
			return nil, err
		}
	}
	return m, nil
}
func validInput(i Input) bool {
	r := i.RuntimeInput
	u := r.Runtime
	return r.Name != "" && len(r.Name) <= 64 && uuid(r.AuthorizationID) && hash(r.AuthorizationSHA256) && hash(r.InputSHA256) && u.SourceType == "local" && u.Kind == "node" && workstore.SafeRelative(u.Executable) == nil && hash(u.SHA256) && workstore.SafeRelative(i.Entrypoint) == nil && hash(i.PolicySHA256) && uuid(i.RuntimeAttempt)
}
func From(r pluginpermission.Record, entry string, proof runtimecheck.Entry) (Input, error) {
	runtime, err := runtimecheck.From(r)
	if err != nil {
		return Input{}, err
	}
	if r.Input.ToolPolicy == nil || !r.ToolsConfirmed {
		return Input{}, result.Fail("MCP_SESSION_TOOL_POLICY_REQUIRED", "先独立审查并授权可信工具合同", 4)
	}
	if !proof.Current || proof.Status != "PASS" || proof.Event.Code != "RUNTIME_VERSION_MATCH" || string(bytesOf(proof.Event.Input)) != string(bytesOf(runtime)) {
		return Input{}, result.Fail("MCP_SESSION_RUNTIME_REQUIRED", "需要当前授权和锁定运行时的有效版本匹配记录", 4)
	}
	i := Input{RuntimeInput: runtime, Entrypoint: entry, PolicySHA256: r.Input.ToolPolicy.Digest(), RuntimeAttempt: proof.Event.Attempt}
	if !validInput(i) {
		return Input{}, invalid()
	}
	return i, nil
}
func validRelay(e *mcprelay.Event) bool {
	if e == nil {
		return false
	}
	if e.RequestSHA256 != "" && !hash(e.RequestSHA256) || e.ResultSHA256 != "" && !hash(e.ResultSHA256) {
		return false
	}
	switch e.Method {
	case "initialize", "tools/list":
		return e.Code == "FORWARD" && hash(e.RequestSHA256) && e.ResultSHA256 == "" || e.Code == "RETURN" && hash(e.ResultSHA256) && e.RequestSHA256 == ""
	case "relay/reject":
		return e.RequestSHA256 == "" && e.ResultSHA256 == "" && (e.Code == "AUTHORIZATION_STALE" || strings.HasPrefix(e.Code, "MCP_"))
	}
	return false
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
		return e.Code == "MCP_SESSION_INTENT" && e.ToolCount == 0 && e.Relay == nil
	case "RELAY":
		return e.Code == "MCP_SESSION_RELAY" && e.ToolCount == 0 && validRelay(e.Relay)
	case "COMPLETE":
		if e.Relay != nil {
			return false
		}
		switch e.Code {
		case "MCP_SESSION_MATCH":
			return e.ToolCount >= 0 && e.ToolCount <= 64
		case "MCP_SESSION_TOOL_MISMATCH", "MCP_SESSION_FAILED", "MCP_SESSION_TIMEOUT", "MCP_SESSION_CANCELED", "MCP_SESSION_ARTIFACT_CHANGED", "MCP_SESSION_START_FAILED", "MCP_SESSION_CLEANUP_FAILED", "MCP_SESSION_AUDIT_FAILED":
			return e.ToolCount == 0
		}
	}
	return false
}
func invalid() error {
	return result.Fail("MCP_SESSION_AUDIT_INVALID", "会话握手审计、绑定或事务锚点不符；保留记录并阻断执行", 5)
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
	progress := map[string]int{}
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
		if e.Phase == "INTENT" && exists || (e.Phase == "COMPLETE" || e.Phase == "RELAY") && (!exists || old.Phase == "COMPLETE" || string(bytesOf(old.Input)) != string(bytesOf(e.Input))) {
			return nil, invalid()
		}
		if e.Phase == "RELAY" {
			stage := progress[e.Attempt]
			switch {
			case e.Relay.Method == "relay/reject":
			case stage == 0 && e.Relay.Method == "initialize" && e.Relay.Code == "FORWARD":
				progress[e.Attempt] = 1
			case stage == 1 && e.Relay.Method == "initialize" && e.Relay.Code == "RETURN":
				progress[e.Attempt] = 2
			case (stage == 2 || stage == 3) && e.Relay.Method == "tools/list" && e.Relay.Code == "FORWARD":
				progress[e.Attempt] = 3
			case stage == 3 && e.Relay.Method == "tools/list" && e.Relay.Code == "RETURN":
				progress[e.Attempt] = 4
			default:
				return nil, invalid()
			}
		}
		if e.Phase == "COMPLETE" && e.Code == "MCP_SESSION_MATCH" && progress[e.Attempt] != 4 {
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
	out := State{Binding: m.Binding, Entries: []Entry{}, Boundary: "只在显式请求后启动已授权的自有可信本地 Node 插件，限时初始化与工具清单核对，不调用业务工具。Tier 0 非隔离，声明权限不构成 OS 限制；不是 AI 客户端生效验收。INTENT 无完成回执时不推断成功，恢复不重放进程。审计锚点只在本机，不能防御同用户重写全部历史。"}
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
				if e.Code == "MCP_SESSION_MATCH" {
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
func (m *Manager) append(ctx context.Context, input Input, attempt, phase, code string, tools int, relay *mcprelay.Event) (Event, error) {
	account, err := user.Current()
	if err != nil || account.Uid == "" || account.Username == "" {
		return Event{}, result.Fail("MCP_SESSION_ACTOR_UNAVAILABLE", "无法确定会话握手账户；未执行", 4)
	}
	e := Event{Version: 1, ID: workstore.ID(), Attempt: attempt, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Input: input, Phase: phase, Code: code, ToolCount: tools, Relay: relay}
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
			return result.Fail("MCP_SESSION_AUDIT_FULL", "会话握手审计已达到容量限制", 4)
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
		plan, err = m.Store.Preview("mcp-session-"+strings.ToLower(phase), before, after)
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
func (m *Manager) Probe(ctx context.Context, input Input, fn func(func(mcprelay.Event) error) (string, int)) (Event, error) {
	if !validInput(input) || fn == nil {
		return Event{}, invalid()
	}
	var out Event
	err := m.operation.Locked(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Reserve enough events for initialization, up to 65 list pages, a rejection and completion.
		if err := m.Store.Locked(func() error {
			if err := m.Store.Pending(); err != nil {
				return err
			}
			tree, err := m.Store.Capture(m.Store.Root)
			if err != nil {
				return err
			}
			events, err := m.read(tree)
			if err != nil {
				return err
			}
			if len(events) > 10000-72 {
				return result.Fail("MCP_SESSION_AUDIT_FULL", "会话审计剩余容量不足；未启动插件", 4)
			}
			return nil
		}); err != nil {
			return err
		}
		intent, err := m.append(ctx, input, workstore.ID(), "INTENT", "MCP_SESSION_INTENT", 0, nil)
		if err != nil {
			return err
		}
		out = intent
		code, tools := "MCP_SESSION_FAILED", 0
		func() {
			defer func() {
				if recover() != nil {
					code, tools = "MCP_SESSION_FAILED", 0
				}
			}()
			code, tools = fn(func(e mcprelay.Event) error {
				auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, err := m.append(auditCtx, input, intent.Attempt, "RELAY", "MCP_SESSION_RELAY", 0, &e)
				return err
			})
		}()
		if !validEvent(Event{Version: 1, Sequence: 1, ID: workstore.ID(), Attempt: intent.Attempt, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Input: input, Phase: "COMPLETE", Code: code, ToolCount: tools, Actor: intent.Actor}) {
			code, tools = "MCP_SESSION_FAILED", 0
		}
		finish, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err = m.append(finish, input, intent.Attempt, "COMPLETE", code, tools, nil)
		return err
	})
	return out, err
}
func (m *Manager) Recover() (int, error) {
	n := 0
	err := m.operation.Locked(func() error {
		var err error
		n, err = m.Store.RecoverGuarded(func(p workstore.Plan) error {
			if p.Action != "mcp-session-intent" && p.Action != "mcp-session-complete" && p.Action != "mcp-session-relay" {
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
		return &result.Error{Code: e.Code, Message: "会话审计事务需要恢复或人工核对；请执行 mcp session-recover", Exit: e.Exit, Recovery: map[string]string{"command": "mcp session-recover"}}
	}
	return err
}
