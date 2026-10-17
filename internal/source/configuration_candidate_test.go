package source

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginconfig"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestConfigurationCandidatePermissionRuntimeAndEventualProcessRetainLibrary(t *testing.T) {
	m, original := sessionFixtureWithManifest(t, func(body string) string {
		return `const c=JSON.parse(process.env.AIDOCK_PLUGIN_CONFIG||'null');if(!c||c.message!=='FUTURE_CONFIG_SENTINEL')process.exit(93);` + body
	}, func(manifest map[string]any) {
		manifest["config_delivery"] = pluginconfig.Delivery
		manifest["config_schema"] = map[string]any{"message": map[string]any{"type": "string", "default": "CURRENT_CONFIG_SENTINEL"}}
	})
	ctx := context.Background()
	lib := rulesLibrary(t, m)
	link := lib.Store.Root
	saveLocal(t, m, ProfileEdit{Profile: ruleProfile("local", "retain target Local"), LibraryRoot: &link})
	m = reloadCopy(t, m)
	s, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	s.Profile["config_overrides"] = map[string]any{original.Name: map[string]any{"message": "FUTURE_CONFIG_SENTINEL"}}
	p, err := m.ProfilePlan(ctx, ProfileEdit{ExpectedSHA256: s.SHA256, Profile: s.Profile})
	if err != nil {
		t.Fatal(err)
	}
	c, err := m.DraftConfigurationCandidate(ctx, p.Plan.Before, p.Plan.After, s.Layers.Digest)
	if err != nil {
		t.Fatal(err)
	}
	before := capture(t, m)
	r, err := m.CandidatePermissions(ctx, c)
	if err != nil || len(r.Entries) != 1 || !r.Entries[0].ReplacesGrant || r.Entries[0].Authorization == "CURRENT" {
		t.Fatal(r, err)
	}
	grant := grantPermission(original.Name)
	grant.ConfirmUnisolated = true
	grant.ConfirmTools = true
	grant.ExpectedSHA256, grant.ExpectedReviewSHA256 = r.SHA256, r.ReviewSHA256
	preview, err := m.PlanCandidatePermission(ctx, c, grant)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPPermission(ctx, preview.Plan.ID, preview.Plan.Digest); err == nil {
		t.Fatal("live-source route consumed a candidate grant")
	}
	if _, err = m.ApplyCandidatePermission(ctx, c, original.Name, preview.Plan.ID, preview.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	r, err = m.CandidatePermissions(ctx, c)
	if err != nil || r.Entries[0].Authorization != "CURRENT" {
		t.Fatal(r, err)
	}
	live, err := m.MCPPermission(ctx)
	if err != nil || live.Authorizations[0].Status == "CURRENT" {
		t.Fatal("future approval authorized the old config", err)
	}
	runtime, err := m.CheckCandidateRuntime(ctx, c, MCPRuntimeRequest{Name: original.Name, Probe: true, ExpectedSHA256: r.SHA256, ExpectedReviewSHA256: r.ReviewSHA256})
	if err != nil || runtime.Code != "RUNTIME_VERSION_MATCH" {
		t.Fatal(runtime, err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var requested MCPSessionRequest
	err = m.WithCandidateMCPClientConfigs(ctx, c, exe, filepath.Dir(m.Store.Local), func(configs []MCPClientConfiguration, requests []MCPSessionRequest) error {
		if len(configs) != 1 || len(requests) != 1 {
			t.Fatal(configs, requests)
		}
		found := false
		for i, arg := range configs[0].Args {
			if arg == "--library-root" && i+1 < len(configs[0].Args) && configs[0].Args[i+1] == lib.Store.Root {
				found = true
			}
		}
		if !found {
			t.Fatal("future target lost its live library binding", configs[0].Args)
		}
		requested = requests[0]
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	requested.Probe = true
	if _, err = m.CheckMCPSession(ctx, requested); err == nil {
		t.Fatal("future connection ran before source application")
	}
	unchangedManager(t, m, before)
	if _, err = m.Apply(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	result, err := m.CheckMCPSession(ctx, requested)
	if err != nil || result.Code != "MCP_SESSION_MATCH" {
		t.Fatal(result, err)
	}
	sessions, err := m.MCPSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(jsonBytes(sessions)), "FUTURE_CONFIG_SENTINEL") {
		t.Fatal("raw future config entered the session audit")
	}
}

func configurationCandidateFixture(t *testing.T) (*Manager, *Manager, ConfigurationCandidate) {
	t.Helper()
	m := manager(t)
	m.Copy = workstore.ID()
	apply(t, m, Request{Action: "create", Name: "guide", Description: "candidate"})
	lib := rulesLibrary(t, m)
	link := lib.Store.Root
	saveLocal(t, m, ProfileEdit{Profile: ruleProfile("local", "LOCAL_TARGET_RULE"), LibraryRoot: &link})
	m = reloadCopy(t, m)
	state, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	state.Profile["rules"] = []any{"FUTURE_SHARED_RULE"}
	p, err := m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile})
	if err != nil {
		t.Fatal(err)
	}
	c, err := m.DraftConfigurationCandidate(context.Background(), p.Plan.Before, p.Plan.After, state.Layers.Digest)
	if err != nil {
		t.Fatal(err)
	}
	return m, lib, c
}

func TestConfigurationCandidateRetainsDestinationLayersWithoutWriting(t *testing.T) {
	m, lib, c := configurationCandidateFixture(t)
	ctx := context.Background()
	sourceBefore := capture(t, m)
	ls, err := m.localProfileStore()
	if err != nil {
		t.Fatal(err)
	}
	localBefore, err := ls.Capture(ls.Root)
	if err != nil {
		t.Fatal(err)
	}
	review, err := m.CandidatePermissions(ctx, c)
	if err != nil || review.Candidate != c.Digest() || len(review.Entries) != 0 {
		t.Fatal(review, err)
	}
	err = m.WithCandidateExport(ctx, c, func(v Export) error {
		if !reflect.DeepEqual(v.Rules, []string{"全局默认", "FUTURE_SHARED_RULE", "LOCAL_TARGET_RULE"}) {
			t.Fatal(v.Rules)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = m.WithCandidateSnapshotInputs(ctx, c, func(v Export, tree workstore.Tree, cfg SnapshotConfiguration) error {
		if cfg.Layers.LibraryRoot != lib.Store.Root || cfg.Layers.Local == nil || workstore.Digest(cfg.LocalTree) != workstore.Digest(localBefore) {
			t.Fatal("destination association was projected away", cfg.Layers.LibraryRoot)
		}
		_, saved, e := m.load(&tree)
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(saved["rules"], []any{"FUTURE_SHARED_RULE"}) {
			t.Fatal("effective rules flattened into shared source", saved["rules"])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	unchangedManager(t, m, sourceBefore)
	localAfter, _ := ls.Capture(ls.Root)
	if workstore.Digest(localBefore) != workstore.Digest(localAfter) {
		t.Fatal("candidate changed Local source")
	}
	// A live library update expires this candidate even if project/Local bytes
	// did not move. Historical frozen replay intentionally has other semantics.
	body, err := os.ReadFile(filepath.Join(lib.Store.Root, "profile.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(lib.Store.Root, "profile.yaml"), append(body, []byte("\n# current library changed\n")...))
	_, err = m.CandidatePermissions(ctx, c)
	code(t, err, "PLAN_STALE")
	_, err = m.DraftConfigurationCandidate(ctx, c.draft.SourceBefore, c.draft.SourceAfter, c.draft.Layers.Digest)
	code(t, err, "PLAN_STALE")
	unchangedManager(t, m, sourceBefore)
}

func TestConfigurationCandidateRejectsChangedRootsAndForeignBinding(t *testing.T) {
	m, _, c := configurationCandidateFixture(t)
	ctx := context.Background()
	other := *m
	other.Copy = workstore.ID()
	_, err := other.CandidatePermissions(ctx, c)
	code(t, err, "PLAN_STALE")
	wrong := workstore.Clone(c.draft.SourceAfter)
	if err := m.Store.PutFile(&wrong, "project.yaml", jsonBytes(map[string]any{"schema_version": 1, "id": workstore.ID(), "name": "foreign"}), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = m.DraftConfigurationCandidate(ctx, c.draft.SourceBefore, wrong, c.draft.Layers.Digest)
	code(t, err, "SOURCE_IDENTITY_MISMATCH")
	write(t, filepath.Join(m.Store.Root, "unrelated-after-review.txt"), []byte("retain external change"))
	_, err = m.CandidatePermissions(ctx, c)
	code(t, err, "PLAN_STALE")
	_, err = m.DraftConfigurationCandidate(ctx, c.draft.SourceBefore, c.draft.SourceAfter, c.draft.Layers.Digest)
	code(t, err, "PLAN_STALE")
}
