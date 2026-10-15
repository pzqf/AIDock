//go:build windows

package credentials

import (
	"context"
	"path/filepath"
	"testing"
)

// Only random test namespaces are touched; no user's existing credential name
// is enumerated, read, rotated or deleted by these tests.
func TestWindowsVaultRoundTripNamespaceIsolationAndCleanup(t *testing.T) {
	m, err := New(filepath.Clean(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	other, err := New(filepath.Clean(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.store.remove("test_key"); _ = other.store.remove("test_key") })
	entry := addSecret(t, m, "test_key")
	current, value, err := m.store.read(entry.Name)
	defer clear(value)
	if err != nil || current.Revision != entry.Revision || string(value) != "FAKE_VAULT_PRIVATE_SENTINEL" {
		t.Fatal("round trip failed", err)
	}
	out, err := other.State(context.Background())
	if err != nil || len(out.Entries) != 0 {
		t.Fatal("namespace isolation failed", err)
	}
	next, err := m.Change(context.Background(), Edit{Action: "rotate", Name: entry.Name, ExpectedRevision: entry.Revision, Value: "FAKE_ROTATED", Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Change(context.Background(), Edit{Action: "delete", Name: entry.Name, ExpectedRevision: next.Revision, Confirm: true, ReferencesReviewed: true})
	if err != nil {
		t.Fatal(err)
	}
	state, err := m.State(context.Background())
	if err != nil || len(state.Entries) != 0 || len(state.History) != 6 {
		t.Fatal("cleanup failed", err)
	}
}
