package native

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func TestDraftQualificationBindsCompleteCandidateAndRejectsWeakerEvidence(t *testing.T) {
	m, _ := writableFixture(t)
	fakeProbes(m, discoveredGuide)
	bundle, err := m.Source.Export()
	if err != nil {
		t.Fatal(err)
	}
	observations := map[string]workstore.Tree{}
	for _, a := range Adapters() {
		tree, err := m.Source.Store.Capture(filepath.Join(m.Root, filepath.FromSlash(a.Directory)))
		if err != nil {
			t.Fatal(err)
		}
		observations[a.ID] = tree
	}
	targets, excluded, err := m.DraftConfigurationTargetsScoped(context.Background(), []string{"claude-code", "codex-cli"}, bundle, observations, nil, SkillsRulesScope)
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.InspectDraftScoped(context.Background(), targets, bundle.Snapshot, true, false, SkillsRulesScope, excluded)
	if err != nil {
		t.Fatal(err)
	}
	q, err := m.QualifyDraft(targets, bundle.Snapshot, SkillsRulesScope, excluded, r)
	if err != nil || len(q.Tools) != 2 || q.CheckDigest != r.Digest {
		t.Fatal(q, err)
	}
	for _, change := range []string{"static", "scope", "snapshot", "version", "target", "input"} {
		t.Run(change, func(t *testing.T) {
			var altered CheckReport
			if err := strict(jsonData(r), &altered); err != nil {
				t.Fatal(err)
			}
			want := "NATIVE_CHECK_REQUIRED"
			switch change {
			case "static":
				altered.Probe = false
			case "scope":
				altered.ApplicationScope = ""
				want = "CHECK_STALE"
			case "snapshot":
				altered.SourceSnapshot = workstore.Hash([]byte("other"))
				want = "CHECK_STALE"
			case "version":
				altered.Tools[0].Version = "99.1.0"
			case "target":
				altered.Tools[0].Target = m.Source.Store.Root
			case "input":
				altered.InputDigest = workstore.Hash([]byte("other"))
				want = "CHECK_STALE"
			}
			altered.Digest = checkDigest(altered)
			_, err := m.QualifyDraft(targets, bundle.Snapshot, SkillsRulesScope, excluded, altered)
			code(t, err, want)
		})
	}
}

func TestDraftQualificationRequiresExactRuleSourceEvidence(t *testing.T) {
	m, _ := writableFixture(t)
	saveRules(t, m, []any{"exact rule bytes"})
	fakeProbes(m, discoveredGuide)
	m.probe.rules = fakeRuleLoad
	bundle, err := m.Source.Export()
	if err != nil {
		t.Fatal(err)
	}
	before, err := m.Source.Store.Capture(filepath.Join(m.Root, ".agents", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	targets, excluded, err := m.DraftConfigurationTargetsScoped(context.Background(), []string{"codex-cli"}, bundle, map[string]workstore.Tree{"codex-cli": before}, nil, SkillsRulesScope)
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.InspectDraftScoped(context.Background(), targets, bundle.Snapshot, true, true, SkillsRulesScope, excluded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.QualifyDraft(targets, bundle.Snapshot, SkillsRulesScope, excluded, r); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"not-requested", "missing", "path", "hash", "status", "code"} {
		t.Run(change, func(t *testing.T) {
			var altered CheckReport
			if err := strict(jsonData(r), &altered); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "not-requested":
				altered.RuleProbe = false
			case "missing":
				altered.Tools[0].Rules = nil
			case "path":
				altered.Tools[0].Rules.Path = "AGENTS.override.md"
			case "hash":
				altered.Tools[0].Rules.SHA256 = workstore.Hash([]byte("other"))
			case "status":
				altered.Tools[0].Rules.Status = "UNKNOWN"
			case "code":
				altered.Tools[0].Rules.Code = "different-evidence"
			}
			altered.Digest = checkDigest(altered)
			_, err := m.QualifyDraft(targets, bundle.Snapshot, SkillsRulesScope, excluded, altered)
			code(t, err, "RULES_CHECK_REQUIRED")
		})
	}
}
