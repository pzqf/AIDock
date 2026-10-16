package native

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func TestNativeUsesFinalInheritedVersionAndExactPerSkillEvidence(t *testing.T) {
	m, _ := writableFixture(t)
	m.Source.Copy = "22222222-2222-4222-8222-222222222222"
	l, err := source.NewGlobal(filepath.Join(t.TempDir(), "library"), m.Source.Store.Local)
	if err != nil {
		t.Fatal(err)
	}
	id := applySource(t, l, source.Request{Action: "create", Name: "layered-guide", Description: "local A", Document: "VERSION_A_BODY"}).SkillID
	state, err := l.State()
	if err != nil {
		t.Fatal(err)
	}
	a := state.Index.Skills[0].Revision
	if err = m.Source.AttachLibrary(l.Store.Root); err != nil {
		t.Fatal(err)
	}
	applySource(t, m.Source, source.Request{Action: "inherit", ID: id, Revision: a})
	applySource(t, l, source.Request{Action: "edit", ID: id, Files: []source.Patch{{Path: "SKILL.md", Content: "---\nname: layered-guide\ndescription: shared B\n---\nVERSION_B_BODY"}}})
	state, err = l.State()
	if err != nil {
		t.Fatal(err)
	}
	b := state.Index.Skills[0].Revision
	applySource(t, m.Source, source.Request{Action: "inherit", ID: id, Revision: b})
	applySource(t, m.Source, source.Request{Action: "enable", ID: id})
	profile, err := m.Source.Profile()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Source.ProfilePlan(context.Background(), source.ProfileEdit{Target: "local", ExpectedSHA256: profile.Layers.LocalSHA256, ExpectedLayers: profile.Layers.Digest, Profile: map[string]any{"schema_version": 2, "name": "local", "skills": map[string]any{"pins": map[string]any{id: a}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.ApplyProfile(context.Background(), "local", p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	before := observe(t, m, m.Source.Store.Root)
	fakeProbes(m, func(_ context.Context, _ toolBinary, tool, cwd, _ string) (discovery, error) {
		dir := ".agents/skills"
		if tool == "claude-code" {
			dir = ".claude/skills"
		}
		return discovery{Skills: []discoveredSkill{{Name: "guide", Description: "example", Enabled: true, Path: filepath.Join(cwd, filepath.FromSlash(dir), "guide/SKILL.md")}, {Name: "layered-guide", Description: "local A", Enabled: true, Path: filepath.Join(cwd, filepath.FromSlash(dir), "layered-guide/SKILL.md")}}}, nil
	})
	plan := qualifiedPlan(t, m)
	if err = m.Apply(context.Background(), plan.ID, plan.Digest); err != nil {
		t.Fatal(err)
	}
	for _, adapter := range Adapters() {
		body, err := os.ReadFile(filepath.Join(m.Root, filepath.FromSlash(adapter.Directory), "layered-guide/SKILL.md"))
		if err != nil || !strings.Contains(string(body), "VERSION_A_BODY") || strings.Contains(string(body), "VERSION_B_BODY") {
			t.Fatal("native target ignored final Local pin", adapter.ID, err)
		}
	}
	report, err := m.Check(context.Background(), CheckRequest{Tools: []string{"claude-code", "codex-cli"}, Probe: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range report.Tools {
		found := false
		for _, s := range tool.Skills {
			if s.ID == id {
				found = s.Revision == a && s.L1 == "PASS" && s.L2 == "PASS" && s.L3 == "UNKNOWN" && s.L4 == "UNKNOWN"
			}
		}
		if !found {
			t.Fatal("evidence not bound to installed final revision", tool)
		}
	}
	unchanged(t, m, m.Source.Store.Root, before)
	history, err := m.History()
	if err != nil || len(history) == 0 {
		t.Fatal(history, err)
	}
	reverse, err := m.Reverse(history[0].Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyReverse(context.Background(), reverse.ID, reverse.Digest); err != nil {
		t.Fatal(err)
	}
	for _, adapter := range Adapters() {
		if _, err = os.Stat(filepath.Join(m.Root, filepath.FromSlash(adapter.Directory), "layered-guide")); !os.IsNotExist(err) {
			t.Fatal("rollback did not remove newly installed package", adapter.ID, err)
		}
	}
	unchanged(t, m, m.Source.Store.Root, before)
}
