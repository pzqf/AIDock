package source

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/runtimecheck"
	"github.com/pzqf/AIDock/internal/workstore"
)

func replayRuntimeFixture(t *testing.T) (*Manager, string, ReplayDraft) {
	t.Helper()
	m, request := sessionFixture(t)
	l := rulesLibrary(t, m)
	root := l.Store.Root
	saveLocal(t, m, ProfileEdit{Profile: ruleProfile("local", "PRIVATE_FROZEN_LOCAL"), LibraryRoot: &root})
	m = reloadCopy(t, m)
	bundle, tree, inputs := historicalObservation(t, m)
	apply(t, m, Request{Action: "create", Name: "current-draft", Description: "PRIVATE_CURRENT_SOURCE"})
	d, err := m.DraftReplay(context.Background(), tree, bundle, inputs)
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.ReplayPermissions(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	req := grantPermission(request.Name)
	req.ConfirmTools, req.ConfirmUnisolated = true, true
	req.ExpectedSHA256, req.ExpectedReviewSHA256 = r.SHA256, r.ReviewSHA256
	p, err := m.PlanReplayPermission(context.Background(), d, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyReplayPermission(context.Background(), d, request.Name, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	return m, request.Name, d
}

func candidateVersion(t *testing.T, m *Manager, d ReplayDraft, name string) runtimecheck.Event {
	t.Helper()
	r, err := m.ReplayRuntime(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	e, err := m.CheckReplayRuntime(context.Background(), d, MCPRuntimeRequest{Name: name, Probe: true, ExpectedSHA256: r.PermissionSHA256, ExpectedReviewSHA256: r.ReviewSHA256})
	if err != nil || e.Code != "RUNTIME_VERSION_MATCH" {
		t.Fatal(e, err)
	}
	return e
}

func TestReplayRuntimeFixedProbeAndFutureClientBindingMatchesAppliedProjection(t *testing.T) {
	m, name, d := replayRuntimeFixture(t)
	ctx := context.Background()
	before := capture(t, m)
	local, _ := m.localProfileStore()
	lb, _ := local.Capture(local.Root)
	permission, _ := m.ReplayPermissions(ctx, d)
	executable, home := filepath.Join(t.TempDir(), "aidock.exe"), filepath.Dir(m.Store.Local)
	called := false
	consume := func([]MCPClientConfiguration, []MCPSessionRequest) error { called = true; return nil }
	if err := m.WithReplayMCPClientConfigs(ctx, d, executable, home, consume); err == nil || called {
		t.Fatal("old runtime proof used for a replacement grant", err)
	}
	read, err := m.ReplayRuntime(ctx, d)
	if err != nil || read.Runtime.AuditCount != 2 || strings.Contains(string(jsonBytes(read)), "PRIVATE_") {
		t.Fatal(read, err)
	}
	for _, e := range read.Runtime.Entries {
		if e.Current {
			t.Fatal("old runtime record is current for new candidate grant")
		}
	}
	t.Setenv("NODE_OPTIONS", "--require=must-not-inherit")
	event := candidateVersion(t, m, d, name)
	var configs []MCPClientConfiguration
	var sessions []MCPSessionRequest
	err = m.WithReplayMCPClientConfigs(ctx, d, executable, home, func(c []MCPClientConfiguration, s []MCPSessionRequest) error { configs, sessions = c, s; return nil })
	if err != nil || len(configs) != 1 || len(sessions) != 1 || sessions[0].Probe || sessions[0].ExpectedRuntimeAttempt != event.Attempt {
		t.Fatal(configs, sessions, err)
	}
	if strings.Contains(strings.Join(configs[0].Args, " "), "--library-root") || m.Library == nil {
		t.Fatal("future args retained current library binding or modified manager")
	}
	if _, err = m.MCPClientConfig(ctx, sessions[0], executable, home); err == nil {
		t.Fatal("candidate args accepted for different current source")
	}
	live, err := m.MCPRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range live.Entries {
		if e.Current {
			t.Fatal("candidate probe reported current for current different source")
		}
	}
	unchangedManager(t, m, before)
	la, _ := local.Capture(local.Root)
	if workstore.Digest(lb) != workstore.Digest(la) {
		t.Fatal("candidate checks wrote Local profile")
	}
	pafter, _ := m.ReplayPermissions(ctx, d)
	if pafter.SHA256 != permission.SHA256 {
		t.Fatal("candidate checks changed grants")
	}
	// Actual two-root projection: the ordinary client gate must reproduce exact
	// argv using the future binding, without accepting any shadow authority.
	c := workstore.Coordinator{Local: filepath.Join(m.Store.Local, "replay-runtime-test"), Identity: "replay-runtime-test", Roots: []workstore.RootBinding{{Path: "source", Root: m.Store.Root}, {Path: "local", Root: local.Root}}, Blobs: m.Store}
	tx, err := c.PreviewObserved("apply", []workstore.BatchTarget{{Path: "source", Before: d.SourceBefore, After: d.SourceAfter}, {Path: "local", Before: d.LocalBefore, After: d.LocalAfter}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(ctx, tx.ID, tx.Digest); err != nil {
		t.Fatal(err)
	}
	future := reloadCopy(t, m)
	actual, err := future.MCPClientConfig(ctx, sessions[0], executable, home)
	if err != nil || !reflect.DeepEqual(actual.Args, configs[0].Args) || actual.Command != configs[0].Command || actual.Binding != configs[0].Binding {
		t.Fatal("ordinary client configuration differs after projection", actual, err)
	}
}

func TestReplayRuntimeRejectsRevocationNewFailureForeignCopyAndStaleInputs(t *testing.T) {
	for _, kind := range []string{"revoked", "new-failure", "copy", "source", "local", "digest", "probe", "review"} {
		t.Run(kind, func(t *testing.T) {
			m, name, d := replayRuntimeFixture(t)
			candidateVersion(t, m, d, name)
			r, err := m.ReplayRuntime(context.Background(), d)
			if err != nil {
				t.Fatal(err)
			}
			req := MCPRuntimeRequest{Name: name, Probe: true, ExpectedSHA256: r.PermissionSHA256, ExpectedReviewSHA256: r.ReviewSHA256}
			switch kind {
			case "revoked":
				permissionApply(t, m, permissionPlan(t, m, pluginpermission.Edit{Action: "revoke", Name: name}))
			case "new-failure":
				runtime, _ := m.runtimeManager()
				if _, err = runtime.Probe(context.Background(), r.Runtime.Entries[0].Event.Input, func() (string, string) { return "RUNTIME_CHECK_FAILED", "" }); err != nil {
					t.Fatal(err)
				}
			case "copy":
				m.Copy = "other-copy"
			case "source":
				write(t, filepath.Join(m.Store.Root, "knowledge/new.md"), []byte("new private draft"))
			case "local":
				local, _ := m.localProfileStore()
				write(t, filepath.Join(local.Root, "profile.yaml"), []byte("changed local"))
			case "digest":
				d.Digest = strings.Repeat("0", 64)
			case "probe":
				req.Probe = false
			case "review":
				req.ExpectedReviewSHA256 = strings.Repeat("0", 64)
			}
			if kind == "probe" || kind == "review" {
				if _, err = m.CheckReplayRuntime(context.Background(), d, req); err == nil {
					t.Fatal("invalid fixed probe accepted")
				}
				return
			}
			called := false
			err = m.WithReplayMCPClientConfigs(context.Background(), d, filepath.Join(t.TempDir(), "aidock.exe"), filepath.Dir(m.Store.Local), func([]MCPClientConfiguration, []MCPSessionRequest) error { called = true; return nil })
			if err == nil || called {
				t.Fatal("invalid candidate consumed or old success reused", kind, err)
			}
		})
	}
}

func TestReplayClientsEmptySetAndExplicitHome(t *testing.T) {
	m := manager(t)
	m.Copy = "copy"
	apply(t, m, Request{Action: "create", Name: "guide", Description: "guide"})
	bundle, tree, inputs := historicalObservation(t, m)
	d, err := m.DraftReplay(context.Background(), tree, bundle, inputs)
	if err != nil {
		t.Fatal(err)
	}
	executable, home := filepath.Join(t.TempDir(), "aidock.exe"), filepath.Dir(m.Store.Local)
	called := false
	err = m.WithReplayMCPClientConfigs(context.Background(), d, executable, home, func(c []MCPClientConfiguration, s []MCPSessionRequest) error {
		called = true
		if len(c) != 0 || len(s) != 0 {
			t.Fatal(c, s)
		}
		return nil
	})
	if err != nil || !called {
		t.Fatal(err)
	}
	if err = m.WithReplayMCPClientConfigs(context.Background(), d, executable, t.TempDir(), func([]MCPClientConfiguration, []MCPSessionRequest) error { t.Fatal("wrong home consumed"); return nil }); err == nil {
		t.Fatal("wrong home accepted")
	}
}
