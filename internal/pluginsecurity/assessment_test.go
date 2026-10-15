package pluginsecurity

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/pluginorigin"
	"github.com/pzqf/AIDock/internal/workstore"
)

func assessed(t *testing.T, c Candidate, now time.Time) (Authorities, Assessment, ed25519.PrivateKey) {
	t.Helper()
	pub, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(pub)
	id, _ := pluginorigin.Fingerprint(encoded)
	kind, source := pluginorigin.Source(c.Plugin)
	keys := Authorities{Version: 1, Authorities: []Authority{{Key: pluginorigin.Key{ID: id, Label: "Independent fixture evaluator", PublicKey: encoded, SourceType: kind, SourceID: source, ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339), Enabled: true}, Provider: "Isolated reference fixture", VerificationURL: "https://example.invalid/evaluator-key"}}}
	claim := func() *Claim {
		return &Claim{Status: "PASS", Method: "Synthetic evaluator 1.0.0; fixture only", Detail: "Synthetic passing evidence, not a real scan", EvidenceURL: "https://example.invalid/report"}
	}
	p := AssessmentPayload{Version: 1, Format: "aidock-plugin-assessment-v1", Reference: snapshot(c, now), Coverage: AssessmentCoverage, Malware: claim(), StaticAnalysis: claim(), PublisherIdentity: claim()}
	a := Assessment{Version: 1, KeyID: id, Payload: p}
	signAssessment(&a, private)
	return keys, a, private
}
func signAssessment(a *Assessment, key ed25519.PrivateKey) {
	a.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, AssessmentSigningBytes(a.Payload)))
}
func TestAssessmentCumulativeTrustSourceCoverageAndNegativeEvidence(t *testing.T) {
	now := time.Date(2026, 10, 16, 1, 0, 0, 0, time.UTC)
	c := fixture()
	c.SourceVerified = true
	c.RuntimeVerified = true
	c.DependenciesVerified = true
	keys, a, private := assessed(t, c, now)
	if _, err := ParseAssessment(JSON(a)); err != nil {
		t.Fatal(err)
	}
	r := effective(c, nil, keys, &a, now)
	if r.Status != "PASS" || r.TrustLevel != "L2" || r.Assessment.Status != "PASS" || r.Covered != 2 || r.ReferenceSource != "signed-assessment" {
		t.Fatal(r)
	}
	for _, field := range []string{"origin", "runtime", "dependencies"} {
		t.Run(field, func(t *testing.T) {
			cc := c
			switch field {
			case "origin":
				cc.SourceVerified = false
			case "runtime":
				cc.RuntimeVerified = false
			case "dependencies":
				cc.DependenciesVerified = false
			}
			r := effective(cc, nil, keys, &a, now)
			want := "L0"
			if field == "dependencies" {
				want = "L1"
			}
			if r.TrustLevel != want {
				t.Fatal(r)
			}
		})
	}
	a.Payload.StaticAnalysis.Status = "UNKNOWN"
	signAssessment(&a, private)
	if r := effective(c, nil, keys, &a, now); r.TrustLevel != "L1" || r.Status != "UNKNOWN" {
		t.Fatal(r)
	}
	a.Payload.Malware.Status = "FAIL"
	signAssessment(&a, private)
	for _, at := range []time.Time{now, now.Add(48 * time.Hour)} {
		if r := effective(c, nil, keys, &a, at); r.Status != "FAIL" || r.TrustLevel != "L0" {
			t.Fatal(r)
		}
	}
	_, clean, _ := assessed(t, c, now)
	clean.KeyID = a.KeyID
	clean.Payload.Reference = a.Payload.Reference
	signAssessment(&clean, private)
	local := snapshot(c, now)
	advisories := []Advisory{{ID: "SIMULATED-RISK", Severity: "high", Summary: "Test fixture only", URL: "https://example.invalid/risk"}}
	local.Packages[0].Advisories = &advisories
	if r := effective(c, &local, keys, &clean, now); r.Status != "FAIL" || r.TrustLevel != "L0" || r.AdvisoryCount != 1 || r.ReferenceSource != "local-reference" {
		t.Fatal("signed clean evidence erased local risk", r)
	}
}
func TestAssessmentSignatureAuthorityTimeAndInputBoundaries(t *testing.T) {
	now := time.Now().UTC()
	c := fixture()
	c.SourceVerified = true
	c.RuntimeVerified = true
	c.DependenciesVerified = true
	for _, name := range []string{"tamper", "domain", "scope", "provider", "disabled", "expired", "unknown-key", "binding", "partial", "future", "expired-report", "malware-unknown", "static-fail"} {
		t.Run(name, func(t *testing.T) {
			keys, a, private := assessed(t, c, now)
			wantStatus, wantLevel := "FAIL", "L0"
			switch name {
			case "tamper":
				a.Payload.Malware.Detail = "tampered"
			case "domain":
				a.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, pluginorigin.SigningBytes(pluginorigin.Payload{Version: 1})))
			case "scope":
				keys.Authorities[0].SourceID = "other"
			case "provider":
				keys.Authorities[0].Provider = "other"
			case "disabled":
				keys.Authorities[0].Enabled = false
			case "expired":
				keys.Authorities[0].ExpiresAt = now.Add(-time.Hour).Format(time.RFC3339)
				wantStatus = "UNKNOWN"
			case "unknown-key":
				keys.Authorities = []Authority{}
				wantStatus = "UNKNOWN"
			case "binding":
				a.Payload.Reference.DefinitionSHA256 = workstore.Hash([]byte("permissions"))
				signAssessment(&a, private)
				wantStatus = "UNKNOWN"
			case "partial":
				a.Payload.Reference.Packages = a.Payload.Reference.Packages[:1]
				signAssessment(&a, private)
				wantStatus = "UNKNOWN"
			case "future":
				a.Payload.Reference.CollectedAt = now.Add(time.Hour).Format(time.RFC3339)
				a.Payload.Reference.ExpiresAt = now.Add(2 * time.Hour).Format(time.RFC3339)
				signAssessment(&a, private)
				wantStatus = "UNKNOWN"
			case "expired-report":
				a.Payload.Reference.CollectedAt = now.Add(-3 * time.Hour).Format(time.RFC3339)
				a.Payload.Reference.ExpiresAt = now.Add(-2 * time.Hour).Format(time.RFC3339)
				signAssessment(&a, private)
				wantStatus = "UNKNOWN"
			case "malware-unknown":
				a.Payload.Malware.Status = "UNKNOWN"
				signAssessment(&a, private)
				wantStatus = "UNKNOWN"
			case "static-fail":
				a.Payload.StaticAnalysis.Status = "FAIL"
				signAssessment(&a, private)
			}
			r := VerifyAssessment(c, keys, &a, now)
			if r.Status != wantStatus || r.TrustLevel != wantLevel {
				t.Fatal(r)
			}
		})
	}
	_, a, _ := assessed(t, c, now)
	b := strings.TrimSuffix(strings.TrimSpace(string(JSON(a))), "}") + `,"trust_level":"L4"}`
	if _, err := ParseAssessment([]byte(b)); err == nil {
		t.Fatal("accepted caller rating")
	}
	for _, bad := range []func(*Assessment){func(a *Assessment) { a.Payload.Coverage = "direct-only" }, func(a *Assessment) { a.Payload.StaticAnalysis = nil }, func(a *Assessment) { a.Payload.Malware.Method = "" }, func(a *Assessment) { a.Payload.PublisherIdentity.EvidenceURL = "https://example.invalid/?token=secret" }} {
		_, a, _ := assessed(t, c, now)
		bad(&a)
		if _, err := ParseAssessment(JSON(a)); err == nil {
			t.Fatal("incomplete assessment accepted")
		}
	}
}
func TestAssessmentAuthoritiesTransactionsRollbackAndReevaluation(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	c := fixture()
	c.SourceVerified = true
	c.RuntimeVerified = true
	c.DependenciesVerified = true
	keys, a, private := assessed(t, c, now)
	m, err := New(t.TempDir(), Binding{Version: 1, Project: "project", Scope: "server", SourceRoot: filepath.Join(t.TempDir(), "source")})
	if err != nil {
		t.Fatal(err)
	}
	m.Now = func() time.Time { return now }
	candidates := []Candidate{c}
	source := workstore.Hash([]byte("source"))
	state, _ := m.State(candidates)
	if _, err = m.Plan(ctx, Edit{Action: "authorities", ExpectedSHA256: state.SHA256, Authorities: &keys}, candidates, source); err == nil {
		t.Fatal("authority without confirmation accepted")
	}
	save := func(r Edit) Preview {
		t.Helper()
		s, e := m.State(candidates)
		if e != nil {
			t.Fatal(e)
		}
		r.ExpectedSHA256 = s.SHA256
		p, e := m.Plan(ctx, r, candidates, source)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = m.Apply(ctx, p.Plan.ID, p.Plan.Digest, candidates, source); e != nil {
			t.Fatal(e)
		}
		return p
	}
	if _, err = m.Plan(ctx, Edit{Action: "assessment-import", ExpectedSHA256: state.SHA256, Document: string(JSON(a))}, candidates, source); err == nil {
		t.Fatal("unregistered evaluator accepted")
	}
	enrolled := save(Edit{Action: "authorities", Authorities: &keys, AuthorityConfirmation: true, AuthorityReason: "Independent verification in isolated fixture"})
	state, _ = m.State(candidates)
	p, err := m.Plan(ctx, Edit{Action: "assessment-import", ExpectedSHA256: state.SHA256, Document: string(JSON(a))}, candidates, source)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := m.State(candidates)
	if len(before.AssessmentDocuments) != 0 {
		t.Fatal("preview wrote assessment")
	}
	if _, err = m.Apply(ctx, p.Plan.ID, p.Plan.Digest, candidates, source); err != nil {
		t.Fatal(err)
	}
	fresh, _ := New(filepath.Dir(m.Store.Root), m.Binding)
	fresh.Now = m.Now
	state, err = fresh.State(candidates)
	if err != nil || state.Results[0].TrustLevel != "L2" || len(state.History) != 2 || state.History[1].Request.AuthorityReason == "" {
		t.Fatal(state, err)
	}
	state, _ = m.State(candidates)
	_, err = m.Plan(ctx, Edit{Action: "rollback", ExpectedSHA256: state.SHA256, Snapshot: enrolled.Plan.ID}, candidates, source)
	if err == nil {
		t.Fatal("rollback restored authority state without confirmation")
	}
	removed := save(Edit{Action: "assessment-remove", SubjectSHA256: a.Payload.Reference.SubjectSHA256})
	state, _ = m.State(candidates)
	if state.Results[0].TrustLevel != "L0" {
		t.Fatal(state)
	}
	save(Edit{Action: "rollback", Snapshot: removed.Plan.ID})
	state, _ = m.State(candidates)
	if state.Results[0].TrustLevel != "L2" {
		t.Fatal(state)
	}
	state, _ = m.State(candidates)
	future, err := m.Plan(ctx, Edit{Action: "assessment-import", ExpectedSHA256: state.SHA256, Document: string(JSON(a))}, candidates, source)
	if err != nil {
		t.Fatal(err)
	}
	m.Now = func() time.Time { return now.Add(2 * time.Hour) }
	if _, err = m.Apply(ctx, future.Plan.ID, future.Plan.Digest, candidates, source); err == nil {
		t.Fatal("expired report applied old review")
	}
	m.Now = func() time.Time { return now }
	a.Payload.Malware.Status = "FAIL"
	signAssessment(&a, private)
	save(Edit{Action: "assessment-import", Document: string(JSON(a))})
	state, _ = m.State(candidates)
	if state.Results[0].Status != "FAIL" {
		t.Fatal(state)
	}
}
