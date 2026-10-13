//go:build windows

package workstore

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"syscall"
)

func normalMode(m uint32, dir bool) uint32 {
	if dir {
		return 0777
	}
	if m&0200 == 0 {
		return 0444
	}
	return 0666
}

func isLink(i os.FileInfo) bool {
	a, ok := i.Sys().(*syscall.Win32FileAttributeData)
	return i.Mode()&os.ModeSymlink != 0 || ok && a.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
func lock(p string) (func(), error) {
	if e := CheckLinks(p); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	o := new(windows.Overlapped)
	e = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, o)
	if e != nil {
		f.Close()
		return nil, e
	}
	return func() { windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, o); f.Close() }, nil
}
func durableRename(r *os.Root, from, to string) error {
	a := filepath.Join(r.Name(), from)
	b := filepath.Join(r.Name(), to)
	if e := CheckLinks(a); e != nil {
		return e
	}
	if e := CheckLinks(b); e != nil {
		return e
	}
	pa, e := windows.UTF16PtrFromString(a)
	if e != nil {
		return e
	}
	pb, e := windows.UTF16PtrFromString(b)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(pa, pb, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
