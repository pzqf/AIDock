package pluginorigin

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/workstore"
)

func fixture(t *testing.T) (pluginlock.Plugin, Keys, Envelope, ed25519.PrivateKey, time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p := pluginlock.Plugin{Name: "fixture", Source: map[string]any{"type": "npm", "package": "@example/fixture", "version": "1.2.3"}, Version: "1.2.3", Platform: "windows-amd64", Entrypoint: "dist/main.js", Artifact: pluginlock.Artifact{Path: "mcp-artifacts/fixture", SHA256: workstore.Hash([]byte("artifact")), Entries: []pluginlock.Entry{{Path: "runtime/helper", SHA256: workstore.Hash([]byte("runtime"))}}}, Runtime: pluginlock.Runtime{Kind: "node", Version: "22.0.0", Constraint: "=22.0.0", Executable: "runtime/helper"}, Resolution: pluginlock.Resolution{Format: "npm-package-lock-v3", Path: "package-lock.json", SHA256: workstore.Hash([]byte("lock"))}, Dependencies: []pluginlock.Dependency{{Name: "a", Path: "node_modules/a", Version: "1.0.0", Resolved: "https://example.invalid/a", Integrity: "sha512-test"}, {Name: "b", Path: "node_modules/b", Version: "2.0.0", Resolved: "https://example.invalid/b", Integrity: "sha512-test"}}}
	public := base64.StdEncoding.EncodeToString(pub)
	id, _ := Fingerprint(public)
	keys := Keys{Version: 1, Keys: []Key{{ID: id, Label: "Fixture publisher", PublicKey: public, SourceType: "npm", SourceID: "@example/fixture", ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339), Enabled: true}}}
	e := Envelope{Version: 1, KeyID: id, Payload: Payload{Version: 1, Kind: "artifact-origin", SubjectSHA256: For(p).SHA256, Publisher: "Fixture Publisher", ReleaseURL: "https://example.invalid/releases/1.2.3", IssuedAt: now.Add(-time.Hour).Format(time.RFC3339), ExpiresAt: now.Add(2 * time.Hour).Format(time.RFC3339), RuntimeVersionVerified: true}}
	sign(&e, priv)
	return p, keys, e, priv, now
}
func sign(e *Envelope, k ed25519.PrivateKey) {
	e.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(k, SigningBytes(e.Payload)))
}
func clonePlugin(p pluginlock.Plugin) pluginlock.Plugin {
	var q pluginlock.Plugin
	json.Unmarshal(JSON(p), &q)
	return q
}

func TestOriginSignatureValidationAndExpiration(t *testing.T) {
	p, keys, e, priv, now := fixture(t)
	if c := Verify(p, keys, &e, now); c.Status != "PASS" || !c.RuntimeVersionVerified {
		t.Fatal(c)
	}
	tests := []struct {
		name, status, code string
		change             func(*Keys, *Envelope)
	}{
		{"unknown key", "UNKNOWN", "MCP_ORIGIN_KEY_UNKNOWN", func(k *Keys, e *Envelope) { k.Keys = []Key{} }},
		{"disabled key", "FAIL", "MCP_ORIGIN_KEY_DISABLED", func(k *Keys, e *Envelope) { k.Keys[0].Enabled = false }},
		{"expired key", "UNKNOWN", "MCP_ORIGIN_KEY_EXPIRED", func(k *Keys, e *Envelope) { k.Keys[0].ExpiresAt = now.Format(time.RFC3339) }},
		{"source scope", "FAIL", "MCP_ORIGIN_KEY_SCOPE_MISMATCH", func(k *Keys, e *Envelope) { k.Keys[0].SourceID = "another" }},
		{"source type", "FAIL", "MCP_ORIGIN_KEY_SCOPE_MISMATCH", func(k *Keys, e *Envelope) { k.Keys[0].SourceType = "registry" }},
		{"subject", "FAIL", "MCP_ORIGIN_SUBJECT_MISMATCH", func(k *Keys, e *Envelope) { e.Payload.SubjectSHA256 = workstore.Hash([]byte("other")); sign(e, priv) }},
		{"tampered publisher", "FAIL", "MCP_ORIGIN_SIGNATURE_INVALID", func(k *Keys, e *Envelope) { e.Payload.Publisher = "forged" }},
		{"invalid signature", "FAIL", "MCP_ORIGIN_SIGNATURE_INVALID", func(k *Keys, e *Envelope) { e.Signature = base64.StdEncoding.EncodeToString(make([]byte, 64)) }},
		{"expired statement", "UNKNOWN", "MCP_ORIGIN_EVIDENCE_EXPIRED", func(k *Keys, e *Envelope) { e.Payload.ExpiresAt = now.Format(time.RFC3339); sign(e, priv) }},
		{"future statement", "UNKNOWN", "MCP_ORIGIN_EVIDENCE_EXPIRED", func(k *Keys, e *Envelope) {
			e.Payload.IssuedAt = now.Add(time.Hour).Format(time.RFC3339)
			sign(e, priv)
		}},
		{"envelope version", "FAIL", "MCP_ORIGIN_EVIDENCE_INVALID", func(k *Keys, e *Envelope) { e.Version = 2 }},
		{"payload version", "FAIL", "MCP_ORIGIN_EVIDENCE_INVALID", func(k *Keys, e *Envelope) { e.Payload.Version = 2; sign(e, priv) }},
		{"kind", "FAIL", "MCP_ORIGIN_EVIDENCE_INVALID", func(k *Keys, e *Envelope) { e.Payload.Kind = "grant-execution"; sign(e, priv) }},
		{"key ID fingerprint", "FAIL", "MCP_ORIGIN_KEYS_INVALID", func(k *Keys, e *Envelope) { k.Keys[0].ID = workstore.Hash([]byte("wrong")); e.KeyID = k.Keys[0].ID }},
		{"duration", "FAIL", "MCP_ORIGIN_EVIDENCE_INVALID", func(k *Keys, e *Envelope) {
			e.Payload.ExpiresAt = now.Add(91 * 24 * time.Hour).Format(time.RFC3339)
			sign(e, priv)
		}},
		{"release credentials", "FAIL", "MCP_ORIGIN_EVIDENCE_INVALID", func(k *Keys, e *Envelope) {
			e.Payload.ReleaseURL = "https://user:pass@example.invalid/release"
			sign(e, priv)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var k Keys
			json.Unmarshal(JSON(keys), &k)
			v := e
			tc.change(&k, &v)
			c := Verify(p, k, &v, now)
			if c.Status != tc.status || c.RuntimeVersionVerified || len(c.Checks) != 1 || c.Checks[0].Code != tc.code {
				t.Fatal(c)
			}
		})
	}
	if c := Verify(p, keys, nil, now); c.Status != "UNKNOWN" {
		t.Fatal(c)
	}
}

func TestOriginSubjectBindsPublisherClaimsAndPreservesLocalChoices(t *testing.T) {
	p, _, _, _, _ := fixture(t)
	sha := For(p).SHA256
	for name, change := range map[string]func(*pluginlock.Plugin){
		"source":             func(p *pluginlock.Plugin) { p.Source["package"] = "another" },
		"version":            func(p *pluginlock.Plugin) { p.Version = "2.0.0" },
		"platform":           func(p *pluginlock.Plugin) { p.Platform = "linux-amd64" },
		"entrypoint":         func(p *pluginlock.Plugin) { p.Entrypoint = "other.js" },
		"artifact":           func(p *pluginlock.Plugin) { p.Artifact.SHA256 = workstore.Hash([]byte("changed")) },
		"runtime bytes":      func(p *pluginlock.Plugin) { p.Artifact.Entries[0].SHA256 = workstore.Hash([]byte("changed")) },
		"runtime version":    func(p *pluginlock.Plugin) { p.Runtime.Version = "22.1.0" },
		"runtime constraint": func(p *pluginlock.Plugin) { p.Runtime.Constraint = "=22.1.0" },
		"resolution":         func(p *pluginlock.Plugin) { p.Resolution.SHA256 = workstore.Hash([]byte("changed")) },
		"dependency":         func(p *pluginlock.Plugin) { p.Dependencies[0].Integrity = "sha512-changed" },
	} {
		t.Run(name, func(t *testing.T) {
			q := clonePlugin(p)
			change(&q)
			if For(q).SHA256 == sha {
				t.Fatal("claim was not bound")
			}
		})
	}
	q := clonePlugin(p)
	q.Name = "project-alias"
	q.Artifact.Path = "mcp-artifacts/other-local-directory"
	q.DefinitionSHA256 = workstore.Hash([]byte("different local permissions"))
	q.Dependencies[0], q.Dependencies[1] = q.Dependencies[1], q.Dependencies[0]
	if For(q).SHA256 != sha {
		t.Fatal("project-local choices or dependency order changed publisher subject")
	}
}

func TestOriginStrictDocuments(t *testing.T) {
	_, keys, e, _, _ := fixture(t)
	if _, err := ParseKeys(JSON(keys)); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseEnvelope(JSON(e)); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{`{"schema_version":1,"schema_version":1,"keys":[]}`, `{"schema_version":1,"keys":[],"private_key":"secret"}`, strings.Replace(string(JSON(keys)), keys.Keys[0].PublicKey, keys.Keys[0].PublicKey+"\n", 1)} {
		if _, err := ParseKeys([]byte(b)); err == nil {
			t.Fatal("accepted malformed keys")
		}
	}
	keys.Keys = append(keys.Keys, keys.Keys[0])
	if _, err := ParseKeys(JSON(keys)); err == nil {
		t.Fatal("duplicate key accepted")
	}
	var v map[string]any
	json.Unmarshal(JSON(e), &v)
	v["trust_level"] = "L4"
	if _, err := ParseEnvelope(JSON(v)); err == nil {
		t.Fatal("self asserted trust accepted")
	}
}
