//go:build windows

package bridge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestJunctionBlockedAndUnselectedJunctionNotScanned(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	packageFixture(t, root)
	writeFile(t, filepath.Join(outside, "SKILL.md"), "must not read")
	link := filepath.Join(root, "skills/outside")
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
		t.Fatalf("junction fixture: %v: %s", err, output)
	}
	t.Cleanup(func() {
		// os.Remove removes only the junction; never recursively walk its target.
		if err := os.Remove(link); err != nil {
			t.Error(err)
		}
	})
	reg := registration(root)
	selected, err := Check(context.Background(), Index{Version: 1, Bridges: []Registration{reg}})
	if err != nil || selected.Status == "FAIL" || len(selected.Bridges[0].Candidates) != 1 {
		t.Fatal("selected scan accessed unregistered junction", selected, err)
	}
	reg.Packages = nil
	all, err := Check(context.Background(), Index{Version: 1, Bridges: []Registration{reg}})
	if err != nil || all.Status != "FAIL" || !hasFinding(all.Bridges[0], "EXTERNAL_SCAN_UNSAFE") || len(all.Bridges[0].Candidates) != 0 {
		t.Fatal("junction not blocked", all, err)
	}
	reg.SourcePath = "skills/outside"
	linkedSource, err := Check(context.Background(), Index{Version: 1, Bridges: []Registration{reg}})
	if err != nil || linkedSource.Status != "FAIL" || !hasFinding(linkedSource.Bridges[0], "EXTERNAL_SOURCE_UNSAFE") {
		t.Fatal("junction source root accepted", linkedSource, err)
	}
}
