package native

import (
	"context"
	"strings"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

// DraftTargets uses the same ownership, drift, collision and complete-package
// compiler as native previews. All inputs are immutable Local assets. It does
// not execute tools, materialize source candidates or write output roots.
func (m *Manager) DraftTargets(ctx context.Context, tools []string, bundle source.Export, observations map[string]workstore.Tree) ([]Target, error) {
	return m.DraftTargetsPreserving(ctx, tools, bundle, observations, nil)
}

// Preservation is derived from the registered copy's local exclusions, never
// from an output path supplied by a client. IDs also protect previous names.
type Preservation struct {
	IDs   []string `json:"ids,omitempty"`
	Names []string `json:"names,omitempty"`
	All   bool     `json:"all,omitempty"`
}

func (p *Preservation) Matches(id, name string) bool {
	if p == nil {
		return false
	}
	if p.All {
		return true
	}
	for _, v := range p.IDs {
		if v == id {
			return true
		}
	}
	for _, v := range p.Names {
		if strings.EqualFold(v, name) {
			return true
		}
	}
	return false
}

func (m *Manager) DraftTargetsPreserving(ctx context.Context, tools []string, bundle source.Export, observations map[string]workstore.Tree, keep *Preservation) ([]Target, error) {
	return m.draftTargets(ctx, tools, bundle, observations, keep, false)
}

// DraftConfigurationTargetsPreserving includes each tool's exact rule file and
// priority observation. Callers must bind both files and directories in recovery.
func (m *Manager) DraftConfigurationTargetsPreserving(ctx context.Context, tools []string, bundle source.Export, observations map[string]workstore.Tree, keep *Preservation) ([]Target, error) {
	return m.draftTargets(ctx, tools, bundle, observations, keep, true)
}

func (m *Manager) draftTargets(ctx context.Context, tools []string, bundle source.Export, observations map[string]workstore.Tree, keep *Preservation, includeRules bool) ([]Target, error) {
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
		t, err := m.draftTargetWithRules(a, bundle, before, keep, includeRules)
		if err != nil {
			return nil, err
		}
		if includeRules && t.Status != "FAIL" {
			t, err = m.targetRule(t, bundle)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, t)
	}
	return out, nil
}
