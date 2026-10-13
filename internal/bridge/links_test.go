package bridge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLinkedPackageRejected(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	packageFixture(t, root)
	writeFile(t, filepath.Join(outside, "private.md"), "must not read")
	if err := os.Symlink(outside, filepath.Join(root, "skills/api/outside")); err != nil {
		t.Skipf("创建 symlink 权限不可用: %v", err)
	}
	report, err := Check(context.Background(), Index{Version: 1, Bridges: []Registration{registration(root)}})
	if err != nil || report.Status != "FAIL" || report.Bridges[0].CurrentRevision != "" {
		t.Fatal(report, err)
	}
	if !hasFinding(report.Bridges[0], "EXTERNAL_SCAN_UNSAFE") || len(report.Bridges[0].Candidates) != 0 {
		t.Fatal("linked target included in candidate", report)
	}
}
