//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package workstore

import (
	"golang.org/x/sys/unix"
	"os"
)

func normalMode(m uint32, _ bool) uint32 { return m }

func isLink(i os.FileInfo) bool { return i.Mode()&os.ModeSymlink != 0 }
func lock(p string) (func(), error) {
	if e := CheckLinks(p); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		return nil, e
	}
	return func() { unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }, nil
}
func durableRename(r *os.Root, from, to string) error {
	if e := r.Rename(from, to); e != nil {
		return e
	}
	d, e := r.Open(".")
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
