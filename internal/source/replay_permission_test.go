package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/workstore"
)

func replayPermissionFixture(t *testing.T) (*Manager, string, ReplayDraft) {
	t.Helper()
	m, name := permissionFixture(t)
	m.Copy = "22222222-2222-4222-8222-222222222222"
	permissionApply(t, m, permissionPlan(t, m, grantPermission(name)))
	bundle, tree, inputs := historicalObservation(t, m)
	apply(t, m, Request{Action: "create", Name: "unapplied-draft", Description: "CURRENT_PRIVATE_DRAFT"})
	d, err := m.DraftReplay(context.Background(), tree, bundle, inputs)
	if err != nil {
		t.Fatal(err)
	}
	return m, name, d
}

func TestReplayPermissionApprovesFutureConfigurationWithoutWritingSource(t *testing.T) {
	m, name, d := replayPermissionFixture(t)
	ctx := context.Background()
	before := capture(t, m)
	r, err := m.ReplayPermissions(ctx, d)
	if err != nil || len(r.Entries) != 1 || r.Entries[0].Name != name || !r.Entries[0].ReplacesGrant || r.Entries[0].Authorization == "CURRENT" {
		t.Fatal(r, err)
	}
	if strings.Contains(string(jsonBytes(r)), "CURRENT_PRIVATE_DRAFT") || strings.Contains(string(jsonBytes(r)), "独立夹具验证权限决策") {
		t.Fatal("review exposed source or historical purpose")
	}
	req := grantPermission(name)
	req.ExpectedSHA256, req.ExpectedReviewSHA256 = r.SHA256, r.ReviewSHA256
	p, err := m.PlanReplayPermission(ctx, d, req)
	if err != nil {
		t.Fatal(err)
	}
	read, err := m.ReplayPermissions(ctx, d)
	if err != nil || read.SHA256 != r.SHA256 {
		t.Fatal("preview changed authority", err)
	}
	if _, err = m.ApplyMCPPermission(ctx, p.Plan.ID, p.Plan.Digest); err == nil {
		t.Fatal("ordinary current-source confirmation consumed candidate grant")
	}
	if _, err = m.ApplyReplayPermission(ctx, d, name, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	read, err = m.ReplayPermissions(ctx, d)
	if err != nil || read.Entries[0].Authorization != "CURRENT" {
		t.Fatal("candidate authorization not current for candidate", read, err)
	}
	live, err := m.MCPPermission(ctx)
	if err != nil || len(live.Authorizations) != 1 || live.Authorizations[0].Status == "CURRENT" {
		t.Fatal("future grant became valid for unchanged current source", live, err)
	}
	unchangedManager(t, m, before)
	// Prove the authorization fingerprints exactly match the eventual two-root
	// projection, not a shadow store or a fabricated always-current status.
	local, _ := m.localProfileStore()
	c := workstore.Coordinator{Local: filepath.Join(m.Store.Local, "replay-test"), Identity: "replay-permission-test", Roots: []workstore.RootBinding{{Path: "source", Root: m.Store.Root}, {Path: "local", Root: local.Root}}, Blobs: m.Store}
	tx, err := c.PreviewObserved("apply", []workstore.BatchTarget{{Path: "source", Before: d.SourceBefore, After: d.SourceAfter}, {Path: "local", Before: d.LocalBefore, After: d.LocalAfter}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(ctx, tx.ID, tx.Digest); err != nil {
		t.Fatal(err)
	}
	live, err = m.MCPPermission(ctx)
	if err != nil || len(live.Authorizations) != 1 || live.Authorizations[0].Status != "CURRENT" || live.Authorizations[0].ExecutionReady {
		t.Fatal("grant does not match applied projection or claims execution", live, err)
	}
	if live.AuditCount != 2 {
		t.Fatal("candidate grant bypassed original audit", live.AuditCount)
	}
}

func TestReplayPermissionRejectsStaleDraftsWrongReviewAndMissingConsent(t *testing.T) {
	for _, kind := range []string{"source", "local", "review", "consent", "action", "digest"} {
		t.Run(kind, func(t *testing.T) {
			m, name, d := replayPermissionFixture(t)
			ctx := context.Background()
			r, err := m.ReplayPermissions(ctx, d)
			if err != nil {
				t.Fatal(err)
			}
			req := grantPermission(name)
			req.ExpectedSHA256, req.ExpectedReviewSHA256 = r.SHA256, r.ReviewSHA256
			switch kind {
			case "source":
				write(t, filepath.Join(m.Store.Root, "knowledge/new.md"), []byte("LATER_PRIVATE_CHANGE"))
			case "local":
				local, _ := m.localProfileStore()
				write(t, filepath.Join(local.Root, "profile.yaml"), []byte("BROKEN_PRIVATE_LOCAL"))
			case "review":
				req.ExpectedReviewSHA256 = strings.Repeat("0", 64)
			case "consent":
				req.ConfirmPermissions = false
			case "action":
				req.Action = "rollback"
			case "digest":
				d.Digest = strings.Repeat("0", 64)
			}
			if _, err = m.PlanReplayPermission(ctx, d, req); err == nil {
				t.Fatal("invalid candidate authorization accepted", kind)
			}
			p, _ := m.permissionManager()
			after, err := p.State(nil)
			if err != nil || after.SHA256 != r.SHA256 || after.AuditCount != 1 {
				t.Fatal("failed request changed authority", err)
			}
		})
	}
}

func TestReplayPermissionRevocationInvalidatesPreparedGrant(t *testing.T) {
	m, name, d := replayPermissionFixture(t)
	ctx := context.Background()
	r, err := m.ReplayPermissions(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	req := grantPermission(name)
	req.ExpectedSHA256, req.ExpectedReviewSHA256 = r.SHA256, r.ReviewSHA256
	p, err := m.PlanReplayPermission(ctx, d, req)
	if err != nil {
		t.Fatal(err)
	}
	permissionApply(t, m, permissionPlan(t, m, pluginpermission.Edit{Action: "revoke", Name: name}))
	_, err = m.ApplyReplayPermission(ctx, d, name, p.Plan.ID, p.Plan.Digest)
	code(t, err, "PLAN_STALE")
}

func TestReplayPermissionActualAuditDamageRemainsAnIntegrityFailure(t *testing.T) {
	m, name, d := replayPermissionFixture(t)
	ctx := context.Background()
	r, err := m.ReplayPermissions(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	req := grantPermission(name)
	req.ExpectedSHA256, req.ExpectedReviewSHA256 = r.SHA256, r.ReviewSHA256
	p, err := m.PlanReplayPermission(ctx, d, req)
	if err != nil {
		t.Fatal(err)
	}
	permission, _ := m.permissionManager()
	tree, err := permission.Store.Capture(permission.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	for path, entry := range tree.Entries {
		if strings.HasPrefix(path, "audit/") && !entry.Dir {
			body, err := permission.Store.Data(entry)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(permission.Store.Root, filepath.FromSlash(path))
			defer write(t, file, body)
			if err = os.Remove(file); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	_, err = m.ApplyReplayPermission(ctx, d, name, p.Plan.ID, p.Plan.Digest)
	code(t, err, "AUTHORIZATION_AUDIT_INVALID")
}

func TestReplayPermissionMissingInstallationIsVisibleAndCannotGrant(t *testing.T) {
	m, name, d := replayPermissionFixture(t)
	installEdit(t, m, plugininstall.Edit{Action: "remove", Name: name})
	r, err := m.ReplayPermissions(context.Background(), d)
	if err != nil || len(r.Entries) != 1 || r.Entries[0].Name != name || r.Entries[0].Installation != "MISSING" {
		t.Fatal("missing installation disappeared from review", r, err)
	}
	req := grantPermission(name)
	req.ExpectedSHA256, req.ExpectedReviewSHA256 = r.SHA256, r.ReviewSHA256
	if _, err = m.PlanReplayPermission(context.Background(), d, req); err == nil {
		t.Fatal("uninstalled candidate granted")
	}
}
