package inventory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSymlinkOutsideSourceExcluded(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	packageFiles(t, filepath.Join(root, "skills/a"), "a")
	write(t, filepath.Join(outside, "secret.txt"), []byte("must-not-read"))
	if err := os.Symlink(outside, filepath.Join(root, "skills/a", "outside")); err != nil {
		t.Skip("OS does not permit test symlink creation:", err)
	}
	r, err := Scan(context.Background(), testManifest(root), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.Skills[0].Files {
		if f.Path == "outside/secret.txt" {
			t.Fatal("link target read")
		}
	}
	found := false
	for _, f := range r.Skills[0].Findings {
		if f.Code == "LINK_EXCLUDED" {
			found = true
		}
	}
	if !found {
		t.Fatal("link not diagnosed")
	}
}
