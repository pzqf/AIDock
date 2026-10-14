package engine

import "github.com/pzqf/AIDock/internal/generator"

func (e *Engine) Generators() (*generator.Manager, error) {
	s, err := e.Sources()
	if err != nil {
		return nil, err
	}
	return generator.New(s), nil
}
