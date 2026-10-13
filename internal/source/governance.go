package source

import (
	"encoding/json"
	"strings"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Governance replaces the three declared semantic sections as one source edit.
// Native frontmatter remains owned by the originating tool.
type Governance struct {
	Dependencies  map[string]any `json:"dependencies"`
	Compatibility map[string]any `json:"compatibility"`
	Requirements  map[string]any `json:"requirements"`
}

func (m *Manager) governanceAction(req Request, s *Skill, pkg workstore.Tree, after *workstore.Tree) error {
	if !revisionPattern.MatchString(req.Revision) || req.Revision != s.Revision {
		return result.Fail("PLAN_STALE", "治理编辑需绑定加载时的完整修订；固定版本已变化，请核对草稿后重读", 3)
	}
	g := req.Governance
	if g == nil || g.Dependencies == nil || g.Compatibility == nil || g.Requirements == nil {
		return result.Fail("INVALID_INPUT", "治理声明必须同时提供 dependencies、compatibility、requirements 对象；空对象用于清除声明", 2)
	}
	if err := m.checkPackage(*s, pkg); err != nil {
		return result.Fail("SOURCE_PACKAGE_DRIFT", "包与固定版本不一致；先核对并保存源编辑，再修改治理声明", 3)
	}
	old := *s
	// Programmatic callers and YAML/JSON frontends receive the same normalized
	// map/array values used by schema validation and preflight.
	var normalized Governance
	if err := json.Unmarshal(jsonBytes(g), &normalized); err != nil {
		return result.Fail("INVALID_INPUT", err.Error(), 2)
	}
	g = &normalized
	s.Dependencies, s.Compatibility, s.Requirements = g.Dependencies, g.Compatibility, g.Requirements
	// Upgrade only this explicitly edited legacy asset. Existing revisions retain
	// their format, raw package bytes and restoration behavior.
	s.RevisionFormat = "semantic-v1"
	s.PackageDigest = portablePackageDigest(pkg)
	s.Revision = packageRevision(*s, pkg)
	if _, err := config.Validate("skills", jsonBytes(Index{Version: 1, Owner: m.Owner, Scope: m.Scope, Skills: []Skill{*s}})); err != nil {
		return result.Fail("INVALID_INPUT", err.Error(), 2)
	}
	return m.recordGovernance(after, old)
}

// Owned provenance can differ from a cached global asset with the same semantic
// revision (an unchanged whole-package override). Keep these records separate.
func (m *Manager) recordGovernance(t *workstore.Tree, s Skill) error {
	p := ".governance-records/" + s.ID + "/" + strings.TrimPrefix(s.Revision, "sha256:") + ".json"
	want := jsonBytes(governed(s))
	if old, ok := t.Entries[p]; ok {
		data, err := m.Store.Data(old)
		if err != nil {
			return err
		}
		if string(data) != string(want) {
			return result.Fail("REVISION_METADATA_CONFLICT", "已有 owned 治理记录不可改写", 3)
		}
		return nil
	}
	return m.Store.PutFile(t, p, want, 0644)
}
