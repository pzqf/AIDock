package source

import (
	"context"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/workstore"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOwnedLockedNodeRuntimeVersionAndRevocationEndToEnd(t *testing.T) {
	ctx := context.Background()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node runtime is not installed; fixed-process coverage remains independent")
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
	state, err := m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	state.Config["servers"].([]any)[0].(map[string]any)["tier"] = 0
	p, err := m.MCPPlan(ctx, MCPEdit{ExpectedSHA256: state.SHA256, Config: state.Config})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCP(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	name := "runtime/node"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	root := filepath.Join(m.Store.Local, filepath.FromSlash(req.ArtifactPath))
	write(t, filepath.Join(root, filepath.FromSlash(name)), binary)
	if err = os.Chmod(filepath.Join(root, filepath.FromSlash(name)), 0755); err != nil {
		t.Fatal(err)
	}
	req.Runtime.Kind = "node"
	req.Runtime.Version = version
	req.Runtime.Constraint = "=" + version
	req.Runtime.Executable = name
	refreshInstallLock(t, m, req)
	installEdit(t, m, allowInstall(req.Name))
	r := grantPermission(req.Name)
	r.ConfirmUnisolated = true
	permissionApply(t, m, permissionPlan(t, m, r))
	approval, err := m.MCPPermission(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, err := m.Store.Capture(m.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_OPTIONS", "--require=must-not-be-inherited")
	t.Setenv("AIDOCK_RUNTIME_PARENT_SENTINEL", "must-not-be-inherited")
	checkReq := MCPRuntimeRequest{Name: req.Name, Probe: true, ExpectedSHA256: approval.SHA256, ExpectedReviewSHA256: approval.ReviewSHA256}
	e, err := m.CheckMCPRuntime(ctx, checkReq)
	if err != nil || e.Code != "RUNTIME_VERSION_MATCH" || strings.TrimPrefix(e.ObservedVersion, "v") != version {
		t.Fatal(e, err)
	}
	s, err := m.MCPRuntime(ctx)
	if err != nil || len(s.Entries) != 1 || !s.Entries[0].Current || s.AuditCount != 2 || s.ExecutionReady {
		t.Fatal(s, err)
	}
	diagnostic, err := m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	observed := false
	for _, server := range diagnostic.Servers {
		for _, finding := range server.Checks {
			if finding.Code == "MCP_RUNTIME_VERSION_OBSERVED" && finding.Status == "PASS" {
				observed = true
			}
		}
	}
	if !observed || diagnostic.ExecutionReady {
		t.Fatal("runtime observation missing or raised plugin execution readiness", diagnostic)
	}
	stale := checkReq
	stale.ExpectedReviewSHA256 = strings.Repeat("0", 64)
	_, err = m.CheckMCPRuntime(ctx, stale)
	code(t, err, "PLAN_STALE")
	permissionApply(t, m, permissionPlan(t, m, pluginpermission.Edit{Action: "revoke", Name: req.Name}))
	_, err = m.CheckMCPRuntime(ctx, checkReq)
	code(t, err, "PLAN_STALE")
	s, err = m.MCPRuntime(ctx)
	if err != nil || s.AuditCount != 2 || s.Entries[0].Current {
		t.Fatal(s, err)
	}
	after, err := m.Store.Capture(m.Store.Root)
	if err != nil || workstore.Digest(after) != workstore.Digest(before) {
		t.Fatal("runtime check modified source", err)
	}
	other := *m
	other.Copy = "other-copy"
	s, err = other.MCPRuntime(ctx)
	if err != nil || s.AuditCount != 0 {
		t.Fatal(s, err)
	}
}

func unisolatedFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	m, req := installFixture(t)
	s, err := m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	s.Config["servers"].([]any)[0].(map[string]any)["tier"] = 0
	p, err := m.MCPPlan(context.Background(), MCPEdit{ExpectedSHA256: s.SHA256, Config: s.Config})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	refreshInstallLock(t, m, req)
	installEdit(t, m, allowInstall(req.Name))
	return m, req.Name
}
func TestUnisolatedOwnTrustNeedsSeparateConfirmationAndRenewedRecoveryConsent(t *testing.T) {
	m, name := unisolatedFixture(t)
	ctx := context.Background()
	state, err := m.MCPPermission(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u := state.Candidates[0].Input.Unisolated
	if u == nil || u.SourceType != "local" || u.Kind != "native" || u.SHA256 == "" {
		t.Fatal(state)
	}
	r := grantPermission(name)
	r.ExpectedSHA256 = state.SHA256
	r.ExpectedReviewSHA256 = state.ReviewSHA256
	_, err = m.MCPPermissionPlan(ctx, r)
	code(t, err, "AUTHORIZATION_UNISOLATED_CONFIRMATION_REQUIRED")
	r.ConfirmUnisolated = true
	permissionApply(t, m, permissionPlan(t, m, r))
	state, err = m.MCPPermission(ctx)
	if err != nil || !state.Authorizations[0].Record.OwnTrusted || state.Authorizations[0].Status != "CURRENT" || state.ExecutionReady {
		t.Fatal(state, err)
	}
	revoked := permissionPlan(t, m, pluginpermission.Edit{Action: "revoke", Name: name})
	permissionApply(t, m, revoked)
	r = grantPermission("")
	r.Action = "rollback"
	r.Snapshot = revoked.Plan.ID
	state, _ = m.MCPPermission(ctx)
	r.ExpectedSHA256 = state.SHA256
	r.ExpectedReviewSHA256 = state.ReviewSHA256
	_, err = m.MCPPermissionPlan(ctx, r)
	code(t, err, "AUTHORIZATION_UNISOLATED_CONFIRMATION_REQUIRED")
	r.ConfirmUnisolated = true
	permissionApply(t, m, permissionPlan(t, m, r))
	// The native fixture is not a supported Node runtime and is never executed.
	state, _ = m.MCPPermission(ctx)
	_, err = m.CheckMCPRuntime(ctx, MCPRuntimeRequest{Name: name, Probe: true, ExpectedSHA256: state.SHA256, ExpectedReviewSHA256: state.ReviewSHA256})
	code(t, err, "RUNTIME_CHECK_UNSUPPORTED")
	checks, err := m.MCPRuntime(ctx)
	if err != nil || checks.AuditCount != 0 {
		t.Fatal(checks, err)
	}
}
func TestRuntimeCheckRequiresProbeCurrentApprovalAndUnambiguousRequest(t *testing.T) {
	m, name := permissionFixture(t)
	ctx := context.Background()
	s, _ := m.MCPPermission(ctx)
	_, err := m.CheckMCPRuntime(ctx, MCPRuntimeRequest{Name: name, ExpectedSHA256: s.SHA256, ExpectedReviewSHA256: s.ReviewSHA256})
	code(t, err, "INVALID_INPUT")
	_, err = m.CheckMCPRuntime(ctx, MCPRuntimeRequest{Name: name, Probe: true, ExpectedSHA256: s.SHA256, ExpectedReviewSHA256: s.ReviewSHA256})
	code(t, err, "MCP_AUTHORIZATION_REQUIRED")
	for _, raw := range []string{`{"probe":true,"probe":false}`, `{"Probe":true}`, `{"probe":null}`, `{"command":"private-sentinel"}`, `{"expected_sha256":12}`} {
		_, err := ParseMCPRuntimeRequest([]byte(raw))
		if err == nil || strings.Contains(err.Error(), "private-sentinel") {
			t.Fatal(err)
		}
	}
}
