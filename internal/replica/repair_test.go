package replica

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func repairRequest(t *testing.T, m *Manager, b []Binding) GenerationRepairRequest {
	t.Helper()
	req := Request{From: b[0].ID, Targets: []string{}}
	for _, target := range b[1:] {
		req.Targets = append(req.Targets, target.ID)
	}
	r, err := m.Compare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return GenerationRepairRequest{Request: req, Comparison: r.Digest, Tools: []string{"claude-code", "codex-cli"}}
}

func setFixtureExcludes(t *testing.T, m *Manager, id string, paths []string) {
	t.Helper()
	before := generatedObservation(t, m, m.Control.Root)
	reg, err := m.registry(before)
	if err != nil {
		t.Fatal(err)
	}
	for i := range reg.Copies {
		if reg.Copies[i].ID == id {
			reg.Copies[i].Excludes = paths
		}
	}
	after := workstore.Clone(before)
	if err = m.Control.PutFile(&after, "replicas.json", data(reg), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := m.Control.Preview("fixture-policy", before, after)
	if err != nil {
		t.Fatal(err)
	}
	m.Control.Validate = nil
	if _, err = m.Control.Apply(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRepairFiveCopiesOwnSourcesMissingAndOlderOutputsRollbackUndo(t *testing.T) {
	m, b, s := fixture(t, 5)
	ctx := context.Background()
	baseline(t, m, b)
	// One target is already current, one is behind its own newly saved source,
	// two have no output roots. The publisher differs from every target.
	for _, i := range []int{1, 2} {
		for _, tool := range []string{"claude-code", "codex-cli"} {
			installFixtureOutput(t, m, s[i], tool)
		}
	}
	for _, i := range []int{0, 2, 3, 4} {
		applySource(t, s[i], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "local.txt", Content: fmt.Sprintf("copy %d own source", i)}}})
	}
	originals := captureSources(t, s)
	control := generatedObservation(t, m, m.Control.Root)
	g, err := m.StageRepair(ctx, repairRequest(t, m, b))
	if err != nil || !g.Repair || g.Blocked || g.NoChanges || len(g.Targets) != 4 {
		t.Fatal(g, err)
	}
	p, err := m.loadResolution(g.Plan, g.PlanDigest)
	if err != nil || !p.Repair || len(p.Request.Decisions) != 0 {
		t.Fatal(p, err)
	}
	generatedEqual(t, m, m.Control.Root, control)
	for _, target := range g.Targets {
		i := 0
		for j := range b {
			if b[j].ID == target.Copy {
				i = j
			}
		}
		if target.SourceBefore != target.SourceAfter || len(target.Changes) != 0 {
			t.Fatal("source changed", target)
		}
		for _, output := range target.Outputs {
			generatedEqual(t, m, output.Root, output.Before)
			if i == 1 && len(output.Changes) != 0 {
				t.Fatal("current output changed")
			}
			if i > 1 && output.After.Entries["guide/local.txt"].Hash != workstore.Hash([]byte(fmt.Sprintf("copy %d own source", i))) {
				t.Fatal("publisher substituted for own source")
			}
			if !output.After.Entries["guide/empty"].Dir || output.After.Entries["guide/asset.bin"].Hash != workstore.Hash([]byte{0, 255, 7}) {
				t.Fatal("incomplete repair")
			}
		}
	}
	_, err = m.Rehearse(ctx, p.ID, p.Digest)
	code(t, err, "GENERATOR_PLAN_REQUIRED")
	r, err := m.RehearseGenerated(ctx, g.ID, g.Digest)
	if err != nil || r.Status != "APPLIED" || !r.Generation.Repair {
		t.Fatal(r, err)
	}
	for _, state := range []string{"APPLIED", "ROLLED_BACK", "UNDONE"} {
		if state != "APPLIED" {
			reverse, err := m.RehearsalReverse(r.ID, r.Transactions[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			r, err = m.RehearsalApply(ctx, r.ID, reverse.ID, reverse.Digest)
			if err != nil || r.Status != state {
				t.Fatal(r, err)
			}
		}
		for _, target := range g.Targets {
			candidate := p.Targets[0]
			for _, v := range p.Targets {
				if v.Copy.ID == target.Copy {
					candidate = v
				}
			}
			generatedEqual(t, m, generatedRoot(r, "copies/"+target.Copy+"/.aidock"), candidate.Before)
			for _, output := range target.Outputs {
				want := output.After
				if state == "ROLLED_BACK" {
					want = output.Before
				}
				generatedEqual(t, m, generatedRoot(r, generatedPath(target.Copy, output.Adapter.ID)), want)
				generatedEqual(t, m, output.Root, output.Before)
			}
		}
		generatedEqual(t, m, generatedRoot(r, "control"), control)
	}
	unchanged(t, s, originals)
}

func TestRepairAlreadyCurrentStaleAndCollisionNeverStartSession(t *testing.T) {
	m, b, s := fixture(t, 2)
	ctx := context.Background()
	for _, tool := range []string{"claude-code", "codex-cli"} {
		installFixtureOutput(t, m, s[1], tool)
	}
	req := repairRequest(t, m, b)
	g, err := m.StageRepair(ctx, req)
	if err != nil || !g.NoChanges || g.Blocked {
		t.Fatal(g, err)
	}
	_, err = m.RehearseGenerated(ctx, g.ID, g.Digest)
	code(t, err, "GENERATOR_ALREADY_CURRENT")
	applySource(t, s[1], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "note.txt", Content: "new"}}})
	_, err = m.RehearseGenerated(ctx, g.ID, g.Digest)
	code(t, err, "PLAN_STALE")
	_, err = m.StageRepair(ctx, req)
	code(t, err, "PLAN_STALE")
	req = repairRequest(t, m, b)
	for _, tools := range [][]string{nil, {"codex-cli", "codex-cli"}, {"shell-script"}} {
		bad := req
		bad.Tools = tools
		_, err = m.StageRepair(ctx, bad)
		want := "TOOL_UNSUPPORTED"
		if len(tools) == 0 {
			want = "TOOL_REQUIRED"
		}
		code(t, err, want)
	}
	g, err = m.StageRepair(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	generatedFixtureWrite(t, g.Targets[0].Outputs[0].Root, "manual.txt", []byte("added after preview"))
	_, err = m.RehearseGenerated(ctx, g.ID, g.Digest)
	code(t, err, "PLAN_STALE")
	generatedFixtureWrite(t, filepath.Join(filepath.Dir(b[1].Root), ".agents/skills"), "guide/asset.bin", []byte("manual drift"))
	g, err = m.StageRepair(ctx, req)
	if err != nil || !g.Blocked {
		t.Fatal("managed drift was repaired", g, err)
	}
	_, err = m.RehearseGenerated(ctx, g.ID, g.Digest)
	code(t, err, "GENERATOR_PREFLIGHT_FAILED")
	if h, err := m.RehearsalHistory(); err != nil || len(h) != 0 {
		t.Fatal("empty or blocked transaction", h, err)
	}
}

func TestRepairExclusionProtectsManagedOldNamesDriftDisabledAndHandwritten(t *testing.T) {
	for _, path := range []string{"skills/guide", "skills/" + skill + "/asset.bin", "skills"} {
		t.Run(path, func(t *testing.T) {
			m, b, s := fixture(t, 2)
			installFixtureOutput(t, m, s[1], "codex-cli")
			root := filepath.Join(filepath.Dir(b[1].Root), ".agents/skills")
			generatedFixtureWrite(t, root, "guide/asset.bin", []byte("excluded manual drift"))
			setFixtureExcludes(t, m, b[1].ID, []string{path})
			applySource(t, s[1], source.Request{Action: "rename", ID: skill, Name: "renamed-guide"})
			before := generatedObservation(t, m, root)
			req := repairRequest(t, m, b)
			req.Tools = []string{"codex-cli"}
			g, err := m.StageRepair(context.Background(), req)
			if err != nil || g.Blocked || !g.NoChanges || g.Targets[0].Preserved == nil {
				t.Fatal(g, err)
			}
			if workstore.Digest(g.Targets[0].Outputs[0].After) != workstore.Digest(before) {
				t.Fatal("excluded output or ownership changed")
			}
			applySource(t, s[1], source.Request{Action: "retire", ID: skill, Reason: "excluded local retirement"})
			req = repairRequest(t, m, b)
			req.Tools = []string{"codex-cli"}
			g, err = m.StageRepair(context.Background(), req)
			if err != nil || g.Blocked || !g.NoChanges || workstore.Digest(g.Targets[0].Outputs[0].After) != workstore.Digest(before) {
				t.Fatal("retirement cancelled exclusion", g, err)
			}
			// Protection cannot be widened by rewriting a preview's checksum.
			g.Targets[0].Preserved.All = !g.Targets[0].Preserved.All
			g.Digest = generationDigest(g)
			if err = saveLocal(m.generationFile(g.ID), g); err != nil {
				t.Fatal(err)
			}
			_, err = m.GenerationShow(g.ID, g.Digest)
			code(t, err, "GENERATOR_MANIFEST_INVALID")
		})
	}
}

func TestGeneratedPropagationPreservesExcludedSkillWhileUpdatingAnother(t *testing.T) {
	m, b, s := fixture(t, 2)
	id := workstore.ID()
	for _, target := range s {
		applySource(t, target, source.Request{Action: "create", ID: id, Name: "other", Description: "ordinary shared skill"})
		applySource(t, target, source.Request{Action: "enable", ID: id})
	}
	installFixtureOutput(t, m, s[1], "codex-cli")
	root := filepath.Join(filepath.Dir(b[1].Root), ".agents/skills")
	generatedFixtureWrite(t, root, "guide/asset.bin", []byte("excluded manual edits"))
	setFixtureExcludes(t, m, b[1].ID, []string{"skills/guide"})
	applySource(t, s[0], source.Request{Action: "edit", ID: id, Files: []source.Patch{{Path: "notes.txt", Content: "propagated other"}}})
	report, err := m.Compare(context.Background(), Request{From: b[0].ID, Targets: []string{b[1].ID}})
	if err != nil {
		t.Fatal(err)
	}
	decisions := []Decision{}
	for _, entry := range report.Targets[0].Entries {
		choice := "skip"
		if entry.ID == id {
			choice = "source"
		}
		decisions = append(decisions, Decision{Target: b[1].ID, Entry: entry.ID, Choice: choice})
	}
	p, err := m.Resolve(context.Background(), ResolveRequest{Request: Request{From: b[0].ID, Targets: []string{b[1].ID}}, Comparison: report.Digest, Decisions: decisions})
	if err != nil {
		t.Fatal(err)
	}
	g, err := m.StageGeneration(context.Background(), GenerationRequest{Plan: p.ID, Digest: p.Digest, Tools: []string{"codex-cli"}})
	if err != nil || g.Blocked {
		t.Fatal(g, err)
	}
	output := g.Targets[0].Outputs[0]
	if output.After.Entries["guide/asset.bin"] != output.Before.Entries["guide/asset.bin"] || output.After.Entries["other/notes.txt"].Hash != workstore.Hash([]byte("propagated other")) {
		t.Fatal("exclusion or ordinary propagation broken")
	}
	r, err := m.RehearseGenerated(context.Background(), g.ID, g.Digest)
	if err != nil {
		t.Fatal(err)
	}
	generatedEqual(t, m, generatedRoot(r, generatedPath(b[1].ID, "codex-cli")), output.After)
}

func TestRepairActualProcessExitRecoversGeneratedRootWithUnchangedSourceAndBaseline(t *testing.T) {
	m, b, s := fixture(t, 2)
	baseline(t, m, b)
	originals := captureSources(t, s)
	req := repairRequest(t, m, b)
	req.Tools = []string{"codex-cli"}
	g, err := m.StageRepair(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.loadResolution(g.Plan, g.PlanDigest)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestGeneratedRehearsalCrashHelper$")
	cmd.Env = append(os.Environ(), "AIDOCK_GENERATED_CRASH_HELPER=1", "AIDOCK_GENERATED_SOURCE="+s[0].Store.Root, "AIDOCK_GENERATED_LOCAL="+s[0].Store.Local, "AIDOCK_GENERATED_PLAN="+g.ID, "AIDOCK_GENERATED_DIGEST="+g.Digest)
	output, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 92 {
		t.Fatalf("child failed before exit point: %v %s", err, output)
	}
	fresh, err := New(s[0])
	if err != nil {
		t.Fatal(err)
	}
	r, err := fresh.RehearsalLatest()
	if err != nil || r == nil || !r.Pending || !r.Generation.Repair {
		t.Fatal(r, err)
	}
	recovered, err := fresh.RehearsalRecover(r.ID)
	if err != nil || recovered.Status != "RECOVERED" {
		t.Fatal(recovered, err)
	}
	generatedEqual(t, m, generatedRoot(recovered, "control"), p.ControlBefore)
	generatedEqual(t, m, generatedRoot(recovered, "copies/"+b[1].ID+"/.aidock"), p.Targets[0].Before)
	generatedEqual(t, m, generatedRoot(recovered, generatedPath(b[1].ID, "codex-cli")), g.Targets[0].Outputs[0].Before)
	unchanged(t, s, originals)
}
