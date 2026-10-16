package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func TestSnapshotReaderSurvivesDamagedLocalBindingWithoutWeakeningWriters(t *testing.T) {
	e, _, _ := diagnosisFixture(t)
	root := source.LocalProfileRoot(e.Roots.Local, e.SourceOwner, e.SourceScope, e.SourceCopy)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	body := []byte("PRIVATE_BROKEN_BINDING")
	if err := os.WriteFile(filepath.Join(root, "binding.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Native(); err == nil {
		t.Fatal("ordinary application bootstrap ignored invalid binding")
	}
	if err := e.ValidateConsoleBinding(); err != nil {
		t.Fatal("damaged Profile made console recovery unreachable", err)
	}
	wrong := *e
	wrong.NativeRoot = t.TempDir()
	if err := wrong.ValidateConsoleBinding(); err == nil {
		t.Fatal("console bootstrap accepted unrelated native target")
	}
	wrong = *e
	wrong.LibraryRoot = e.SourceRoot
	if err := wrong.ValidateConsoleBinding(); err == nil {
		t.Fatal("console bootstrap accepted overlapping library")
	}
	m, err := e.SnapshotReader()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := m.ProjectSnapshots(context.Background())
	if err != nil || len(rows.Items) != 0 {
		t.Fatal(rows, err)
	}
	after, err := os.ReadFile(filepath.Join(root, "binding.json"))
	if err != nil || string(after) != string(body) {
		t.Fatal("snapshot reader replaced damaged binding", err)
	}
}
