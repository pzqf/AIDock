package projectsnapshot

import (
	"context"
	"github.com/pzqf/AIDock/internal/workstore"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveV3AppliedBindingAndPrivateNativeMaterialization(t *testing.T) {
	a, c, s, n := layeredSnapshotFixture(t)
	c.ActiveKey = strings.Repeat("a", 64)
	r := mustCapture(t, a, c, s, n)
	if r.Version != 3 || r.ActiveKey != c.ActiveKey {
		t.Fatal(r.Version, r.ActiveKey)
	}
	private := &workstore.Store{Root: a.Binding.NativeRoot, Local: filepath.Join(a.Local, "test-native-private")}
	loaded, err := a.MaterializeNative(context.Background(), r.ID, private)
	if err != nil || loaded.Commit != r.Commit {
		t.Fatal(err)
	}
	for _, target := range r.Transaction.Targets {
		for _, tree := range []workstore.Tree{target.Before, target.After} {
			for _, entry := range tree.Entries {
				if entry.Dir {
					continue
				}
				if _, err = private.Data(entry); err != nil {
					t.Fatal("native bytes not materialized", err)
				}
			}
		}
	}
	if _, err = a.MaterializeNative(context.Background(), r.ID, &workstore.Store{Root: a.Binding.NativeRoot, Local: a.Local}); err == nil {
		t.Fatal("native bytes allowed in ordinary Local source bucket")
	}
	c.Configuration = nil
	if _, err = a.Capture(context.Background(), c, s, n); err == nil {
		t.Fatal("binding snapshot without frozen configuration accepted")
	}
}
