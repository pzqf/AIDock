package source

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestFrozenMCPUsesAppliedInputsWhileSavedSourceAndLocalDraftsDiverge(t *testing.T) {
	m, request := sessionFixture(t)
	ctx := context.Background()
	var frozen FrozenMCP
	if err := m.WithSnapshotInputs(func(e Export, s workstore.Tree, c SnapshotConfiguration) error {
		frozen = FrozenMCP{Tree: s, Effective: e, Configuration: c}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	apply(t, m, Request{Action: "create", Name: "later-draft", Description: "PRIVATE_CURRENT_DRAFT"})
	saveLocal(t, m, ProfileEdit{Profile: ruleProfile("local", "PRIVATE_CURRENT_LOCAL_RULE")})
	before := capture(t, m)
	local, _ := m.localProfileStore()
	localBefore, _ := local.Capture(local.Root)
	executable, home := filepath.Join(t.TempDir(), "aidock.exe"), filepath.Dir(m.Store.Local)
	if _, err := m.MCPClientConfig(ctx, request, executable, home); err == nil {
		t.Fatal("live-source path silently used an old authorization")
	}
	called := false
	err := m.WithFrozenMCPClientConfigs(ctx, frozen, []MCPSessionRequest{request}, executable, home, func(configs []MCPClientConfiguration) error {
		called = true
		if len(configs) != 1 || configs[0].Binding.Copy != m.Copy {
			t.Fatal(configs)
		}
		return nil
	})
	if err != nil || !called {
		t.Fatal("frozen applied authorization was replaced by later draft", err)
	}
	// A common coordinator already owns these exact roots. Its gate must not
	// reacquire them, and cannot omit an enabled MCP or accept unrelated bytes.
	err = m.Store.Locked(func() error {
		return local.Locked(func() error {
			if err := m.ValidateFrozenMCPTransaction(ctx, frozen, before, localBefore, nil); err == nil {
				t.Fatal("common transaction omitted enabled MCP")
			}
			if err := m.ValidateFrozenMCPTransaction(ctx, frozen, frozen.Tree, localBefore, []MCPSessionRequest{request}); err == nil {
				t.Fatal("common transaction used unrelated current source")
			}
			return m.ValidateFrozenMCPTransaction(ctx, frozen, before, localBefore, []MCPSessionRequest{request})
		})
	})
	if err != nil {
		t.Fatal("lock-owning common transaction failed current authority gate", err)
	}
	unchangedManager(t, m, before)
	localAfter, _ := local.Capture(local.Root)
	if workstore.Digest(localBefore) != workstore.Digest(localAfter) {
		t.Fatal("applied input gate changed Local draft")
	}
	permissionApply(t, m, permissionPlan(t, m, pluginpermission.Edit{Action: "revoke", Name: request.Name}))
	called = false
	err = m.WithFrozenMCPClientConfigs(ctx, frozen, []MCPSessionRequest{request}, executable, home, func([]MCPClientConfiguration) error { called = true; return nil })
	if err == nil || called {
		t.Fatal("frozen archive revived a revoked authorization")
	}
	err = m.Store.Locked(func() error {
		return local.Locked(func() error {
			return m.ValidateFrozenMCPTransaction(ctx, frozen, before, localBefore, []MCPSessionRequest{request})
		})
	})
	if err == nil {
		t.Fatal("common transaction revived revoked authorization")
	}
}
