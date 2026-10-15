package console

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/pluginorigin"
	"github.com/pzqf/AIDock/internal/pluginsecurity"
	"github.com/pzqf/AIDock/internal/source"
)

func TestMCPAssessmentAPIAuthorityConfirmationHistoryRollbackAndDraftContract(t *testing.T) {
	s, c, csrf := sourceServer(t)
	origin := "http://" + s.address
	m, _ := s.engine.Sources()
	ctx := context.Background()
	p, err := m.Plan(ctx, source.Request{Action: "create", Name: "guide", Description: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	pub, _, _ := ed25519.GenerateKey(nil)
	encoded := base64.StdEncoding.EncodeToString(pub)
	keys := pluginsecurity.Authorities{Version: 1, Authorities: []pluginsecurity.Authority{{Key: pluginorigin.Key{Label: "API isolated evaluator", PublicKey: encoded, SourceType: "npm", SourceID: "@fixture/root", ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339), Enabled: true}, Provider: "API test only", VerificationURL: "https://example.invalid/key"}}}
	call := func(path string, body any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		w := request(s, "POST", "/api/mcp/security/"+path, string(raw), c, origin, csrf)
		var out map[string]any
		if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			t.Fatal(w.Code, w.Body.String(), e)
		}
		return out
	}
	state, _ := m.MCPSecurity(ctx)
	r := pluginsecurity.Edit{Action: "authorities", ExpectedSHA256: state.SHA256, Authorities: &keys}
	if v := call("plan", r); v["ok"] != false || v["code"] != "INVALID_INPUT" {
		t.Fatal(v)
	}
	r.AuthorityConfirmation = true
	r.AuthorityReason = "Independently verified this isolated test key"
	v := call("plan", r)
	if v["ok"] != true {
		t.Fatal(v)
	}
	raw, _ := json.Marshal(v["items"])
	var preview pluginsecurity.Preview
	json.Unmarshal(raw, &preview)
	state, _ = m.MCPSecurity(ctx)
	if len(state.Authorities.Authorities) != 0 {
		t.Fatal("preview enrolled key")
	}
	v = call("apply", map[string]string{"plan_id": preview.Plan.ID, "digest": preview.Plan.Digest})
	if v["ok"] != true {
		t.Fatal(v)
	}
	state, _ = m.MCPSecurity(ctx)
	if len(state.Authorities.Authorities) != 1 || len(state.History) != 1 || state.History[0].Request.AuthorityReason != r.AuthorityReason || state.ExecutionReady {
		t.Fatal(state)
	}
	rollback := pluginsecurity.Edit{Action: "rollback", Snapshot: preview.Plan.ID, ExpectedSHA256: state.SHA256}
	if v = call("plan", rollback); v["ok"] != false || v["code"] != "ASSESSMENT_AUTHORITY_CONFIRMATION_REQUIRED" {
		t.Fatal(v)
	}
	rollback.AuthorityConfirmation = true
	rollback.AuthorityReason = "Explicitly rechecked authority rollback"
	v = call("plan", rollback)
	if v["ok"] != true {
		t.Fatal(v)
	}
	raw, _ = json.Marshal(v["items"])
	json.Unmarshal(raw, &preview)
	v = call("apply", map[string]string{"plan_id": preview.Plan.ID, "digest": preview.Plan.Digest})
	if v["ok"] != true {
		t.Fatal(v)
	}
	state, _ = m.MCPSecurity(ctx)
	if len(state.Authorities.Authorities) != 0 || len(state.History) != 2 {
		t.Fatal(state)
	}
}
