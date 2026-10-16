package mcprelay

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"time"
)

type Binding struct {
	Project     string `json:"project"`
	Scope       string `json:"scope"`
	Copy        string `json:"copy"`
	Plugin      string `json:"plugin"`
	DataLevel   string `json:"data_level"`
	Destination string `json:"destination"`
}

type Tool struct{ Access, DescriptorSHA256 string }
type Event struct {
	RequestID     string `json:"request_id,omitempty"`
	Tool          string `json:"tool,omitempty"`
	Method        string `json:"method"`
	Code          string `json:"code"`
	RequestSHA256 string `json:"request_sha256,omitempty"`
	ResultSHA256  string `json:"result_sha256,omitempty"`
}
type Policy struct {
	Binding Binding
	Tools   map[string]Tool
	// Guards come from the trusted launcher, never from client parameters or tool
	// annotations. No guard means no session. Recheck also covers revocation.
	Recheck func(context.Context) error
	// Guard keeps current authority locked through one bounded write, never while waiting for a response.
	Guard            func(context.Context, func() error) error
	StopOnRevocation bool
	Arguments        func(Binding, string, json.RawMessage) error
	Result           func(Binding, string, json.RawMessage) error
	Audit            func(Event) error
	Timeout          time.Duration
}
type Endpoint struct {
	Reader io.ReadCloser
	Writer io.WriteCloser
}

func (e Endpoint) close() { _ = e.Reader.Close(); _ = e.Writer.Close() }

type incoming struct {
	fromClient bool
	f          frame
	err        error
}
type pending struct {
	original                   json.RawMessage
	key, method, tool, version string
	deadline                   time.Time
	tools                      map[string]json.RawMessage
	cursors                    map[string]bool
	epoch                      uint64
}
type relay struct {
	client, server Endpoint
	p              Policy
	ctx            context.Context
	seq            uint64
	state          string
	pending        map[string]*pending
	keys           map[string]bool
	listed         map[string]bool
	listing        bool
	epoch          uint64
	cancelled      map[string]bool
}

// Serve owns and closes both endpoints on EOF, cancellation, protocol failure or
// timeout. Its caller separately owns process/broker termination and waiting.
func Serve(ctx context.Context, client, server Endpoint, p Policy) error {
	if client.Reader == nil || client.Writer == nil || server.Reader == nil || server.Writer == nil {
		return fail("MCP_ENDPOINT_REQUIRED")
	}
	defer client.close()
	defer server.close()
	if p.Binding.Project == "" || p.Binding.Scope == "" || p.Binding.Plugin == "" || p.Binding.DataLevel != "Public" || p.Binding.Destination != "unknown-external-client" || p.Recheck == nil || p.Arguments == nil || p.Result == nil || p.Audit == nil {
		return fail("MCP_POLICY_REQUIRED")
	}
	// Snapshot caller-owned maps; later mutation must not raise session authority.
	tools := map[string]Tool{}
	for name, t := range p.Tools {
		if name == "" || len(name) > 128 || (t.Access != "read" && t.Access != "write") || len(t.DescriptorSHA256) != 64 {
			return fail("MCP_POLICY_REQUIRED")
		}
		tools[name] = t
	}
	p.Tools = tools
	if p.Timeout <= 0 {
		p.Timeout = 30 * time.Second
	}
	if p.Timeout > time.Minute {
		return fail("MCP_POLICY_REQUIRED")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r := &relay{client: client, server: server, p: p, ctx: ctx, state: "new", pending: map[string]*pending{}, keys: map[string]bool{}, listed: map[string]bool{}, cancelled: map[string]bool{}}
	input := make(chan incoming, 2)
	read := func(endpoint Endpoint, fromClient bool) {
		s := bufio.NewScanner(endpoint.Reader)
		s.Buffer(make([]byte, 4096), MaxFrame+1)
		for s.Scan() {
			f, err := parse(s.Bytes())
			select {
			case input <- incoming{fromClient, f, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
		err := s.Err()
		if err == nil {
			err = io.EOF
		}
		select {
		case input <- incoming{fromClient: fromClient, err: err}:
		case <-ctx.Done():
		}
	}
	go read(client, true)
	go read(server, false)
	// Closing endpoints also releases a blocked transport write, not just reads.
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-ctx.Done():
			client.close()
			server.close()
		case <-watchDone:
		}
	}()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			for _, p := range r.pending {
				if !time.Now().Before(p.deadline) {
					_ = r.reject(p.original, "MCP_REQUEST_TIMEOUT")
					return fail("MCP_REQUEST_TIMEOUT")
				}
			}
		case in := <-input:
			if err := ctx.Err(); err != nil {
				return err
			}
			if in.err != nil {
				if in.fromClient && errors.Is(in.err, io.EOF) {
					return nil
				}
				code := "MCP_UPSTREAM_CLOSED"
				if !errors.Is(in.err, io.EOF) {
					code = "MCP_PROTOCOL_INVALID"
				}
				if in.fromClient {
					_ = r.reject(nil, code)
				} else {
					for _, p := range r.pending {
						_ = r.reject(p.original, code)
					}
				}
				return fail(code)
			}
			var err error
			if in.fromClient {
				err = r.fromClient(in.f)
			} else {
				err = r.fromServer(in.f)
			}
			if err != nil {
				code := "MCP_SESSION_FAILED"
				var classified *Error
				if errors.As(err, &classified) {
					code = classified.Code
				}
				for _, p := range r.pending {
					_ = r.reject(p.original, code)
				}
				return err
			}
		}
	}
}

func send(w io.Writer, value any) error { return json.NewEncoder(w).Encode(value) }
func (r *relay) write(w io.WriteCloser, value any) error {
	done := make(chan error, 1)
	go func() { done <- send(w, value) }()
	timer := time.NewTimer(r.p.Timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-r.ctx.Done():
		_ = w.Close()
		return r.ctx.Err()
	case <-timer.C:
		_ = w.Close()
		return fail("MCP_TRANSPORT_TIMEOUT")
	}
}

func (r *relay) reject(id json.RawMessage, code string) error {
	if id == nil {
		id = json.RawMessage("null")
	}
	if err := r.p.Audit(Event{Method: "relay/reject", Code: code}); err != nil {
		return fail("MCP_AUDIT_UNAVAILABLE")
	}
	return r.write(r.client.Writer, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32001, "message": code, "data": map[string]string{"code": code}}})
}
func (r *relay) guarded(fn func() error) error {
	if r.p.Guard != nil {
		return r.p.Guard(r.ctx, fn)
	}
	return fn()
}

func (r *relay) stale(id json.RawMessage) error {
	if err := r.reject(id, "AUTHORIZATION_STALE"); err != nil {
		return err
	}
	if r.p.StopOnRevocation {
		return fail("AUTHORIZATION_STALE")
	}
	return nil
}

func (r *relay) request(p *pending, params any) error {
	r.seq++
	id := "aidock-" + strconv.FormatUint(r.seq, 10)
	r.pending[id] = p
	return r.guarded(func() error {
		if err := r.p.Audit(Event{Method: p.method, Code: "FORWARD", RequestID: id, Tool: p.tool, RequestSHA256: digest(bytesOf(params))}); err != nil {
			return fail("MCP_AUDIT_UNAVAILABLE")
		}
		return r.write(r.server.Writer, map[string]any{"jsonrpc": "2.0", "id": id, "method": p.method, "params": params})
	})
}
func (r *relay) fromClient(f frame) error {
	if f.Method == "" {
		return fail("MCP_CLIENT_RESPONSE_UNEXPECTED")
	}
	if f.ID == nil {
		switch f.Method {
		case "notifications/initialized":
			if r.state != "initialized" {
				return fail("MCP_NOT_INITIALIZED")
			}
			r.state = "ready"
			return r.guarded(func() error { return r.write(r.server.Writer, map[string]any{"jsonrpc": "2.0", "method": f.Method}) })
		case "notifications/cancelled":
			var p struct {
				ID json.RawMessage `json:"requestId"`
			}
			if decode(f.Params, &p) != nil {
				return fail("MCP_FRAME_INVALID")
			}
			key, err := idKey(p.ID)
			if err != nil {
				return err
			}
			for id, wait := range r.pending {
				if wait.key == key {
					if len(r.cancelled) >= 256 {
						return fail("MCP_REQUEST_LIMIT")
					}
					r.cancelled[id] = true
					delete(r.pending, id)
					delete(r.keys, key)
					if wait.method == "tools/list" {
						r.listing = false
						r.listed = map[string]bool{}
					}
					if wait.method == "initialize" {
						return fail("MCP_INITIALIZATION_CANCELLED")
					}
					return r.guarded(func() error {
						return r.write(r.server.Writer, map[string]any{"jsonrpc": "2.0", "method": f.Method, "params": map[string]any{"requestId": id}})
					})
				}
			}
			return nil
		default:
			return nil // Never forward client logs, progress or unsolicited data.
		}
	}
	key, _ := idKey(f.ID)
	if r.keys[key] {
		return fail("MCP_DUPLICATE_REQUEST")
	}
	if len(r.pending) >= 64 {
		return r.reject(f.ID, "MCP_REQUEST_LIMIT")
	}
	p := &pending{original: f.ID, key: key, method: f.Method, deadline: time.Now().Add(r.p.Timeout), epoch: r.epoch}
	if err := r.p.Recheck(r.ctx); err != nil {
		return r.stale(f.ID)
	}
	var params map[string]json.RawMessage
	if f.Params != nil {
		if decode(f.Params, &params) != nil || params == nil {
			return r.reject(f.ID, "MCP_PARAMS_INVALID")
		}
	}
	// Native clients attach standard request metadata (for example progress
	// tokens). Validate its shape, then drop it at this privacy boundary.
	if raw, ok := params["_meta"]; ok {
		var meta map[string]json.RawMessage
		if decode(raw, &meta) != nil || meta == nil {
			return r.reject(f.ID, "MCP_PARAMS_INVALID")
		}
		delete(params, "_meta")
	}
	var forwarded any = map[string]any{}
	switch f.Method {
	case "initialize":
		if r.state != "new" {
			return r.reject(f.ID, "MCP_ALREADY_INITIALIZED")
		}
		if json.Unmarshal(params["protocolVersion"], &p.version) != nil || p.version == "" || len(p.version) > 64 {
			return r.reject(f.ID, "MCP_VERSION_UNSUPPORTED")
		}
		// A client can offer a newer protocol. Negotiate an implemented
		// version without forwarding any behavior from an unknown protocol.
		if !supported(p.version) {
			p.version = "2025-11-25"
		}
		forwarded = map[string]any{"protocolVersion": p.version, "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "AIDock managed relay", "version": "1"}}
		r.state = "initializing"
	case "ping":
		return r.write(r.client.Writer, map[string]any{"jsonrpc": "2.0", "id": f.ID, "result": map[string]any{}})
	case "tools/list":
		if r.state != "ready" {
			return r.reject(f.ID, "MCP_NOT_INITIALIZED")
		}
		if len(params) > 0 {
			return r.reject(f.ID, "MCP_PARAMS_INVALID")
		}
		if r.listing {
			return r.reject(f.ID, "MCP_LIST_IN_PROGRESS")
		}
		r.listing = true
		r.listed = map[string]bool{}
		p.tools = map[string]json.RawMessage{}
		p.cursors = map[string]bool{}
		p.epoch = r.epoch
	case "tools/call":
		if r.state != "ready" {
			return r.reject(f.ID, "MCP_NOT_INITIALIZED")
		}
		if json.Unmarshal(params["name"], &p.tool) != nil {
			return r.reject(f.ID, "MCP_PARAMS_INVALID")
		}
		for k := range params {
			if k != "name" && k != "arguments" {
				return r.reject(f.ID, "MCP_PARAMS_INVALID")
			}
		}
		t, known := r.p.Tools[p.tool]
		if !known || !r.listed[p.tool] || t.Access != "read" {
			return r.reject(f.ID, "AUTHORIZATION_REQUIRED")
		}
		args := params["arguments"]
		if args == nil {
			args = json.RawMessage("{}")
		}
		var object map[string]json.RawMessage
		if decode(args, &object) != nil || object == nil {
			return r.reject(f.ID, "MCP_PARAMS_INVALID")
		}
		if err := r.p.Arguments(r.p.Binding, p.tool, args); err != nil {
			return r.reject(f.ID, "MCP_SCOPE_DENIED")
		}
		forwarded = map[string]any{"name": p.tool, "arguments": args}
	default:
		return r.reject(f.ID, "MCP_METHOD_NOT_ALLOWED")
	}
	r.keys[key] = true
	return r.request(p, forwarded)
}

func (r *relay) fromServer(f frame) error {
	if f.Method != "" {
		if f.ID != nil {
			// A server cannot solicit roots, model sampling or UI approval through an
			// untrusted client. Its ping can be answered without disclosing context.
			if f.Method == "ping" {
				return r.write(r.server.Writer, map[string]any{"jsonrpc": "2.0", "id": f.ID, "result": map[string]any{}})
			}
			return r.write(r.server.Writer, map[string]any{"jsonrpc": "2.0", "id": f.ID, "error": map[string]any{"code": -32601, "message": "MCP_METHOD_NOT_ALLOWED"}})
		}
		if f.Method == "notifications/tools/list_changed" {
			r.epoch++
			r.listed = map[string]bool{}
			return r.guarded(func() error { return r.write(r.client.Writer, map[string]any{"jsonrpc": "2.0", "method": f.Method}) })
		}
		return nil // Drop unsolicited log/progress/resource notifications.
	}
	var id string
	if json.Unmarshal(f.ID, &id) != nil {
		return fail("MCP_RESPONSE_UNEXPECTED")
	}
	if r.cancelled[id] {
		delete(r.cancelled, id)
		return nil
	}
	p, ok := r.pending[id]
	if !ok {
		return fail("MCP_RESPONSE_UNEXPECTED")
	}
	finish := func() {
		delete(r.pending, id)
		delete(r.keys, p.key)
		if p.method == "tools/list" {
			r.listing = false
		}
	}
	if err := r.p.Recheck(r.ctx); err != nil {
		finish()
		return r.stale(p.original)
	}
	if f.Error != nil {
		finish()
		if p.method == "initialize" {
			r.state = "failed"
		}
		return r.reject(p.original, "MCP_UPSTREAM_ERROR")
	}
	var result any
	switch p.method {
	case "initialize":
		var v struct {
			Protocol string `json:"protocolVersion"`
		}
		if decode(f.Result, &v) != nil || !supported(v.Protocol) {
			return fail("MCP_VERSION_UNSUPPORTED")
		}
		result = map[string]any{"protocolVersion": v.Protocol, "capabilities": map[string]any{"tools": map[string]bool{"listChanged": true}}, "serverInfo": map[string]string{"name": "AIDock managed relay", "version": "1"}}
		r.state = "initialized"
	case "tools/list":
		if p.epoch != r.epoch {
			finish()
			return r.reject(p.original, "MCP_TOOL_LIST_STALE")
		}
		var v struct {
			Tools []json.RawMessage `json:"tools"`
			Next  string            `json:"nextCursor"`
		}
		if decode(f.Result, &v) != nil || v.Tools == nil {
			return fail("MCP_TOOL_INVALID")
		}
		for _, raw := range v.Tools {
			var tool struct {
				Name string `json:"name"`
			}
			if decode(raw, &tool) != nil || tool.Name == "" {
				return fail("MCP_TOOL_INVALID")
			}
			if _, exists := p.tools[tool.Name]; exists {
				return fail("MCP_TOOL_INVALID")
			}
			if len(p.tools) >= 256 {
				return fail("MCP_TOOL_LIMIT")
			}
			p.tools[tool.Name] = nil // Count unknown tools too; do not forward them.
			if trusted, ok := r.p.Tools[tool.Name]; ok {
				hash, err := DescriptorDigest(raw)
				if err != nil || hash != trusted.DescriptorSHA256 {
					return fail("MCP_TOOL_CHANGED")
				}
				if err := r.p.Result(r.p.Binding, "tools/list", raw); err != nil {
					return fail("MCP_RESULT_DENIED")
				}
				p.tools[tool.Name] = raw
			}
		}
		if v.Next != "" {
			if len(v.Next) > 4096 || p.cursors[v.Next] || len(p.cursors) >= 64 {
				return fail("MCP_CURSOR_INVALID")
			}
			p.cursors[v.Next] = true
			delete(r.pending, id)
			return r.request(p, map[string]string{"cursor": v.Next})
		}
		tools := []json.RawMessage{}
		names := []string{}
		for name := range p.tools {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if p.tools[name] != nil {
				tools = append(tools, p.tools[name])
				r.listed[name] = true
			}
		}
		result = map[string]any{"tools": tools}
	case "tools/call":
		if p.epoch != r.epoch {
			finish()
			return r.reject(p.original, "MCP_TOOL_LIST_STALE")
		}
		var object map[string]json.RawMessage
		if decode(f.Result, &object) != nil || object == nil {
			return fail("MCP_RESULT_INVALID")
		}
		if err := r.p.Result(r.p.Binding, p.tool, f.Result); err != nil {
			finish()
			return r.reject(p.original, "MCP_RESULT_DENIED")
		}
		result = json.RawMessage(f.Result)
	}
	return r.guarded(func() error {
		finish()
		if err := r.p.Audit(Event{Method: p.method, Code: "RETURN", RequestID: id, Tool: p.tool, ResultSHA256: digest(bytesOf(result))}); err != nil {
			return fail("MCP_AUDIT_UNAVAILABLE")
		}
		return r.write(r.client.Writer, map[string]any{"jsonrpc": "2.0", "id": p.original, "result": result})
	})
}
