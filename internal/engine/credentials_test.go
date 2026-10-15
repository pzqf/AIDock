package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/credentials"
	"github.com/pzqf/AIDock/internal/result"
)

func TestCredentialReviewIncludesDisabledDeclarationsAndDetectsSourceChange(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(t.TempDir(), "local")
	e := &Engine{Roots: config.Roots{Local: local}, SourceRoot: root, SourceOwner: "project-a", SourceScope: "workspace", SourceCopy: "copy-a"}
	doc := map[string]any{"schema_version": 1, "servers": []any{map[string]any{"name": "plugin-a", "source": map[string]any{"type": "local", "path": "plugin"}, "transport": "stdio", "entrypoint": "server.js", "tier": 0, "enabled": false, "permissions": map[string]any{"fs": map[string]any{"paths": []any{}, "mode": "ro"}, "net": map[string]any{"default": "deny", "allow": []any{}}, "exec": false}, "secrets": []any{"missing_key"}}}}
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(root, "mcp.yaml"), b, 0600); err != nil {
		t.Fatal(err)
	}
	state, err := e.CredentialState(context.Background())
	if err != nil || len(state.Unknown) != 0 || len(state.References) != 1 || state.References[0].Present || state.References[0].Copy != "copy-a" {
		t.Fatal(state, err)
	}
	server := doc["servers"].([]any)[0].(map[string]any)
	server["secrets"] = []any{"changed_key"}
	b, _ = json.Marshal(doc)
	_ = os.WriteFile(filepath.Join(root, "mcp.yaml"), b, 0600)
	_, err = e.ChangeCredential(context.Background(), credentials.Edit{Action: "delete", Name: "missing_key", ExpectedRevision: "00000000000000000000000000000000", Confirm: true, ReferencesReviewed: true, ExpectedReferences: state.Review})
	if typed, ok := err.(*result.Error); !ok || typed.Code != "CREDENTIAL_REFERENCES_STALE" {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(root, "mcp.yaml"), []byte("not: [valid"), 0600)
	state, err = e.CredentialState(context.Background())
	if err != nil || len(state.Unknown) != 1 || len(state.References) != 0 {
		t.Fatal(state, err)
	}
}
