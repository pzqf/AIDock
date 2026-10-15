package source

import (
	"context"
	"errors"
	"github.com/pzqf/AIDock/internal/credentials"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/workstore"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsMCPScopedCredentialHandoffAndRevocation(t *testing.T) {
	m, name := secretSourceFixture(t)
	ctx := context.Background()
	r := grantPermission(name)
	r.ConfirmSecrets = true
	permissionApply(t, m, permissionPlan(t, m, r))
	v, err := credentials.New(m.Store.Local)
	if err != nil {
		t.Fatal(err)
	}
	missingCalls := 0
	err = m.WithMCPEnvironment(ctx, name, func([]string) error { missingCalls++; return nil })
	code(t, err, "CREDENTIAL_MISSING")
	if missingCalls != 0 {
		t.Fatal("missing credential disclosed")
	}
	e, err := v.Change(ctx, credentials.Edit{Action: "add", Name: "api_key", Value: "DYNAMIC_TEST_INJECTION_PRIVATE_SENTINEL", Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, err := v.Change(ctx, credentials.Edit{Action: "delete", Name: e.Name, ExpectedRevision: e.Revision, ReferencesReviewed: true, Confirm: true})
		if err != nil {
			t.Error(err)
		}
	}()
	before, _ := m.Store.Capture(m.Store.Root)
	calls := 0
	consume := func(env []string) error {
		calls++
		if len(env) != 1 || env[0] != "SERVICE_TOKEN=DYNAMIC_TEST_INJECTION_PRIVATE_SENTINEL" {
			t.Fatal("wrong scoped environment")
		}
		return nil
	}
	if err = m.WithMCPEnvironment(ctx, name, consume); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	state, err := v.State(ctx)
	if err != nil || len(state.History) != 3 || state.History[2].Name != "api_key" || len(state.Pending) != 0 {
		t.Fatal(state, err)
	}
	// A different physical-copy approval cannot be inherited.
	originalCopy := m.Copy
	m.Copy = "another-independent-copy"
	err = m.WithMCPEnvironment(ctx, name, consume)
	m.Copy = originalCopy
	code(t, err, "MCP_AUTHORIZATION_REQUIRED")
	if calls != 1 {
		t.Fatal("cross-copy disclosure")
	}
	// Rotation changes the vault revision without changing the approved names.
	rotated, err := v.Change(ctx, credentials.Edit{Action: "rotate", Name: e.Name, ExpectedRevision: e.Revision, Value: "ROTATED_INJECTION_PRIVATE_SENTINEL", Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	e = rotated
	err = m.WithMCPEnvironment(ctx, name, func(env []string) error {
		if len(env) != 1 || env[0] != "SERVICE_TOKEN=ROTATED_INJECTION_PRIVATE_SENTINEL" {
			t.Fatal("did not use current revision")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err = v.State(ctx)
	if err != nil || state.History[len(state.History)-1].After != rotated.Revision {
		t.Fatal("revision missing from audit", err)
	}
	// A callback diagnostic containing the value is never propagated.
	err = m.WithMCPEnvironment(ctx, name, func([]string) error { return errors.New("DYNAMIC_TEST_INJECTION_PRIVATE_SENTINEL") })
	code(t, err, "CREDENTIAL_CONSUMER_FAILED")
	if strings.Contains(err.Error(), "PRIVATE_SENTINEL") {
		t.Fatal("leak")
	}
	revoked := permissionPlan(t, m, pluginpermission.Edit{Action: "revoke", Name: name})
	permissionApply(t, m, revoked)
	err = m.WithMCPEnvironment(ctx, name, consume)
	code(t, err, "MCP_AUTHORIZATION_REQUIRED")
	if calls != 1 {
		t.Fatal("revoked handoff")
	}
	after, _ := m.Store.Capture(m.Store.Root)
	if workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("handoff modified project")
	}
	filepath.WalkDir(m.Store.Local, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			t.Fatal(e)
		}
		if !d.IsDir() {
			b, e := os.ReadFile(p)
			if e != nil || strings.Contains(string(b), "PRIVATE_SENTINEL") {
				t.Fatal("value on disk", p)
			}
		}
		return nil
	})
}
