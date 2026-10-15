package source

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/workstore"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func permissionFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	m, req := installFixture(t)
	installEdit(t, m, allowInstall(req.Name))
	return m, req.Name
}
func grantPermission(name string) pluginpermission.Edit {
	return pluginpermission.Edit{Action: "grant", Name: name, ConfirmPermissions: true, Purpose: "独立夹具验证权限决策，不运行程序", DataLevel: "Public", Destination: "unknown-external-client"}
}
func permissionPlan(t *testing.T, m *Manager, r pluginpermission.Edit) pluginpermission.Preview {
	t.Helper()
	s, err := m.MCPPermission(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r.ExpectedSHA256 = s.SHA256
	if r.Action != "revoke" {
		r.ExpectedReviewSHA256 = s.ReviewSHA256
	}
	p, err := m.MCPPermissionPlan(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func permissionApply(t *testing.T, m *Manager, p pluginpermission.Preview) {
	t.Helper()
	if _, err := m.ApplyMCPPermission(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
}
func TestMCPPermissionGrantRevokeRollbackRequiresNewApprovalAndPreservesSource(t *testing.T) {
	m, name := permissionFixture(t)
	ctx := context.Background()
	sourceBefore, _ := m.Store.Capture(m.Store.Root)
	p := permissionPlan(t, m, grantPermission(name))
	s, _ := m.MCPPermission(ctx)
	if len(s.Authorizations) != 0 || s.AuditCount != 0 {
		t.Fatal("preview wrote approval", s)
	}
	_, err := m.ApplyMCPPermission(ctx, p.Plan.ID, "")
	code(t, err, "INVALID_INPUT")
	permissionApply(t, m, p)
	_, err = m.ApplyMCPPermission(ctx, p.Plan.ID, p.Plan.Digest)
	code(t, err, "PLAN_CONSUMED")
	fresh, _ := New(m.Store.Root, m.Store.Local, m.Owner, m.Scope)
	s, err = fresh.MCPPermission(ctx)
	if err != nil || len(s.Authorizations) != 1 || s.Authorizations[0].Status != "CURRENT" || s.Authorizations[0].ExecutionReady || s.AuditCount != 1 || s.Authorizations[0].Record.Actor == "" {
		t.Fatal(s, err)
	}
	original := s.Authorizations[0].Record.ID
	checked, err := m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, server := range checked.Servers {
		for _, f := range server.Checks {
			if f.Code == "MCP_PROJECT_AUTHORIZED" {
				found = true
			}
		}
	}
	if !found || checked.ExecutionReady {
		t.Fatal(checked)
	}
	revoked := permissionPlan(t, m, pluginpermission.Edit{Action: "revoke", Name: name})
	permissionApply(t, m, revoked)
	s, _ = m.MCPPermission(ctx)
	if len(s.Authorizations) != 0 || s.AuditCount != 2 {
		t.Fatal(s)
	}
	restore := pluginpermission.Edit{Action: "rollback", Snapshot: revoked.Plan.ID, ExpectedSHA256: s.SHA256, ExpectedReviewSHA256: s.ReviewSHA256}
	_, err = m.MCPPermissionPlan(ctx, restore)
	code(t, err, "AUTHORIZATION_CONFIRMATION_REQUIRED")
	confirmed := grantPermission("")
	confirmed.Action = "rollback"
	confirmed.Snapshot = revoked.Plan.ID
	rolled := permissionPlan(t, m, confirmed)
	permissionApply(t, m, rolled)
	s, _ = m.MCPPermission(ctx)
	if s.Authorizations[0].Record.ID == original || s.Authorizations[0].Status != "CURRENT" || s.AuditCount != 3 {
		t.Fatal(s)
	}
	undo := permissionPlan(t, m, pluginpermission.Edit{Action: "rollback", Snapshot: rolled.Plan.ID})
	permissionApply(t, m, undo)
	s, _ = m.MCPPermission(ctx)
	if len(s.Authorizations) != 0 || s.AuditCount != 4 {
		t.Fatal(s)
	}
	after, _ := m.Store.Capture(m.Store.Root)
	if workstore.Digest(after) != workstore.Digest(sourceBefore) {
		t.Fatal("permission changed source")
	}
	installed, _ := m.MCPInstall(ctx)
	if len(installed.Installations) != 1 {
		t.Fatal("permission changed installations")
	}
}
func TestMCPPermissionExplicitConfirmationAndDataBoundary(t *testing.T) {
	m, name := permissionFixture(t)
	ctx := context.Background()
	s, _ := m.MCPPermission(ctx)
	for _, kind := range []string{"no-confirm", "no-purpose", "sensitive", "destination", "review"} {
		t.Run(kind, func(t *testing.T) {
			r := grantPermission(name)
			r.ExpectedSHA256 = s.SHA256
			r.ExpectedReviewSHA256 = s.ReviewSHA256
			switch kind {
			case "no-confirm":
				r.ConfirmPermissions = false
			case "no-purpose":
				r.Purpose = " "
			case "sensitive":
				r.DataLevel = "Internal"
			case "destination":
				r.Destination = "https://example.com"
			case "review":
				r.ExpectedReviewSHA256 = strings.Repeat("0", 64)
			}
			_, err := m.MCPPermissionPlan(ctx, r)
			if err == nil {
				t.Fatal("accepted unchecked scope", kind)
			}
		})
	}
}
func TestMCPPermissionCannotGrantPreparedOrStaleInstallations(t *testing.T) {
	m, req := installFixture(t)
	s, _ := m.MCPPermission(context.Background())
	r := grantPermission(req.Name)
	r.ExpectedSHA256 = s.SHA256
	r.ExpectedReviewSHA256 = s.ReviewSHA256
	_, err := m.MCPPermissionPlan(context.Background(), r)
	code(t, err, "AUTHORIZATION_INSTALL_REQUIRED")
	installEdit(t, m, allowInstall(req.Name))
	permissionApply(t, m, permissionPlan(t, m, r))
	installEdit(t, m, plugininstall.Edit{Action: "remove", Name: req.Name})
	s, _ = m.MCPPermission(context.Background())
	if s.Authorizations[0].Status != "STALE" {
		t.Fatal(s)
	}
}
func TestMCPPermissionSourceLayerArtifactInstallationAndReviewChangesInvalidate(t *testing.T) {
	for _, kind := range []string{"source", "local-layer", "artifact", "installation", "lock"} {
		t.Run(kind, func(t *testing.T) {
			m, name := permissionFixture(t)
			if kind == "local-layer" {
				m.Copy = "permission-layer-copy"
			}
			p := permissionPlan(t, m, grantPermission(name))
			ctx := context.Background()
			switch kind {
			case "source":
				write(t, filepath.Join(m.Store.Root, "notes.txt"), []byte("external source note"))
			case "local-layer":
				s, err := m.Profile()
				if err != nil {
					t.Fatal(err)
				}
				profile := map[string]any{"schema_version": 2, "name": "permission-layer", "description": "new source of effective configuration"}
				q, err := m.ProfilePlan(ctx, ProfileEdit{Target: "local", ExpectedLayers: s.Layers.Digest, ExpectedSHA256: s.Layers.LocalSHA256, Profile: profile})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = m.ApplyProfile(ctx, "local", q.Plan.ID, q.Plan.Digest); err != nil {
					t.Fatal(err)
				}
			case "artifact":
				state, _ := m.MCPInstall(ctx)
				write(t, filepath.Join(m.Store.Local, filepath.FromSlash(state.Installations[0].Record.Plugin.Artifact.Path), ".hidden"), []byte("changed"))
			case "installation":
				installEdit(t, m, allowInstall(name))
			case "lock":
				b, _ := os.ReadFile(filepath.Join(m.Store.Root, "plugins.lock"))
				write(t, filepath.Join(m.Store.Root, "plugins.lock"), append(b, ' '))
			}
			_, err := m.ApplyMCPPermission(ctx, p.Plan.ID, p.Plan.Digest)
			code(t, err, "PLAN_STALE")
			state, _ := m.MCPPermission(ctx)
			if len(state.Authorizations) != 0 {
				t.Fatal("stale plan wrote approval")
			}
		})
	}
}
func TestMCPPermissionRevokeWorksWithBrokenPortableDefinitionsAndLock(t *testing.T) {
	m, name := permissionFixture(t)
	permissionApply(t, m, permissionPlan(t, m, grantPermission(name)))
	write(t, filepath.Join(m.Store.Root, "mcp.yaml"), []byte("malformed: ["))
	write(t, filepath.Join(m.Store.Root, "plugins.lock"), []byte("malformed: ["))
	p := permissionPlan(t, m, pluginpermission.Edit{Action: "revoke", Name: name})
	permissionApply(t, m, p)
	s, err := m.MCPPermission(context.Background())
	if err != nil || len(s.Authorizations) != 0 || s.AuditCount != 2 {
		t.Fatal(s, err)
	}
}
func TestMCPPermissionCopyScopeBindingAndTamperedPlanAreRejected(t *testing.T) {
	m, name := permissionFixture(t)
	p := permissionPlan(t, m, grantPermission(name))
	other, _ := New(m.Store.Root, m.Store.Local, m.Owner, "other")
	pm, _ := m.permissionManager()
	om, _ := other.permissionManager()
	if pm.Store.Root == om.Store.Root {
		t.Fatal("scope reused permission root")
	}
	_, err := other.ApplyMCPPermission(context.Background(), p.Plan.ID, p.Plan.Digest)
	if err == nil {
		t.Fatal("other scope confirmed approval")
	}
	after := workstore.Clone(p.Plan.After)
	pm.Store.PutFile(&after, "outside.txt", []byte("forged"), 0600)
	forged, err := pm.Store.PreviewBound(p.Plan.Action, p.Plan.Before, after, p.Plan.Inputs)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.ApplyMCPPermission(context.Background(), forged.ID, forged.Digest)
	code(t, err, "INVALID_AUTHORIZATION_PLAN")
	permissionApply(t, m, p)
	otherRoot := filepath.Join(filepath.Dir(m.Store.Root), "other-copy", ".aidock")
	copyManager, _ := New(otherRoot, m.Store.Local, m.Owner, m.Scope)
	cm, _ := copyManager.permissionManager()
	if cm.Store.Root == pm.Store.Root {
		t.Fatal("physical copy reused approval")
	}
	tree, _ := pm.Store.Capture(pm.Store.Root)
	for path, e := range tree.Entries {
		if strings.HasPrefix(path, "audit/") {
			file := filepath.Join(pm.Store.Root, filepath.FromSlash(path))
			b, _ := pm.Store.Data(e)
			write(t, file, []byte("{}"))
			s, _ := m.MCPPermission(context.Background())
			if len(s.Checks) == 0 || len(s.Authorizations) > 0 {
				t.Fatal("tampered audit accepted", s)
			}
			write(t, file, b)
			break
		}
	}
}
func TestMCPPermissionRecoveryAfterProcessExit(t *testing.T) {
	if os.Getenv("AIDOCK_PERMISSION_CRASH_HELPER") == "1" {
		var i struct{ Root, Local, Owner, Scope, Plan, Digest string }
		if err := json.Unmarshal([]byte(os.Getenv("AIDOCK_PERMISSION_CRASH_INPUT")), &i); err != nil {
			t.Fatal(err)
		}
		m, err := New(i.Root, i.Local, i.Owner, i.Scope)
		if err != nil {
			t.Fatal(err)
		}
		err = m.withPermission(context.Background(), true, func(p *pluginpermission.Manager, c []pluginpermission.Candidate, hash string, _ []inventory.Finding) error {
			p.Store.Hook = func(_ string, n int) error {
				if n == 1 {
					os.Exit(94)
				}
				return nil
			}
			_, err := p.Apply(context.Background(), i.Plan, i.Digest, c, hash)
			return err
		})
		t.Fatal("helper failed to exit", err)
		return
	}
	m, name := permissionFixture(t)
	p := permissionPlan(t, m, grantPermission(name))
	input, _ := json.Marshal(map[string]string{"Root": m.Store.Root, "Local": m.Store.Local, "Owner": m.Owner, "Scope": m.Scope, "Plan": p.Plan.ID, "Digest": p.Plan.Digest})
	cmd := exec.Command(os.Args[0], "-test.run=^TestMCPPermissionRecoveryAfterProcessExit$")
	cmd.Env = append(os.Environ(), "AIDOCK_PERMISSION_CRASH_HELPER=1", "AIDOCK_PERMISSION_CRASH_INPUT="+string(input))
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 94 {
		t.Fatalf("crash helper %s %v", out, err)
	}
	fresh, _ := New(m.Store.Root, m.Store.Local, m.Owner, m.Scope)
	s, err := fresh.MCPPermission(context.Background())
	if err != nil || !s.Recovery {
		t.Fatal(s, err)
	}
	write(t, filepath.Join(m.Store.Root, "plugins.lock"), []byte("malformed: ["))
	n, err := fresh.RecoverMCPPermission(context.Background())
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	s, err = fresh.MCPPermission(context.Background())
	if err != nil || s.Recovery || len(s.Authorizations) > 0 || s.AuditCount != 0 {
		t.Fatal(s, err)
	}
}

func TestMCPPermissionSavedApprovalBecomesStaleAndCannotRestoreAfterConfigurationChange(t *testing.T) {
	m, name := permissionFixture(t)
	ctx := context.Background()
	permissionApply(t, m, permissionPlan(t, m, grantPermission(name)))
	revoked := permissionPlan(t, m, pluginpermission.Edit{Action: "revoke", Name: name})
	permissionApply(t, m, revoked)
	write(t, filepath.Join(m.Store.Root, "source-note.txt"), []byte("configuration changed"))
	restore := grantPermission("")
	restore.Action = "rollback"
	restore.Snapshot = revoked.Plan.ID
	s, _ := m.MCPPermission(ctx)
	restore.ExpectedSHA256 = s.SHA256
	restore.ExpectedReviewSHA256 = s.ReviewSHA256
	_, err := m.MCPPermissionPlan(ctx, restore)
	code(t, err, "SNAPSHOT_DEPENDENCY_MISSING")
	permissionApply(t, m, permissionPlan(t, m, grantPermission(name)))
	write(t, filepath.Join(m.Store.Root, "source-note.txt"), []byte("configuration changed again"))
	s, _ = m.MCPPermission(ctx)
	if s.Authorizations[0].Status != "STALE" {
		t.Fatal("saved approval remained current", s)
	}
	pm, _ := m.permissionManager()
	m.Copy = "another-copy-id"
	other, _ := m.permissionManager()
	if pm.Store.Root == other.Store.Root {
		t.Fatal("copy id reused authorization")
	}
	s, err = m.MCPPermission(ctx)
	if err != nil || len(s.Authorizations) != 0 {
		t.Fatal("other copy read authorization", s, err)
	}
}
