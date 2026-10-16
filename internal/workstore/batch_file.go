package workstore

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/result"
)

// Single-file targets use one virtual entry so snapshots never scan siblings.
const FileContent = "content"

func validateFileTree(t Tree) error {
	if !t.Exists && len(t.Entries) == 0 {
		return nil
	}
	e, ok := t.Entries[FileContent]
	if !t.Exists || len(t.Entries) != 1 || !ok || e.Dir {
		return result.Fail("BACKUP_INVALID", "单文件快照必须只有 content 普通文件", 5)
	}
	return validateTree(t)
}
func (b *Batch) filePath(p string) bool {
	for _, v := range b.FilePaths {
		if v == p {
			return true
		}
	}
	return false
}
func (b *Batch) capture(t BatchTarget) (Tree, error) {
	s := b.store(t.Path)
	if t.File {
		return s.CaptureFile(s.Root)
	}
	return s.Capture(s.Root)
}

// CaptureFile stores the exact bytes and mode of this file only, rejecting links
// and directories. Its blobs can be passed to Batch.Preview like directory blobs.
func (s *Store) CaptureFile(p string) (Tree, error) {
	return s.snapshotFile(p, true)
}

// ObserveFile hashes exactly one regular file without storing its bytes.
func (s *Store) ObserveFile(p string) (Tree, error) {
	return s.snapshotFile(p, false)
}

func (s *Store) snapshotFile(p string, persist bool) (Tree, error) {
	t := Tree{Entries: map[string]Entry{}}
	if err := CheckLinks(p); err != nil {
		return t, err
	}
	i, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return t, nil
	}
	if err != nil {
		return t, err
	}
	if !i.Mode().IsRegular() || isLink(i) {
		return t, fmt.Errorf("单文件目标不是普通文件")
	}
	data, err := ReadFile(p)
	if err != nil {
		return t, err
	}
	var e Entry
	if persist {
		e, err = s.put(data)
		if err != nil {
			return t, err
		}
	} else {
		e = Entry{Hash: Hash(data), Size: int64(len(data))}
	}
	e.Mode = normalMode(uint32(i.Mode().Perm()), false)
	t.Exists, t.Entries[FileContent] = true, e
	return t, nil
}
func (s *Store) transitionFile(ctx context.Context, before, after Tree) error {
	if err := validateFileTree(before); err != nil {
		return err
	}
	if err := validateFileTree(after); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	now, err := s.CaptureFile(s.Root)
	if err != nil {
		return err
	}
	if Digest(now) != Digest(before) {
		return result.Fail("PLAN_STALE", "写入前单文件目标已变化", 3)
	}
	if Digest(before) == Digest(after) {
		return nil
	}
	if after.Exists {
		e := after.Entries[FileContent]
		data, err := s.Data(e)
		if err != nil {
			return err
		}
		if err = s.atomic(s.Root, data, os.FileMode(e.Mode)); err != nil {
			return err
		}
	} else {
		if err = CheckLinks(s.Root); err != nil {
			return err
		}
		r, err := os.OpenRoot(filepath.Dir(s.Root))
		if err != nil {
			return err
		}
		err = r.Remove(filepath.Base(s.Root))
		r.Close()
		if err != nil {
			return err
		}
	}
	if s.Hook != nil {
		if err = s.Hook("replaced", 1); err != nil {
			return err
		}
	}
	now, err = s.CaptureFile(s.Root)
	if err == nil && Digest(now) != Digest(after) {
		err = fmt.Errorf("事务结束时单文件目标变化")
	}
	return err
}
func (s *Store) fileRecoveryTrees(p Plan) (Tree, Tree, error) {
	return s.fileRecoveryTreesWith(p, false)
}

func (s *Store) fileRecoveryTreesWith(p Plan, observeOnly bool) (Tree, Tree, error) {
	var observed Tree
	fail := func(err error) (Tree, Tree, error) { return observed, Tree{}, err }
	if err := validateFileTree(p.Before); err != nil {
		return fail(err)
	}
	if err := validateFileTree(p.After); err != nil {
		return fail(err)
	}
	if Digest(p.Before) != Digest(p.After) {
		tmp := filepath.Join(filepath.Dir(s.Root), s.targetTemp(filepath.Base(s.Root)))
		data, err := ReadFile(tmp)
		if err == nil {
			approved := false
			for _, tree := range []Tree{p.Before, p.After} {
				if e, ok := tree.Entries[FileContent]; ok {
					expected, e := s.Data(e)
					if e != nil {
						return fail(e)
					}
					approved = approved || bytes.HasPrefix(expected, data)
				}
			}
			if !approved {
				return fail(fmt.Errorf("单文件暂存被外部修改"))
			}
			if !observeOnly {
				if err = os.Remove(tmp); err != nil {
					return fail(err)
				}
			}
		} else if !os.IsNotExist(err) {
			return fail(err)
		}
	}
	capture := s.CaptureFile
	if observeOnly {
		capture = s.ObserveFile
	}
	now, err := capture(s.Root)
	if err != nil {
		return fail(err)
	}
	observed = now
	if Digest(now) != Digest(p.Before) && Digest(now) != Digest(p.After) {
		return fail(fmt.Errorf("恢复单文件目标被外部修改: %s", s.Root))
	}
	return now, Clone(p.Before), nil
}
