package source

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/mcppolicy"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/runtimecheck"
	"github.com/pzqf/AIDock/internal/workstore"
)

func sessionFixture(t *testing.T, secrets ...bool) (*Manager, MCPSessionRequest) {
	return sessionFixtureWithScript(t, nil, secrets...)
}

func sessionFixtureWithScript(t *testing.T, modify func(string) string, secrets ...bool) (*Manager, MCPSessionRequest) {
	t.Helper()
	ctx := context.Background()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable for real session fixture")
	}
	versionBytes, err := exec.Command(node, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	version := strings.TrimPrefix(strings.TrimSpace(string(versionBytes)), "v")
	binary, err := os.ReadFile(node)
	if err != nil {
		t.Fatal(err)
	}
	m, req := installFixture(t)
	m.Copy = "session-fixture-copy"
	state, err := m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	server := state.Config["servers"].([]any)[0].(map[string]any)
	if len(secrets) > 0 && secrets[0] {
		server["secrets"] = []any{"session_key"}
		server["env"] = map[string]any{"SERVICE_TOKEN": "${SECRET:session_key}"}
	}
	server["tier"] = 0
	server["enabled"] = true
	var policy any
	_ = json.Unmarshal(jsonBytes(mcppolicy.Example()), &policy)
	server["tool_policy"] = policy
	plan, err := m.MCPPlan(ctx, MCPEdit{ExpectedSHA256: state.SHA256, Config: state.Config})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCP(ctx, plan.Plan.ID, plan.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	executable := "runtime/node"
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	root := filepath.Join(m.Store.Local, filepath.FromSlash(req.ArtifactPath))
	write(t, filepath.Join(root, filepath.FromSlash(executable)), binary)
	if err = os.Chmod(filepath.Join(root, filepath.FromSlash(executable)), 0755); err != nil {
		t.Fatal(err)
	}
	descriptor := string(mcppolicy.Example().Tools[0].Descriptor)
	body := `'use strict';
if(process.env.NODE_OPTIONS||process.env.AIDOCK_SESSION_PARENT_SENTINEL)process.exit(98);
const descriptor=` + descriptor + `;
const readline=require('node:readline').createInterface({input:process.stdin});
readline.on('line',line=>{let f=JSON.parse(line);if(!f.id)return;let result;if(f.method==='initialize')result={protocolVersion:'2025-11-25',serverInfo:{name:'private-sentinel-do-not-expose',version:'1'},capabilities:{tools:{}}};else if(f.method==='tools/list')result={tools:[descriptor,{name:'unknown_private',inputSchema:{type:'object'},description:'private-sentinel-do-not-expose'}]};else process.exit(97);process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:f.id,result})+'\n');});
`
	if modify != nil {
		body = modify(body)
	}
	if len(secrets) > 0 && secrets[0] {
		body = "if(!process.env.SERVICE_TOKEN)process.exit(96);\n" + strings.Replace(body, "name:\x27private-sentinel-do-not-expose\x27", "name:process.env.SERVICE_TOKEN", 1)
		manifestPath := filepath.Join(root, "plugin.yaml")
		raw, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		var manifest map[string]any
		_ = json.Unmarshal(raw, &manifest)
		manifest["secrets"] = server["secrets"]
		write(t, manifestPath, jsonBytes(manifest))
	}
	write(t, filepath.Join(root, "dist", "index.js"), []byte(body))
	req.Runtime.Kind = "node"
	req.Runtime.Version = version
	req.Runtime.Constraint = "=" + version
	req.Runtime.Executable = executable
	refreshInstallLock(t, m, req)
	installEdit(t, m, allowInstall(req.Name))
	grant := grantPermission(req.Name)
	grant.ConfirmUnisolated = true
	grant.ConfirmTools = true
	grant.ConfirmSecrets = len(secrets) > 0 && secrets[0]
	permissionApply(t, m, permissionPlan(t, m, grant))
	permission, err := m.MCPPermission(ctx)
	if err != nil {
		t.Fatal(err)
	}
	versionEvent, err := m.CheckMCPRuntime(ctx, MCPRuntimeRequest{Name: req.Name, Probe: true, ExpectedSHA256: permission.SHA256, ExpectedReviewSHA256: permission.ReviewSHA256})
	if err != nil || versionEvent.Code != "RUNTIME_VERSION_MATCH" {
		t.Fatal(versionEvent, err)
	}
	return m, MCPSessionRequest{Name: req.Name, Probe: true, ExpectedSHA256: permission.SHA256, ExpectedReviewSHA256: permission.ReviewSHA256, ExpectedRuntimeAttempt: versionEvent.Attempt}
}
func TestSessionLockedRealNodeHandshakeAuditAndRevocation(t *testing.T) {
	m, req := sessionFixture(t)
	ctx := context.Background()
	before, _ := m.Store.Capture(m.Store.Root)
	artifact, _ := m.Store.Capture(filepath.Join(m.Store.Local, "mcp-artifacts", "memory"))
	t.Setenv("NODE_OPTIONS", "--require=must-not-inherit")
	t.Setenv("AIDOCK_SESSION_PARENT_SENTINEL", "must-not-inherit")
	state, err := m.MCPSessions(ctx)
	if err != nil || state.AuditCount != 0 {
		t.Fatal(state, err)
	}
	missing := req
	missing.Probe = false
	_, err = m.CheckMCPSession(ctx, missing)
	code(t, err, "INVALID_INPUT")
	stale := req
	stale.ExpectedRuntimeAttempt = workstore.ID()
	_, err = m.CheckMCPSession(ctx, stale)
	code(t, err, "PLAN_STALE")
	event, err := m.CheckMCPSession(ctx, req)
	if err != nil || event.Code != "MCP_SESSION_MATCH" || event.ToolCount != 1 {
		t.Fatal(event, err)
	}
	diagnostic, err := m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range diagnostic.Servers {
		for _, f := range s.Checks {
			if f.Code == "MCP_SESSION_HANDSHAKE_OBSERVED" && f.Status == "PASS" {
				found = true
			}
		}
	}
	if !found || diagnostic.ExecutionReady {
		t.Fatal("session evidence missing or raised readiness", diagnostic)
	}
	state, err = m.MCPSessions(ctx)
	if err != nil || state.AuditCount != 6 || len(state.Entries) != 1 || state.Entries[0].Status != "PASS" || !state.Entries[0].Current || state.ExecutionReady {
		t.Fatal(state, err)
	}
	manager, err := m.sessionManager()
	if err != nil {
		t.Fatal(err)
	}
	tree, err := manager.Store.Capture(manager.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range tree.Entries {
		if entry.Dir {
			continue
		}
		raw, err := manager.Store.Data(entry)
		if err != nil || strings.Contains(string(raw), "private-sentinel") || strings.Contains(string(raw), "must-not-inherit") {
			t.Fatal(string(raw), err)
		}
	}
	permissionApply(t, m, permissionPlan(t, m, pluginpermission.Edit{Action: "revoke", Name: req.Name}))
	_, err = m.CheckMCPSession(ctx, req)
	code(t, err, "PLAN_STALE")
	state, err = m.MCPSessions(ctx)
	if err != nil || state.AuditCount != 6 || state.Entries[0].Current {
		t.Fatal(state, err)
	}
	after, _ := m.Store.Capture(m.Store.Root)
	artifactAfter, _ := m.Store.Capture(filepath.Join(m.Store.Local, "mcp-artifacts", "memory"))
	if workstore.Digest(before) != workstore.Digest(after) || workstore.Digest(artifact) != workstore.Digest(artifactAfter) {
		t.Fatal("session changed source or artifacts")
	}
	other := *m
	other.Copy = "other-copy"
	state, err = other.MCPSessions(ctx)
	if err != nil || state.AuditCount != 0 {
		t.Fatal(state, err)
	}
}
func TestSessionNewerFailedRuntimePreventsOlderSuccessAndNoLaunchIntent(t *testing.T) {
	m, req := sessionFixture(t)
	ctx := context.Background()
	permission, _ := m.MCPPermission(ctx)
	input, err := runtimecheck.From(permission.Authorizations[0].Record)
	if err != nil {
		t.Fatal(err)
	}
	manager, _ := m.runtimeManager()
	event, err := manager.Probe(ctx, input, func() (string, string) { return "RUNTIME_CHECK_FAILED", "" })
	if err != nil {
		t.Fatal(err)
	}
	req.ExpectedRuntimeAttempt = event.Attempt
	_, err = m.CheckMCPSession(ctx, req)
	code(t, err, "MCP_SESSION_RUNTIME_REQUIRED")
	state, err := m.MCPSessions(ctx)
	if err != nil || state.AuditCount != 0 {
		t.Fatal(state, err)
	}
}
func TestSessionOriginalRequestRejectsAmbiguousExecutionChoices(t *testing.T) {
	for _, raw := range []string{`{"probe":true,"probe":false}`, `{"Probe":true}`, `{"probe":null}`, `{"expected_runtime_attempt":false}`, `{"command":"private-sentinel"}`} {
		_, err := ParseMCPSessionRequest([]byte(raw))
		if err == nil || strings.Contains(err.Error(), "private-sentinel") {
			t.Fatal(err)
		}
	}
}
