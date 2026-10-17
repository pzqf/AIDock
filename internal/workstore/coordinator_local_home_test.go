package workstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCoordinatorProjectLocalHomeIsSafeWhenActualRootsAreDisjoint(t *testing.T) {
	project := t.TempDir()
	data := filepath.Join(project, ".aidock-data")
	if err := os.MkdirAll(data, 0700); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(data, "transactions")
	source := filepath.Join(project, ".aidock")
	native := filepath.Join(project, ".agents")
	override := filepath.Join(data, "profile")
	blobs := &Store{Root: source, Local: local}
	c := &Coordinator{Local: local, Identity: "project-local-home", Blobs: blobs, Roots: []RootBinding{{Path: "source", Root: source}, {Path: "native", Root: native, NativeProject: project}, {Path: "override", Root: override}}}
	before := Tree{Entries: map[string]Entry{}}
	after := Tree{Entries: map[string]Entry{}}
	if err := blobs.PutFile(&after, "guide.md", []byte("bounded write"), 0644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(local, "keep.txt")
	if err := os.WriteFile(marker, []byte("Local data must remain"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := c.Preview("apply", []BatchTarget{{Path: "source", Before: before, After: after}, {Path: "native", Before: before, After: after}, {Path: "override", Before: before, After: after}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	reverse, err := c.Reverse(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(context.Background(), reverse.ID, reverse.Digest); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "Local data must remain" {
		t.Fatal("project-local data home replaced", err)
	}
	for _, root := range []string{source, native, override} {
		if _, err := os.Lstat(root); !os.IsNotExist(err) {
			t.Fatal("actual target not reversed", root, err)
		}
	}
	unsafe := *c
	unsafe.Local = filepath.Join(native, "transaction-data")
	if _, err = unsafe.PreviewObserved("apply", p.Targets); err == nil {
		t.Fatal("actual replacement root containing Local accepted")
	}
}
