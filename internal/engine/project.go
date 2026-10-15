package engine

import (
	"path/filepath"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/project"
)

func (e *Engine) Projects() (*project.Manager, error) { return project.New(e.Roots.Local) }

// ProjectEngine is an immutable binding for one request. Another tab cannot
// redirect a delayed write by changing a process-global selected project.
func (e *Engine) ProjectEngine(id string) (*Engine, error) {
	m, err := e.Projects()
	if err != nil {
		return nil, err
	}
	b, err := m.Resolve(id)
	if err != nil {
		return nil, err
	}
	bound := *e
	bound.ProjectName = b.Name
	if e.SourceRoot == b.Root && e.SourceOwner == b.Project && e.SourceScope == b.Scope && (e.SourceCopy == "" || e.SourceCopy == b.Copy) {
		bound.NativeRoot, bound.SourceCopy = filepath.Dir(b.Root), b.Copy
		return &bound, nil
	}
	bound.Manifest = inventory.Manifest{}
	bound.SourceRoot, bound.SourceOwner, bound.SourceScope = b.Root, b.Project, b.Scope
	bound.NativeRoot, bound.SourceCopy = filepath.Dir(b.Root), b.Copy
	bound.GlobalRoot, bound.LibraryRoot, bound.BridgeFile = "", "", ""
	return &bound, nil
}
