package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/workstore"
)

func historicalObservation(t *testing.T, m *Manager) (Export, workstore.Tree, SnapshotConfiguration) {
	t.Helper()
	var b Export
	var tree workstore.Tree
	var inputs SnapshotConfiguration
	if err := m.WithSnapshotInputs(func(e Export, s workstore.Tree, c SnapshotConfiguration) error { b, tree, inputs = e, s, c; return nil }); err != nil {
		t.Fatal(err)
	}
	return b, tree, inputs
}

func TestHistoricalExportUsesFrozenInputsAfterLiveLibraryAndLocalDamage(t *testing.T) {
	m := manager(t)
	created := apply(t, m, Request{Action: "create", Name: "guide", Description: "historical"})
	apply(t, m, Request{Action: "enable", ID: created.SkillID})
	m.Copy = "history-copy"
	l := rulesLibrary(t, m)
	root := l.Store.Root
	saveLocal(t, m, ProfileEdit{Profile: ruleProfile("local", "PRIVATE_LOCAL_HISTORY"), LibraryRoot: &root})
	m = reloadCopy(t, m)
	bundle, tree, inputs := historicalObservation(t, m)
	// The archive must reconstruct without parsing any of these live inputs.
	write(t, filepath.Join(l.Store.Root, "profile.yaml"), []byte("rules: [broken"))
	write(t, filepath.Join(inputs.Layers.LocalRoot, "profile.yaml"), []byte("name: [broken"))
	write(t, filepath.Join(m.Store.Root, "profile.yaml"), []byte("skills: [broken"))
	current, libBefore := capture(t, m), capture(t, l)
	localStore, _ := m.localProfileStore()
	localBefore, err := localStore.Capture(localStore.Root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.ExportHistorical(context.Background(), tree, bundle, inputs)
	if err != nil || !got.Preflight.Candidate || len(got.Packages) != 1 || strings.Join(got.Rules, ",") != strings.Join(bundle.Rules, ",") {
		t.Fatal(got.Preflight.Status, err)
	}
	unchangedManager(t, m, current)
	unchangedManager(t, l, libBefore)
	localAfter, err := localStore.Capture(localStore.Root)
	if err != nil || workstore.Digest(localBefore) != workstore.Digest(localAfter) {
		t.Fatal("historical reconstruction changed live local bytes", err)
	}
	wrong := bundle
	wrong.Copy = "foreign"
	if _, err = m.ExportHistorical(context.Background(), tree, wrong, inputs); err == nil {
		t.Fatal("foreign binding accepted")
	}
	wrong = bundle
	wrong.Rules = []string{"different"}
	if _, err = m.ExportHistorical(context.Background(), tree, wrong, inputs); err == nil {
		t.Fatal("archived effective rule mismatch accepted")
	}
}

func TestHistoricalExportRetainsAllPackageProvenanceOffline(t *testing.T) {
	m, l := manager(t), library(t)
	base := apply(t, l, Request{Action: "create", Name: "base-guide", Description: "base"})
	other := apply(t, l, Request{Action: "create", Name: "inherited-guide", Description: "inherited"})
	attach(t, m, l)
	for _, id := range []string{base.SkillID, other.SkillID} {
		apply(t, m, Request{Action: "inherit", ID: id, Revision: selectedSkill(t, l, id).Revision})
		apply(t, m, Request{Action: "enable", ID: id})
	}
	apply(t, m, Request{Action: "override", ID: base.SkillID, Revision: selectedSkill(t, l, base.SkillID).Revision})
	apply(t, m, Request{Action: "edit", ID: base.SkillID, Files: []Patch{{Path: "SKILL.md", Content: string(body("base-guide")) + "project override"}}})
	fork := apply(t, m, Request{Action: "fork", ID: base.SkillID, Revision: selectedSkill(t, m, base.SkillID).Revision, Name: "fork-guide"})
	apply(t, m, Request{Action: "enable", ID: fork.SkillID})
	_, binary := externalFixture(t, m)
	ext := apply(t, m, adoption(t, m))
	apply(t, m, Request{Action: "enable", ID: ext.SkillID})
	bundle, tree, inputs := historicalObservation(t, m)
	write(t, binary, []byte("external later draft"))
	if err := os.Remove(m.BridgeFile); err != nil {
		t.Fatal(err)
	}
	m.Library = nil
	before := capture(t, m)
	got, err := m.ExportHistorical(context.Background(), tree, bundle, inputs)
	if err != nil || len(got.Packages) != 4 {
		t.Fatal(len(got.Packages), err)
	}
	layers := map[string]int{}
	for _, p := range got.Packages {
		layers[p.Layer]++
	}
	if layers["inherited"] != 1 || layers["override"] != 1 || layers["fork"] != 1 || layers["external"] != 1 {
		t.Fatal(layers)
	}
	if len(got.Authorities) == 0 {
		t.Fatal("external authority protection lost when registration went offline")
	}
	unchangedManager(t, m, before)
	draft, err := m.DraftReplay(context.Background(), tree, bundle, inputs)
	if err != nil || workstore.Hash(jsonBytes(draft.Report.Effective)) != workstore.Hash(jsonBytes(bundle.ProfileReport.Effective)) {
		t.Fatal("offline inherited/override/fork/external projection changed effective semantics", err)
	}
}

func TestHistoricalExportRechecksCurrentPermissionAfterRevocation(t *testing.T) {
	m, name := permissionFixture(t)
	m.Copy = "history-copy"
	state, err := m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	state.Config["servers"].([]any)[0].(map[string]any)["enabled"] = true
	p, err := m.MCPPlan(context.Background(), MCPEdit{ExpectedSHA256: state.SHA256, Config: state.Config})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	permissionApply(t, m, permissionPlan(t, m, grantPermission(name)))
	bundle, tree, inputs := historicalObservation(t, m)
	permissionApply(t, m, permissionPlan(t, m, pluginpermission.Edit{Action: "revoke", Name: name}))
	wasAuthorized := false
	for _, f := range bundle.Preflight.ProfileChecks {
		wasAuthorized = wasAuthorized || f.Code == "MCP_PROJECT_AUTHORIZED"
	}
	if !wasAuthorized {
		t.Fatal("fixture was not authorized before revocation")
	}
	permissionBefore, err := m.MCPPermission(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.ExportHistorical(context.Background(), tree, bundle, inputs)
	if err != nil {
		t.Fatal(err)
	}
	authorized := false
	for _, f := range got.Preflight.ProfileChecks {
		authorized = authorized || f.Code == "MCP_PROJECT_AUTHORIZED"
	}
	if authorized {
		t.Fatal("historical authorization revived after current revocation")
	}
	permissionAfter, err := m.MCPPermission(context.Background())
	if err != nil || permissionBefore.SHA256 != permissionAfter.SHA256 || permissionBefore.AuditCount != permissionAfter.AuditCount || len(permissionAfter.Authorizations) != 0 {
		t.Fatal("historical assessment mutated authorization authority", err)
	}
}
