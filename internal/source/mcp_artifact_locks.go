package source

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/pzqf/AIDock/internal/pluginartifact"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Lock the actual artifact roots before observing candidates and keep them held
// through installation registration. All projects sharing a path contend on the
// same lock used by import, rollback and recovery.
func (m *Manager) withArtifactLocks(t workstore.Tree, fn func() error) error {
	e, ok := t.Entries["plugins.lock"]
	if !ok {
		return fn()
	}
	b, err := m.Store.Data(e)
	if err != nil {
		return err
	}
	lock, err := pluginlock.Parse(b)
	if err != nil {
		return fn()
	}
	paths := []string{}
	for _, p := range lock.Plugins {
		paths = append(paths, p.Artifact.Path)
	}
	sort.Strings(paths)
	var next func(int) error
	next = func(i int) error {
		if i == len(paths) {
			return fn()
		}
		s, err := pluginartifact.RecoveryStore(m.Store.Local, paths[i])
		if err != nil {
			return err
		}
		if err = workstore.CheckLinks(filepath.Dir(s.Root)); err != nil {
			return err
		}
		if _, err = os.Stat(filepath.Dir(s.Root)); os.IsNotExist(err) {
			return next(i + 1)
		} else if err != nil {
			return err
		}
		return s.Locked(func() error { return next(i + 1) })
	}
	return next(0)
}
