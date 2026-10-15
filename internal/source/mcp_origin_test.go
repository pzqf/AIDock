package source

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/pluginorigin"
	"github.com/pzqf/AIDock/internal/workstore"
)

func saveOriginEdit(t *testing.T, m *Manager, req pluginorigin.Edit) pluginorigin.Preview {
	t.Helper()
	ctx := context.Background()
	s, err := m.MCPOrigin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	req.ExpectedSHA256 = s.SHA256
	p, err := m.MCPOriginPlan(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPOrigin(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestMCPOriginIntegratesLockDiagnosticsWithoutPortableTrustOrExecution(t *testing.T) {
	m := manager(t)
	ctx := context.Background()
	req := mcpLocalArtifact(t, m)
	inspected, err := m.InspectMCPLock(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	lp, err := m.MCPLockPlan(ctx, MCPLockEdit{ExpectedSHA256: inspected.ExpectedSHA256, Document: inspected.Document})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPLock(ctx, lp.Plan.ID, lp.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	lock, _ := pluginlock.Parse([]byte(inspected.Document))
	plugin := lock.Plugins[0]
	subject := pluginorigin.For(plugin)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	key := base64.StdEncoding.EncodeToString(pub)
	id, _ := pluginorigin.Fingerprint(key)
	now := time.Now().UTC()
	keys := pluginorigin.Keys{Version: 1, Keys: []pluginorigin.Key{{ID: id, Label: "Fixture local publisher", PublicKey: key, SourceType: subject.SourceType, SourceID: subject.SourceID, ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339), Enabled: true}}}
	before, _ := m.Store.Capture(m.Store.Root)
	saveOriginEdit(t, m, pluginorigin.Edit{Action: "keys", Keys: &keys})
	e := pluginorigin.Envelope{Version: 1, KeyID: id, Payload: pluginorigin.Payload{Version: 1, Kind: "artifact-origin", SubjectSHA256: subject.SHA256, Publisher: "Fixture", ReleaseURL: "https://example.invalid/releases/1.2.3", IssuedAt: now.Add(-time.Hour).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), RuntimeVersionVerified: true}}
	e.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, pluginorigin.SigningBytes(e.Payload)))
	saved := saveOriginEdit(t, m, pluginorigin.Edit{Action: "evidence", Evidence: string(pluginorigin.JSON(e))})
	after, _ := m.Store.Capture(m.Store.Root)
	if workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("origin wrote portable source")
	}
	s, err := m.MCPLock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, f := range s.Plugins[0].Checks {
		codes[f.Code] = true
	}
	if !codes["MCP_ORIGIN_SIGNATURE_VERIFIED"] || !codes["MCP_RUNTIME_PUBLISHER_ATTESTED"] || !codes["MCP_SECURITY_ASSESSMENT_MISSING"] || codes["MCP_SOURCE_TRUST_UNVERIFIED"] || codes["MCP_RUNTIME_VERSION_UNVERIFIED"] || s.Status != "UNKNOWN" || s.Plugins[0].TrustLevel != "L0" || s.ExecutionReady {
		t.Fatal(s)
	}
	// Physical source binding is stable even if callers omit the copy flag.
	m.Copy = "different-cli-copy-flag"
	state, err := m.MCPOrigin(ctx)
	if err != nil || state.Results[0].Status != "PASS" {
		t.Fatal(state, err)
	}
	other, err := New(m.Store.Root, t.TempDir(), m.Owner, m.Scope)
	if err != nil {
		t.Fatal(err)
	}
	state, err = other.MCPOrigin(ctx)
	if err != nil || len(state.Keys.Keys) != 0 || state.Results[0].Status != "UNKNOWN" {
		t.Fatal(state, err)
	}
	rolled := saveOriginEdit(t, m, pluginorigin.Edit{Action: "rollback", Snapshot: saved.Plan.ID})
	state, _ = m.MCPOrigin(ctx)
	if state.Results[0].Status != "UNKNOWN" || state.AuditCount != 3 {
		t.Fatal(state)
	}
	saveOriginEdit(t, m, pluginorigin.Edit{Action: "rollback", Snapshot: rolled.Plan.ID})
	state, _ = m.MCPOrigin(ctx)
	if state.Results[0].Status != "PASS" || state.AuditCount != 4 {
		t.Fatal(state)
	}
	keys.Keys[0].Enabled = false
	saveOriginEdit(t, m, pluginorigin.Edit{Action: "keys", Keys: &keys})
	s, _ = m.MCPLock(ctx)
	if s.Status != "FAIL" {
		t.Fatal("disabled key remained trusted", s)
	}
}

func TestMCPOriginSourceDriftAndCrossKindConfirmation(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "guide", Description: "fixture"})
	ctx := context.Background()
	state, _ := m.MCPOrigin(ctx)
	keys := pluginorigin.Keys{Version: 1, Keys: []pluginorigin.Key{}}
	p, err := m.MCPOriginPlan(ctx, pluginorigin.Edit{Action: "keys", Keys: &keys, ExpectedSHA256: state.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.ApplyMCPLock(ctx, p.Plan.ID, p.Plan.Digest)
	if err == nil {
		t.Fatal("origin plan accepted by lock confirmation")
	}
	apply(t, m, Request{Action: "create", Name: "another", Description: "source drift"})
	_, err = m.ApplyMCPOrigin(ctx, p.Plan.ID, p.Plan.Digest)
	code(t, err, "PLAN_STALE")
	saved := saveOriginEdit(t, m, pluginorigin.Edit{Action: "keys", Keys: &keys})
	_, err = m.Apply(ctx, saved.Plan.ID, saved.Plan.Digest)
	if err == nil {
		t.Fatal("origin plan accepted by source confirmation")
	}
	if err = os.WriteFile(filepath.Join(m.Store.Root, "plugins.lock"), []byte("broken ["), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = m.RecoverMCPOrigin(ctx); err != nil {
		t.Fatal("malformed portable lock blocked origin recovery", err)
	}
}
