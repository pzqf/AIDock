package native

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestFullPublicationMCPDeletionRemovesOwnedLauncherAndRestoresOriginalFrozenView(t *testing.T) {
	m, c, executable, home := nativePublicationMCPFixture(t)
	ctx := context.Background()
	first, original, proof := compilePublicationFixture(t, m, c, executable, home)
	if _, err := first.Capture(ctx, proof); err != nil {
		t.Fatal(err)
	}
	original.Validate = func(workstore.BatchPlan) error { return first.Validate(ctx, false) }
	original.BeforeCommit = func(workstore.BatchPlan) error { return first.Validate(ctx, true) }
	if _, err := original.Apply(ctx, proof.Transaction.ID, proof.Transaction.Digest); err != nil {
		t.Fatal(err)
	}
	old, err := m.loadAppliedRecord(ctx, first.ActiveKey())
	if err != nil {
		t.Fatal(err)
	}
	assertPublicationClientFrozen(t, m, first.ActiveKey(), first.prepared.Sessions[0], executable)
	permissionBefore, err := m.Source.MCPPermission(ctx)
	if err != nil {
		t.Fatal(err)
	}
	installBefore, err := m.Source.MCPInstall(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Definition deletion does not silently erase a configuration reference.
	// The saved draft is explicitly cleared before constructing the full batch.
	profile, err := m.Source.Profile()
	if err != nil {
		t.Fatal(err)
	}
	delete(profile.Profile, "config_overrides")
	clear, err := m.Source.ProfilePlan(ctx, source.ProfileEdit{ExpectedSHA256: profile.SHA256, Profile: profile.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.Apply(ctx, clear.Plan.ID, clear.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	state, err := m.Source.MCP()
	if err != nil {
		t.Fatal(err)
	}
	removal, err := m.Source.MCPPlan(ctx, source.MCPEdit{ExpectedSHA256: state.SHA256, ExpectedLock: state.LockSHA256, ExpectedRemovals: state.RemovalsSHA256, SharedRemovals: []string{"project-memory"}, Config: map[string]any{"schema_version": 1, "servers": []any{}}})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = m.Source.Profile()
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := m.Source.DraftConfigurationCandidate(ctx, removal.Plan.Before, removal.Plan.After, profile.Layers.Digest)
	if err != nil {
		t.Fatal(err)
	}
	beforeSource, beforeLocal := snapshotDraftTrees(t, m)
	beforeNative := snapshotNativeTrees(t, m)
	material, joint, removedProof := compilePublicationFixture(t, m, candidate, executable, home)
	if len(material.prepared.Sessions) != 0 || len(material.prepared.Report.MCP) != 0 {
		t.Fatal("deleted MCP remained an enabled candidate")
	}
	if _, err = material.Capture(ctx, removedProof); err != nil {
		t.Fatal(err)
	}
	if !reflectTreesEqual(beforeNative, snapshotNativeTrees(t, m)) {
		t.Fatal("deletion preparation applied native files")
	}
	joint.Validate = func(workstore.BatchPlan) error { return material.Validate(ctx, false) }
	joint.BeforeCommit = func(workstore.BatchPlan) error { return material.Validate(ctx, true) }
	if _, err = joint.Apply(ctx, removedProof.Transaction.ID, removedProof.Transaction.Digest); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(m.Root, ".codex/config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(actual), "--applied-key") || strings.Contains(string(actual), "aidock-project-memory") || !strings.Contains(string(actual), "MCP_NATIVE_PRIVATE_SENTINEL") {
		t.Fatal("deletion retained a managed launcher or erased handwritten fields")
	}
	called := false
	if err = m.withAppliedNative(ctx, old, first.prepared.Sessions[0], func() error { called = true; return nil }); err == nil || called {
		t.Fatal("removed native connection accepted the old launch parameters")
	}
	current, err := m.Source.MCP()
	if err != nil || len(current.Removals) != 1 || len(current.Servers) != 0 {
		t.Fatal(current, err)
	}
	permissionAfter, err := m.Source.MCPPermission(ctx)
	if err != nil || permissionAfter.SHA256 != permissionBefore.SHA256 || permissionAfter.AuditCount != permissionBefore.AuditCount {
		t.Fatal("deletion changed authorization history", err)
	}
	installAfter, err := m.Source.MCPInstall(ctx)
	if err != nil || installAfter.SHA256 != installBefore.SHA256 || installAfter.AuditCount != installBefore.AuditCount {
		t.Fatal("deletion uninstalled the retained package", err)
	}
	// Restoring the actual deletion batch must retain the old fixed config,
	// separately from the current shared draft that no longer has that value.
	reverse, err := joint.RestoreSelected(removedProof.Transaction.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	restore, err := m.InspectPublicationRestoration(ctx, removedProof, reverse.ID, reverse.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if len(restore.Views()) != 1 {
		t.Fatal("deletion backup lost the old fixed view")
	}
	if err = restore.Recheck(ctx); err != nil {
		t.Fatal(err)
	}
	joint.Validate = func(workstore.BatchPlan) error { return restore.Validate(ctx, false) }
	joint.BeforeCommit = func(workstore.BatchPlan) error { return restore.Validate(ctx, true) }
	if _, err = joint.Apply(ctx, reverse.ID, reverse.Digest); err != nil {
		t.Fatal(err)
	}
	if !reflectTreesEqual(beforeNative, snapshotNativeTrees(t, m)) {
		t.Fatal("rollback failed to restore native bytes")
	}
	nowSource, nowLocal := snapshotDraftTrees(t, m)
	if workstore.Digest(nowSource) != workstore.Digest(beforeSource) || workstore.Digest(nowLocal) != workstore.Digest(beforeLocal) {
		t.Fatal("rollback did not preserve the independently cleared draft/Local")
	}
	assertPublicationClientFrozen(t, m, first.ActiveKey(), first.prepared.Sessions[0], executable)
}
