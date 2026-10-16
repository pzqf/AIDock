package mcprelay

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var readDescriptor = json.RawMessage(`{"name":"read","description":"fixed read","inputSchema":{"type":"object"}}`)
var writeDescriptor = json.RawMessage(`{"name":"save","annotations":{"readOnlyHint":true},"inputSchema":{"type":"object"}}`)

func testPolicy(events *[]Event, mu *sync.Mutex) Policy {
	rd, _ := DescriptorDigest(readDescriptor)
	wd, _ := DescriptorDigest(writeDescriptor)
	return Policy{Binding: Binding{Project: "A", Scope: "workspace", Copy: "one", Plugin: "fixed", DataLevel: "Public", Destination: "unknown-external-client"}, Tools: map[string]Tool{"read": {"read", rd}, "save": {"write", wd}}, Timeout: time.Second,
		Recheck: func(context.Context) error { return nil },
		Arguments: func(b Binding, _ string, raw json.RawMessage) error {
			var a struct {
				Project string `json:"project"`
			}
			if decode(raw, &a) != nil || a.Project != b.Project {
				return errors.New("private reason")
			}
			return nil
		},
		Result: func(b Binding, method string, raw json.RawMessage) error {
			if method == "tools/list" {
				return nil
			}
			var v struct{ Project, Level string }
			if decode(raw, &v) != nil || v.Project != b.Project || v.Level != "Public" {
				return errors.New("private result")
			}
			return nil
		},
		Audit: func(e Event) error { mu.Lock(); defer mu.Unlock(); *events = append(*events, e); return nil }}
}

type harness struct {
	client, upstream net.Conn
	cr, sr           *bufio.Reader
	done             chan error
	cancel           context.CancelFunc
}

func newHarness(t *testing.T, p Policy) *harness {
	t.Helper()
	client, relayClient := net.Pipe()
	server, relayServer := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	h := &harness{client, server, bufio.NewReader(client), bufio.NewReader(server), make(chan error, 1), cancel}
	go func() {
		h.done <- Serve(ctx, Endpoint{relayClient, relayClient}, Endpoint{relayServer, relayServer}, p)
	}()
	t.Cleanup(func() { cancel(); client.Close(); server.Close() })
	return h
}
func put(t *testing.T, c net.Conn, v any) {
	t.Helper()
	_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := send(c, v); err != nil {
		t.Fatal(err)
	}
}
func get(t *testing.T, c net.Conn, b *bufio.Reader) map[string]json.RawMessage {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	raw, err := b.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]json.RawMessage
	if err = decode(raw, &v); err != nil {
		t.Fatal(err, string(raw))
	}
	return v
}
func req(id any, method string, params any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
}
func (h *harness) exchange(t *testing.T, id any, method string, params, result any) map[string]json.RawMessage {
	t.Helper()
	put(t, h.client, req(id, method, params))
	forwarded := get(t, h.upstream, h.sr)
	put(t, h.upstream, map[string]any{"jsonrpc": "2.0", "id": forwarded["id"], "result": result})
	return get(t, h.client, h.cr)
}
func (h *harness) ready(t *testing.T) {
	t.Helper()
	v := h.exchange(t, 7, "initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"sampling": map[string]any{}}, "clientInfo": map[string]string{"secret": "ignored"}}, map[string]any{"protocolVersion": "2025-11-25", "instructions": "private hidden instruction", "capabilities": map[string]any{"resources": map[string]any{}}})
	if string(v["id"]) != "7" || strings.Contains(string(v["result"]), "private") || strings.Contains(string(v["result"]), "resources") {
		t.Fatal(v)
	}
	put(t, h.client, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	get(t, h.upstream, h.sr)
}
func (h *harness) list(t *testing.T) {
	t.Helper()
	h.exchange(t, "list", "tools/list", map[string]any{}, map[string]any{"tools": []json.RawMessage{readDescriptor, writeDescriptor}})
}
func assertCode(t *testing.T, v map[string]json.RawMessage, code string) {
	t.Helper()
	if !strings.Contains(string(v["error"]), `"message":"`+code+`"`) {
		t.Fatalf("want %s: %s", code, bytesOf(v))
	}
}

func TestRelayReadScopeAndWriteAuthorization(t *testing.T) {
	var events []Event
	var mu sync.Mutex
	h := newHarness(t, testPolicy(&events, &mu))
	h.ready(t)
	h.list(t)
	for _, tc := range []struct {
		name string
		args any
		code string
	}{{"save", map[string]string{"project": "A"}, "AUTHORIZATION_REQUIRED"}, {"unknown", map[string]string{"project": "A"}, "AUTHORIZATION_REQUIRED"}, {"read", map[string]string{"project": "B", "secret": "PRIVATE_ARGUMENT"}, "MCP_SCOPE_DENIED"}} {
		put(t, h.client, req("secret-id", "tools/call", map[string]any{"name": tc.name, "arguments": tc.args}))
		assertCode(t, get(t, h.client, h.cr), tc.code)
	}
	v := h.exchange(t, "read-id", "tools/call", map[string]any{"name": "read", "arguments": map[string]string{"project": "A"}}, map[string]any{"project": "A", "level": "Public", "content": []any{}})
	if string(v["id"]) != `"read-id"` || v["error"] != nil {
		t.Fatal(v)
	}
	v = h.exchange(t, 9, "tools/call", map[string]any{"name": "read", "arguments": map[string]string{"project": "A"}}, map[string]any{"project": "B", "level": "Internal", "content": []any{"PRIVATE_RESULT"}})
	assertCode(t, v, "MCP_RESULT_DENIED")
	mu.Lock()
	defer mu.Unlock()
	s := string(bytesOf(events))
	if strings.Contains(s, "PRIVATE") || strings.Contains(s, "secret-id") {
		t.Fatal(s)
	}
}

func TestRelayGuardRejectsRevocationBetweenRecheckAndResultWrite(t *testing.T) {
	var events []Event
	var mu sync.Mutex
	p := testPolicy(&events, &mu)
	var revoked atomic.Bool
	p.Guard = func(ctx context.Context, transfer func() error) error {
		if revoked.Load() {
			return fail("AUTHORIZATION_STALE")
		}
		return transfer()
	}
	h := newHarness(t, p)
	h.ready(t)
	h.list(t)
	put(t, h.client, req(41, "tools/call", map[string]any{"name": "read", "arguments": map[string]string{"project": "A"}}))
	a := get(t, h.upstream, h.sr)
	// Recheck remains successful. The final transfer gate must independently
	// reject this response and preserve the original request identity.
	revoked.Store(true)
	put(t, h.upstream, map[string]any{"jsonrpc": "2.0", "id": a["id"], "result": map[string]any{"project": "A", "level": "Public", "content": []any{"BLOCKED_BODY"}}})
	v := get(t, h.client, h.cr)
	assertCode(t, v, "AUTHORIZATION_STALE")
	if string(v["id"]) != "41" || v["result"] != nil {
		t.Fatal(v)
	}
	if err := <-h.done; err == nil {
		t.Fatal("revoked transfer did not stop the relay")
	}
}
func TestRelayPagedToolsAndTrustedDescriptor(t *testing.T) {
	var events []Event
	var mu sync.Mutex
	h := newHarness(t, testPolicy(&events, &mu))
	h.ready(t)
	put(t, h.client, req(2, "tools/list", map[string]any{}))
	a := get(t, h.upstream, h.sr)
	put(t, h.upstream, map[string]any{"jsonrpc": "2.0", "id": a["id"], "result": map[string]any{"tools": []json.RawMessage{readDescriptor}, "nextCursor": "private-cursor"}})
	b := get(t, h.upstream, h.sr)
	if string(a["id"]) == string(b["id"]) || !strings.Contains(string(b["params"]), "private-cursor") {
		t.Fatal(a, b)
	}
	put(t, h.upstream, map[string]any{"jsonrpc": "2.0", "id": b["id"], "result": map[string]any{"tools": []any{map[string]any{"name": "untrusted", "description": "PRIVATE_TOOL"}}}})
	v := get(t, h.client, h.cr)
	if strings.Contains(string(v["result"]), "PRIVATE") || strings.Contains(string(v["result"]), "cursor") {
		t.Fatal(v)
	}
	put(t, h.client, req(3, "tools/list", map[string]any{}))
	a = get(t, h.upstream, h.sr)
	put(t, h.upstream, map[string]any{"jsonrpc": "2.0", "id": a["id"], "result": map[string]any{"tools": []any{map[string]any{"name": "read", "description": "changed"}}}})
	assertCode(t, get(t, h.client, h.cr), "MCP_TOOL_CHANGED")
	if err := <-h.done; err == nil || err.Error() != "MCP_TOOL_CHANGED" {
		t.Fatal(err)
	}
}
func TestRelayDoesNotExposeServerRequestsOrErrors(t *testing.T) {
	var events []Event
	var mu sync.Mutex
	h := newHarness(t, testPolicy(&events, &mu))
	h.ready(t)
	h.list(t)
	put(t, h.upstream, req(7, "sampling/createMessage", map[string]string{"text": "PRIVATE_SAMPLING"}))
	assertCode(t, get(t, h.upstream, h.sr), "MCP_METHOD_NOT_ALLOWED")
	put(t, h.client, req(8, "tools/call", map[string]any{"name": "read", "arguments": map[string]string{"project": "A"}}))
	a := get(t, h.upstream, h.sr)
	put(t, h.upstream, map[string]any{"jsonrpc": "2.0", "id": a["id"], "error": map[string]any{"code": -1, "message": "PRIVATE_UPSTREAM", "data": "PRIVATE_DATA"}})
	v := get(t, h.client, h.cr)
	assertCode(t, v, "MCP_UPSTREAM_ERROR")
	if strings.Contains(string(bytesOf(v)), "PRIVATE") {
		t.Fatal(v)
	}
}
func TestRelayRevocationAndListChanges(t *testing.T) {
	var events []Event
	var mu sync.Mutex
	revoked := false
	p := testPolicy(&events, &mu)
	p.Recheck = func(context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		if revoked {
			return errors.New("revoked")
		}
		return nil
	}
	h := newHarness(t, p)
	h.ready(t)
	h.list(t)
	put(t, h.upstream, map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"})
	get(t, h.client, h.cr)
	put(t, h.client, req(1, "tools/call", map[string]any{"name": "read", "arguments": map[string]string{"project": "A"}}))
	assertCode(t, get(t, h.client, h.cr), "AUTHORIZATION_REQUIRED")
	put(t, h.client, req(2, "tools/list", map[string]any{}))
	a := get(t, h.upstream, h.sr)
	mu.Lock()
	revoked = true
	mu.Unlock()
	put(t, h.upstream, map[string]any{"jsonrpc": "2.0", "id": a["id"], "result": map[string]any{"tools": []json.RawMessage{readDescriptor}}})
	assertCode(t, get(t, h.client, h.cr), "AUTHORIZATION_STALE")
}
func TestRelayEOFTimeoutAndCancellation(t *testing.T) {
	for _, mode := range []string{"client-eof", "server-eof", "timeout", "cancel", "blocked-write"} {
		t.Run(mode, func(t *testing.T) {
			var events []Event
			var mu sync.Mutex
			p := testPolicy(&events, &mu)
			p.Timeout = 80 * time.Millisecond
			h := newHarness(t, p)
			switch mode {
			case "client-eof":
				h.client.Close()
			case "cancel":
				h.cancel()
			case "server-eof", "timeout":
				put(t, h.client, req(1, "initialize", map[string]string{"protocolVersion": "2025-11-25"}))
				get(t, h.upstream, h.sr)
				code := "MCP_REQUEST_TIMEOUT"
				if mode == "server-eof" {
					h.upstream.Close()
					code = "MCP_UPSTREAM_CLOSED"
				}
				assertCode(t, get(t, h.client, h.cr), code)
			case "blocked-write":
				put(t, h.client, req(1, "initialize", map[string]string{"protocolVersion": "2025-11-25"}))
				h.cancel()
			}
			select {
			case err := <-h.done:
				if mode == "client-eof" && err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("relay did not release transports")
			}
		})
	}
}
func TestRelayCancellationLateResponseAndDuplicateID(t *testing.T) {
	var events []Event
	var mu sync.Mutex
	h := newHarness(t, testPolicy(&events, &mu))
	h.ready(t)
	h.list(t)
	put(t, h.client, req(1, "tools/call", map[string]any{"name": "read", "arguments": map[string]string{"project": "A"}}))
	a := get(t, h.upstream, h.sr)
	put(t, h.client, map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": 1, "reason": "PRIVATE_REASON"}})
	b := get(t, h.upstream, h.sr)
	if strings.Contains(string(b["params"]), "PRIVATE") {
		t.Fatal(b)
	}
	put(t, h.upstream, map[string]any{"jsonrpc": "2.0", "id": a["id"], "result": map[string]string{"private": "ignored"}})
	v := h.exchange(t, 1, "tools/call", map[string]any{"name": "read", "arguments": map[string]string{"project": "A"}}, map[string]any{"project": "A", "level": "Public", "content": []any{}})
	if v["error"] != nil {
		t.Fatal(v)
	}
	put(t, h.client, req(2, "tools/list", map[string]any{}))
	get(t, h.upstream, h.sr)
	put(t, h.client, req(2, "ping", map[string]any{}))
	assertCode(t, get(t, h.client, h.cr), "MCP_DUPLICATE_REQUEST")
	if err := <-h.done; err == nil {
		t.Fatal("duplicate id retained")
	}
}
func TestProtocolRejectsAmbiguity(t *testing.T) {
	for _, raw := range []string{`[]`, `{"jsonrpc":"2.0","id":null,"method":"ping"}`, `{"jsonrpc":"2.0","id":1,"id":2,"method":"ping"}`, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"project":"A","project":"B"}}`, `{"jsonrpc":"2.0","id":1,"result":{},"error":{}}`, `{"jsonrpc":"2.0","id":1.5,"method":"ping"}`, `{"jsonrpc":"2.0","id":9007199254740992,"method":"ping"}`, strings.Repeat(" ", MaxFrame+1)} {
		if _, err := parse([]byte(raw)); err == nil {
			t.Fatalf("accepted %.100s", raw)
		}
	}
	if a, _ := idKey(json.RawMessage("0")); func() bool { b, _ := idKey(json.RawMessage("-0")); return a != b }() {
		t.Fatal("numeric identity alias")
	}
}
func TestRelayRequiresTrustedGuardsAndAudit(t *testing.T) {
	var events []Event
	var mu sync.Mutex
	p := testPolicy(&events, &mu)
	p.Arguments = nil
	h := newHarness(t, p)
	if err := <-h.done; err == nil || err.Error() != "MCP_POLICY_REQUIRED" {
		t.Fatal(err)
	}
	p = testPolicy(&events, &mu)
	p.Audit = func(Event) error { return io.ErrClosedPipe }
	h = newHarness(t, p)
	put(t, h.client, req(1, "initialize", map[string]string{"protocolVersion": "2025-11-25"}))
	if err := <-h.done; err == nil || err.Error() != "MCP_AUDIT_UNAVAILABLE" {
		t.Fatal(err)
	}
}

func TestRelayConcurrentResponsesAndChangedInFlightTool(t *testing.T) {
	var events []Event
	var mu sync.Mutex
	h := newHarness(t, testPolicy(&events, &mu))
	h.ready(t)
	h.list(t)
	call := func(id any) {
		put(t, h.client, req(id, "tools/call", map[string]any{"name": "read", "arguments": map[string]string{"project": "A"}}))
	}
	call("one")
	a := get(t, h.upstream, h.sr)
	call(2)
	b := get(t, h.upstream, h.sr)
	result := map[string]any{"project": "A", "level": "Public", "content": []any{}}
	put(t, h.upstream, map[string]any{"jsonrpc": "2.0", "id": b["id"], "result": result})
	if v := get(t, h.client, h.cr); string(v["id"]) != "2" {
		t.Fatal(v)
	}
	put(t, h.upstream, map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"})
	get(t, h.client, h.cr)
	put(t, h.upstream, map[string]any{"jsonrpc": "2.0", "id": a["id"], "result": result})
	v := get(t, h.client, h.cr)
	assertCode(t, v, "MCP_TOOL_LIST_STALE")
	if string(v["id"]) != `"one"` {
		t.Fatal(v)
	}
}

func TestRelayWriteBackpressureHasDeadline(t *testing.T) {
	var events []Event
	var mu sync.Mutex
	p := testPolicy(&events, &mu)
	p.Timeout = 80 * time.Millisecond
	h := newHarness(t, p)
	put(t, h.client, req(1, "initialize", map[string]string{"protocolVersion": "2025-11-25"}))
	// The server never reads its pipe. A blocked transport must not defeat the
	// request deadline or strand the session forever.
	assertCode(t, get(t, h.client, h.cr), "MCP_TRANSPORT_TIMEOUT")
	select {
	case err := <-h.done:
		if err == nil {
			t.Fatal("backpressure succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked transport retained")
	}
}

func TestCheckRequestCannotEscalateStaticRead(t *testing.T) {
	for _, raw := range []string{`{"probe":false,"probe":true}`, `{"Probe":true}`, `{"probe":null}`, `{"probe":"true"}`, `{"probe":true,"command":"anything"}`, `null`, `[]`} {
		if _, err := ParseCheckRequest(json.RawMessage(raw)); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	for _, raw := range []string{`{}`, `{"probe":false}`} {
		if probe, err := ParseCheckRequest(json.RawMessage(raw)); err != nil || probe {
			t.Fatal(probe, err)
		}
	}
	if r, err := Check(context.Background(), false); err != nil || r.Status != "NOT_RUN" || r.Probe || len(r.Events) != 0 || r.ExecutionReady {
		t.Fatal(r, err)
	}
}
