package source

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/mcppolicy"
	"github.com/pzqf/AIDock/internal/mcprelay"
	"github.com/pzqf/AIDock/internal/mcpsession"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/runtimecheck"
	"github.com/pzqf/AIDock/internal/workstore"
)

type clientHarness struct {
	client net.Conn
	reader *bufio.Reader
	done   chan struct{}
	event  mcpsession.Event
	err    error
}

func clientFixture(t *testing.T) (*Manager, MCPSessionRequest, string, string) {
	t.Helper()
	started, release := filepath.Join(t.TempDir(), "started"), filepath.Join(t.TempDir(), "release")
	m, req := sessionFixtureWithScript(t, func(string) string {
		return `const descriptor=` + string(mcppolicy.Example().Tools[0].Descriptor) + `;
const fs=require('node:fs'),readline=require('node:readline').createInterface({input:process.stdin});
readline.on('line',line=>{const f=JSON.parse(line);if(!f.id)return;
const send=result=>process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:f.id,result})+'\n');
if(f.method==='initialize')return send({protocolVersion:'2025-11-25',serverInfo:{name:'UPSTREAM_PRIVATE_METADATA'}});
if(f.method==='tools/list')return send({tools:[descriptor]});
if(f.method!=='tools/call')process.exit(97);
const a=f.params.arguments,result={project:a.project,scope:a.scope,copy:a.copy,level:a.query==='internal'?'Internal':'Public',content:[{type:'text',text:'PUBLIC_FIXTURE_RESULT'}]};
if(a.query==='hold'){fs.writeFileSync(` + string(jsonBytes(started)) + `,'started');const timer=setInterval(()=>{if(fs.existsSync(` + string(jsonBytes(release)) + `)){clearInterval(timer);send(result);}},15);return;}
send(result);});`
	})
	return m, req, started, release
}

func openClient(t *testing.T, m *Manager, req MCPSessionRequest) *clientHarness {
	t.Helper()
	client, relay := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	h := &clientHarness{client: client, reader: bufio.NewReader(client), done: make(chan struct{})}
	go func() {
		h.event, h.err = m.ServeMCPClient(ctx, req, mcprelay.Endpoint{Reader: relay, Writer: relay})
		close(h.done)
	}()
	t.Cleanup(func() { cancel(); client.Close(); <-h.done })
	return h
}
func (h *clientHarness) put(t *testing.T, v any) {
	t.Helper()
	h.client.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(h.client).Encode(v); err != nil {
		t.Fatal(err)
	}
}
func (h *clientHarness) get(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	h.client.SetReadDeadline(time.Now().Add(10 * time.Second))
	b, err := h.reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]json.RawMessage
	if json.Unmarshal(b, &out) != nil {
		t.Fatal(string(b))
	}
	return out
}
func (h *clientHarness) request(t *testing.T, method string, params any) map[string]json.RawMessage {
	h.put(t, map[string]any{"jsonrpc": "2.0", "id": "PRIVATE_CLIENT_ID", "method": method, "params": params})
	return h.get(t)
}
func (h *clientHarness) ready(t *testing.T) {
	t.Helper()
	v := h.request(t, "initialize", map[string]any{"protocolVersion": "2025-11-25", "clientInfo": map[string]string{"name": "PRIVATE_CLIENT_METADATA"}})
	if strings.Contains(string(v["result"]), "PRIVATE") || v["error"] != nil {
		t.Fatal(v)
	}
	h.put(t, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	v = h.request(t, "tools/list", map[string]any{})
	if v["error"] != nil || !strings.Contains(string(v["result"]), "project_query") {
		t.Fatal(v)
	}
}
func callArgs(m *Manager, query string) map[string]any {
	return map[string]any{"name": "project_query", "arguments": map[string]string{"project": m.Owner, "scope": m.Scope, "copy": m.Copy, "query": query}}
}
func waitMarker(t *testing.T, p string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(p); err == nil {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("upstream did not receive the call")
}
func waitClient(t *testing.T, h *clientHarness) {
	t.Helper()
	select {
	case <-h.done:
		if h.err != nil {
			t.Fatal(h.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client lifetime or cleanup did not finish")
	}
}

func TestClientRealReadScopeFilteringAuditAndCleanEOF(t *testing.T) {
	m, req, _, _ := clientFixture(t)
	before, _ := m.Store.Capture(m.Store.Root)
	artifactRoot := filepath.Join(m.Store.Local, "mcp-artifacts", "memory")
	artifact, _ := m.Store.Capture(artifactRoot)
	h := openClient(t, m, req)
	h.ready(t)
	v := h.request(t, "tools/call", callArgs(m, "PRIVATE_QUERY_SENTINEL"))
	if v["error"] != nil || !strings.Contains(string(v["result"]), "PUBLIC_FIXTURE_RESULT") {
		t.Fatal(v)
	}
	bad := callArgs(m, "PRIVATE_QUERY_SENTINEL")
	bad["arguments"].(map[string]string)["project"] = "other-project"
	v = h.request(t, "tools/call", bad)
	if !strings.Contains(string(v["error"]), "MCP_SCOPE_DENIED") {
		t.Fatal(v)
	}
	bad = callArgs(m, "PRIVATE_QUERY_SENTINEL")
	bad["name"] = "unapproved_write"
	v = h.request(t, "tools/call", bad)
	if !strings.Contains(string(v["error"]), "AUTHORIZATION_REQUIRED") {
		t.Fatal(v)
	}
	v = h.request(t, "tools/call", callArgs(m, "internal"))
	if !strings.Contains(string(v["error"]), "MCP_RESULT_DENIED") || strings.Contains(string(v["result"]), "PUBLIC_FIXTURE_RESULT") {
		t.Fatal(v)
	}
	h.client.Close()
	waitClient(t, h)
	if h.event.Code != "MCP_CLIENT_CLOSED" {
		t.Fatal(h.event)
	}
	s, err := m.MCPSessions(context.Background())
	if err != nil || len(s.Entries) != 1 || s.Entries[0].Status != "CLOSED" || !s.Entries[0].Current || s.ExecutionReady || s.Entries[0].ReadRequests != 2 || s.Entries[0].CompletedReads != 1 || s.Entries[0].Rejections != 3 {
		t.Fatal(s, err)
	}
	sessions, _ := m.sessionManager()
	tree, _ := sessions.Store.Capture(sessions.Store.Root)
	for _, e := range tree.Entries {
		if e.Dir {
			continue
		}
		b, _ := sessions.Store.Data(e)
		for _, hidden := range []string{"PRIVATE_CLIENT", "PRIVATE_QUERY_SENTINEL", "PUBLIC_FIXTURE_RESULT", "UPSTREAM_PRIVATE"} {
			if strings.Contains(string(b), hidden) {
				t.Fatal("body, client identity or upstream metadata leaked into audit")
			}
		}
	}
	after, _ := m.Store.Capture(m.Store.Root)
	afterArtifact, _ := m.Store.Capture(artifactRoot)
	if workstore.Digest(before) != workstore.Digest(after) || workstore.Digest(artifact) != workstore.Digest(afterArtifact) {
		t.Fatal("client changed source or artifact")
	}
}

func TestClientConfigurationIsReadOnlyAndBoundToCurrentApproval(t *testing.T) {
	m, req, _, _ := clientFixture(t)
	command := filepath.Join(t.TempDir(), "aidock.exe")
	out, err := m.MCPClientConfig(context.Background(), req, command, filepath.Dir(m.Store.Local))
	if err != nil || out.Command != command || out.Binding.Copy != m.Copy || strings.Contains(strings.Join(out.Args, " "), "--json") || !strings.Contains(strings.Join(out.Args, " "), "--allow-unisolated") {
		t.Fatal(out, err)
	}
	s, err := m.MCPSessions(context.Background())
	if err != nil || s.AuditCount != 0 {
		t.Fatal(s, err)
	}
	permissionApply(t, m, permissionPlan(t, m, pluginpermission.Edit{Action: "revoke", Name: req.Name}))
	if _, err := m.MCPClientConfig(context.Background(), req, command, filepath.Dir(m.Store.Local)); err == nil {
		t.Fatal("revoked configuration preview remained available")
	}
}

func TestClientInputContentionRetriesBeforeTransferOnly(t *testing.T) {
	m, req, _, _ := clientFixture(t)
	locked, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- m.Store.Locked(func() error { close(locked); <-release; return nil })
	}()
	<-locked
	calls := 0
	resultCh := make(chan error, 1)
	go func() {
		resultCh <- m.withMCPClientInput(context.Background(), req, func(lockedMCPSession) error { calls++; return nil })
	}()
	// Release after the first attempted lock so the retry has real contention.
	time.Sleep(50 * time.Millisecond)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-resultCh; err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	calls = 0
	err := m.withMCPClientInput(context.Background(), req, func(lockedMCPSession) error {
		calls++
		return result.Fail("SOURCE_BUSY", "transfer already attempted", 3)
	})
	if err == nil || calls != 1 {
		t.Fatal("retried an entered callback", calls, err)
	}
}

func TestClientAuthorityCanChangeWhileWaitingAndBlocksReturn(t *testing.T) {
	for _, mode := range []string{"revoke", "disable", "artifact", "runtime"} {
		t.Run(mode, func(t *testing.T) {
			m, req, started, release := clientFixture(t)
			h := openClient(t, m, req)
			h.ready(t)
			h.put(t, map[string]any{"jsonrpc": "2.0", "id": 51, "method": "tools/call", "params": callArgs(m, "hold")})
			waitMarker(t, started)
			switch mode {
			case "revoke":
				permissionApply(t, m, permissionPlan(t, m, pluginpermission.Edit{Action: "revoke", Name: req.Name}))
			case "disable":
				s, err := m.MCP()
				if err != nil {
					t.Fatal(err)
				}
				s.Config["servers"].([]any)[0].(map[string]any)["enabled"] = false
				p, err := m.MCPPlan(context.Background(), MCPEdit{ExpectedSHA256: s.SHA256, Config: s.Config})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = m.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
					t.Fatal(err)
				}
			case "artifact":
				write(t, filepath.Join(m.Store.Local, "mcp-artifacts", "memory", "unexpected.txt"), []byte("drift"))
			case "runtime":
				p, _ := m.MCPPermission(context.Background())
				i, _ := runtimecheck.From(p.Authorizations[0].Record)
				r, _ := m.runtimeManager()
				if _, err := r.Probe(context.Background(), i, func() (string, string) { return "RUNTIME_CHECK_FAILED", "" }); err != nil {
					t.Fatal(err)
				}
			}
			write(t, release, []byte("release"))
			v := h.get(t)
			if !strings.Contains(string(v["error"]), "AUTHORIZATION_STALE") || v["result"] != nil {
				t.Fatal(v)
			}
			waitClient(t, h)
			if h.event.Code != "MCP_CLIENT_REVOKED" {
				t.Fatal(h.event)
			}
		})
	}
}
