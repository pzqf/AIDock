package native

import (
	"context"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func TestLocalProfileChangesExpireChecksAndCompileSameRulesForBothTools(t *testing.T) {
	m, _ := writableFixture(t)
	m.Source.Copy = "copy-layer-native"
	fakeProbes(m, discoveredGuide)
	m.probe.rules = fakeRuleLoad
	check, err := m.Check(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := m.Plan(context.Background(), Request{Tools: []string{"codex-cli"}, CheckID: check.ID})
	if err != nil || !plan.WriteSupported {
		t.Fatal(plan, err)
	}
	state, err := m.Source.Profile()
	if err != nil {
		t.Fatal(err)
	}
	before := observe(t, m, m.Source.Store.Root)
	local := map[string]any{"schema_version": 2, "name": "此副本", "rules": []any{"本机规则，不随共享源传播。"}}
	edit, err := m.Source.ProfilePlan(context.Background(), source.ProfileEdit{Target: "local", ExpectedSHA256: state.Layers.LocalSHA256, Profile: local})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.ApplyProfile(context.Background(), "local", edit.Plan.ID, edit.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	if err = m.Apply(context.Background(), plan.ID, plan.Digest); err == nil {
		t.Fatal("old check authorized a changed local layer")
	}
	unchanged(t, m, m.Source.Store.Root, before)
	preview := rulesPlan(t, m)
	for _, target := range preview.Targets {
		if target.Rule == nil || !strings.Contains(target.Rule.Managed, local["rules"].([]any)[0].(string)) {
			t.Fatal(target.Rule)
		}
	}
	check, err = m.Check(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true, RuleProbe: true})
	if err != nil {
		t.Fatal(err)
	}
	qualified, err := m.Plan(context.Background(), Request{Tools: []string{"codex-cli"}, CheckID: check.ID})
	if err != nil || !qualified.WriteSupported {
		t.Fatal(qualified, err)
	}
	if err = m.Apply(context.Background(), qualified.ID, qualified.Digest); err != nil {
		t.Fatal(err)
	}
}
