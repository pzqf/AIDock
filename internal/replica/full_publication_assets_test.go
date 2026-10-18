package replica

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestFullRestorationRetainsLaterDraftAndUnrelatedBoundImages(t *testing.T) {
	m, p := storedFullPublicationFixture(t)
	c, err := m.fullPublicationCoordinator(p)
	if err != nil {
		t.Fatal(err)
	}
	// This intentionally unqualified fixture exercises immutable backup roles,
	// not the formal native owner or its permission/checking gates.
	if _, err = c.Apply(context.Background(), p.Proof.Transaction.ID, p.Proof.Transaction.Digest); err != nil {
		t.Fatal(err)
	}
	copy := p.Proof.Members[0].Copy
	prefix := projectsnapshot.PublicationMemberPrefix(copy)
	bodies := map[string][]byte{
		prefix + "source":        []byte("later saved source draft is retained\n"),
		prefix + "local":         []byte("later local override is retained\n"),
		prefix + "native/.codex": []byte("unrelated native sibling is retained\n"),
	}
	files := map[string]string{}
	var selectedSource *workstore.Store
	for _, root := range c.Roots {
		if root.Path == prefix+"source" {
			selectedSource = &workstore.Store{Root: root.Root, Local: m.Source.Store.Local}
		}
		if body, ok := bodies[root.Path]; ok {
			if root.File {
				t.Fatal("expected directory root", root.Path)
			}
			if err = os.MkdirAll(root.Root, 0700); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(root.Root, "later-unrelated.txt")
			if err = os.WriteFile(file, body, 0600); err != nil {
				t.Fatal(err)
			}
			files[root.Path] = file
		}
	}
	if len(files) != len(bodies) {
		t.Fatal("missing bound fixture roots")
	}
	_, selection, err := m.fullRestorationSelection(p, p.Proof.Transaction, []string{copy})
	if err != nil {
		t.Fatal(err)
	}
	seed, err := c.RestoreSelected(p.Proof.Transaction.ID, selection)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range seed.Targets {
		if _, ok := bodies[target.Path]; !ok {
			continue
		}
		entry, ok := target.Before.Entries["later-unrelated.txt"]
		if !ok || target.After.Entries["later-unrelated.txt"] != entry {
			t.Fatal("later file not preserved by original selection", target.Path)
		}
		if _, err = c.Blobs.Data(entry); err == nil {
			t.Fatal("test did not exercise the missing common-cache case", target.Path)
		}
	}
	if _, err = c.ReviseReversal(seed.ID, seed.Digest, seed.Targets); err == nil {
		t.Fatal("revision unexpectedly accepted incomplete immutable assets")
	}
	if err = retainFullRestorationImages(c, seed); err != nil {
		t.Fatal(err)
	}
	if err = retainFullRestorationImages(c, seed); err != nil {
		t.Fatal("retention is not idempotent", err)
	}
	for _, target := range seed.Targets {
		body, ok := bodies[target.Path]
		if !ok {
			continue
		}
		entry := target.Before.Entries["later-unrelated.txt"]
		cached, err := c.Blobs.Data(entry)
		if err != nil || string(cached) != string(body) {
			t.Fatal("immutable retained content missing", target.Path, err)
		}
		live, err := os.ReadFile(files[target.Path])
		if err != nil || string(live) != string(body) {
			t.Fatal("retaining backup changed live files", target.Path, err)
		}
		if target.Path == prefix+"native/.codex" {
			if _, err = m.Source.Store.Data(entry); err == nil {
				t.Fatal("raw native sibling leaked into the publisher source cache")
			}
			if selectedSource == nil {
				t.Fatal("selected source cache not bound")
			}
			if _, err = selectedSource.Data(entry); err == nil {
				t.Fatal("raw native sibling leaked into the selected member source cache")
			}
		}
	}
	revised, err := c.ReviseReversal(seed.ID, seed.Digest, seed.Targets)
	if err != nil {
		t.Fatal("real common revision still lacks retained drafts", err)
	}
	for i, target := range revised.Targets {
		if workstore.Digest(target.Before) != workstore.Digest(seed.Targets[i].Before) || workstore.Digest(target.After) != workstore.Digest(seed.Targets[i].After) {
			t.Fatal("retention changed the immutable proposal")
		}
	}
}
