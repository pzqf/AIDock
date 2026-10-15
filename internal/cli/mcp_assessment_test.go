package cli

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/pluginorigin"
	"github.com/pzqf/AIDock/internal/pluginsecurity"
	"github.com/pzqf/AIDock/internal/source"
)

func TestMCPAssessmentCLISignedL2StandardInstallAndRevocation(t *testing.T) {
	m, flags := mcpCLIArtifactFixture(t)
	ctx := context.Background()
	root := filepath.Join(m.Store.Local, "mcp-artifacts", "fixture")
	for p, b := range map[string]string{"package.json": `{"name":"fixture","version":"1.2.3"}`, "package-lock.json": `{"lockfileVersion":3,"packages":{"":{"name":"fixture","version":"1.2.3"}}}`} {
		if err := os.WriteFile(filepath.Join(root, p), []byte(b), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v, err := m.InspectMCPLock(ctx, pluginlock.Preparation{Name: "fixture", Version: "1.2.3", ArtifactPath: "mcp-artifacts/fixture", Runtime: pluginlock.Runtime{Kind: "native", Version: "1.2.3", Constraint: "=1.2.3", Executable: "helper"}})
	if err != nil {
		t.Fatal(err)
	}
	lp, err := m.MCPLockPlan(ctx, source.MCPLockEdit{ExpectedSHA256: v.ExpectedSHA256, Document: v.Document})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPLock(ctx, lp.Plan.ID, lp.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "request.json")
	call := func(args ...string) (int, map[string]any) {
		t.Helper()
		exit, out, _ := invoke(t, append(args, flags...)...)
		return exit, out
	}
	save := func(group string, req any) {
		t.Helper()
		b, _ := json.Marshal(req)
		if err := os.WriteFile(file, b, 0600); err != nil {
			t.Fatal(err)
		}
		exit, out := call("mcp", group, "plan", "--file", file)
		if exit != 0 {
			t.Fatal(exit, out)
		}
		p := out["items"].(map[string]any)["plan"].(map[string]any)
		exit, out = call("mcp", group, "apply", "--plan", p["id"].(string), "--expected-digest", p["digest"].(string))
		if exit != 0 {
			t.Fatal(exit, out)
		}
	}
	lock, _ := pluginlock.Parse([]byte(v.Document))
	subject := pluginorigin.For(lock.Plugins[0])
	pub, private, _ := ed25519.GenerateKey(nil)
	key := base64.StdEncoding.EncodeToString(pub)
	id, _ := pluginorigin.Fingerprint(key)
	now := time.Now().UTC()
	keys := pluginorigin.Keys{Version: 1, Keys: []pluginorigin.Key{{ID: id, Label: "CLI fixture publisher", PublicKey: key, SourceType: subject.SourceType, SourceID: subject.SourceID, ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339), Enabled: true}}}
	origin, _ := m.MCPOrigin(ctx)
	save("origin", pluginorigin.Edit{Action: "keys", ExpectedSHA256: origin.SHA256, Keys: &keys})
	e := pluginorigin.Envelope{Version: 1, KeyID: id, Payload: pluginorigin.Payload{Version: 1, Kind: "artifact-origin", SubjectSHA256: subject.SHA256, Publisher: "CLI fixture", ReleaseURL: "https://example.invalid/release", IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), RuntimeVersionVerified: true}}
	e.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, pluginorigin.SigningBytes(e.Payload)))
	origin, _ = m.MCPOrigin(ctx)
	save("origin", pluginorigin.Edit{Action: "evidence", ExpectedSHA256: origin.SHA256, Evidence: string(pluginorigin.JSON(e))})
	pub, private, _ = ed25519.GenerateKey(nil)
	key = base64.StdEncoding.EncodeToString(pub)
	id, _ = pluginorigin.Fingerprint(key)
	authorities := pluginsecurity.Authorities{Version: 1, Authorities: []pluginsecurity.Authority{{Key: pluginorigin.Key{ID: id, Label: "CLI independent evaluator", PublicKey: key, SourceType: subject.SourceType, SourceID: subject.SourceID, ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339), Enabled: true}, Provider: "CLI synthetic evaluator", VerificationURL: "https://example.invalid/key"}}}
	s, _ := m.MCPSecurity(ctx)
	save("security", pluginsecurity.Edit{Action: "authorities", ExpectedSHA256: s.SHA256, Authorities: &authorities, AuthorityConfirmation: true, AuthorityReason: "Independent verification of isolated CLI fixture"})
	s, _ = m.MCPSecurity(ctx)
	payload := s.AssessmentSubjects[0]
	payload.Reference.Provider = authorities.Authorities[0].Provider
	payload.Reference.ReferenceURL = "https://example.invalid/report"
	payload.Reference.CollectedAt = now.Add(-time.Minute).Format(time.RFC3339)
	payload.Reference.ExpiresAt = now.Add(time.Hour).Format(time.RFC3339)
	for i := range payload.Reference.Packages {
		a := []pluginsecurity.Advisory{}
		payload.Reference.Packages[i].Advisories = &a
	}
	for _, c := range []*pluginsecurity.Claim{payload.Malware, payload.StaticAnalysis, payload.PublisherIdentity} {
		*c = pluginsecurity.Claim{Status: "PASS", Method: "Synthetic CLI evaluator 1.0.0", Detail: "Test only, not actual assessment", EvidenceURL: "https://example.invalid/report"}
	}
	report := pluginsecurity.Assessment{Version: 1, KeyID: id, Payload: payload}
	report.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, pluginsecurity.AssessmentSigningBytes(payload)))
	save("security", pluginsecurity.Edit{Action: "assessment-import", ExpectedSHA256: s.SHA256, Document: string(pluginsecurity.JSON(report))})
	exit, out := call("mcp", "security", "check")
	if exit != 0 || out["items"].(map[string]any)["results"].([]any)[0].(map[string]any)["trust_level"] != "L2" {
		t.Fatal(exit, out)
	}
	_, out = call("mcp", "install", "state")
	sha := out["items"].(map[string]any)["sha256"].(string)
	save("install", map[string]any{"action": "install", "expected_sha256": sha, "name": "fixture"})
	exit, out = call("mcp", "install", "sbom", "--name", "fixture")
	if exit != 0 || out["items"].(map[string]any)["record"].(map[string]any)["trust_level"] != "L2" {
		t.Fatal(exit, out)
	}
	authorities.Authorities[0].Enabled = false
	s, _ = m.MCPSecurity(ctx)
	save("security", pluginsecurity.Edit{Action: "authorities", ExpectedSHA256: s.SHA256, Authorities: &authorities, AuthorityConfirmation: true, AuthorityReason: "Revoke fixture evaluator"})
	exit, out = call("mcp", "security", "check")
	if exit != 5 {
		t.Fatal(exit, out)
	}
}
