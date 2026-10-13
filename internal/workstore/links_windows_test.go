//go:build windows

package workstore

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestJunctionSourceAndLocalRootsRejected(t *testing.T) {
	d := t.TempDir()
	target := filepath.Join(d, "target")
	if e := os.Mkdir(target, 0755); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(d, "junction")
	b, e := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if e != nil {
		t.Fatalf("junction: %v %s", e, b)
	}
	for _, s := range []*Store{{Root: filepath.Join(link, ".aidock"), Local: filepath.Join(d, "local")}, {Root: filepath.Join(d, ".aidock"), Local: filepath.Join(link, "local")}} {
		errorCode(t, s.Locked(func() error { return nil }), "PATH_UNSAFE")
	}
	s := &Store{Root: filepath.Join(d, ".aidock"), Local: filepath.Join(d, "local")}
	if _, e = s.Capture(link); e == nil {
		t.Fatal("junction package accepted")
	}
}
