package native

import (
	"context"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

// DraftTargets uses the same ownership, drift, collision and complete-package
// compiler as native previews. All inputs are immutable Local assets. It does
// not execute tools, materialize source candidates or write output roots.
func (m *Manager) DraftTargets(ctx context.Context, tools []string, bundle source.Export, observations map[string]workstore.Tree) ([]Target, error) {
	as, err := selectAdapters(tools)
	if err != nil {
		return nil, err
	}
	if bundle.Root != m.Source.Store.Root || bundle.Owner != m.Source.Owner || bundle.Scope != m.Source.Scope || len(bundle.Authorities) != 0 || bundle.BridgeDigest != "" {
		return nil, result.Fail("REPLICA_GENERATOR_AUTHORITY_UNSUPPORTED", "暂存输入需绑定当前 owned 候选", 4)
	}
	for _, p := range bundle.Packages {
		if p.Layer != "owned" || p.ReadOnly || p.Skill.Source.Mode != "owned" {
			return nil, result.Fail("REPLICA_GENERATOR_AUTHORITY_UNSUPPORTED", "不接管非 owned 包", 4)
		}
	}
	if len(observations) != len(as) {
		return nil, result.Fail("GENERATOR_MANIFEST_INVALID", "生成观察与工具选择不一致", 5)
	}
	out := []Target{}
	for _, a := range as {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		before, ok := observations[a.ID]
		if !ok {
			return nil, result.Fail("GENERATOR_MANIFEST_INVALID", "缺生成目录的完整观察", 5)
		}
		t, err := m.draftTarget(a, bundle, before)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}
