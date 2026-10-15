package mcprelay

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"
)

const probeBoundary = "本次只检查 AIDock 内置固定 MCP 夹具与 stdio 中继，不读取或启动项目插件，不写项目文件。不构成实际插件授权、OS 隔离、凭据、受管启动或 L3/L4 验收。客户端 EOF、超时或取消后等待固定测试子进程退出。"

var probeRead = json.RawMessage(`{"name":"fixture_read","description":"AIDock fixed public fixture","inputSchema":{"type":"object"}}`)
var probeWrite = json.RawMessage(`{"name":"fixture_write","annotations":{"readOnlyHint":true},"inputSchema":{"type":"object"}}`)

type Finding struct {
	Code   string `json:"code"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}
type Report struct {
	Version        int       `json:"schema_version"`
	Created        string    `json:"created"`
	Status         string    `json:"status"`
	Probe          bool      `json:"probe"`
	Findings       []Finding `json:"findings"`
	Events         []Event   `json:"events"`
	Boundary       string    `json:"boundary"`
	ExecutionReady bool      `json:"execution_ready"`
}

// ParseCheckRequest keeps the explicit probe choice unambiguous. In particular,
// duplicate or case-folded fields must not turn a static read into execution.
func ParseCheckRequest(raw json.RawMessage) (bool, error) {
	var fields map[string]json.RawMessage
	if decode(raw, &fields) != nil || fields == nil {
		return false, fail("INVALID_INPUT")
	}
	probe := false
	for key, value := range fields {
		if key != "probe" || string(value) == "null" || json.Unmarshal(value, &probe) != nil {
			return false, fail("INVALID_INPUT")
		}
	}
	return probe, nil
}

func fixturePolicy(events *[]Event, mu *sync.Mutex) Policy {
	rd, _ := DescriptorDigest(probeRead)
	wd, _ := DescriptorDigest(probeWrite)
	b := Binding{Project: "fixture-project", Scope: "fixture-scope", Copy: "fixture-copy", Plugin: "aidock-fixed-fixture", DataLevel: "Public", Destination: "unknown-external-client"}
	return Policy{Binding: b, Tools: map[string]Tool{"fixture_read": {"read", rd}, "fixture_write": {"write", wd}}, Timeout: 500 * time.Millisecond,
		Recheck: func(context.Context) error { return nil },
		Arguments: func(b Binding, _ string, raw json.RawMessage) error {
			var p struct{ Project, Scope, Copy string }
			if decode(raw, &p) != nil || p.Project != b.Project || p.Scope != b.Scope || p.Copy != b.Copy {
				return fail("MCP_SCOPE_DENIED")
			}
			return nil
		},
		Result: func(b Binding, method string, raw json.RawMessage) error {
			if method == "tools/list" {
				return nil
			}
			var p struct{ Project, Scope, Copy, Level string }
			if decode(raw, &p) != nil || p.Project != b.Project || p.Scope != b.Scope || p.Copy != b.Copy || p.Level != "Public" {
				return fail("MCP_RESULT_DENIED")
			}
			return nil
		},
		Audit: func(e Event) error { mu.Lock(); defer mu.Unlock(); *events = append(*events, e); return nil }}
}

// Helper can only run this fixed, file/network-free protocol fixture. It accepts
// no command, runtime, path, environment or plugin selection from the caller.
func Helper(args []string, in io.Reader, out io.Writer) (bool, int) {
	if len(args) == 0 || args[0] != "__aidock_mcp_relay_probe" {
		return false, 0
	}
	if len(args) != 2 || (args[1] != "normal" && args[1] != "crash" && args[1] != "hang") {
		return true, 91
	}
	s := bufio.NewScanner(in)
	s.Buffer(make([]byte, 4096), MaxFrame+1)
	calls := 0
	for s.Scan() {
		f, err := parse(s.Bytes())
		if err != nil {
			return true, 92
		}
		if f.ID == nil {
			continue
		}
		if args[1] == "crash" {
			return true, 77
		}
		if args[1] == "hang" {
			continue
		}
		var result any
		switch f.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "fixed fixture", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []json.RawMessage{probeRead, probeWrite, json.RawMessage(`{"name":"untrusted_fixture","inputSchema":{"type":"object"}}`)}}
		case "tools/call":
			calls++
			var p struct{ Arguments struct{ Case string } }
			_ = decode(f.Params, &p)
			level := "Public"
			content := "fixed-public-result"
			if p.Arguments.Case == "private" {
				level = "Internal"
				content = "FIXTURE_PRIVATE_RELAY_RESULT"
			}
			result = map[string]any{"project": "fixture-project", "scope": "fixture-scope", "copy": "fixture-copy", "level": level, "calls": calls, "content": []any{map[string]string{"type": "text", "text": content}}}
		default:
			return true, 93
		}
		if send(out, map[string]any{"jsonrpc": "2.0", "id": f.ID, "result": result}) != nil {
			return true, 94
		}
	}
	if s.Err() != nil {
		return true, 95
	}
	return true, 0
}

type fixtureSession struct {
	client  net.Conn
	read    *bufio.Reader
	done    chan error
	cancel  context.CancelFunc
	command *exec.Cmd
}

func startFixture(ctx context.Context, mode string, p Policy) (*fixtureSession, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fail("MCP_FIXTURE_UNAVAILABLE")
	}
	ctx, cancel := context.WithCancel(ctx)
	c := exec.CommandContext(ctx, exe, "__aidock_mcp_relay_probe", mode)
	c.Env = []string{"GOMAXPROCS=2"}
	if root := os.Getenv("SystemRoot"); root != "" {
		c.Env = append(c.Env, "SystemRoot="+root)
	}
	c.Stderr = io.Discard
	c.WaitDelay = time.Second
	in, err := c.StdinPipe()
	if err != nil {
		cancel()
		return nil, fail("MCP_FIXTURE_UNAVAILABLE")
	}
	out, err := c.StdoutPipe()
	if err != nil {
		in.Close()
		cancel()
		return nil, fail("MCP_FIXTURE_UNAVAILABLE")
	}
	if err = c.Start(); err != nil {
		in.Close()
		out.Close()
		cancel()
		return nil, fail("MCP_FIXTURE_UNAVAILABLE")
	}
	client, relaySide := net.Pipe()
	s := &fixtureSession{client, bufio.NewReader(client), make(chan error, 1), cancel, c}
	go func() { s.done <- Serve(ctx, Endpoint{relaySide, relaySide}, Endpoint{out, in}, p) }()
	return s, nil
}
func (s *fixtureSession) request(id int, method string, params any) (map[string]json.RawMessage, error) {
	_ = s.client.SetDeadline(time.Now().Add(2 * time.Second))
	if err := send(s.client, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, fail("MCP_FIXTURE_IO")
	}
	raw, err := s.read.ReadBytes('\n')
	if err != nil {
		return nil, fail("MCP_FIXTURE_IO")
	}
	var v map[string]json.RawMessage
	if decode(raw, &v) != nil || string(v["id"]) != string(bytesOf(id)) {
		return nil, fail("MCP_FIXTURE_PROTOCOL")
	}
	return v, nil
}
func (s *fixtureSession) close() (error, bool) {
	s.client.Close()
	var relayErr error
	select {
	case relayErr = <-s.done:
	case <-time.After(2 * time.Second):
		s.cancel()
		relayErr = fail("MCP_FIXTURE_CLEANUP")
	}
	// Wait only after Serve has released StdoutPipe, so Wait cannot truncate a
	// response while the relay is still reading it.
	wait := make(chan error, 1)
	go func() { wait <- s.command.Wait() }()
	select {
	case <-wait:
		s.cancel()
		return relayErr, true
	case <-time.After(250 * time.Millisecond):
		s.cancel()
	}
	select {
	case <-wait:
		return relayErr, true
	case <-time.After(2 * time.Second):
		return relayErr, false
	}
}

// Check executes only the current AIDock executable's fixed Helper. The public
// API deliberately has no executable path, environment, or arbitrary transport.
func Check(ctx context.Context, probe bool) (Report, error) {
	r := Report{Version: 1, Created: time.Now().UTC().Format(time.RFC3339Nano), Status: "NOT_RUN", Probe: probe, Findings: []Finding{}, Events: []Event{}, Boundary: probeBoundary}
	if !probe {
		return r, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	r.Status = "PASS"
	var events []Event
	var mu sync.Mutex
	add := func(code string, ok bool, detail string) {
		status := "PASS"
		if !ok {
			status = "FAIL"
			r.Status = "FAIL"
		}
		r.Findings = append(r.Findings, Finding{code, status, detail})
	}
	args := map[string]string{"project": "fixture-project", "scope": "fixture-scope", "copy": "fixture-copy"}
	s, err := startFixture(ctx, "normal", fixturePolicy(&events, &mu))
	if err != nil {
		return r, err
	}
	normalErr := func() error {
		v, err := s.request(1, "initialize", map[string]string{"protocolVersion": "2025-11-25"})
		if err != nil {
			return err
		}
		add("RELAY_INITIALIZE", v["error"] == nil, "固定子进程完成 JSON-RPC 初始化并保留客户端请求身份")
		if err = send(s.client, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
			return err
		}
		v, err = s.request(2, "tools/list", map[string]any{})
		if err != nil {
			return err
		}
		var list struct{ Tools []json.RawMessage }
		_ = decode(v["result"], &list)
		add("RELAY_TRUSTED_TOOL_LIST", len(list.Tools) == 2, "只返回与可信声明摘要一致的工具；未知工具不进入清单")
		v, err = s.request(3, "tools/call", map[string]any{"name": "fixture_read", "arguments": args})
		if err != nil {
			return err
		}
		add("RELAY_BOUND_PUBLIC_READ", v["error"] == nil, "固定项目、作用域、副本和 Public 结果通过读取中继")
		for i, c := range []struct {
			code, name string
			arguments  map[string]string
			want       string
		}{{"RELAY_WRITE_DENIED", "fixture_write", args, "AUTHORIZATION_REQUIRED"}, {"RELAY_UNKNOWN_DENIED", "untrusted_fixture", args, "AUTHORIZATION_REQUIRED"}, {"RELAY_PROJECT_DENIED", "fixture_read", map[string]string{"project": "other-project", "scope": "fixture-scope", "copy": "fixture-copy"}, "MCP_SCOPE_DENIED"}, {"RELAY_COPY_DENIED", "fixture_read", map[string]string{"project": "fixture-project", "scope": "fixture-scope", "copy": "other-copy"}, "MCP_SCOPE_DENIED"}, {"RELAY_PRIVATE_RESULT_DENIED", "fixture_read", map[string]string{"project": "fixture-project", "scope": "fixture-scope", "copy": "fixture-copy", "case": "private"}, "MCP_RESULT_DENIED"}} {
			v, err = s.request(10+i, "tools/call", map[string]any{"name": c.name, "arguments": c.arguments})
			if err != nil {
				return err
			}
			var e struct{ Message string }
			_ = decode(v["error"], &e)
			detail := map[string]string{"RELAY_WRITE_DENIED": "未批准写调用被拒绝，服务器自报只读不授予权限", "RELAY_UNKNOWN_DENIED": "未登记工具的调用被拒绝", "RELAY_PROJECT_DENIED": "其它项目参数被拒绝", "RELAY_COPY_DENIED": "其它副本参数被拒绝", "RELAY_PRIVATE_RESULT_DENIED": "Internal 结果在交付客户端前被拦截"}[c.code]
			add(c.code, e.Message == c.want, detail)
		}
		v, err = s.request(20, "tools/call", map[string]any{"name": "fixture_read", "arguments": args})
		if err != nil {
			return err
		}
		var result struct{ Calls int }
		_ = decode(v["result"], &result)
		add("RELAY_DENIED_NOT_FORWARDED", result.Calls == 3, "固定服务器计数证明写、未知、跨项目和跨副本调用没有到达服务器")
		return nil
	}()
	relayErr, clean := s.close()
	add("RELAY_CLIENT_EOF_CLEANUP", clean && relayErr == nil, "客户端 EOF 后中继关闭管道并等待固定子进程退出")
	if normalErr != nil {
		add("RELAY_FIXTURE_PROTOCOL", false, "固定协议交互未通过；不提升实际项目运行资格")
	}
	for _, mode := range []string{"crash", "hang"} {
		s, err = startFixture(ctx, mode, fixturePolicy(&events, &mu))
		if err != nil {
			return r, err
		}
		v, requestErr := s.request(1, "initialize", map[string]string{"protocolVersion": "2025-11-25"})
		var e struct{ Message string }
		_ = decode(v["error"], &e)
		want, code := "MCP_UPSTREAM_CLOSED", "RELAY_CRASH_STRUCTURED"
		if mode == "hang" {
			want, code = "MCP_REQUEST_TIMEOUT", "RELAY_TIMEOUT_CLEANUP"
		}
		_, clean = s.close()
		detail := "固定服务器崩溃时返回结构化错误，并等待子进程退出"
		if mode == "hang" {
			detail = "固定服务器未响应时按期返回超时，并等待子进程退出"
		}
		add(code, requestErr == nil && e.Message == want && clean, detail)
	}
	s, err = startFixture(ctx, "hang", fixturePolicy(&events, &mu))
	if err != nil {
		return r, err
	}
	s.cancel()
	relayErr, clean = s.close()
	add("RELAY_CANCEL_CLEANUP", clean && errors.Is(relayErr, context.Canceled), "会话取消关闭管道并等待固定子进程退出")
	mu.Lock()
	r.Events = append(r.Events, events...)
	mu.Unlock()
	return r, nil
}
