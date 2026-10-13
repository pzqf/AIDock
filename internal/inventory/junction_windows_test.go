//go:build windows

package inventory

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// Junctions do not require the symlink privilege, so Windows still has an actual
// reparse-point negative test when developer-mode symlink creation is unavailable.
func TestWindowsJunctionNotTraversed(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	packageFiles(t, filepath.Join(root, "skills/a"), "a")
	write(t, filepath.Join(outside, "outside-secret.md"), []byte("must-not-read"))
	link := filepath.Join(root, "skills/a/outside")
	if err := os.Mkdir(link, 0700); err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(link)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(p, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	sub, _ := windows.UTF16FromString(`\??\` + outside)
	printName, _ := windows.UTF16FromString(outside)
	buf := make([]byte, 16+2*(len(sub)+len(printName)))
	binary.LittleEndian.PutUint32(buf[0:4], 0xa0000003)
	binary.LittleEndian.PutUint16(buf[4:6], uint16(len(buf)-8))
	binary.LittleEndian.PutUint16(buf[8:10], 0)
	binary.LittleEndian.PutUint16(buf[10:12], uint16(2*(len(sub)-1)))
	binary.LittleEndian.PutUint16(buf[12:14], uint16(2*len(sub)))
	binary.LittleEndian.PutUint16(buf[14:16], uint16(2*(len(printName)-1)))
	for i, v := range append(sub, printName...) {
		binary.LittleEndian.PutUint16(buf[16+2*i:18+2*i], v)
	}
	var returned uint32
	err = windows.DeviceIoControl(handle, 0x900a4, &buf[0], uint32(len(buf)), nil, 0, &returned, nil)
	windows.CloseHandle(handle)
	if err != nil {
		t.Fatal(err)
	}
	// Remove only the junction itself before testing's recursive temp cleanup.
	t.Cleanup(func() {
		if err := windows.RemoveDirectory(p); err != nil {
			t.Error(err)
		}
	})
	r, err := Scan(context.Background(), testManifest(root), Options{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range r.Skills[0].Findings {
		if f.Code == "LINK_EXCLUDED" {
			found = true
		}
	}
	if !found {
		t.Fatal("junction not diagnosed")
	}
	for _, f := range r.Skills[0].Files {
		if f.Path == "outside/outside-secret.md" {
			t.Fatal("junction target read")
		}
	}
}
