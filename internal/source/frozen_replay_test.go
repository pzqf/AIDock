package source

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func TestFrozenRestoreReauthorizationUsesOriginalContractAndFreshRuntime(t *testing.T) {
	m, original := sessionFixture(t)
	ctx := context.Background()
	var f FrozenMCP
	if err := m.WithSnapshotInputs(func(e Export, s workstore.Tree, c SnapshotConfiguration) error {
		f = FrozenMCP{Tree: s, Effective: e, Configuration: c}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	apply(t, m, Request{Action: "create", Name: "new-saved-draft", Description: "PRIVATE_NEW_DRAFT"})
	saveLocal(t, m, ProfileEdit{Profile: ruleProfile("local", "PRIVATE_NEW_LOCAL")})
	grant := grantPermission(original.Name)
	grant.ConfirmTools = true
	grant.ConfirmUnisolated = true
	permissionApply(t, m, permissionPlan(t, m, grant))
	before := capture(t, m)
	local, _ := m.localProfileStore()
	localBefore, _ := local.Capture(local.Root)
	d, err := m.FrozenReplayCandidate(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if workstore.Digest(d.SourceAfter) != f.Effective.Snapshot || d.Layers.Digest != f.Effective.ConfigurationDigest || d.Intent != "restore-applied" {
		t.Fatal("restore changed original applied fingerprint")
	}
	review, err := m.ReplayPermissions(ctx, d)
	if err != nil || review.Entries[0].Authorization == "CURRENT" {
		t.Fatal("old replaced permission became current without consent", review, err)
	}
	grant.ExpectedSHA256, grant.ExpectedReviewSHA256 = review.SHA256, review.ReviewSHA256
	p, err := m.PlanReplayPermission(ctx, d, grant)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyReplayPermission(ctx, d, original.Name, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	state, err := m.ReplayRuntime(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	exe, home := filepath.Join(t.TempDir(), "aidock.exe"), filepath.Dir(m.Store.Local)
	if err = m.WithReplayMCPClientConfigs(ctx, d, exe, home, func([]MCPClientConfiguration, []MCPSessionRequest) error { return nil }); err == nil {
		t.Fatal("new approval reused old runtime attempt")
	}
	event, err := m.CheckReplayRuntime(ctx, d, MCPRuntimeRequest{Name: original.Name, Probe: true, ExpectedSHA256: state.PermissionSHA256, ExpectedReviewSHA256: state.ReviewSHA256})
	if err != nil || event.Code != "RUNTIME_VERSION_MATCH" {
		t.Fatal(event, err)
	}
	called := false
	err = m.WithReplayMCPClientConfigs(ctx, d, exe, home, func(_ []MCPClientConfiguration, requests []MCPSessionRequest) error {
		called = true
		if requests[0].ExpectedSHA256 == original.ExpectedSHA256 || requests[0].ExpectedRuntimeAttempt == original.ExpectedRuntimeAttempt {
			t.Fatal("restore reused old launch receipts")
		}
		return nil
	})
	if err != nil || !called {
		t.Fatal(err)
	}
	unchangedManager(t, m, before)
	after, _ := local.Capture(local.Root)
	if workstore.Digest(after) != workstore.Digest(localBefore) {
		t.Fatal("reauthorization applied saved draft")
	}
	live, err := m.MCPPermission(ctx)
	if err != nil || live.Authorizations[0].Status == "CURRENT" {
		t.Fatal("old applied approval authorized different saved draft", err)
	}
}

func TestFrozenRestoreProtectsCurrentDeclaredLibraryWithoutFollowingIt(t *testing.T) {
	m := manager(t)
	m.Copy = "frozen-restore-copy"
	apply(t, m, Request{Action: "create", Name: "guide", Description: "fixture"})
	saveLocal(t, m, ProfileEdit{Profile: ruleProfile("local", "historical")})
	bundle, tree, cfg := historicalObservation(t, m)
	local, _ := m.localProfileStore()
	root := filepath.Join(filepath.Dir(m.Store.Root), ".agents")
	write(t, filepath.Join(local.Root, "binding.json"), m.localProfileBinding(root))
	d, err := m.FrozenReplayCandidate(context.Background(), FrozenMCP{Tree: tree, Effective: bundle, Configuration: cfg})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range d.Authorities {
		found = found || r == root
	}
	if !found {
		t.Fatal("current explicit library root lost during frozen restore review")
	}
	if _, err = m.FrozenReplayCandidate(context.Background(), FrozenMCP{Tree: tree, Effective: bundle, Configuration: cfg}); err != nil {
		t.Fatal("protection observation attempted to load nonexistent live library", err)
	}
}
