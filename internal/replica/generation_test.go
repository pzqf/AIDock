package replica

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/generator"
	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func externalWriteTree(t *testing.T, m *Manager, root string, tree workstore.Tree) {
	t.Helper()
	for path, entry := range tree.Entries {
		file := filepath.Join(root, filepath.FromSlash(path))
		if entry.Dir {
			if err := os.MkdirAll(file, 0755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		bytes, err := m.Source.Store.Data(entry)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(file, bytes, os.FileMode(entry.Mode)); err != nil {
			t.Fatal(err)
		}
	}
}

func stageExternalFixture(t *testing.T, m *Manager) (generator.Stage, generator.Validation) {
	t.Helper()
	dir := t.TempDir()
	runtime := filepath.Join(dir, "runtime")
	if err := os.MkdirAll(runtime, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtime, "generator.exe"), []byte("offline fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "adapter.json")
	contract := generator.Contract{Version: 1, ID: "replica-external", Release: "1.0.0", Protocol: generator.Protocol, Runtime: runtime, Entry: "generator.exe", Tools: []string{"claude-code", "codex-cli"}, Network: "deny", Children: "deny"}
	b, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(adapter, b, 0644); err != nil {
		t.Fatal(err)
	}
	gm := generator.New(m.Source)
	p, err := gm.Inspect(context.Background(), adapter)
	if err != nil {
		t.Fatal(err)
	}
	o, err := gm.Stage(context.Background(), adapter, p.Digest)
	if err != nil {
		t.Fatal(err)
	}
	externalWriteTree(t, m, filepath.Join(o.Stage.Root, "output"), o.Stage.Preview.Expected)
	r := map[string]any{"schema_version": 1, "stage_id": o.Stage.ID, "stage_digest": o.Stage.Digest, "adapter_sha256": o.Stage.Preview.AdapterHash, "runtime_digest": workstore.Digest(o.Stage.Preview.Runtime), "input_digest": workstore.Digest(o.Stage.Preview.Input), "tools": o.Stage.Preview.Contract.Tools}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(o.Stage.Root, "output", "result.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	v, err := gm.Validate(context.Background(), o.Stage.ID, o.Stage.Digest)
	if err != nil || v.Status != "PASS" {
		t.Fatal(v, err)
	}
	return o.Stage, v
}

func generatedFixtureWrite(t *testing.T, root, path string, bytes []byte) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, bytes, 0644); err != nil {
		t.Fatal(err)
	}
}
func generatedObservation(t *testing.T, m *Manager, root string) workstore.Tree {
	t.Helper()
	tree, err := m.Control.Capture(root)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}
func generatedEqual(t *testing.T, m *Manager, root string, tree workstore.Tree) {
	t.Helper()
	if workstore.Digest(generatedObservation(t, m, root)) != workstore.Digest(tree) {
		t.Fatal("tree changed", root)
	}
}
func generatedRoot(r Rehearsal, path string) string {
	for _, root := range r.Roots {
		if root.Path == path {
			return root.Root
		}
	}
	return ""
}

func TestGeneratedFiveCopyRehearsalCompletePackagesRollbackUndoAndHistory(t *testing.T) {
	m, b, s := fixture(t, 5)
	ctx := context.Background()
	baseline(t, m, b)
	applySource(t, s[0], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "references/notes.txt", Content: "publisher attachment changed"}}})
	for _, copy := range b[1:] {
		for _, tool := range []string{"claude-code", "codex-cli"} {
			root := filepath.Join(filepath.Dir(copy.Root), filepath.FromSlash(mirrorDirectory(tool)))
			generatedFixtureWrite(t, root, "handwritten/data.bin", []byte{1, 0, 255})
			if err := os.MkdirAll(filepath.Join(root, "handwritten", "empty"), 0755); err != nil {
				t.Fatal(err)
			}
		}
	}
	originals := captureSources(t, s)
	p, err := m.Resolve(ctx, resolveRequest(t, m, b, map[string]string{b[1].ID: "source", b[2].ID: "source", b[3].ID: "source", b[4].ID: "skip"}))
	if err != nil {
		t.Fatal(err)
	}
	g, err := m.StageGeneration(ctx, GenerationRequest{Plan: p.ID, Digest: p.Digest, Tools: []string{"codex-cli", "claude-code"}})
	if err != nil || g.Blocked || len(g.Targets) != 3 {
		t.Fatal(g, err)
	}
	for _, target := range g.Targets {
		for _, output := range target.Outputs {
			generatedEqual(t, m, output.Root, output.Before)
			if _, err := os.Stat(filepath.Join(filepath.Dir(output.Root), "."+filepath.Base(output.Root)+".aidock-lock")); !os.IsNotExist(err) {
				t.Fatal("stage created target-side lock", err)
			}
			if !output.After.Entries["guide/empty"].Dir || output.After.Entries["guide/asset.bin"].Hash != workstore.Hash([]byte{0, 255, 7}) || output.After.Entries["handwritten/data.bin"].Hash != output.Before.Entries["handwritten/data.bin"].Hash {
				t.Fatal("incomplete generated package", output.Adapter.ID)
			}
		}
	}
	unchanged(t, s, originals)
	r, err := m.RehearseGenerated(ctx, g.ID, g.Digest)
	if err != nil || r.Status != "APPLIED" || r.Generation == nil || len(r.Roots) != 10 || r.Transactions[0].Targets != 3 || r.Transactions[0].Generated != 6 {
		t.Fatal(r, err)
	}
	check := func(after bool) {
		for _, candidate := range p.Targets {
			tree := candidate.Before
			if after {
				tree = candidate.After
			}
			generatedEqual(t, m, generatedRoot(r, "copies/"+candidate.Copy.ID+"/.aidock"), tree)
		}
		control := p.ControlBefore
		if after {
			control = p.ControlAfter
		}
		generatedEqual(t, m, generatedRoot(r, "control"), control)
		for _, target := range g.Targets {
			for _, output := range target.Outputs {
				tree := output.Before
				if after {
					tree = output.After
				}
				generatedEqual(t, m, generatedRoot(r, generatedPath(target.Copy, output.Adapter.ID)), tree)
				generatedEqual(t, m, output.Root, output.Before)
			}
		}
	}
	check(true)
	for _, want := range []string{"ROLLED_BACK", "UNDONE"} {
		reverse, err := m.RehearsalReverse(r.ID, r.Transactions[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		r, err = m.RehearsalApply(ctx, r.ID, reverse.ID, reverse.Digest)
		if err != nil || r.Status != want {
			t.Fatal(r, err)
		}
		check(want == "UNDONE")
	}
	// Recovery/reporting uses the embedded immutable generation manifest even
	// when its standalone preview has gone and an original project is offline.
	if err = os.Remove(m.generationFile(g.ID)); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(b[1].Root)
	if err = os.Rename(parent, parent+"-offline"); err != nil {
		t.Fatal(err)
	}
	fresh, err := New(s[0])
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := fresh.RehearsalShow(r.ID)
	if err != nil || reopened.Status != "UNDONE" || reopened.Generation.Digest != g.Digest {
		t.Fatal(reopened, err)
	}
	if err = os.Rename(parent+"-offline", parent); err != nil {
		t.Fatal(err)
	}
	unchanged(t, s, originals)
}

func TestGeneratedStaleCollisionCancellationAndManifestBindings(t *testing.T) {
	m, b, s := fixture(t, 2)
	ctx := context.Background()
	applySource(t, s[0], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "asset.txt", Content: "new"}}})
	p, err := m.Resolve(ctx, resolveRequest(t, m, b, map[string]string{b[1].ID: "source"}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.StageGeneration(ctx, GenerationRequest{Plan: p.ID, Digest: p.Digest, Tools: []string{"arbitrary-script"}})
	code(t, err, "TOOL_UNSUPPORTED")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = m.StageGeneration(cancelled, GenerationRequest{Plan: p.ID, Digest: p.Digest, Tools: []string{"codex-cli"}})
	if err == nil {
		t.Fatal("cancel ignored")
	}
	g, err := m.StageGeneration(ctx, GenerationRequest{Plan: p.ID, Digest: p.Digest, Tools: []string{"codex-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.RehearseGenerated(ctx, g.ID, "wrong")
	code(t, err, "PLAN_CONFIRMATION_REQUIRED")
	root := g.Targets[0].Outputs[0].Root
	generatedFixtureWrite(t, root, "new-handwritten.txt", []byte("do not overwrite"))
	_, err = m.RehearseGenerated(ctx, g.ID, g.Digest)
	code(t, err, "PLAN_STALE")
	if history, err := m.RehearsalHistory(); err != nil || len(history) != 0 {
		t.Fatal("stale generation seeded a session", history, err)
	}
	generatedFixtureWrite(t, root, "guide/SKILL.md", []byte("handwritten collision"))
	g, err = m.StageGeneration(ctx, GenerationRequest{Plan: p.ID, Digest: p.Digest, Tools: []string{"codex-cli"}})
	if err != nil || !g.Blocked {
		t.Fatal("handwritten collision not blocked", g, err)
	}
	before := generatedObservation(t, m, root)
	_, err = m.RehearseGenerated(ctx, g.ID, g.Digest)
	code(t, err, "GENERATOR_PREFLIGHT_FAILED")
	generatedEqual(t, m, root, before)
	// Even recomputing a damaged Local checksum cannot widen the fixed mapping.
	g.Targets[0].Outputs[0].Root = filepath.Dir(root)
	g.Digest = generationDigest(g)
	if err = saveLocal(m.generationFile(g.ID), g); err != nil {
		t.Fatal(err)
	}
	_, err = m.GenerationShow(g.ID, g.Digest)
	code(t, err, "GENERATOR_MANIFEST_INVALID")
}

func TestExternalValidatedOutputStagesPerCopyAndBecomesStale(t *testing.T) {
	m, b, s := fixture(t, 2)
	ctx := context.Background()
	applySource(t, s[0], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "references/notes.txt", Content: "external output"}}})
	stage, validation := stageExternalFixture(t, m)
	p, err := m.Resolve(ctx, resolveRequest(t, m, b, map[string]string{b[1].ID: "source"}))
	if err != nil {
		t.Fatal(err)
	}
	g, err := m.StageExternalGeneration(ctx, ExternalGenerationRequest{Plan: p.ID, Digest: p.Digest, Stage: stage.ID, StageDigest: stage.Digest})
	if err != nil || g.Generator != externalMirrorGenerator || g.Version != 3 || g.ExternalValidation != validation.ID || g.Blocked || len(g.Targets) != 1 {
		t.Fatal(g, err)
	}
	if _, err = m.RehearseGenerated(ctx, g.ID, g.Digest); err != nil {
		t.Fatal(err)
	}
	// A changed return byte invalidates the external candidate before another
	// rehearsal can be created; no new session is allowed from stale output.
	root := filepath.Join(stage.Root, "output", ".agents", "skills", "guide", "SKILL.md")
	if err = os.WriteFile(root, []byte("changed externally"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = m.RehearseGenerated(ctx, g.ID, g.Digest)
	code(t, err, "GENERATOR_STAGE_STALE")
	if history, historyErr := m.RehearsalHistory(); historyErr != nil || len(history) != 1 {
		t.Fatal("stale external output created a second session", history, historyErr)
	}
}

func installFixtureOutput(t *testing.T, m *Manager, s *source.Manager, tool string) {
	t.Helper()
	tree, err := s.Store.Capture(s.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := s.ExportCandidate(tree)
	if err != nil {
		t.Fatal(err)
	}
	n, err := native.New(s, filepath.Dir(s.Store.Root))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(n.Root, filepath.FromSlash(mirrorDirectory(tool)))
	before := generatedObservation(t, m, root)
	outputs, err := n.DraftTargets(context.Background(), []string{tool}, bundle, map[string]workstore.Tree{tool: before})
	if err != nil {
		t.Fatal(err)
	}
	if err = transfer(s.Store, m.Control, outputs[0].After); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(root), 0755); err != nil {
		t.Fatal(err)
	}
	store := &workstore.Store{Root: root, Local: m.Control.Local}
	if err = transfer(s.Store, store, before, outputs[0].After); err != nil {
		t.Fatal(err)
	}
	p, err := store.Preview("fixture", before, outputs[0].After)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Apply(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedDisableDriftAndInterruptedJointRecovery(t *testing.T) {
	m, b, s := fixture(t, 2)
	ctx := context.Background()
	baseline(t, m, b)
	installFixtureOutput(t, m, s[1], "codex-cli")
	root := filepath.Join(filepath.Dir(b[1].Root), ".agents", "skills")
	generatedFixtureWrite(t, root, "handwritten/data.bin", []byte{3, 2, 1})
	applySource(t, s[0], source.Request{Action: "disable", ID: skill})
	p, err := m.Resolve(ctx, resolveRequest(t, m, b, map[string]string{b[1].ID: "source"}))
	if err != nil {
		t.Fatal(err)
	}
	g, err := m.StageGeneration(ctx, GenerationRequest{Plan: p.ID, Digest: p.Digest, Tools: []string{"codex-cli"}})
	if err != nil || g.Blocked {
		t.Fatal(g, err)
	}
	output := g.Targets[0].Outputs[0]
	if _, ok := output.After.Entries["guide"]; ok {
		t.Fatal("disabled skill still generated")
	}
	if output.After.Entries["handwritten/data.bin"] != output.Before.Entries["handwritten/data.bin"] {
		t.Fatal("handwritten lost")
	}
	// Managed drift must be diagnosed before any rehearsal root is written.
	original, err := os.ReadFile(filepath.Join(root, "guide", "asset.bin"))
	if err != nil {
		t.Fatal(err)
	}
	generatedFixtureWrite(t, root, "guide/asset.bin", []byte("manual drift"))
	drift, err := m.StageGeneration(ctx, GenerationRequest{Plan: p.ID, Digest: p.Digest, Tools: []string{"codex-cli"}})
	if err != nil || !drift.Blocked {
		t.Fatal("managed drift accepted", drift, err)
	}
	generatedFixtureWrite(t, root, "guide/asset.bin", original)
	originals := captureSources(t, s)
	m.RehearsalHook = func(stage string, i int) error {
		if stage == "target-complete" && i == 2 || stage == "recovered-target" && i == 0 {
			return fmt.Errorf("generator-root interruption")
		}
		return nil
	}
	_, err = m.RehearseGenerated(ctx, g.ID, g.Digest)
	code(t, err, "RECOVERY_CONFLICT")
	r, err := m.RehearsalLatest()
	if err != nil || r == nil || !r.Pending || r.Generation == nil {
		t.Fatal(r, err)
	}
	generatedEqual(t, m, root, output.Before)
	fresh, err := New(s[0])
	if err != nil {
		t.Fatal(err)
	}
	isolated := generatedRoot(*r, generatedPath(b[1].ID, "codex-cli"))
	ownerBytes, err := m.Control.Data(output.After.Entries[".aidock-ownership.json"])
	if err != nil {
		t.Fatal(err)
	}
	generatedFixtureWrite(t, isolated, ".aidock-ownership.json", []byte("external modification"))
	_, err = fresh.RehearsalRecover(r.ID)
	code(t, err, "RECOVERY_CONFLICT")
	generatedFixtureWrite(t, isolated, ".aidock-ownership.json", ownerBytes)
	generatedFixtureWrite(t, isolated, "unrelated-note.txt", []byte("keep after recovery"))
	recovered, err := fresh.RehearsalRecover(r.ID)
	if err != nil || recovered.Status != "RECOVERED" {
		t.Fatal(recovered, err)
	}
	expected := workstore.Clone(output.Before)
	if err = m.Control.PutFile(&expected, "unrelated-note.txt", []byte("keep after recovery"), 0644); err != nil {
		t.Fatal(err)
	}
	generatedEqual(t, m, generatedRoot(recovered, generatedPath(b[1].ID, "codex-cli")), expected)
	generatedEqual(t, m, generatedRoot(recovered, "control"), p.ControlBefore)
	generatedEqual(t, m, generatedRoot(recovered, "copies/"+b[1].ID+"/.aidock"), p.Targets[0].Before)
	unchanged(t, s, originals)
}

func TestGeneratedRehearsalCrashHelper(t *testing.T) {
	if os.Getenv("AIDOCK_GENERATED_CRASH_HELPER") != "1" {
		return
	}
	s, err := source.New(os.Getenv("AIDOCK_GENERATED_SOURCE"), os.Getenv("AIDOCK_GENERATED_LOCAL"), owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	m.RehearsalHook = func(stage string, i int) error {
		if stage == "target-complete" && i == 2 {
			os.Exit(92)
		}
		return nil
	}
	_, err = m.RehearseGenerated(context.Background(), os.Getenv("AIDOCK_GENERATED_PLAN"), os.Getenv("AIDOCK_GENERATED_DIGEST"))
	t.Fatalf("crash point not reached: %v", err)
}

func TestGeneratedActualProcessExitResumesAllRootsFromDurableManifest(t *testing.T) {
	m, b, s := fixture(t, 2)
	ctx := context.Background()
	applySource(t, s[0], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "references/notes.txt", Content: "new generation"}}})
	originals := captureSources(t, s)
	p, err := m.Resolve(ctx, resolveRequest(t, m, b, map[string]string{b[1].ID: "source"}))
	if err != nil {
		t.Fatal(err)
	}
	g, err := m.StageGeneration(ctx, GenerationRequest{Plan: p.ID, Digest: p.Digest, Tools: []string{"codex-cli"}})
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
	if err != nil || r == nil || !r.Pending || r.Generation == nil {
		t.Fatal(r, err)
	}
	for _, root := range r.Roots {
		if _, err = os.Stat(workstore.CoordinatedMarker(root.Root)); err != nil {
			t.Fatal("crash lost barrier", err)
		}
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
