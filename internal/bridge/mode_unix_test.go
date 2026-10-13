//go:build !windows

package bridge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestExecutableBitsParticipateInCandidateRevision(t *testing.T) {
	root := t.TempDir()
	packageFixture(t, root)
	p := filepath.Join(root, "skills/api/script.sh")
	writeFile(t, p, "#!/bin/sh\nexit 1\n")
	idx := Index{Version: 1, Bridges: []Registration{registration(root)}}
	first, err := Check(context.Background(), idx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0700); err != nil {
		t.Fatal(err)
	}
	second, err := Check(context.Background(), idx)
	if err != nil || first.Bridges[0].CurrentRevision == second.Bridges[0].CurrentRevision {
		t.Fatal("executable bit omitted from revision", err)
	}
}
