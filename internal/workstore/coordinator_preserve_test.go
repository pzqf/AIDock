package workstore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCoordinatorReversePreservesRecoveredImmutableMaterialAndStillChecksDrift(t *testing.T) {
	c, p := coordinatedFixture(t)
	cache := []byte{0, 255, 42}
	if err := c.Blobs.PutFile(&p.Targets[0].After, ".revisions/restored/payload.bin", cache, 0644); err != nil {
		t.Fatal(err)
	}
	p, err := c.Preview("apply", p.Targets)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	root := c.store(p.Targets[0].Path).Root
	bodyPath := filepath.Join(root, "body")
	if err = os.WriteFile(bodyPath, []byte("external body edit"), 0644); err != nil {
		t.Fatal(err)
	}
	preserve := map[string][]string{p.Targets[0].Path: {".revisions"}}
	if _, err = c.ReversePreserving(p.ID, preserve); err == nil {
		t.Fatal("preserving history bypassed ordinary changed-file guard")
	}
	afterBody, err := c.Blobs.Data(p.Targets[0].After.Entries["body"])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(bodyPath, afterBody, 0644); err != nil {
		t.Fatal(err)
	}
	reverse, err := c.ReversePreserving(p.ID, preserve)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(context.Background(), reverse.ID, reverse.Digest); err != nil {
		t.Fatal(err)
	}
	retained, err := os.ReadFile(filepath.Join(root, ".revisions/restored/payload.bin"))
	if err != nil || !bytes.Equal(retained, cache) {
		t.Fatal("undo deleted recovered immutable history", err)
	}
	beforeBody, err := c.Blobs.Data(p.Targets[0].Before.Entries["body"])
	if err != nil {
		t.Fatal(err)
	}
	now, err := os.ReadFile(bodyPath)
	if err != nil || !bytes.Equal(now, beforeBody) {
		t.Fatal("owned body was not reversed", err)
	}
	undo, err := c.ReversePreserving(reverse.ID, preserve)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(context.Background(), undo.ID, undo.Digest); err != nil {
		t.Fatal(err)
	}
	coordinatedEqual(t, c, p.Targets, true)
	for _, invalid := range []map[string][]string{{"foreign": {".revisions"}}, {p.Targets[0].Path: {"../escape"}}, {p.Targets[0].Path: {""}}} {
		if _, err = c.ReversePreserving(undo.ID, invalid); err == nil {
			t.Fatal("invalid preservation scope accepted")
		}
	}
}
