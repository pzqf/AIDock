package native

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func publicationRestoreCandidateFixture(t *testing.T) (*Manager, PublicationRestorationCandidate, PublicationRestorationMaterial, *workstore.Coordinator, projectsnapshot.PublicationCommit, string, string) {
	t.Helper()
	ctx := context.Background()
	m, candidate, executable, home := nativePublicationMCPFixture(t)
	material, c, proof := compilePublicationFixture(t, m, candidate, executable, home)
	if _, err := material.Capture(ctx, proof); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Apply(ctx, proof.Transaction.ID, proof.Transaction.Digest); err != nil {
		t.Fatal(err)
	}
	rollback, err := c.Reverse(proof.Transaction.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(ctx, rollback.ID, rollback.Digest); err != nil {
		t.Fatal(err)
	}
	undo, err := c.Reverse(rollback.ID)
	if err != nil {
		t.Fatal(err)
	}
	review, err := m.InspectPublicationRestoration(ctx, proof, undo.ID, undo.Digest)
	if err != nil {
		t.Fatal(err)
	}
	old, err := m.PublicationRestoreCandidate(ctx, proof, undo.ID, undo.Digest, material.ActiveKey())
	if err != nil {
		t.Fatal(err)
	}
	return m, old, review, c, proof, executable, home
}

func TestPublicationRestorationCandidateReauthorizesExactOldContract(t *testing.T) {
	m, candidate, old, common, proof, executable, home := publicationRestoreCandidateFixture(t)
	ctx := context.Background()
	beforeSource, beforeLocal := snapshotDraftTrees(t, m)
	beforeNative := snapshotNativeTrees(t, m)
	if strings.TrimSpace(string(jsonData(candidate))) != "{}" || len(candidate.Tools()) != 1 || candidate.Tools()[0] != "codex-cli" {
		t.Fatal("candidate leaked raw material or tool selection")
	}
	tools := candidate.Tools()
	tools[0] = "caller-mutation"
	if candidate.Tools()[0] != "codex-cli" {
		t.Fatal("caller could change candidate")
	}
	_, err := m.PublicationRestoreCandidate(ctx, proof, candidate.plan.ID, candidate.plan.Digest, strings.Repeat("0", 64))
	code(t, err, "PUBLICATION_RESTORE_VIEW_NOT_SELECTED")
	_, err = candidate.PlanPermission(ctx, pluginpermission.Edit{Action: "revoke"})
	code(t, err, "INVALID_INPUT")
	_, err = candidate.Prepare(ctx, CheckRequest{Tools: []string{"claude-code"}}, executable, home)
	code(t, err, "PUBLICATION_RESTORE_TOOL_SELECTION_MISMATCH")
	artifacts, err := candidate.Artifacts(ctx)
	if err != nil || len(artifacts.Artifacts) != 1 || artifacts.Artifacts[0].Status != "MATCHED" {
		t.Fatal(artifacts, err)
	}
	installed, err := candidate.Installations(ctx)
	if err != nil || len(installed.Installations) != 1 {
		t.Fatal(err)
	}
	reinstall := plugininstall.Edit{Action: "install", Name: "project-memory", ExpectedSHA256: installed.SHA256}
	_, err = candidate.PlanInstallation(ctx, reinstall)
	code(t, err, "INSTALL_RISK_CONFIRMATION_REQUIRED")
	reinstall.AllowUnverified, reinstall.RiskReason = true, "原冻结合同的独立重装测试，不运行包"
	ip, err := candidate.PlanInstallation(ctx, reinstall)
	if err != nil || ip.Kind != "install" || !ip.RiskAccepted || ip.Candidate != candidate.Digest() {
		t.Fatal(ip, err)
	}
	alternateSeed, err := common.Reverse(candidate.plan.Reference)
	if err != nil {
		t.Fatal(err)
	}
	alternate, err := m.PublicationRestoreCandidate(ctx, proof, alternateSeed.ID, alternateSeed.Digest, candidate.view.Record.ActiveKey)
	if err != nil {
		t.Fatal(err)
	}
	_, err = alternate.ApplyInstallation(ctx, "project-memory", ip.ID, ip.Digest)
	code(t, err, "PLAN_STALE")
	if _, err = candidate.ApplyInstallation(ctx, "project-memory", ip.ID, ip.Digest); err != nil {
		t.Fatal(err)
	}
	if err = old.Recheck(ctx); err == nil {
		t.Fatal("reinstallation kept archived old launch authority current")
	}

	permissions, err := m.Source.MCPPermission(ctx)
	if err != nil {
		t.Fatal(err)
	}
	revoke, err := m.Source.MCPPermissionPlan(ctx, pluginpermission.Edit{Action: "revoke", Name: "project-memory", ExpectedSHA256: permissions.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.ApplyMCPPermission(ctx, revoke.Plan.ID, revoke.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	if err = old.Recheck(ctx); err == nil {
		t.Fatal("old launch authority survived revocation")
	}
	review, err := candidate.Permissions(ctx)
	if err != nil || len(review.Entries) != 1 || !review.Entries[0].Enabled {
		t.Fatal("old exact contract unavailable for review", err)
	}
	grant, err := candidate.PlanPermission(ctx, pluginpermission.Edit{Action: "grant", Name: "project-memory", ExpectedSHA256: review.SHA256, ExpectedReviewSHA256: review.ReviewSHA256, Purpose: "独立测试明确审查原冻结合同", DataLevel: "Public", Destination: "unknown-external-client", ConfirmPermissions: true, ConfirmTools: true, ConfirmUnisolated: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := common.Reverse(candidate.plan.Reference)
	if err != nil {
		t.Fatal(err)
	}
	otherCandidate, err := m.PublicationRestoreCandidate(ctx, proof, second.ID, second.Digest, candidate.view.Record.ActiveKey)
	if err != nil || otherCandidate.draft.Digest != candidate.draft.Digest {
		t.Fatal("fixture did not isolate common proposal binding from the same frozen contract", err)
	}
	_, err = otherCandidate.ApplyPermission(ctx, "project-memory", grant.ID, grant.Digest)
	code(t, err, "PLAN_STALE")
	if _, err = candidate.ApplyPermission(ctx, "project-memory", grant.ID, grant.Digest); err != nil {
		t.Fatal(err)
	}
	review, err = candidate.Permissions(ctx)
	if err != nil || review.Entries[0].Authorization != "CURRENT" {
		t.Fatal("new grant did not match original frozen contract", review.Entries, err)
	}
	checked, err := candidate.CheckRuntime(ctx, source.MCPRuntimeRequest{Name: "project-memory", Probe: true, ExpectedSHA256: review.SHA256, ExpectedReviewSHA256: review.ReviewSHA256})
	if err != nil || checked.Code != "RUNTIME_VERSION_MATCH" {
		t.Fatal(checked.Code, err)
	}
	if _, err = candidate.Runtime(ctx); err != nil {
		t.Fatal(err)
	}
	if err = old.Recheck(ctx); err == nil {
		t.Fatal("new grant resurrected original launch parameters")
	}

	// A new stable AIDock executable is allowed for the explicit regeneration;
	// it never makes the old executable hash or old grant current again.
	newExecutable := filepath.Join(t.TempDir(), "new-aidock.exe")
	write(t, newExecutable, []byte("new unexecuted restoration test host"))
	prepared, err := candidate.Prepare(ctx, CheckRequest{Probe: true, RuleProbe: true}, newExecutable, home)
	if err != nil || prepared.Status != "CHECKED" || !prepared.Fresh || !prepared.MCPReady || prepared.CanApply {
		t.Fatal("explicit full restore preparation failed", prepared.Status, prepared.Gates, err)
	}
	read, err := candidate.ReadPreparation(ctx, prepared.ID)
	if err != nil || !read.Fresh || read.Status != "CHECKED" {
		t.Fatal(read.Status, err)
	}
	if _, err = m.loadConfigurationPreparation(prepared.ID); err == nil {
		t.Fatal("ordinary configuration owner accepted restoration receipt")
	}
	if strings.Contains(string(jsonData(prepared)), "FROZEN_FUTURE_CONFIG") || strings.Contains(string(jsonData(prepared)), "NATIVE_PRIVATE_SENTINEL") {
		t.Fatal("preparation exposed raw config or native body")
	}
	s, l := snapshotDraftTrees(t, m)
	if workstore.Digest(s) != workstore.Digest(beforeSource) || workstore.Digest(l) != workstore.Digest(beforeLocal) || !reflectTreesEqual(beforeNative, snapshotNativeTrees(t, m)) {
		t.Fatal("review, grant or preparation wrote source/Local/native files")
	}
	write(t, newExecutable, []byte("changed after check"))
	read, err = candidate.ReadPreparation(ctx, prepared.ID)
	if err != nil || read.Fresh || read.Status != "STALE" || read.MCPReady || len(read.Changes) != 0 {
		t.Fatal("changed current launcher retained checked receipt", read.Status, err)
	}
	write(t, newExecutable, []byte("new unexecuted restoration test host"))
	material, err := m.CompilePublicationRestoration(ctx, proof, candidate.plan.ID, candidate.plan.Digest, []PublicationRestorationCheck{{Binding: candidate.view.Record.ActiveKey, ID: prepared.ID, Digest: prepared.Digest}}, newExecutable, home)
	if err != nil {
		t.Fatal("new grant did not compile into actual common restoration", err)
	}
	if strings.TrimSpace(string(jsonData(material))) != "{}" {
		t.Fatal("regeneration exposed raw compiler material")
	}
	byPath := map[string]workstore.BatchTarget{}
	for _, target := range material.Targets() {
		byPath[target.Path] = target
	}
	for _, target := range old.member.Targets {
		next := byPath[target.Path]
		if workstore.Digest(next.Before) != workstore.Digest(target.Before) || ((target.Path == "source" || target.Path == "local") && workstore.Digest(next.After) != workstore.Digest(target.After)) {
			t.Fatal("regeneration altered actual before-image or saved draft role", target.Path)
		}
	}
	targets := clonePublicationTargets(candidate.plan.Targets)
	prefix := projectsnapshot.PublicationMemberPrefix(m.Source.Copy)
	for i, target := range targets {
		if strings.HasPrefix(target.Path, prefix) {
			next := byPath[strings.TrimPrefix(target.Path, prefix)]
			next.Path = target.Path
			targets[i] = next
		}
	}
	revised, err := common.ReviseReversal(candidate.plan.ID, candidate.plan.Digest, targets)
	if err != nil {
		t.Fatal(err)
	}
	actual := proof
	actual.Transaction = revised
	records, err := material.Capture(ctx, actual)
	if err != nil || len(records) != 1 || records[0].Version != 5 || !records[0].RestoredDraft || records[0].Publication == nil {
		t.Fatal("regeneration lacks the actual common archive proof", err)
	}
	newKey := records[0].ActiveKey
	if newKey == candidate.view.Record.ActiveKey {
		t.Fatal("replaced grant/executable retained old application binding")
	}
	_, err = m.loadAppliedRecord(ctx, newKey)
	code(t, err, "APPLIED_VIEW_NOT_COMMITTED")
	common.Validate = func(tx workstore.BatchPlan) error {
		if err := common.CheckRestoration(candidate.plan.Reference, proofForNode(t, common, candidate.plan.Reference).Digest, nil, candidate.plan); err != nil {
			return err
		}
		return material.Validate(ctx, actual, false)
	}
	common.BeforeCommit = func(tx workstore.BatchPlan) error { return material.Validate(ctx, actual, true) }
	err = source.WithConfigurationTransactionLocks([]*source.Manager{m.Source}, []string{source.LocalProfileRoot(m.Source.Store.Local, m.Source.Owner, m.Source.Scope, m.Source.Copy)}, func() error {
		_, err := common.Apply(ctx, revised.ID, revised.Digest)
		return err
	})
	if err != nil {
		t.Fatal("regenerated common commit failed", err)
	}
	view, err := m.loadAppliedRecord(ctx, newKey)
	if err != nil || view.Record.Publication.Transaction.ID != revised.ID {
		t.Fatal("regenerated view did not reference actual committed shared node", err)
	}
	assertPublicationClientFrozen(t, m, newKey, view.Plan.Request.Sessions[0], newExecutable)
	if err := old.Recheck(ctx); err == nil {
		t.Fatal("old authority became valid after new shared commit")
	}
	if history, err := m.ProfileApplicationHistory(); err != nil || len(history) != 0 {
		t.Fatal("review fabricated independent member journal", history, err)
	}
	if history, err := m.snapshotReplayHistoryRecords(); err != nil || len(history) != 0 {
		t.Fatal("review fabricated independent project replay journal", history, err)
	}
}

func proofForNode(t *testing.T, c *workstore.Coordinator, id string) workstore.BatchPlan {
	t.Helper()
	plan, err := c.LoadPlan(id)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestPublicationRegeneratedRestorationFaultAndLateRevocation(t *testing.T) {
	for _, mode := range []string{"fault", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			m, candidate, _, common, proof, _, home := publicationRestoreCandidateFixture(t)
			ctx := context.Background()
			executable := filepath.Join(t.TempDir(), "new-aidock.exe")
			write(t, executable, []byte("independently checked new restoration binary"))
			prepared, err := candidate.Prepare(ctx, CheckRequest{Probe: true, RuleProbe: true}, executable, home)
			if err != nil || prepared.Status != "CHECKED" || !prepared.MCPReady {
				t.Fatal(prepared.Status, err)
			}
			material, err := m.CompilePublicationRestoration(ctx, proof, candidate.plan.ID, candidate.plan.Digest, []PublicationRestorationCheck{{Binding: candidate.view.Record.ActiveKey, ID: prepared.ID, Digest: prepared.Digest}}, executable, home)
			if err != nil {
				t.Fatal(err)
			}
			byPath := map[string]workstore.BatchTarget{}
			for _, target := range material.Targets() {
				byPath[projectsnapshot.PublicationMemberPrefix(m.Source.Copy)+target.Path] = target
			}
			targets := clonePublicationTargets(candidate.plan.Targets)
			for i, target := range targets {
				if next, ok := byPath[target.Path]; ok {
					next.Path = target.Path
					targets[i] = next
				}
			}
			revised, err := common.ReviseReversal(candidate.plan.ID, candidate.plan.Digest, targets)
			if err != nil {
				t.Fatal(err)
			}
			actual := proof
			actual.Transaction = revised
			records, err := material.Capture(ctx, actual)
			if err != nil || len(records) != 1 {
				t.Fatal(err)
			}
			common.Validate = func(workstore.BatchPlan) error { return material.Validate(ctx, actual, false) }
			common.BeforeCommit = func(workstore.BatchPlan) error { return material.Validate(ctx, actual, true) }
			if mode == "fault" {
				common.Hook = func(stage string, i int) error {
					if stage == "target" && i == 5 {
						return errors.New("owned generated restoration target failure")
					}
					return nil
				}
			} else {
				state, err := m.Source.MCPPermission(ctx)
				if err != nil {
					t.Fatal(err)
				}
				revoke, err := m.Source.MCPPermissionPlan(ctx, pluginpermission.Edit{Action: "revoke", Name: "project-memory", ExpectedSHA256: state.SHA256})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = m.Source.ApplyMCPPermission(ctx, revoke.Plan.ID, revoke.Plan.Digest); err != nil {
					t.Fatal(err)
				}
			}
			beforeSource, beforeLocal := snapshotDraftTrees(t, m)
			beforeNative := snapshotNativeTrees(t, m)
			err = source.WithConfigurationTransactionLocks([]*source.Manager{m.Source}, []string{source.LocalProfileRoot(m.Source.Store.Local, m.Source.Owner, m.Source.Scope, m.Source.Copy)}, func() error {
				_, err := common.Apply(ctx, revised.ID, revised.Digest)
				return err
			})
			if err == nil {
				t.Fatal("fault or late revocation was ignored")
			}
			s, l := snapshotDraftTrees(t, m)
			if workstore.Digest(s) != workstore.Digest(beforeSource) || workstore.Digest(l) != workstore.Digest(beforeLocal) || !reflectTreesEqual(beforeNative, snapshotNativeTrees(t, m)) {
				t.Fatal("failed regeneration left source/Local/native changes")
			}
			_, err = m.loadAppliedRecord(ctx, records[0].ActiveKey)
			code(t, err, "APPLIED_VIEW_NOT_COMMITTED")
			if err = common.Pending(); err != nil {
				t.Fatal("failure left pending common markers", err)
			}
		})
	}
}
