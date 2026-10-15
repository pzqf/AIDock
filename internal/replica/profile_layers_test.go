package replica

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func saveCopyProfile(t *testing.T, m *source.Manager, profile map[string]any) {
	t.Helper()
	state, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.ProfilePlan(context.Background(), source.ProfileEdit{Target: "local", ExpectedSHA256: state.Layers.LocalSHA256, ExpectedLayers: state.Layers.Digest, Profile: profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyProfile(context.Background(), "local", p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationUsesEachCopyLocalProfileWithoutSharingIt(t *testing.T) {
	m, b, copies := fixture(t, 3)
	ctx := context.Background()
	baseline(t, m, b)
	applySource(t, copies[0], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "asset.txt", Content: "publisher"}}})
	local, err := m.copySource(b[1])
	if err != nil {
		t.Fatal(err)
	}
	profile := map[string]any{"schema_version": 2, "name": "local", "skills": map[string]any{"disable": []any{skill}}}
	saveCopyProfile(t, local, profile)
	state, err := local.Profile()
	if err != nil {
		t.Fatal(err)
	}
	originals := captureSources(t, copies)
	shared, err := local.Shared()
	if err != nil || len(shared.Entries) != 1 || !shared.Entries[0].Enabled {
		t.Fatal("machine switch leaked into shared comparison", shared, err)
	}
	p, err := m.Resolve(ctx, resolveRequest(t, m, b, map[string]string{b[1].ID: "source", b[2].ID: "source"}))
	if err != nil {
		t.Fatal(err)
	}
	g, err := m.StageGeneration(ctx, GenerationRequest{Plan: p.ID, Digest: p.Digest, Tools: []string{"codex-cli", "claude-code"}})
	if err != nil || len(g.Targets) != 2 || g.Blocked {
		t.Fatal(g, err)
	}
	for _, target := range g.Targets {
		if target.ConfigurationDigest == "" {
			t.Fatal("missing configuration binding")
		}
		for _, output := range target.Outputs {
			_, enabled := output.After.Entries["guide/SKILL.md"]
			if enabled != (target.Copy == b[2].ID) {
				t.Fatal("target ignored its own switch", target.Copy, output.Adapter.ID)
			}
		}
	}
	check, err := m.CheckGeneration(ctx, GenerationCheckRequest{Plan: g.ID, Digest: g.Digest})
	if err != nil {
		t.Fatal(err)
	}
	shown, err := m.GenerationCheckShow(ctx, check.ID)
	if err != nil || shown.Stale {
		t.Fatal(shown, err)
	}
	unchanged(t, copies, originals)
	after, err := local.Profile()
	if err != nil || after.Layers.LocalSHA256 != state.Layers.LocalSHA256 {
		t.Fatal("generation modified machine configuration", err)
	}
	// Even a description-only edit invalidates prior evidence although the
	// generated package set and shared comparison stay exactly the same.
	profile["description"] = "late machine edit"
	saveCopyProfile(t, local, profile)
	_, err = m.RehearseGenerated(ctx, g.ID, g.Digest)
	code(t, err, "PLAN_STALE")
	shown, err = m.GenerationCheckShow(ctx, check.ID)
	if err != nil || !shown.Stale {
		t.Fatal("machine edit did not expire check", shown, err)
	}
	unchanged(t, copies, originals)
	if rows, err := m.RehearsalHistory(); err != nil || len(rows) != 0 {
		t.Fatal("stale configuration seeded a rehearsal", rows, err)
	}
}

func TestExternalGenerationExpiresWhenCopyLocalProfileChanges(t *testing.T) {
	m, b, copies := fixture(t, 2)
	ctx := context.Background()
	local, err := m.copySource(b[1])
	if err != nil {
		t.Fatal(err)
	}
	profile := map[string]any{"schema_version": 2, "name": "local", "description": "before"}
	saveCopyProfile(t, local, profile)
	stage, _ := stageExternalFixture(t, m)
	builtIn, err := m.StageRepair(ctx, repairRequest(t, m, b))
	if err != nil {
		t.Fatal(err)
	}
	g, err := m.StageExternalGeneration(ctx, ExternalGenerationRequest{Plan: builtIn.Plan, Digest: builtIn.PlanDigest, Stage: stage.ID, StageDigest: stage.Digest})
	if err != nil {
		t.Fatal(err)
	}
	before := captureSources(t, copies)
	profile["description"] = "after"
	saveCopyProfile(t, local, profile)
	_, err = m.RehearseGenerated(ctx, g.ID, g.Digest)
	code(t, err, "PLAN_STALE")
	unchanged(t, copies, before)
}

func TestReplicaRuleConfigurationJoinsRepairAndRollback(t *testing.T) {
	m, b, copies := fixture(t, 2)
	local, err := m.copySource(b[1])
	if err != nil {
		t.Fatal(err)
	}
	saveCopyProfile(t, local, map[string]any{"schema_version": 2, "name": "local", "rules": []any{"preserve this rule"}})
	before := captureSources(t, copies)
	g, err := m.StageRepair(context.Background(), repairRequest(t, m, b))
	if err != nil || g.Blocked || g.Version != 4 || len(g.Targets) != 1 {
		t.Fatal("rule target must join configuration generation", g, err)
	}
	for _, output := range g.Targets[0].Outputs {
		if output.Rule == nil || !strings.Contains(output.Rule.Managed, "preserve this rule") {
			t.Fatal("missing merged rule", output)
		}
	}
	r, err := m.RehearseGenerated(context.Background(), g.ID, g.Digest)
	if err != nil || r.Status != "APPLIED" || len(r.Roots) != 6 || r.Transactions[0].RuleFiles != 2 {
		t.Fatal(r, err)
	}
	for _, after := range []bool{false, true} {
		reverse, err := m.RehearsalReverse(r.ID, r.Transactions[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		r, err = m.RehearsalApply(context.Background(), r.ID, reverse.ID, reverse.Digest)
		if err != nil {
			t.Fatal(err)
		}
		for _, output := range g.Targets[0].Outputs {
			path := generatedRoot(r, generatedRulePath(b[1].ID, output.Adapter.ID))
			current, err := m.Control.CaptureFile(path)
			want := output.Rule.Before
			if after {
				want = output.Rule.After
			}
			if err != nil || workstore.Digest(current) != workstore.Digest(want) {
				t.Fatal("rule rollback/undo lost bytes", err)
			}
		}
	}
	for _, tool := range []string{"codex-cli", "claude-code"} {
		root := filepath.Join(filepath.Dir(b[1].Root), filepath.FromSlash(mirrorDirectory(tool)))
		if generatedObservation(t, m, root).Exists {
			t.Fatal("unsupported rule configuration wrote native files")
		}
	}
	unchanged(t, copies, before)
}
