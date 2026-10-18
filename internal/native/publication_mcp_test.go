package native

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/mcppolicy"
	"github.com/pzqf/AIDock/internal/mcprelay"
	"github.com/pzqf/AIDock/internal/mcpsession"
	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func nativePublicationMCPFixture(t *testing.T) (*Manager, source.ConfigurationCandidate, string, string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("official installed Node unavailable")
	}
	versionRaw, err := exec.Command(node, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	version := strings.TrimPrefix(strings.TrimSpace(string(versionRaw)), "v")
	m, _, executable, home := nativeConfigurationFixture(t)
	ctx := context.Background()
	raw, err := config.ReadFile(filepath.Join("..", "config", "testdata", "mcp.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := config.Validate("mcp", raw)
	if err != nil {
		t.Fatal(err)
	}
	conf := v.(map[string]any)
	server := conf["servers"].([]any)[0].(map[string]any)
	var policy any
	if err := json.Unmarshal(jsonData(mcppolicy.Example()), &policy); err != nil {
		t.Fatal(err)
	}
	server["name"], server["tier"], server["enabled"], server["entrypoint"], server["tool_policy"] = "project-memory", 0, true, "index.js", policy
	state, err := m.Source.MCP()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := m.Source.MCPPlan(ctx, source.MCPEdit{ExpectedSHA256: state.SHA256, Config: conf})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.ApplyMCP(ctx, plan.Plan.ID, plan.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	artifactPath, runtimeExe := "mcp-artifacts/full-publication", "runtime/node"
	if runtime.GOOS == "windows" {
		runtimeExe += ".exe"
	}
	artifact := filepath.Join(m.Source.Store.Local, filepath.FromSlash(artifactPath))
	binary, err := os.ReadFile(node)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(artifact, filepath.FromSlash(runtimeExe)), binary)
	if err := os.Chmod(filepath.Join(artifact, filepath.FromSlash(runtimeExe)), 0755); err != nil {
		t.Fatal(err)
	}
	descriptor := string(mcppolicy.Example().Tools[0].Descriptor)
	body := `const config=JSON.parse(process.env.AIDOCK_PLUGIN_CONFIG||'null');if(!config||config.message!=='FROZEN_FUTURE_CONFIG')process.exit(93);
const descriptor=` + descriptor + `;const readline=require('node:readline').createInterface({input:process.stdin});
readline.on('line',line=>{const f=JSON.parse(line);if(!f.id)return;const send=result=>process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:f.id,result})+'\n');
if(f.method==='initialize')return send({protocolVersion:'2025-11-25',serverInfo:{name:'owned-publication'},capabilities:{tools:{}}});
if(f.method==='tools/list')return send({tools:[descriptor]});if(f.method!=='tools/call')process.exit(94);const a=f.params.arguments;
send({project:a.project,scope:a.scope,copy:a.copy,level:'Public',content:[{type:'text',text:'OWNED_FROZEN_PUBLIC_RESULT'}]});});`
	write(t, filepath.Join(artifact, "index.js"), []byte(body))
	manifest := map[string]any{"schema_version": 1, "name": "owned-publication", "type": "mcp-server", "version": "1.0.0", "source": server["source"], "permissions": server["permissions"], "dependencies": []any{}, "config_delivery": "env-json-v1", "config_schema": map[string]any{"message": map[string]any{"type": "string", "default": "CURRENT_CONFIG"}}}
	write(t, filepath.Join(artifact, "plugin.yaml"), jsonData(manifest))
	lock, err := m.Source.InspectMCPLock(ctx, pluginlock.Preparation{Name: "project-memory", ArtifactPath: artifactPath, Version: "1.0.0", Runtime: pluginlock.Runtime{Kind: "node", Version: version, Constraint: "=" + version, Executable: runtimeExe}})
	if err != nil {
		t.Fatal(err)
	}
	lp, err := m.Source.MCPLockPlan(ctx, source.MCPLockEdit{ExpectedSHA256: lock.ExpectedSHA256, Document: lock.Document})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.ApplyMCPLock(ctx, lp.Plan.ID, lp.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	installed, err := m.Source.MCPInstall(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ip, err := m.Source.MCPInstallPlan(ctx, plugininstall.Edit{Action: "install", Name: "project-memory", ExpectedSHA256: installed.SHA256, AllowUnverified: true, RiskReason: "独立自有 Node 提交证明测试"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.ApplyMCPInstall(ctx, ip.Plan.ID, ip.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	profile, err := m.Source.Profile()
	if err != nil {
		t.Fatal(err)
	}
	profile.Profile["config_overrides"] = map[string]any{"project-memory": map[string]any{"message": "FROZEN_FUTURE_CONFIG"}}
	future, err := m.Source.ProfilePlan(ctx, source.ProfileEdit{ExpectedSHA256: profile.SHA256, Profile: profile.Profile})
	if err != nil {
		t.Fatal(err)
	}
	c, err := m.Source.DraftConfigurationCandidate(ctx, future.Plan.Before, future.Plan.After, profile.Layers.Digest)
	if err != nil {
		t.Fatal(err)
	}
	review, err := m.Source.CandidatePermissions(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := m.Source.PlanCandidatePermission(ctx, c, pluginpermission.Edit{Action: "grant", Name: "project-memory", ExpectedSHA256: review.SHA256, ExpectedReviewSHA256: review.ReviewSHA256, Purpose: "自有临时项目的固定全批视图测试", DataLevel: "Public", Destination: "unknown-external-client", ConfirmPermissions: true, ConfirmTools: true, ConfirmUnisolated: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.ApplyCandidatePermission(ctx, c, "project-memory", grant.Plan.ID, grant.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	review, err = m.Source.CandidatePermissions(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := m.Source.CheckCandidateRuntime(ctx, c, source.MCPRuntimeRequest{Name: "project-memory", Probe: true, ExpectedSHA256: review.SHA256, ExpectedReviewSHA256: review.ReviewSHA256})
	if err != nil || checked.Code != "RUNTIME_VERSION_MATCH" {
		t.Fatal(checked.Code, err)
	}
	return m, c, executable, home
}

func TestConfigurationPublicationRealNodeUsesCommittedFrozenInputAndCurrentGrant(t *testing.T) {
	m, c, executable, home := nativePublicationMCPFixture(t)
	ctx := context.Background()
	material, coordinator, proof := compilePublicationFixture(t, m, c, executable, home)
	if len(material.prepared.Sessions) != 1 {
		t.Fatal("enabled real MCP omitted")
	}
	r, err := material.Capture(ctx, proof)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.loadAppliedRecord(ctx, material.ActiveKey())
	code(t, err, "APPLIED_VIEW_NOT_COMMITTED")
	coordinator.Validate = func(workstore.BatchPlan) error { return material.Validate(ctx, false) }
	coordinator.BeforeCommit = func(workstore.BatchPlan) error { return material.Validate(ctx, true) }
	if _, err = coordinator.Apply(ctx, proof.Transaction.ID, proof.Transaction.Digest); err != nil {
		t.Fatal(err)
	}
	view, err := m.loadAppliedRecord(ctx, material.ActiveKey())
	if err != nil || view.Record.ID != r.ID {
		t.Fatal(err)
	}
	public, err := m.AppliedViews(ctx)
	if err != nil || len(public.Items) != 1 || public.Items[0].Publication != proof.Publication || public.Items[0].PublicationMembers != 1 || strings.Contains(string(jsonData(public)), "FROZEN_FUTURE_CONFIG") {
		t.Fatal("public applied view lost common identity or exposed input", err)
	}
	data, err := os.ReadFile(filepath.Join(m.Root, ".codex/config.toml"))
	if err != nil || !strings.Contains(string(data), "--applied-key") || !strings.Contains(string(data), material.ActiveKey()) || strings.Contains(string(data), "FROZEN_FUTURE_CONFIG") || strings.Contains(string(data), "--library-root") {
		t.Fatal("native launcher is not bound to frozen input", err)
	}
	state, err := m.Source.Profile()
	if err != nil {
		t.Fatal(err)
	}
	state.Profile["config_overrides"] = map[string]any{"project-memory": map[string]any{"message": "LATER_DRAFT_CONFIG"}}
	draft, err := m.Source.ProfilePlan(ctx, source.ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.Apply(ctx, draft.Plan.ID, draft.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	client, relay := net.Pipe()
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	done := make(chan struct{})
	var event mcpsession.Event
	var sessionErr error
	req := material.prepared.Sessions[0]
	req.Probe = true
	go func() {
		event, sessionErr = m.ServeAppliedMCPClient(bounded, material.ActiveKey(), req, executable, mcprelay.Endpoint{Reader: relay, Writer: relay})
		close(done)
	}()
	t.Cleanup(func() { cancel(); client.Close(); <-done })
	reader := bufio.NewReader(client)
	send := func(v any) {
		t.Helper()
		client.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := json.NewEncoder(client).Encode(v); err != nil {
			t.Fatal(err)
		}
	}
	request := func(id int, method string, params any) map[string]json.RawMessage {
		t.Helper()
		send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		client.SetReadDeadline(time.Now().Add(10 * time.Second))
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var response map[string]json.RawMessage
		if json.Unmarshal(line, &response) != nil || response["error"] != nil {
			t.Fatal("owned frozen client failed", string(line))
		}
		return response
	}
	request(1, "initialize", map[string]any{"protocolVersion": "2025-11-25", "clientInfo": map[string]string{"name": "owned-publication-client"}})
	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	request(2, "tools/list", map[string]any{})
	response := request(3, "tools/call", map[string]any{"name": "project_query", "arguments": map[string]string{"project": m.Source.Owner, "scope": m.Source.Scope, "copy": m.Source.Copy, "query": "public owned fixture"}})
	if !strings.Contains(string(response["result"]), "OWNED_FROZEN_PUBLIC_RESULT") {
		t.Fatal("the plugin did not receive frozen future input")
	}
	client.Close()
	select {
	case <-done:
		if sessionErr != nil || event.Code != "MCP_CLIENT_CLOSED" {
			t.Fatal(event.Code, sessionErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owned Node client did not close")
	}
	permission, err := m.Source.MCPPermission(ctx)
	if err != nil {
		t.Fatal(err)
	}
	revoke, err := m.Source.MCPPermissionPlan(ctx, pluginpermission.Edit{Action: "revoke", Name: "project-memory", ExpectedSHA256: permission.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.ApplyMCPPermission(ctx, revoke.Plan.ID, revoke.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	called := false
	err = m.Source.WithFrozenMCPClientConfigs(ctx, view.Frozen, material.prepared.Sessions, executable, home, func([]source.MCPClientConfiguration) error { called = true; return nil })
	if err == nil || called {
		t.Fatal("global commit resurrected revoked grant")
	}
	sessions, err := m.Source.MCPSessions(ctx)
	if err != nil || strings.Contains(string(jsonData(sessions)), "FROZEN_FUTURE_CONFIG") || strings.Contains(string(jsonData(sessions)), "OWNED_FROZEN_PUBLIC_RESULT") {
		t.Fatal("raw input/result body entered session audit", err)
	}
}
