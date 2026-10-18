package source

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginartifact"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestFrozenReplaySetupImportsExactMissingVersionAndReplacesInstallationIndependently(t *testing.T) {
	m, c, archive, oldPath := candidateUpgradeFixture(t)
	ctx := context.Background()
	var frozen FrozenMCP
	if err := m.WithCandidateSnapshotInputs(ctx, c, func(e Export, tree workstore.Tree, cfg SnapshotConfiguration) error {
		frozen = FrozenMCP{Tree: tree, Effective: e, Configuration: cfg}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	d, err := m.FrozenReplayCandidate(ctx, frozen)
	if err != nil || d.Intent != "restore-applied" || d.Digest == c.Digest() {
		t.Fatal("frozen review lost exact old contract intent", err)
	}
	before := capture(t, m)
	local, _ := m.localProfileStore()
	localBefore, err := local.Capture(local.Root)
	if err != nil {
		t.Fatal(err)
	}
	old, err := m.Store.Capture(filepath.Join(m.Store.Local, oldPath))
	if err != nil {
		t.Fatal(err)
	}
	state, err := m.ReplayArtifacts(ctx, d)
	if err != nil || len(state.Artifacts) != 1 || state.Artifacts[0].Status != "MISSING" {
		t.Fatal(state, err)
	}
	edit := pluginartifact.Edit{Action: "import", Name: state.Artifacts[0].Name, ArchivePath: archive, ExpectedSHA256: state.Artifacts[0].SHA256, ExpectedLockSHA256: state.LockSHA256}
	p, err := m.PlanReplayArtifact(ctx, d, edit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyCandidateArtifact(ctx, c, edit.Name, p.Plan.ID, p.Plan.Digest); err == nil {
		t.Fatal("live future candidate consumed exact frozen restore setup")
	}
	if _, err = m.ApplyReplayArtifact(ctx, d, edit.Name, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	state, err = m.ReplayArtifacts(ctx, d)
	if err != nil || state.Artifacts[0].Status != "MATCHED" {
		t.Fatal(state, err)
	}
	installations, err := m.ReplayInstallations(ctx, d)
	if err != nil || installations.Installations[0].Status == "CURRENT" {
		t.Fatal(installations, err)
	}
	install := allowInstall(edit.Name)
	install.ExpectedSHA256 = installations.SHA256
	install.AllowUnverified = false
	_, err = m.PlanReplayInstallation(ctx, d, install)
	code(t, err, "INSTALL_RISK_CONFIRMATION_REQUIRED")
	install.AllowUnverified = true
	ip, err := m.PlanReplayInstallation(ctx, d, install)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPInstall(ctx, ip.Plan.ID, ip.Plan.Digest); err == nil {
		t.Fatal("ordinary source route consumed frozen installation plan")
	}
	if _, err = m.ApplyReplayInstallation(ctx, d, install.Name, ip.Plan.ID, ip.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	installations, err = m.ReplayInstallations(ctx, d)
	if err != nil || installations.Installations[0].Status != "CURRENT" || installations.Installations[0].Record.Plugin.Version != "2.0.0" || installations.ExecutionReady {
		t.Fatal(installations, err)
	}
	permissions, err := m.ReplayPermissions(ctx, d)
	if err != nil || permissions.Entries[0].Installation != "CURRENT" || permissions.Entries[0].Authorization == "CURRENT" {
		t.Fatal("installation restored permission without separate grant", err)
	}
	unchangedManager(t, m, before)
	localAfter, err := local.Capture(local.Root)
	if err != nil || workstore.Digest(localAfter) != workstore.Digest(localBefore) {
		t.Fatal("frozen setup changed current Local draft", err)
	}
	currentOld, err := m.Store.Capture(filepath.Join(m.Store.Local, oldPath))
	if err != nil || workstore.Digest(currentOld) != workstore.Digest(old) {
		t.Fatal("old package was overwritten", err)
	}
}
