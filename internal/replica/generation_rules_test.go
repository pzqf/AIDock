package replica

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func replicaRuleFixture(t *testing.T, count int) (*Manager, []Binding, []*source.Manager) {
	t.Helper()
	m, bindings, copies := fixture(t, count)
	for i, copy := range bindings[1:] {
		local, err := m.copySource(copy)
		if err != nil {
			t.Fatal(err)
		}
		saveCopyProfile(t, local, map[string]any{"schema_version": 2, "name": "local", "rules": []any{fmt.Sprintf("Local rule %d: preserve scope and report rollback.", i+1)}})
		for _, path := range []string{"CLAUDE.md", "AGENTS.md"} {
			generatedFixtureWrite(t, filepath.Dir(copy.Root), path, []byte(fmt.Sprintf("\xef\xbb\xbf# Manual %d\r\nKeep these bytes.\r\n\r\n", i+1)))
		}
	}
	return m, bindings, copies
}

func replicaFileEqual(t *testing.T, m *Manager, path string, want workstore.Tree) {
	t.Helper()
	now, err := m.Control.CaptureFile(path)
	if err != nil || workstore.Digest(now) != workstore.Digest(want) {
		t.Fatal("file snapshot differs", path, err)
	}
}

func TestGenerationRulesFiveCopiesJointRollbackUndo(t *testing.T) {
	m, b, s := replicaRuleFixture(t, 5)
	ctx := context.Background()
	baseline(t, m, b)
	applySource(t, s[0], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "asset.txt", Content: "publisher update"}}})
	originals := captureSources(t, s)
	control := generatedObservation(t, m, m.Control.Root)
	p, err := m.Resolve(ctx, resolveRequest(t, m, b, map[string]string{b[1].ID: "source", b[2].ID: "source", b[3].ID: "source", b[4].ID: "source"}))
	if err != nil {
		t.Fatal(err)
	}
	g, err := m.StageGeneration(ctx, GenerationRequest{Plan: p.ID, Digest: p.Digest, Tools: []string{"claude-code", "codex-cli"}})
	if err != nil || g.Blocked || g.NoChanges || g.Version != 4 || len(g.Targets) != 4 {
		t.Fatal(g, err)
	}
	for _, target := range g.Targets {
		index := 0
		for i, copy := range b {
			if copy.ID == target.Copy {
				index = i
			}
		}
		for _, output := range target.Outputs {
			if output.Rule == nil || !strings.Contains(output.Rule.Managed, fmt.Sprintf("Local rule %d:", index)) || !strings.HasPrefix(output.Rule.Prefix, "\xef\xbb\xbf# Manual") {
				t.Fatal("local rule or manual bytes lost", target.Copy, output.Rule)
			}
		}
	}
	r, err := m.RehearseGenerated(ctx, g.ID, g.Digest)
	if err != nil || r.Status != "APPLIED" || len(r.Roots) != 21 || r.Transactions[0].Targets != 4 || r.Transactions[0].Generated != 8 || r.Transactions[0].RuleFiles != 8 {
		t.Fatal(r, err)
	}
	check := func(after bool) {
		t.Helper()
		for _, target := range g.Targets {
			for _, output := range target.Outputs {
				rule, dir := output.Rule.Before, output.Before
				if after {
					rule, dir = output.Rule.After, output.After
				}
				replicaFileEqual(t, m, generatedRoot(r, generatedRulePath(target.Copy, output.Adapter.ID)), rule)
				generatedEqual(t, m, generatedRoot(r, generatedPath(target.Copy, output.Adapter.ID)), dir)
				replicaFileEqual(t, m, filepath.Join(filepath.Dir(output.Root), "..", output.Rule.Path), output.Rule.Before)
			}
		}
		for _, candidate := range p.Targets {
			want := candidate.Before
			if after {
				want = candidate.After
			}
			generatedEqual(t, m, generatedRoot(r, "copies/"+candidate.Copy.ID+"/.aidock"), want)
		}
		want := p.ControlBefore
		if after {
			want = p.ControlAfter
		}
		generatedEqual(t, m, generatedRoot(r, "control"), want)
		unchanged(t, s, originals)
		generatedEqual(t, m, m.Control.Root, control)
	}
	check(true)
	for _, after := range []bool{false, true} {
		plan, err := m.RehearsalReverse(r.ID, r.Transactions[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		r, err = m.RehearsalApply(ctx, r.ID, plan.ID, plan.Digest)
		if err != nil {
			t.Fatal(err)
		}
		check(after)
	}
}

func TestGenerationRulesLateObservationExpiresCandidateAndCheck(t *testing.T) {
	for _, path := range []string{"CLAUDE.md", "AGENTS.md", "AGENTS.override.md"} {
		t.Run(path, func(t *testing.T) {
			m, b, _ := replicaRuleFixture(t, 2)
			ctx := context.Background()
			g, err := m.StageRepair(ctx, repairRequest(t, m, b))
			if err != nil {
				t.Fatal(err)
			}
			r, err := m.CheckGeneration(ctx, GenerationCheckRequest{Plan: g.ID, Digest: g.Digest})
			if err != nil || r.RuleProbe || r.Targets[0].Inspection.RuleProbe {
				t.Fatal(r, err)
			}
			for _, tool := range r.Targets[0].Inspection.Tools {
				if tool.Rules == nil || tool.Rules.Status != "UNKNOWN" || tool.Rules.Code != "RULES_PROBE_REQUIRED" {
					t.Fatal("default check claimed rule evidence", tool)
				}
			}
			generatedFixtureWrite(t, filepath.Dir(b[1].Root), path, []byte("late manual edit"))
			_, err = m.RehearseGenerated(ctx, g.ID, g.Digest)
			code(t, err, "PLAN_STALE")
			shown, err := m.GenerationCheckShow(ctx, r.ID)
			if err != nil || !shown.Stale {
				t.Fatal(shown, err)
			}
			if history, err := m.RehearsalHistory(); err != nil || len(history) != 0 {
				t.Fatal("stale rules created a session", history, err)
			}
		})
	}
}

func TestGenerationRulesRecomputedManifestCannotChangeRuleContract(t *testing.T) {
	for _, change := range []string{"path", "after", "changes", "remove", "override"} {
		t.Run(change, func(t *testing.T) {
			m, b, _ := replicaRuleFixture(t, 2)
			ctx := context.Background()
			g, err := m.StageRepair(ctx, repairRequest(t, m, b))
			if err != nil {
				t.Fatal(err)
			}
			output := &g.Targets[0].Outputs[0]
			switch change {
			case "path":
				output.Rule.Path = "../outside.md"
			case "after":
				if err = m.Control.PutFile(&output.Rule.After, workstore.FileContent, []byte("forged rules"), 0644); err != nil {
					t.Fatal(err)
				}
				output.Rule.Changes = workstore.Changes(output.Rule.Before, output.Rule.After)
			case "changes":
				output.Rule.Changes = nil
			case "remove":
				output.Rule = nil
			case "override":
				g.Targets[0].Outputs[1].Rule.Override.Path = "private.md"
			}
			g.Digest = generationDigest(g)
			if err = saveLocal(m.generationFile(g.ID), g); err != nil {
				t.Fatal(err)
			}
			_, err = m.CheckGeneration(ctx, GenerationCheckRequest{Plan: g.ID, Digest: g.Digest, Probe: true, RuleProbe: true})
			code(t, err, "GENERATOR_MANIFEST_INVALID")
			_, err = m.RehearseGenerated(ctx, g.ID, g.Digest)
			code(t, err, "GENERATOR_MANIFEST_INVALID")
		})
	}
}

func TestGenerationRulesBlockingAndExternalSkillOnlyContract(t *testing.T) {
	for _, kind := range []string{"shadowed", "unowned", "drift", "external"} {
		t.Run(kind, func(t *testing.T) {
			m, b, _ := replicaRuleFixture(t, 2)
			ctx := context.Background()
			want := "RULES_OWNERSHIP_REQUIRED"
			switch kind {
			case "shadowed":
				generatedFixtureWrite(t, filepath.Dir(b[1].Root), "AGENTS.override.md", []byte("priority override"))
				want = "RULES_SHADOWED"
			case "unowned":
				generatedFixtureWrite(t, filepath.Dir(b[1].Root), "CLAUDE.md", []byte("<!-- aidock:begin -->\nunowned\n<!-- aidock:end -->\n"))
			case "drift":
				initial, err := m.StageRepair(ctx, repairRequest(t, m, b))
				if err != nil {
					t.Fatal(err)
				}
				for _, output := range initial.Targets[0].Outputs {
					if err = transfer(m.Control, m.Source.Store, output.After); err != nil {
						t.Fatal(err)
					}
					externalWriteTree(t, m, output.Root, output.After)
					text, err := m.Control.Data(output.Rule.After.Entries[workstore.FileContent])
					if err != nil {
						t.Fatal(err)
					}
					text = bytes.Replace(text, []byte("Local rule"), []byte("Manual edit"), 1)
					generatedFixtureWrite(t, filepath.Dir(b[1].Root), output.Rule.Path, text)
				}
				want = "RULES_MANAGED_DRIFT"
			case "external":
				want = "RULES_TARGET_REQUIRED"
			}
			g, err := m.StageRepair(ctx, repairRequest(t, m, b))
			if err != nil {
				t.Fatal(err)
			}
			if kind == "external" {
				stage, _ := stageExternalFixture(t, m)
				g, err = m.StageExternalGeneration(ctx, ExternalGenerationRequest{Plan: g.Plan, Digest: g.PlanDigest, Stage: stage.ID, StageDigest: stage.Digest})
			}
			if err != nil || !g.Blocked {
				t.Fatal("rules were silently dropped or overwritten", g, err)
			}
			found := false
			for _, output := range g.Targets[0].Outputs {
				for _, finding := range output.Findings {
					found = found || finding.Code == want && finding.Status == "FAIL"
				}
			}
			if !found {
				t.Fatal("missing rule blocker", want, g)
			}
			r, err := m.CheckGeneration(ctx, GenerationCheckRequest{Plan: g.ID, Digest: g.Digest, Probe: true, RuleProbe: true})
			if err != nil || r.CandidateStatus != "FAIL" || r.Targets[0].Inspection.Probe || r.Targets[0].Inspection.RuleProbe {
				t.Fatal("blocked batch started tools", r, err)
			}
			_, err = m.RehearseGenerated(ctx, g.ID, g.Digest)
			code(t, err, "GENERATOR_PREFLIGHT_FAILED")
		})
	}
}

func TestGenerationRulesCrashHelper(t *testing.T) {
	if os.Getenv("AIDOCK_RULES_CRASH_HELPER") != "1" {
		return
	}
	s, err := source.New(os.Getenv("AIDOCK_RULES_SOURCE"), os.Getenv("AIDOCK_RULES_LOCAL"), owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	// The generated batch starts with control and source, then Claude's
	// directory and rule file. Exit after that file is durably replaced.
	m.RehearsalHook = func(stage string, index int) error {
		if stage == "target-complete" && index == 3 {
			os.Exit(95)
		}
		return nil
	}
	_, err = m.RehearseGenerated(context.Background(), os.Getenv("AIDOCK_RULES_PLAN"), os.Getenv("AIDOCK_RULES_DIGEST"))
	t.Fatal("crash point not reached", err)
}

func TestGenerationRulesProcessExitRecoveryPreservesExternalEdit(t *testing.T) {
	m, b, s := replicaRuleFixture(t, 2)
	ctx := context.Background()
	g, err := m.StageRepair(ctx, repairRequest(t, m, b))
	if err != nil {
		t.Fatal(err)
	}
	originals := captureSources(t, s)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestGenerationRulesCrashHelper$")
	cmd.Env = append(os.Environ(), "AIDOCK_RULES_CRASH_HELPER=1", "AIDOCK_RULES_SOURCE="+s[0].Store.Root, "AIDOCK_RULES_LOCAL="+s[0].Store.Local, "AIDOCK_RULES_PLAN="+g.ID, "AIDOCK_RULES_DIGEST="+g.Digest)
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 95 {
		t.Fatalf("child failed before durable file write: %v %s", err, out)
	}
	fresh, err := New(s[0])
	if err != nil {
		t.Fatal(err)
	}
	r, err := fresh.RehearsalLatest()
	if err != nil || r == nil || !r.Pending {
		t.Fatal(r, err)
	}
	file := generatedRoot(*r, generatedRulePath(b[1].ID, "claude-code"))
	want := g.Targets[0].Outputs[0].Rule
	replicaFileEqual(t, m, file, want.After)
	generatedFixtureWrite(t, filepath.Dir(file), filepath.Base(file), []byte("external edit after crash"))
	_, err = fresh.RehearsalRecover(r.ID)
	code(t, err, "RECOVERY_CONFLICT")
	got, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(got, []byte("external edit after crash")) {
		t.Fatal("recovery overwrote external file", err)
	}
	data, err := m.Control.Data(want.After.Entries[workstore.FileContent])
	if err != nil {
		t.Fatal(err)
	}
	generatedFixtureWrite(t, filepath.Dir(file), filepath.Base(file), data)
	recovered, err := fresh.RehearsalRecover(r.ID)
	if err != nil || recovered.Status != "RECOVERED" {
		t.Fatal(recovered, err)
	}
	for _, output := range g.Targets[0].Outputs {
		replicaFileEqual(t, m, generatedRoot(recovered, generatedRulePath(b[1].ID, output.Adapter.ID)), output.Rule.Before)
		generatedEqual(t, m, generatedRoot(recovered, generatedPath(b[1].ID, output.Adapter.ID)), output.Before)
	}
	unchanged(t, s, originals)
}
