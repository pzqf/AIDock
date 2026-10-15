package native

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func TestSkillEvidenceSeparatesFilesDiscoveryAndUnrelatedConflicts(t *testing.T) {
	m, id := fixture(t)
	second := applySource(t, m.Source, source.Request{Action: "create", Name: "second", Description: "second"})
	applySource(t, m.Source, source.Request{Action: "enable", ID: second.SkillID})
	fakeProbes(m, discoveredGuide)
	ctx := context.Background()
	plan, err := m.compile(ctx, Request{Tools: []string{"codex-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	materialize(t, m, plan.Targets[0])
	write(t, filepath.Join(m.Root, ".agents/skills/second/SKILL.md"), []byte("manual drift"))
	before := observe(t, m, plan.Targets[0].Root)
	report, err := m.Check(ctx, CheckRequest{Tools: []string{"codex-cli"}, Probe: true})
	if err != nil || report.Tools[0].L1 != "FAIL" {
		t.Fatal(report, err)
	}
	for _, skill := range report.Tools[0].Skills {
		want := "PASS"
		if skill.ID == second.SkillID {
			want = "FAIL"
		}
		if skill.L1 != want || skill.L2 != "UNKNOWN" || skill.L3 != "UNKNOWN" || skill.L4 != "UNKNOWN" || skill.FileDetail == "" {
			t.Fatal("borrowed aggregate evidence", id, skill)
		}
	}
	if len(report.Tools[0].Skills) != 2 {
		t.Fatal(report)
	}
	unchanged(t, m, plan.Targets[0].Root, before)
}

func TestSkillFileEvidenceDistinguishesMissingUnownedAndOldRevision(t *testing.T) {
	for _, scenario := range []string{"missing", "unowned", "old", "empty-directory-drift"} {
		t.Run(scenario, func(t *testing.T) {
			m, id := fixture(t)
			fakeProbes(m, discoveredGuide)
			ctx := context.Background()
			plan, err := m.compile(ctx, Request{Tools: []string{"codex-cli"}})
			if err != nil {
				t.Fatal(err)
			}
			want := "NOT_APPLIED"
			if scenario != "missing" {
				materialize(t, m, plan.Targets[0])
			}
			switch scenario {
			case "unowned":
				if err := os.Remove(filepath.Join(plan.Targets[0].Root, ownershipFile)); err != nil {
					t.Fatal(err)
				}
				want = "FAIL"
			case "old":
				applySource(t, m.Source, source.Request{Action: "edit", ID: id, Files: []source.Patch{{Path: "references/notes.md", Content: "new reference"}}})
			case "empty-directory-drift":
				if err := os.Remove(filepath.Join(plan.Targets[0].Root, "guide/empty")); err != nil {
					t.Fatal(err)
				}
				want = "FAIL"
			}
			report, err := m.Check(ctx, CheckRequest{Tools: []string{"codex-cli"}})
			if err != nil || len(report.Tools[0].Skills) != 1 || report.Tools[0].Skills[0].L1 != want {
				t.Fatal(report, err)
			}
		})
	}
}

func TestLegacyCheckWithoutSkillLevelsRetainsItsDigest(t *testing.T) {
	m, _ := fixture(t)
	fakeProbes(m, discoveredGuide)
	report, err := m.Check(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := range report.Tools[0].Skills {
		skill := &report.Tools[0].Skills[i]
		skill.L1, skill.L2, skill.L3, skill.L4, skill.FileDetail = "", "", "", "", ""
	}
	report.Digest = checkDigest(report)
	write(t, filepath.Join(m.checksDir(), "reports", report.ID+".json"), jsonData(report))
	latest, err := m.Latest(context.Background())
	if err != nil || latest.Stale || latest.Digest != report.Digest || latest.Tools[0].Skills[0].L1 != "" {
		t.Fatal(latest, err)
	}
}
