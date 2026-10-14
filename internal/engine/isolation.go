package engine

import "github.com/pzqf/AIDock/internal/isolation"

func (e *Engine) Isolation() (*isolation.Manager, error) { return isolation.New(e.Roots.Local) }
