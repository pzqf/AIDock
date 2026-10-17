package native

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestConnectionRestoreCompilerUsesImmutableImageAndPreservesManualBytes(t *testing.T) {
	m, req, client := connectionFixture(t)
	p := compiledConnection(t, m, req, &client)
	if _, err := m.connectionBatch().Apply(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	images := map[string]workstore.Tree{}
	for _, path := range []string{".mcp.json", ".codex/config.toml", connectionLedgerPath} {
		tree, err := m.connectionStore().CaptureFile(filepath.Join(m.Root, path))
		if err != nil {
			t.Fatal(err)
		}
		images[path] = tree
	}
	write(t, filepath.Join(m.Root, ".mcp.json"), []byte("PHYSICAL_CHANGED_BODY_MUST_NOT_BE_READ"))
	write(t, filepath.Join(m.Root, ".codex/config.toml"), []byte("PHYSICAL_CHANGED_TOML_MUST_NOT_BE_READ"))
	physical := snapshotNativeTrees(t, m)
	client.Args = append(client.Args, "REGENERATED_CURRENT_RECEIPT")
	targets, err := m.compileConnectionImages(images, req.Tools, []MCPConnectionRequest{req}, []*source.MCPClientConfiguration{&client}, client.Command, filepath.Dir(m.Source.Store.Local), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		if workstore.Digest(target.Before) != workstore.Digest(images[target.Path]) {
			t.Fatal("restore read a different before-image", target.Path)
		}
		if target.Path == connectionLedgerPath {
			continue
		}
		body, err := m.connectionStore().Data(target.After.Entries[workstore.FileContent])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(body, []byte("BACKUP_PRIVATE")) || !bytes.Contains(body, []byte("REGENERATED_CURRENT_RECEIPT")) || bytes.Contains(body, []byte("PHYSICAL_CHANGED")) {
			t.Fatal("lost manual backup or imported live body", target.Path)
		}
	}
	if !reflectTreesEqual(physical, snapshotNativeTrees(t, m)) {
		t.Fatal("restore compilation mutated native files")
	}
}
