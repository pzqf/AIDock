package source

import (
	"context"
	"encoding/json"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func secretSourceFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	m, req := installFixture(t)
	s, err := m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	server := s.Config["servers"].([]any)[0].(map[string]any)
	server["enabled"] = true
	server["secrets"] = []any{"api_key", "unused"}
	server["env"] = map[string]any{"SERVICE_TOKEN": "${SECRET:api_key}"}
	p, err := m.MCPPlan(context.Background(), MCPEdit{ExpectedSHA256: s.SHA256, Config: s.Config})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.Store.Local, filepath.FromSlash(req.ArtifactPath), "plugin.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	_ = json.Unmarshal(b, &manifest)
	manifest["secrets"] = server["secrets"]
	write(t, path, jsonBytes(manifest))
	refreshInstallLock(t, m, req)
	installEdit(t, m, allowInstall(req.Name))
	return m, req.Name
}
func TestMCPSecretPermissionRequiresSeparateConsentAndRecoveryReapproval(t *testing.T) {
	m, name := secretSourceFixture(t)
	ctx := context.Background()
	s, err := m.MCPPermission(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Candidates) != 1 || len(s.Candidates[0].Input.Secrets) != 1 || s.Candidates[0].Input.Secrets[0] != "api_key" {
		t.Fatal(s)
	}
	req := grantPermission(name)
	req.ExpectedSHA256 = s.SHA256
	req.ExpectedReviewSHA256 = s.ReviewSHA256
	_, err = m.MCPPermissionPlan(ctx, req)
	code(t, err, "AUTHORIZATION_SECRET_CONFIRMATION_REQUIRED")
	req.ConfirmSecrets = true
	permissionApply(t, m, permissionPlan(t, m, req))
	revoked := permissionPlan(t, m, pluginpermission.Edit{Action: "revoke", Name: name})
	permissionApply(t, m, revoked)
	req = grantPermission("")
	req.Action = "rollback"
	req.Snapshot = revoked.Plan.ID
	s, _ = m.MCPPermission(ctx)
	req.ExpectedSHA256 = s.SHA256
	req.ExpectedReviewSHA256 = s.ReviewSHA256
	_, err = m.MCPPermissionPlan(ctx, req)
	code(t, err, "AUTHORIZATION_SECRET_CONFIRMATION_REQUIRED")
	req.ConfirmSecrets = true
	permissionApply(t, m, permissionPlan(t, m, req))
}
func TestMCPEnvironmentGateNeedsEffectiveEnablementAndCurrentAuthorization(t *testing.T) {
	m, name := permissionFixture(t)
	ctx := context.Background()
	calls := 0
	consume := func([]string) error { calls++; return nil }
	err := m.WithMCPEnvironment(ctx, name, consume)
	code(t, err, "MCP_NOT_ENABLED")
	s, _ := m.MCP()
	s.Config["servers"].([]any)[0].(map[string]any)["enabled"] = true
	p, err := m.MCPPlan(ctx, MCPEdit{ExpectedSHA256: s.SHA256, Config: s.Config})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCP(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	// Definition change invalidates installation/approval. No callback can run.
	if err = m.WithMCPEnvironment(ctx, name, consume); err == nil || calls != 0 {
		t.Fatal("ungated handoff", err, calls)
	}
}
func TestCurrentPermissionLockSerializesRevocationAndRejectsStale(t *testing.T) {
	m, name := permissionFixture(t)
	ctx := context.Background()
	permissionApply(t, m, permissionPlan(t, m, grantPermission(name)))
	s, _ := m.MCPPermission(ctx)
	p, _ := m.permissionManager()
	calls := 0
	err := p.WithCurrent(ctx, nil, name, func(pluginpermission.Record) error { calls++; return nil })
	code(t, err, "MCP_AUTHORIZATION_REQUIRED")
	if calls != 0 {
		t.Fatal("stale delivery")
	}
	inside := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- p.WithCurrent(ctx, s.Candidates, name, func(pluginpermission.Record) error { close(inside); <-release; return nil })
	}()
	<-inside
	planned := make(chan error, 1)
	go func() {
		_, err := p.Plan(ctx, pluginpermission.Edit{Action: "revoke", Name: name, ExpectedSHA256: s.SHA256}, s.Candidates, s.Candidates[0].Input.ConfigurationSHA256)
		planned <- err
	}()
	var blocked error
	returned := false
	select {
	case blocked = <-planned:
		returned = true
	case <-time.After(40 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if returned {
		if blocked == nil {
			t.Fatal("revocation planning crossed held authorization lock")
		}
	} else if err := <-planned; err != nil {
		t.Fatal(err)
	}

}

func TestMCPParseEditRejectsDuplicateMappingWithoutEcho(t *testing.T) {
	for _, raw := range []string{`{"config":{"servers":[{"env":{"API_TOKEN":"private-sentinel","API_TOKEN":"other"}}]}}`, `{"Config":{}}`, `{"config":null}`, `{"private-sentinel":true}`} {
		_, err := ParseMCPEdit([]byte(raw))
		if err == nil || strings.Contains(err.Error(), "private-sentinel") {
			t.Fatal("ambiguous map or leaked value", err)
		}
	}
}
