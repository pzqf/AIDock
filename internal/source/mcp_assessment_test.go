package source

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/pluginorigin"
	"github.com/pzqf/AIDock/internal/pluginsecurity"
	"github.com/pzqf/AIDock/internal/workstore"
)

func assessmentFixture(t *testing.T) (*Manager, pluginlock.Preparation, pluginsecurity.Authorities, pluginsecurity.Assessment, ed25519.PrivateKey) {
	t.Helper()
	m, req := installFixture(t)
	ctx := context.Background()
	root := filepath.Join(m.Store.Local, filepath.FromSlash(req.ArtifactPath))
	defs, _ := m.MCP()
	server := defs.Config["servers"].([]any)[0].(map[string]any)
	write(t, filepath.Join(root, "plugin.yaml"), jsonBytes(map[string]any{"schema_version": 1, "name": "publisher-package", "type": "mcp-server", "version": "1.2.3", "source": server["source"], "permissions": server["permissions"], "dependencies": []any{}}))
	write(t, filepath.Join(root, "package.json"), []byte(`{"name":"fixture","version":"1.2.3"}`))
	write(t, filepath.Join(root, "package-lock.json"), []byte(`{"lockfileVersion":3,"packages":{"":{"name":"fixture","version":"1.2.3"}}}`))
	refreshInstallLock(t, m, req)
	lock, _ := m.MCPLock(ctx)
	parsed, _ := pluginlock.Parse([]byte(lock.Document))
	subject := pluginorigin.For(parsed.Plugins[0])
	now := time.Now().UTC()
	pub, private, _ := ed25519.GenerateKey(nil)
	encoded := base64.StdEncoding.EncodeToString(pub)
	id, _ := pluginorigin.Fingerprint(encoded)
	keys := pluginorigin.Keys{Version: 1, Keys: []pluginorigin.Key{{ID: id, Label: "Fixture publisher", PublicKey: encoded, SourceType: subject.SourceType, SourceID: subject.SourceID, ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339), Enabled: true}}}
	saveOriginEdit(t, m, pluginorigin.Edit{Action: "keys", Keys: &keys})
	origin := pluginorigin.Envelope{Version: 1, KeyID: id, Payload: pluginorigin.Payload{Version: 1, Kind: "artifact-origin", SubjectSHA256: subject.SHA256, Publisher: "Fixture publisher", ReleaseURL: "https://example.invalid/source", IssuedAt: now.Add(-time.Hour).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), RuntimeVersionVerified: true}}
	origin.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, pluginorigin.SigningBytes(origin.Payload)))
	saveOriginEdit(t, m, pluginorigin.Edit{Action: "evidence", Evidence: string(pluginorigin.JSON(origin))})
	state, _ := m.MCPSecurity(ctx)
	payload := state.AssessmentSubjects[0]
	payload.Reference.Provider = "Independent fixture evaluator"
	payload.Reference.ReferenceURL = "https://example.invalid/assessment"
	payload.Reference.CollectedAt = now.Add(-time.Minute).Format(time.RFC3339)
	payload.Reference.ExpiresAt = now.Add(time.Hour).Format(time.RFC3339)
	for i := range payload.Reference.Packages {
		a := []pluginsecurity.Advisory{}
		payload.Reference.Packages[i].Advisories = &a
	}
	for _, c := range []*pluginsecurity.Claim{payload.Malware, payload.StaticAnalysis, payload.PublisherIdentity} {
		*c = pluginsecurity.Claim{Status: "PASS", Method: "Synthetic fixture evaluator 1.0.0", Detail: "Simulated test evidence only", EvidenceURL: "https://example.invalid/assessment"}
	}
	pub, private, _ = ed25519.GenerateKey(nil)
	encoded = base64.StdEncoding.EncodeToString(pub)
	id, _ = pluginorigin.Fingerprint(encoded)
	authorities := pluginsecurity.Authorities{Version: 1, Authorities: []pluginsecurity.Authority{{Key: pluginorigin.Key{ID: id, Label: "Independent fixture", PublicKey: encoded, SourceType: subject.SourceType, SourceID: subject.SourceID, ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339), Enabled: true}, Provider: payload.Reference.Provider, VerificationURL: "https://example.invalid/key"}}}
	a := pluginsecurity.Assessment{Version: 1, KeyID: id, Payload: payload}
	a.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, pluginsecurity.AssessmentSigningBytes(payload)))
	return m, req, authorities, a, private
}
func TestMCPAssessmentL2InstallationStandardPathRiskDowngradeAndBytes(t *testing.T) {
	m, req, keys, a, private := assessmentFixture(t)
	ctx := context.Background()
	before, _ := m.Store.Capture(m.Store.Root)
	artifactBefore, _, _ := pluginlock.Observe(ctx, filepath.Join(m.Store.Local, filepath.FromSlash(req.ArtifactPath)))
	securitySave(t, m, pluginsecurity.Edit{Action: "authorities", Authorities: &keys, AuthorityConfirmation: true, AuthorityReason: "Independently checked fixture identity and key"})
	securitySave(t, m, pluginsecurity.Edit{Action: "assessment-import", Document: string(pluginsecurity.JSON(a))})
	lock, err := m.MCPLock(ctx)
	if err != nil || lock.Plugins[0].TrustLevel != "L2" || lock.Plugins[0].Status != "PASS" || lock.ExecutionReady {
		t.Fatal(lock, err)
	}
	prior, _ := m.MCPInstall(ctx)
	old, err := m.MCPInstallPlan(ctx, plugininstall.Edit{Action: "install", Name: req.Name, ExpectedSHA256: prior.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	a.Payload.StaticAnalysis.Method += "; revised evaluator method"
	a.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, pluginsecurity.AssessmentSigningBytes(a.Payload)))
	securitySave(t, m, pluginsecurity.Edit{Action: "assessment-import", Document: string(pluginsecurity.JSON(a))})
	_, err = m.ApplyMCPInstall(ctx, old.Plan.ID, old.Plan.Digest)
	code(t, err, "PLAN_STALE")
	prior, _ = m.MCPInstall(ctx)
	old, err = m.MCPInstallPlan(ctx, plugininstall.Edit{Action: "install", Name: req.Name, ExpectedSHA256: prior.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	origin, _ := m.MCPOrigin(ctx)
	origin.Keys.Keys[0].Label += " rechecked"
	saveOriginEdit(t, m, pluginorigin.Edit{Action: "keys", Keys: &origin.Keys})
	_, err = m.ApplyMCPInstall(ctx, old.Plan.ID, old.Plan.Digest)
	code(t, err, "PLAN_STALE")
	installed := installEdit(t, m, plugininstall.Edit{Action: "install", Name: req.Name})
	entry, err := m.MCPInstalledSBOM(ctx, req.Name)
	if err != nil || entry.Record.TrustLevel != "L2" || entry.Record.RiskReason != "" || entry.Status != "CURRENT" || entry.ExecutionReady {
		t.Fatal(entry, err)
	}
	removed := installEdit(t, m, plugininstall.Edit{Action: "remove", Name: req.Name})
	installEdit(t, m, plugininstall.Edit{Action: "rollback", Snapshot: removed.Plan.ID})
	installEdit(t, m, plugininstall.Edit{Action: "rollback", Snapshot: installed.Plan.ID})
	a.Payload.StaticAnalysis.Status = "UNKNOWN"
	a.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, pluginsecurity.AssessmentSigningBytes(a.Payload)))
	securitySave(t, m, pluginsecurity.Edit{Action: "assessment-import", Document: string(pluginsecurity.JSON(a))})
	state, _ := m.MCPInstall(ctx)
	_, err = m.MCPInstallPlan(ctx, plugininstall.Edit{Action: "install", Name: req.Name, ExpectedSHA256: state.SHA256})
	code(t, err, "INSTALL_RISK_CONFIRMATION_REQUIRED")
	installEdit(t, m, allowInstall(req.Name))
	entry, _ = m.MCPInstalledSBOM(ctx, req.Name)
	if entry.Record.TrustLevel != "L1" {
		t.Fatal(entry)
	}
	a.Payload.Malware.Status = "FAIL"
	a.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, pluginsecurity.AssessmentSigningBytes(a.Payload)))
	securitySave(t, m, pluginsecurity.Edit{Action: "assessment-import", Document: string(pluginsecurity.JSON(a))})
	state, _ = m.MCPInstall(ctx)
	edit := allowInstall(req.Name)
	edit.ExpectedSHA256 = state.SHA256
	_, err = m.MCPInstallPlan(ctx, edit)
	code(t, err, "INSTALL_CHECK_FAILED")
	after, _ := m.Store.Capture(m.Store.Root)
	artifactAfter, _, _ := pluginlock.Observe(ctx, filepath.Join(m.Store.Local, filepath.FromSlash(req.ArtifactPath)))
	if workstore.Digest(before) != workstore.Digest(after) || pluginlock.Digest(artifactBefore) != pluginlock.Digest(artifactAfter) {
		t.Fatal("assessment or registration wrote source/artifact")
	}
}
func TestMCPAssessmentAuthorityChangeInvalidatesInstallAndRollbackRepairsCorruptCache(t *testing.T) {
	m, req, keys, a, _ := assessmentFixture(t)
	ctx := context.Background()
	enrolled := securitySave(t, m, pluginsecurity.Edit{Action: "authorities", Authorities: &keys, AuthorityConfirmation: true, AuthorityReason: "Independent fixture identity check"})
	saved := securitySave(t, m, pluginsecurity.Edit{Action: "assessment-import", Document: string(pluginsecurity.JSON(a))})
	state, _ := m.MCPInstall(ctx)
	old, err := m.MCPInstallPlan(ctx, plugininstall.Edit{Action: "install", Name: req.Name, ExpectedSHA256: state.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	keys.Authorities[0].Enabled = false
	disabled := securitySave(t, m, pluginsecurity.Edit{Action: "authorities", Authorities: &keys, AuthorityConfirmation: true, AuthorityReason: "Disable fixture authority"})
	_, err = m.ApplyMCPInstall(ctx, old.Plan.ID, old.Plan.Digest)
	code(t, err, "PLAN_STALE")
	lock, _ := m.MCPLock(ctx)
	if lock.Plugins[0].TrustLevel != "L0" || lock.Plugins[0].Status != "FAIL" {
		t.Fatal(lock)
	}
	securitySave(t, m, pluginsecurity.Edit{Action: "rollback", Snapshot: disabled.Plan.ID, AuthorityConfirmation: true, AuthorityReason: "Independently rechecked before restoring authority"})
	s, _ := m.MCPSecurity(ctx)
	if err = os.WriteFile(filepath.Join(s.Root, "authorities.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	s, _ = m.MCPSecurity(ctx)
	if len(s.Results) != 0 || len(s.Checks) == 0 {
		t.Fatal(s)
	}
	_, err = m.MCPSecurityPlan(ctx, pluginsecurity.Edit{Action: "rollback", Snapshot: saved.Plan.ID, ExpectedSHA256: s.SHA256})
	code(t, err, "ASSESSMENT_AUTHORITY_CONFIRMATION_REQUIRED")
	securitySave(t, m, pluginsecurity.Edit{Action: "rollback", Snapshot: saved.Plan.ID, AuthorityConfirmation: true, AuthorityReason: "Restore independently checked historical authority after corrupt cache"})
	s, _ = m.MCPSecurity(ctx)
	if len(s.Authorities.Authorities) != 1 || len(s.AssessmentDocuments) != 0 {
		t.Fatal(s)
	}
	// Every authority transition retains its explicit request, including rollback.
	var got bool
	for _, h := range s.History {
		if h.ID == enrolled.Plan.ID && h.Request.AuthorityConfirmation {
			got = true
		}
	}
	if !got {
		t.Fatal("authority confirmation missing from history")
	}
}
