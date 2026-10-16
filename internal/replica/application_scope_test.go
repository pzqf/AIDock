package replica

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func enableReplicaScopeMCP(t *testing.T, s *source.Manager) {
	t.Helper()
	b, err := config.ReadFile(filepath.Join("..", "config", "testdata", "mcp.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := config.Validate("mcp", b)
	if err != nil {
		t.Fatal(err)
	}
	conf := v.(map[string]any)
	conf["servers"].([]any)[0].(map[string]any)["enabled"] = true
	state, err := s.MCP()
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.MCPPlan(context.Background(), source.MCPEdit{ExpectedSHA256: state.SHA256, Config: conf})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
}

func scopedReplicaFixture(t *testing.T, count int) (*Manager, []Binding, []*source.Manager) {
	t.Helper()
	m, b, s := replicaRuleFixture(t, count)
	for _, copy := range s {
		enableReplicaScopeMCP(t, copy)
		for path, content := range map[string]string{
			".mcp.json":                  "{\r\n  \"mcpServers\":{\"manual\":{\"command\":\"keep\"}}\r\n}\r\n",
			".codex/config.toml":         "# manual\r\nmodel = \"keep\"\r\n[mcp_servers.manual]\r\ncommand = \"keep\"\r\n",
			".aidock-mcp-ownership.json": "unrelated sidecar bytes",
		} {
			generatedFixtureWrite(t, filepath.Dir(copy.Store.Root), path, []byte(content))
		}
	}
	return m, b, s
}

func scopedRepair(t *testing.T, m *Manager, b []Binding) Generation {
	t.Helper()
	req := repairRequest(t, m, b)
	req.ApplicationScope = native.SkillsRulesScope
	g, err := m.StageRepair(context.Background(), req)
	if err != nil || g.Blocked || g.ApplicationScope != native.SkillsRulesScope || g.Version != 5 {
		t.Fatal(g, err)
	}
	return g
}

func TestReplicaApplicationScopeFiveCopiesJointRollbackUndo(t *testing.T) {
	m, b, copies := scopedReplicaFixture(t, 5)
	ctx := context.Background()
	baseline(t, m, b)
	applySource(t, copies[0], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "asset.txt", Content: "publisher update with MCP enabled"}}})
	originals := map[string]workstore.Tree{}
	for _, copy := range copies {
		root := filepath.Dir(copy.Store.Root)
		originals[root] = generatedObservation(t, m, root)
	}
	control := generatedObservation(t, m, m.Control.Root)
	p, err := m.Resolve(ctx, resolveRequest(t, m, b, map[string]string{b[1].ID: "source", b[2].ID: "source", b[3].ID: "source", b[4].ID: "source"}))
	if err != nil {
		t.Fatal(err)
	}
	req := GenerationRequest{Plan: p.ID, Digest: p.Digest, Tools: []string{"claude-code", "codex-cli"}}
	full, err := m.StageGeneration(ctx, req)
	if err != nil || !full.Blocked || full.ApplicationScope != "" || full.Version != 4 {
		t.Fatal("default must retain complete profile gates", full, err)
	}
	req.ApplicationScope = native.SkillsRulesScope
	g, err := m.StageGeneration(ctx, req)
	if err != nil || g.Blocked || g.Version != 5 || len(g.Targets) != 4 {
		t.Fatal(g, err)
	}
	for _, target := range g.Targets {
		if len(target.ExcludedChecks) == 0 {
			t.Fatal("MCP diagnoses disappeared", target.Copy)
		}
		for _, f := range target.ExcludedChecks {
			if f.Component != "mcp" {
				t.Fatal("non-MCP diagnosis excluded", f)
			}
		}
		for _, output := range target.Outputs {
			if output.Rule == nil || len(output.Packages) != 1 {
				t.Fatal("skills or rules lost", output)
			}
		}
	}
	check, err := m.CheckGeneration(ctx, GenerationCheckRequest{Plan: g.ID, Digest: g.Digest})
	if err != nil || check.ApplicationScope != g.ApplicationScope || check.WriteSupported || check.PublicationStatus != "BLOCKED" {
		t.Fatal(check, err)
	}
	for i, target := range check.Targets {
		if target.Inspection.ApplicationScope != g.ApplicationScope || string(data(target.Inspection.ExcludedChecks)) != string(data(g.Targets[i].ExcludedChecks)) {
			t.Fatal("scope evidence lost", target)
		}
	}
	reopened, err := m.ReopenGeneration(ctx, check.ID)
	if err != nil || !reopened.Current || reopened.Generation.Digest != g.Digest || reopened.Generation.ApplicationScope != native.SkillsRulesScope {
		t.Fatal(reopened, err)
	}
	r, err := m.RehearseGenerated(ctx, g.ID, g.Digest)
	if err != nil || r.Status != "APPLIED" || len(r.Roots) != 21 || r.Transactions[0].Generated != 8 || r.Transactions[0].RuleFiles != 8 {
		t.Fatal(r, err)
	}
	verify := func(after bool) {
		t.Helper()
		for _, target := range g.Targets {
			for _, output := range target.Outputs {
				tree, rule := output.Before, output.Rule.Before
				if after {
					tree, rule = output.After, output.Rule.After
				}
				generatedEqual(t, m, generatedRoot(r, generatedPath(target.Copy, output.Adapter.ID)), tree)
				replicaFileEqual(t, m, generatedRoot(r, generatedRulePath(target.Copy, output.Adapter.ID)), rule)
			}
		}
		for _, target := range p.Targets {
			want := target.Before
			if after {
				want = target.After
			}
			generatedEqual(t, m, generatedRoot(r, "copies/"+target.Copy.ID+"/.aidock"), want)
		}
		want := p.ControlBefore
		if after {
			want = p.ControlAfter
		}
		generatedEqual(t, m, generatedRoot(r, "control"), want)
		for root, want := range originals {
			generatedEqual(t, m, root, want)
		}
		generatedEqual(t, m, m.Control.Root, control)
	}
	verify(true)
	for _, after := range []bool{false, true} {
		plan, err := m.RehearsalReverse(r.ID, r.Transactions[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		r, err = m.RehearsalApply(ctx, r.ID, plan.ID, plan.Digest)
		if err != nil {
			t.Fatal(err)
		}
		verify(after)
	}
}

func TestReplicaApplicationScopeCannotBeChangedByRehashingCandidate(t *testing.T) {
	for _, change := range []string{"scope", "legacy", "strip", "excluded-missing", "excluded-altered", "excluded-general"} {
		t.Run(change, func(t *testing.T) {
			m, b, _ := scopedReplicaFixture(t, 2)
			g := scopedRepair(t, m, b)
			switch change {
			case "scope":
				g.ApplicationScope = "full"
			case "legacy":
				g.Version = 4
			case "strip":
				g.Version, g.ApplicationScope, g.Targets[0].ExcludedChecks = 4, "", nil
			case "excluded-missing":
				g.Targets[0].ExcludedChecks = nil
			case "excluded-altered":
				g.Targets[0].ExcludedChecks[0].Detail = "forged diagnosis"
			case "excluded-general":
				g.Targets[0].ExcludedChecks = append(g.Targets[0].ExcludedChecks, inventory.Finding{Component: "profile", Status: "FAIL", Code: "MCP_LOOKALIKE"})
			}
			g.Digest = generationDigest(g)
			if err := saveLocal(m.generationFile(g.ID), g); err != nil {
				t.Fatal(err)
			}
			_, err := m.CheckGeneration(context.Background(), GenerationCheckRequest{Plan: g.ID, Digest: g.Digest, Probe: true, RuleProbe: true})
			code(t, err, "GENERATOR_MANIFEST_INVALID")
			_, err = m.RehearseGenerated(context.Background(), g.ID, g.Digest)
			code(t, err, "GENERATOR_MANIFEST_INVALID")
			if sessions, err := m.RehearsalHistory(); err != nil || len(sessions) != 0 {
				t.Fatal("forged scope started rehearsal", sessions, err)
			}
		})
	}
}

func TestReplicaApplicationScopeStillExpiresOnCompleteConfiguration(t *testing.T) {
	for _, change := range []string{"mcp", "local", "rules"} {
		t.Run(change, func(t *testing.T) {
			m, b, copies := scopedReplicaFixture(t, 2)
			g := scopedRepair(t, m, b)
			ctx := context.Background()
			r, err := m.CheckGeneration(ctx, GenerationCheckRequest{Plan: g.ID, Digest: g.Digest})
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "mcp":
				path := filepath.Join(copies[1].Store.Root, "mcp.yaml")
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(path, append(body, []byte("\n ")...), 0644); err != nil {
					t.Fatal(err)
				}
			case "local":
				local, err := m.copySource(b[1])
				if err != nil {
					t.Fatal(err)
				}
				saveCopyProfile(t, local, map[string]any{"schema_version": 2, "name": "local", "description": "late local change"})
			case "rules":
				generatedFixtureWrite(t, filepath.Dir(b[1].Root), "AGENTS.md", []byte("late manual rule"))
			}
			_, err = m.RehearseGenerated(ctx, g.ID, g.Digest)
			code(t, err, "PLAN_STALE")
			shown, err := m.GenerationCheckShow(ctx, r.ID)
			if err != nil || !shown.Stale {
				t.Fatal(shown, err)
			}
		})
	}
}

func TestReplicaApplicationScopeInvalidInputDoesNotCreatePlans(t *testing.T) {
	m, b, _ := fixture(t, 2)
	for _, scope := range []string{"mcp", "skills", "SKILLS-RULES", " skills-rules"} {
		_, err := m.StageGeneration(context.Background(), GenerationRequest{ApplicationScope: scope})
		code(t, err, "INVALID_INPUT")
		req := repairRequest(t, m, b)
		req.ApplicationScope = scope
		_, err = m.StageRepair(context.Background(), req)
		code(t, err, "INVALID_INPUT")
	}
}
