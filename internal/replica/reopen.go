package replica

import (
	"context"

	"github.com/pzqf/AIDock/internal/result"
)

// GenerationReopen separates candidate freshness from historical evidence.
// Reading it starts no tool and grants no publication or installation authority.
type GenerationReopen struct {
	Generation Generation      `json:"generation"`
	Check      GenerationCheck `json:"check"`
	Source     Binding         `json:"source"`
	Targets    []Binding       `json:"targets"`
	Current    bool            `json:"current"`
	Reason     string          `json:"reason,omitempty"`
}

func (m *Manager) ReopenGeneration(ctx context.Context, checkID string) (GenerationReopen, error) {
	r, err := m.GenerationCheckShow(ctx, checkID)
	if err != nil {
		return GenerationReopen{}, err
	}
	g, p, err := m.loadGeneration(r.Generation, r.GenerationDigest)
	if err != nil {
		return GenerationReopen{}, err
	}
	if len(r.Targets) != len(g.Targets) || r.Repair != g.Repair || r.NoChanges != g.NoChanges {
		return GenerationReopen{}, result.Fail("CHECK_CORRUPT", "历史检查与原候选的范围不一致", 5)
	}
	v := GenerationReopen{Generation: g, Check: r, Source: p.Source, Targets: []Binding{}}
	for i, target := range r.Targets {
		if string(data(target.Copy)) != string(data(p.Targets[i].Copy)) || target.Source != g.Targets[i].SourceAfter {
			return GenerationReopen{}, result.Fail("CHECK_CORRUPT", "历史检查与原候选的副本或源摘要不一致", 5)
		}
		v.Targets = append(v.Targets, p.Targets[i].Copy)
	}
	// A tool change or expired check does not necessarily invalidate the saved
	// candidate. Revalidate it independently before offering another check.
	if err = m.revalidateGeneration(ctx, g, p); err == nil {
		err = m.checkGenerationCompiler(ctx, g, p)
	}
	if err != nil {
		if ctx.Err() != nil {
			return GenerationReopen{}, ctx.Err()
		}
		v.Reason = err.Error()
		v.Check.Stale, v.Check.StaleReason = true, v.Reason
		return v, nil
	}
	v.Current = true
	return v, nil
}
