package replica

import (
	"path/filepath"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

func transferOutput(from, to *workstore.Store, output native.Target) error {
	trees := []workstore.Tree{output.Before, output.After}
	if r := output.Rule; r != nil {
		trees = append(trees, r.Before, r.After)
		if r.Override != nil {
			trees = append(trees, r.Override.Before)
		}
	}
	return transfer(from, to, trees...)
}

func (m *Manager) revalidateRule(copy Binding, output native.Target) error {
	r := output.Rule
	if r == nil {
		return nil
	}
	inputs := []native.RuleInput{{Path: r.Path, Before: r.Before}}
	if r.Override != nil {
		inputs = append(inputs, *r.Override)
	}
	for _, input := range inputs {
		path := filepath.Join(filepath.Dir(copy.Root), input.Path)
		if err := workstore.CoordinatedPending(path); err != nil {
			return err
		}
		now, err := m.Control.CaptureFile(path)
		if err != nil {
			return err
		}
		if workstore.Digest(now) != workstore.Digest(input.Before) {
			return result.Fail("PLAN_STALE", "规则文件或优先级输入在暂存后变化，请重新生成清单", 3)
		}
	}
	return nil
}
