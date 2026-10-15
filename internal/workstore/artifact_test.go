package workstore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestArtifactQuotaDoesNotExpandSourceStores(t *testing.T) {
	d := t.TempDir()
	source := &Store{Root: filepath.Join(d, "source"), Local: filepath.Join(d, "transactions")}
	large := bytes.Repeat([]byte{0, 255, 10}, (11<<20)/3)
	tree := Tree{Exists: true, Entries: map[string]Entry{}}
	if err := source.PutFile(&tree, "runtime", large, 0644); err == nil {
		t.Fatal("normal source accepted a runtime-sized binary")
	}
	artifact := &Store{Root: filepath.Join(d, "artifact"), Local: filepath.Join(d, "transactions"), PlanKind: "plugin-artifact"}
	if err := artifact.PutFile(&tree, "runtime", large, 0644); err != nil {
		t.Fatal(err)
	}
	before, err := artifact.Capture(artifact.Root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := artifact.Preview("import", before, tree)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = artifact.Apply(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(artifact.Root, "runtime"))
	if err != nil || !bytes.Equal(b, large) {
		t.Fatal("large binary changed", err)
	}
	if _, err = source.Capture(artifact.Root); err == nil {
		t.Fatal("normal source capture accepted an oversized binary")
	}
	if _, err = source.Data(tree.Entries["runtime"]); err == nil {
		t.Fatal("normal source accepted an oversized backup reference")
	}
}
