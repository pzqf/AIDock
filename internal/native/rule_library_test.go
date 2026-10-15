package native

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestRuleLibraryCompilesToBothToolsAndRehearsesWithoutSourceWrites(t *testing.T) {
	m, _ := writableFixture(t)
	state, err := m.Source.Profile()
	if err != nil {
		t.Fatal(err)
	}
	files := []source.RuleFile{{ID: workstore.ID(), Name: "规范", Enabled: true, Body: "文件化规则正文。\n保留段落。"}, {ID: workstore.ID(), Name: "停用", Body: "never compile me"}}
	p, err := m.Source.ProfilePlan(context.Background(), source.ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile, RuleFiles: &files, ExpectedRulesSHA256: state.RulesSHA256})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(m.Source.Store.Root, "rules", "unmanaged.md"), []byte("unmanaged text must not compile"))
	before := observe(t, m, m.Source.Store.Root)
	plan := rulesPlan(t, m)
	if plan.Blocked {
		t.Fatal(plan)
	}
	for _, target := range plan.Targets {
		if target.Rule == nil || !strings.Contains(target.Rule.Managed, files[0].Body) || strings.Contains(target.Rule.Managed, files[1].Body) || strings.Contains(target.Rule.Managed, "unmanaged text") {
			t.Fatal(target.Rule)
		}
	}
	r, err := m.Rehearse(context.Background(), plan.ID, plan.Digest)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		data, err := os.ReadFile(filepath.Join(r.Root, name))
		if err != nil || !strings.Contains(string(data), files[0].Body) {
			t.Fatal(name, err)
		}
	}
	unchanged(t, m, m.Source.Store.Root, before)
	fakeProbes(m, discoveredGuide)
	m.probe.rules = fakeRuleLoad
	check, err := m.Check(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true, RuleProbe: true})
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
	// The second tool remains a preview-only candidate here.
	materialize(t, m, plan.Targets[0])
	data, err := m.Source.Store.Data(plan.Targets[0].Rule.After.Entries[workstore.FileContent])
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(m.Root, "CLAUDE.md"), data)
	again := rulesPlan(t, m)
	for _, target := range again.Targets {
		if len(target.Changes) != 0 || len(target.Rule.Changes) != 0 {
			t.Fatal("file rule installation is not idempotent", target)
		}
	}
}
