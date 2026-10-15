package cli

import (
	"context"
	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"os"
	"path/filepath"
	"testing"
)

func TestMCPPermissionCLIGrantRevokeExplicitRestoreAndRecovery(t *testing.T) {
	m, flags := mcpCLIArtifactFixture(t)
	ctx := context.Background()
	s, err := m.MCPInstall(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.MCPInstallPlan(ctx, plugininstall.Edit{Action: "install", Name: "fixture", ExpectedSHA256: s.SHA256, AllowUnverified: true, RiskReason: "isolated CLI fixture, no execution"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPInstall(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	call := func(args ...string) map[string]any {
		t.Helper()
		exit, out, _ := invoke(t, append(args, flags...)...)
		if exit != 0 {
			t.Fatal(exit, out)
		}
		return out["items"].(map[string]any)
	}
	file := filepath.Join(t.TempDir(), "permission.json")
	plan := func(r pluginpermission.Edit) map[string]any {
		t.Helper()
		state := call("mcp", "permission", "state")
		r.ExpectedSHA256 = state["sha256"].(string)
		if r.Action != "revoke" {
			r.ExpectedReviewSHA256 = state["review_sha256"].(string)
		}
		if err := os.WriteFile(file, pluginpermission.JSON(r), 0600); err != nil {
			t.Fatal(err)
		}
		return call("mcp", "permission", "plan", "--file", file)["plan"].(map[string]any)
	}
	apply := func(p map[string]any) {
		t.Helper()
		call("mcp", "permission", "apply", "--plan", p["id"].(string), "--expected-digest", p["digest"].(string))
	}
	grant := pluginpermission.Edit{Action: "grant", Name: "fixture", ConfirmPermissions: true, Purpose: "CLI isolated fixture", DataLevel: "Public", Destination: "unknown-external-client"}
	gp := plan(grant)
	exit, _, _ := invoke(t, append([]string{"mcp", "permission", "apply", "--plan", gp["id"].(string)}, flags...)...)
	if exit == 0 {
		t.Fatal("missing confirmation accepted")
	}
	apply(gp)
	state := call("mcp", "permission", "state")
	if len(state["authorizations"].([]any)) != 1 || state["execution_ready"] != false {
		t.Fatal(state)
	}
	revoked := plan(pluginpermission.Edit{Action: "revoke", Name: "fixture"})
	apply(revoked)
	state = call("mcp", "permission", "state")
	r := pluginpermission.Edit{Action: "rollback", Snapshot: revoked["id"].(string), ExpectedSHA256: state["sha256"].(string), ExpectedReviewSHA256: state["review_sha256"].(string)}
	os.WriteFile(file, pluginpermission.JSON(r), 0600)
	exit, out, _ := invoke(t, append([]string{"mcp", "permission", "plan", "--file", file}, flags...)...)
	if exit != 4 || out["code"] != "AUTHORIZATION_CONFIRMATION_REQUIRED" {
		t.Fatal(exit, out)
	}
	r.ConfirmPermissions = true
	r.Purpose = "explicitly restore permission"
	r.DataLevel = "Public"
	r.Destination = "unknown-external-client"
	apply(plan(r))
	call("mcp", "permission", "recover")
	state = call("mcp", "permission", "state")
	if state["audit_count"] != float64(3) {
		t.Fatal(state)
	}
}
