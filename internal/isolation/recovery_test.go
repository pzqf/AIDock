package isolation

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPendingDoesNotAdoptUnknownOrEscapingDirectories(t *testing.T) {
	local := filepath.Join(t.TempDir(), "local")
	m, _ := New(local)
	rows, err := m.Pending()
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	if _, err = os.Stat(local); !os.IsNotExist(err) {
		t.Fatal("read created Local")
	}
	id := "d26f53ce-62ce-4d6b-b25a-1cbdca310bb0"
	root := filepath.Join(local, "isolation-probes", "probe-"+id)
	_ = os.MkdirAll(root, 0700)
	_ = os.WriteFile(filepath.Join(root, "keep.txt"), []byte("private"), 0600)
	if _, err = m.Pending(); err == nil {
		t.Fatal("unrecognized folder adopted")
	}
	p := Pending{Version: 1, ID: id, Root: root, Profile: "AIDock.Probe." + id, Host: platformInfo().Host}
	if err = writePending(p); err != nil {
		t.Fatal(err)
	}
	rows, err = m.Pending()
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	p.Root = filepath.Dir(root)
	p.Digest = pendingDigest(p)
	_ = os.WriteFile(filepath.Join(root, ".aidock-isolation.json"), bytes(p), 0600)
	if _, err = m.Pending(); err == nil {
		t.Fatal("escaping recovery root accepted")
	}
	if b, err := os.ReadFile(filepath.Join(root, "keep.txt")); err != nil || string(b) != "private" {
		t.Fatal("unknown data changed", err)
	}
}
