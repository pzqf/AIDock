//go:build windows

package generator

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestLockedOutputRetainsMarkerForCleanupRetry(t *testing.T) {
	m, adapter := fixture(t)
	s := stage(t, m, adapter).Stage
	file := filepath.Join(s.Root, "output", "locked.bin")
	write(t, file, []byte("locked"))
	ptr, err := windows.UTF16PtrFromString(file)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(ptr, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if h != 0 {
			_ = windows.CloseHandle(h)
		}
	}()
	o, err := m.Show(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Cleanup(ctx, s.ID, o.ObservationDigest)
	code(t, err, "GENERATOR_CLEANUP_REQUIRED")
	if _, err = os.Stat(filepath.Join(s.Root, ".aidock-generator.json")); err != nil {
		t.Fatal("marker lost", err)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "locked" {
		t.Fatal("locked content lost", err)
	}
	if err = windows.CloseHandle(h); err != nil {
		t.Fatal(err)
	}
	h = 0
	fresh := New(m.Source)
	o, err = fresh.Show(ctx, s.ID)
	if err != nil || o.State != "CLEANING" {
		t.Fatal(o, err)
	}
	if _, err = fresh.Cleanup(ctx, s.ID, o.ObservationDigest); err != nil {
		t.Fatal(err)
	}
}
func testJunction(t *testing.T, link, target string) {
	t.Helper()
	if err := os.Mkdir(link, 0755); err != nil {
		t.Fatal(err)
	}
	ptr, _ := windows.UTF16PtrFromString(link)
	h, err := windows.CreateFile(ptr, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	sub, _ := windows.UTF16FromString(`\??\` + target)
	printName, _ := windows.UTF16FromString(target)
	buf := make([]byte, 16+2*(len(sub)+len(printName)))
	binary.LittleEndian.PutUint32(buf[:4], 0xa0000003)
	binary.LittleEndian.PutUint16(buf[4:6], uint16(len(buf)-8))
	binary.LittleEndian.PutUint16(buf[10:12], uint16(2*(len(sub)-1)))
	binary.LittleEndian.PutUint16(buf[12:14], uint16(2*len(sub)))
	binary.LittleEndian.PutUint16(buf[14:16], uint16(2*(len(printName)-1)))
	for i, v := range append(sub, printName...) {
		binary.LittleEndian.PutUint16(buf[16+2*i:18+2*i], v)
	}
	var bytes uint32
	if err = windows.DeviceIoControl(h, 0x900a4, &buf[0], uint32(len(buf)), nil, 0, &bytes, nil); err != nil {
		t.Fatal(err)
	}
}
func TestJunctionInReturnedTreeNeverReadOrRemoved(t *testing.T) {
	m, adapter := fixture(t)
	s := stage(t, m, adapter).Stage
	outside := t.TempDir()
	write(t, filepath.Join(outside, "sentinel"), []byte("preserve"))
	link := filepath.Join(s.Root, "output", "outside")
	testJunction(t, link, outside)
	defer func() { ptr, _ := windows.UTF16PtrFromString(link); _ = windows.RemoveDirectory(ptr) }()
	o, err := m.Show(ctx, s.ID)
	if err != nil || o.CleanupSafe {
		t.Fatal(o, err)
	}
	if _, err = m.Validate(ctx, s.ID, s.Digest); err == nil {
		t.Fatal("junction accepted")
	}
	if _, err = m.Cleanup(ctx, s.ID, o.ObservationDigest); err == nil {
		t.Fatal("junction cleanup accepted")
	}
	if b, err := os.ReadFile(filepath.Join(outside, "sentinel")); err != nil || string(b) != "preserve" {
		t.Fatal("outside changed", err)
	}
}
