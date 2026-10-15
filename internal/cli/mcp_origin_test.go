package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/pluginorigin"
	"github.com/pzqf/AIDock/internal/source"
)

func TestMCPOriginCLIRegisterPreviewConfirmRollbackAndRecover(t *testing.T) {
	d := t.TempDir()
	root, home := filepath.Join(d, ".aidock"), filepath.Join(d, "home")
	owner := "11111111-1111-4111-8111-111111111111"
	m, err := source.New(root, filepath.Join(home, "local"), owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p, err := m.Plan(ctx, source.Request{Action: "create", Name: "guide", Description: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	flags := []string{"--managed-root", root, "--home", home, "--project", owner, "--scope", "server", "--json"}
	exit, v, _ := invoke(t, append([]string{"mcp", "origin", "state"}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	state := v["items"].(map[string]any)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	keys := pluginorigin.Keys{Version: 1, Keys: []pluginorigin.Key{{Label: "Fixture", PublicKey: base64.StdEncoding.EncodeToString(pub), SourceType: "npm", SourceID: "fixture", ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339), Enabled: true}}}
	file := filepath.Join(d, "origin-request.json")
	req := pluginorigin.Edit{Action: "keys", ExpectedSHA256: state["sha256"].(string), Keys: &keys}
	if err = os.WriteFile(file, pluginorigin.JSON(req), 0600); err != nil {
		t.Fatal(err)
	}
	exit, v, _ = invoke(t, append([]string{"mcp", "origin", "plan", "--file", file}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	plan := v["items"].(map[string]any)["plan"].(map[string]any)
	s, _ := m.MCPOrigin(ctx)
	if len(s.Keys.Keys) != 0 {
		t.Fatal("preview wrote keys")
	}
	exit, v, _ = invoke(t, append([]string{"mcp", "origin", "apply", "--plan", plan["id"].(string), "--expected-digest", "wrong"}, flags...)...)
	if exit != 3 || v["code"] != "PLAN_STALE" {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, append([]string{"mcp", "origin", "apply", "--plan", plan["id"].(string), "--expected-digest", plan["digest"].(string)}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	s, _ = m.MCPOrigin(ctx)
	if len(s.Keys.Keys) != 1 || s.AuditCount != 1 {
		t.Fatal(s)
	}
	req = pluginorigin.Edit{Action: "rollback", Snapshot: plan["id"].(string), ExpectedSHA256: s.SHA256}
	os.WriteFile(file, pluginorigin.JSON(req), 0600)
	exit, v, _ = invoke(t, append([]string{"mcp", "origin", "plan", "--file", file}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	plan = v["items"].(map[string]any)["plan"].(map[string]any)
	exit, v, _ = invoke(t, append([]string{"mcp", "origin", "apply", "--plan", plan["id"].(string), "--expected-digest", plan["digest"].(string)}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	s, _ = m.MCPOrigin(ctx)
	if len(s.Keys.Keys) != 0 || s.AuditCount != 2 {
		t.Fatal(s)
	}
	exit, v, _ = invoke(t, append([]string{"mcp", "origin", "recover"}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	exit, _, _ = invoke(t, append([]string{"mcp", "origin", "show", "--subject", "../../bad"}, flags...)...)
	if exit != 2 {
		t.Fatal(exit)
	}
}
