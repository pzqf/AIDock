package replica

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/generator"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestExternalFiveCopiesRollbackSurvivesStageCleanup(t *testing.T) {
	m, bindings, sources := fixture(t, 5)
	ctx := context.Background()
	baseline(t, m, bindings)
	applySource(t, sources[0], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "references/notes.txt", Content: "offline complete package"}}})
	for _, copy := range bindings[1:] {
		for _, tool := range []string{"claude-code", "codex-cli"} {
			root := filepath.Join(filepath.Dir(copy.Root), filepath.FromSlash(mirrorDirectory(tool)))
			generatedFixtureWrite(t, root, "handwritten/keep.bin", []byte{0, 128, 255})
		}
	}
	originals := captureSources(t, sources)
	control := generatedObservation(t, m, m.Control.Root)
	stage, _ := stageExternalFixture(t, m)
	choices := map[string]string{}
	for _, copy := range bindings[1:] {
		choices[copy.ID] = "source"
	}
	p, err := m.Resolve(ctx, resolveRequest(t, m, bindings, choices))
	if err != nil {
		t.Fatal(err)
	}
	g, err := m.StageExternalGeneration(ctx, ExternalGenerationRequest{p.ID, p.Digest, stage.ID, stage.Digest})
	if err != nil || len(g.Targets) != 4 || g.Blocked || len(g.Tools) != 2 {
		t.Fatal(g, err)
	}
	for _, target := range g.Targets {
		for _, output := range target.Outputs {
			if !output.After.Entries["guide/empty"].Dir || output.After.Entries["guide/asset.bin"].Hash != workstore.Hash([]byte{0, 255, 7}) || output.After.Entries["handwritten/keep.bin"] != output.Before.Entries["handwritten/keep.bin"] {
				t.Fatal("incomplete external output or lost handwritten file")
			}
			generatedEqual(t, m, output.Root, output.Before)
		}
	}
	r, err := m.RehearseGenerated(ctx, g.ID, g.Digest)
	if err != nil || r.Status != "APPLIED" || len(r.Roots) != 13 {
		t.Fatal(r, err)
	}
	gm := generator.New(sources[0])
	o, err := gm.Show(ctx, stage.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = gm.Cleanup(ctx, stage.ID, o.ObservationDigest); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(m.generationFile(g.ID)); err != nil {
		t.Fatal(err)
	}
	fresh, err := New(sources[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ROLLED_BACK", "UNDONE"} {
		plan, err := fresh.RehearsalReverse(r.ID, r.Transactions[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		r, err = fresh.RehearsalApply(ctx, r.ID, plan.ID, plan.Digest)
		if err != nil || r.Status != want {
			t.Fatal(r, err)
		}
		for i, target := range g.Targets {
			for _, output := range target.Outputs {
				tree := output.After
				if want == "ROLLED_BACK" {
					tree = output.Before
				}
				generatedEqual(t, m, generatedRoot(r, generatedPath(target.Copy, output.Adapter.ID)), tree)
			}
			sourceTree := p.Targets[i].After
			if want == "ROLLED_BACK" {
				sourceTree = p.Targets[i].Before
			}
			generatedEqual(t, m, generatedRoot(r, "copies/"+target.Copy+"/.aidock"), sourceTree)
		}
		controlTree := p.ControlAfter
		if want == "ROLLED_BACK" {
			controlTree = p.ControlBefore
		}
		generatedEqual(t, m, generatedRoot(r, "control"), controlTree)
	}
	unchanged(t, sources, originals)
	generatedEqual(t, m, m.Control.Root, control)
}

func TestExternalManifestRehashDoesNotAuthorizeChangedOutputs(t *testing.T) {
	m, b, s := fixture(t, 2)
	ctx := context.Background()
	applySource(t, s[0], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "new.txt", Content: "publisher"}}})
	stage, _ := stageExternalFixture(t, m)
	p, err := m.Resolve(ctx, resolveRequest(t, m, b, map[string]string{b[1].ID: "source"}))
	if err != nil {
		t.Fatal(err)
	}
	g, err := m.StageExternalGeneration(ctx, ExternalGenerationRequest{p.ID, p.Digest, stage.ID, stage.Digest})
	if err != nil {
		t.Fatal(err)
	}
	output := &g.Targets[0].Outputs[0]
	if err = m.Control.PutFile(&output.After, "unapproved.txt", []byte("must not enter rehearsal"), 0644); err != nil {
		t.Fatal(err)
	}
	output.Changes = workstore.Changes(output.Before, output.After)
	g.Digest = generationDigest(g)
	if err = saveLocal(m.generationFile(g.ID), g); err != nil {
		t.Fatal(err)
	}
	_, err = m.RehearseGenerated(ctx, g.ID, g.Digest)
	code(t, err, "GENERATOR_MANIFEST_INVALID")
	_, err = m.CheckGeneration(ctx, GenerationCheckRequest{Plan: g.ID, Digest: g.Digest})
	code(t, err, "GENERATOR_MANIFEST_INVALID")
	if h, err := m.RehearsalHistory(); err != nil || len(h) != 0 {
		t.Fatal("forged candidate started a session", h, err)
	}
}

func TestExternalTargetOnlySkillsRemainDiagnostic(t *testing.T) {
	m, b, s := fixture(t, 2)
	ctx := context.Background()
	applySource(t, s[0], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "new.txt", Content: "publisher"}}})
	extra := workstore.ID()
	applySource(t, s[1], source.Request{Action: "create", ID: extra, Name: "target-only", Description: "keep target"})
	applySource(t, s[1], source.Request{Action: "enable", ID: extra})
	stage, _ := stageExternalFixture(t, m)
	req := resolveRequest(t, m, b, map[string]string{b[1].ID: "source"})
	for i := range req.Decisions {
		if req.Decisions[i].Entry == extra {
			req.Decisions[i].Choice = "target"
		}
	}
	p, err := m.Resolve(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	originals := captureSources(t, s)
	_, err = m.StageExternalGeneration(ctx, ExternalGenerationRequest{p.ID, p.Digest, stage.ID, stage.Digest})
	code(t, err, "GENERATOR_PACKAGE_MISMATCH")
	unchanged(t, s, originals)
}

func TestExternalProcessExitRecoveryDoesNotNeedReturnDirectory(t *testing.T) {
	m, b, s := fixture(t, 2)
	ctx := context.Background()
	applySource(t, s[0], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "new.txt", Content: "publisher"}}})
	stage, _ := stageExternalFixture(t, m)
	p, err := m.Resolve(ctx, resolveRequest(t, m, b, map[string]string{b[1].ID: "source"}))
	if err != nil {
		t.Fatal(err)
	}
	g, err := m.StageExternalGeneration(ctx, ExternalGenerationRequest{p.ID, p.Digest, stage.ID, stage.Digest})
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestGeneratedRehearsalCrashHelper$")
	cmd.Env = append(os.Environ(), "AIDOCK_GENERATED_CRASH_HELPER=1", "AIDOCK_GENERATED_SOURCE="+s[0].Store.Root, "AIDOCK_GENERATED_LOCAL="+s[0].Store.Local, "AIDOCK_GENERATED_PLAN="+g.ID, "AIDOCK_GENERATED_DIGEST="+g.Digest)
	output, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 92 {
		t.Fatalf("child did not reach durable recovery point: %v %s", err, output)
	}
	gm := generator.New(s[0])
	o, err := gm.Show(ctx, stage.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = gm.Cleanup(ctx, stage.ID, o.ObservationDigest); err != nil {
		t.Fatal(err)
	}
	fresh, err := New(s[0])
	if err != nil {
		t.Fatal(err)
	}
	r, err := fresh.RehearsalLatest()
	if err != nil || r == nil || !r.Pending {
		t.Fatal(r, err)
	}
	recovered, err := fresh.RehearsalRecover(r.ID)
	if err != nil || recovered.Status != "RECOVERED" {
		t.Fatal(recovered, err)
	}
	generatedEqual(t, m, generatedRoot(recovered, "control"), p.ControlBefore)
	generatedEqual(t, m, generatedRoot(recovered, "copies/"+b[1].ID+"/.aidock"), p.Targets[0].Before)
	for _, output := range g.Targets[0].Outputs {
		generatedEqual(t, m, generatedRoot(recovered, generatedPath(b[1].ID, output.Adapter.ID)), output.Before)
	}
}
