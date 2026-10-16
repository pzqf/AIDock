package source

import (
	"context"
	"os"
	"sort"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// WithReplayExport recompiles the exact future project projection, including
// pinned inherited/override/external packages. It never reloads live defaults
// to replace frozen layers or downgrades a read-only package to owned content.
// The consumer must not reenter a source-locking method.
func (m *Manager) WithReplayExport(ctx context.Context, d ReplayDraft, use func(Export) error) error {
	if use == nil {
		return result.Fail("INVALID_INPUT", "候选导出需要明确消费者", 2)
	}
	return m.withReplayPermission(ctx, d, func(_ *pluginpermission.Manager, _ []pluginpermission.Candidate, _ []inventory.Finding) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if m.BridgeFile != "" {
			body, err := workstore.ReadFile(m.BridgeFile)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			observed := ""
			if err == nil {
				observed = workstore.Hash(body)
			}
			if observed != d.BridgeDigest {
				return result.Fail("PLAN_STALE", "外部来源保护登记在候选审查后变化", 3)
			}
		}
		idx, shared, err := m.load(&d.SourceAfter)
		if err != nil {
			return err
		}
		_, skills, err := m.resolve(&d.SourceAfter, idx, shared)
		if err != nil {
			return err
		}
		skills, err = m.selectProfileSkills(d.SourceAfter, skills, d.Report.Effective)
		if err != nil {
			return err
		}
		out := Export{Root: m.Store.Root, Owner: m.Owner, Scope: m.Scope, Copy: m.Copy, Snapshot: workstore.Digest(d.SourceAfter), ConfigurationDigest: d.Layers.Digest, ProfileReport: &d.Report, Rules: profileTexts(d.Report.Effective), Packages: []ExportPackage{}, Authorities: append([]string{}, d.Authorities...), BridgeDigest: d.BridgeDigest}
		if m.Library != nil {
			found := false
			for _, root := range out.Authorities {
				found = found || root == m.Library.Store.Root
			}
			if !found {
				out.Authorities = append(out.Authorities, m.Library.Store.Root)
			}
		}
		sort.Strings(out.Authorities)
		out.Preflight, err = m.preflight(d.SourceAfter, idx, shared, d.Layers)
		out.Preflight.Candidate = true
		if err != nil {
			return err
		}
		enabled := map[string]bool{}
		for _, raw := range array(switches(d.Report.Effective)["enable"]) {
			enabled[raw.(string)] = true
		}
		for _, raw := range array(switches(d.Report.Effective)["disable"]) {
			enabled[raw.(string)] = false
		}
		for _, item := range skills {
			if !enabled[item.Skill.ID] {
				continue
			}
			prefix := item.Skill.Source.Path
			if item.ReadOnly {
				prefix = revisionPath(item.Skill)
			}
			pkg := subtree(d.SourceAfter, prefix)
			if err = m.checkPackage(item.Skill, pkg); err != nil {
				return err
			}
			out.Packages = append(out.Packages, ExportPackage{EffectiveSkill: item, Tree: pkg})
		}
		return use(out)
	})
}
