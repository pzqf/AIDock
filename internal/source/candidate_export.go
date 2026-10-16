package source

import (
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
	"strings"
)

// ExportCandidate compiles an immutable source candidate without materializing
// it in a project. Its blobs must already belong to this manager's Local store.
// Cross-copy generation currently supports independent owned sources only.
func (m *Manager) ExportCandidate(t workstore.Tree) (Export, error) {
	out := Export{Root: m.Store.Root, Owner: m.Owner, Scope: m.Scope, Copy: m.Copy,
		Snapshot: workstore.Digest(t), Authorities: []string{}, Packages: []ExportPackage{}}
	if _, ok := t.Entries[m.identityFile()]; !ok {
		return out, result.Fail("SOURCE_NOT_INITIALIZED", "候选缺项目身份", 4)
	}
	idx, profile, err := m.load(&t)
	if err != nil {
		return out, err
	}
	_, effective, err := m.resolve(&t, idx, profile)
	if err != nil {
		return out, err
	}
	layers, err := m.profileLayers()
	if err != nil {
		return out, err
	}
	resolved, err := m.resolveProfile(t, profile, effective, layers)
	if err != nil {
		return out, err
	}
	out.ConfigurationDigest, out.ProfileReport = layers.Digest, &resolved
	out.Rules = profileTexts(resolved.Effective)
	effective, err = m.selectProfileSkills(t, effective, resolved.Effective)
	if err != nil {
		return out, err
	}
	for _, item := range effective {
		s := item.Skill
		if item.Layer != "owned" || item.ReadOnly || s.Source.Mode != "owned" || len(s.BaseSource) != 0 || len(s.Upstream) != 0 || s.Source.PluginID != "" || s.Source.LockDigest != "" {
			return out, result.Fail("REPLICA_GENERATOR_AUTHORITY_UNSUPPORTED", "候选生成只接入独立 owned 技能；外部、继承、覆盖与分叉需单独适配", 4)
		}
	}
	for p := range t.Entries {
		if strings.HasPrefix(p, ".external-approvals/") {
			return out, result.Fail("REPLICA_GENERATOR_AUTHORITY_UNSUPPORTED", "候选含外部批准记录，不能作为 owned 生成接管", 4)
		}
	}
	out.Preflight, err = m.preflight(t, idx, profile, layers)
	out.Preflight.Candidate = true
	if err != nil {
		return out, err
	}
	if err = blockEnabled(out.Preflight); err != nil {
		return out, err
	}
	profile = resolved.Effective
	enabled := map[string]bool{}
	for _, id := range switches(profile)["enable"].([]any) {
		enabled[id.(string)] = true
	}
	for _, id := range switches(profile)["disable"].([]any) {
		enabled[id.(string)] = false
	}
	for _, item := range effective {
		if enabled[item.Skill.ID] {
			out.Packages = append(out.Packages, ExportPackage{EffectiveSkill: item, Tree: subtree(t, item.Skill.Source.Path)})
		}
	}
	return out, nil
}
