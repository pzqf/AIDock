package workstore

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Complete runtime packages can have thousands of dependency files. Check the
// actual file immediately before each replacement, then let transition's final
// full capture verify the complete candidate. Other store kinds keep their
// existing full-tree checks and quotas.
func (s *Store) currentForChange(p string) (Entry, bool, error) {
	if s.PlanKind != "plugin-artifact" {
		t, err := s.Capture(s.Root)
		e, ok := t.Entries[p]
		return e, ok, err
	}
	if err := SafeRelative(p); err != nil {
		return Entry{}, false, err
	}
	if err := CheckLinks(filepath.Join(s.Root, filepath.FromSlash(p))); err != nil {
		return Entry{}, false, err
	}
	r, err := os.OpenRoot(s.Root)
	if err != nil {
		return Entry{}, false, err
	}
	defer r.Close()
	i, err := r.Lstat(p)
	if os.IsNotExist(err) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	if i.IsDir() {
		return Entry{Dir: true, Mode: normalMode(uint32(i.Mode().Perm()), true)}, true, nil
	}
	if !i.Mode().IsRegular() {
		return Entry{}, false, fmt.Errorf("目标不是普通文件")
	}
	f, err := r.Open(p)
	if err != nil {
		return Entry{}, false, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(i, opened) {
		return Entry{}, false, fmt.Errorf("目标在打开时变化")
	}
	b, err := io.ReadAll(io.LimitReader(f, s.fileLimit()+1))
	if err != nil {
		return Entry{}, false, err
	}
	if int64(len(b)) > s.fileLimit() {
		return Entry{}, false, fmt.Errorf("产物文件超限")
	}
	after, err := f.Stat()
	if err != nil || after.Size() != i.Size() || !after.ModTime().Equal(i.ModTime()) {
		return Entry{}, false, fmt.Errorf("目标在读取时变化")
	}
	e, err := s.put(b)
	e.Mode = normalMode(uint32(i.Mode().Perm()), false)
	return e, true, err
}
