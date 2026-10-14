package replica

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func checkedFixture(t *testing.T, n int) (*Manager, []Binding, []*source.Manager, Generation) {
	t.Helper()
	m, b, s := fixture(t, n)
	g, err := m.StageRepair(context.Background(), repairRequest(t, m, b))
	if err != nil {
		t.Fatal(err)
	}
	return m, b, s, g
}

func TestGenerationCheckFiveCopiesStaticMatrixAndPersistentHistory(t *testing.T) {
	m, b, s, g := checkedFixture(t, 5)
	ctx := context.Background()
	originals := captureSources(t, s)
	control := generatedObservation(t, m, m.Control.Root)
	r, err := m.CheckGeneration(ctx, GenerationCheckRequest{Plan: g.ID, Digest: g.Digest})
	if err != nil || r.Probe || r.WriteSupported || r.PublicationStatus != "BLOCKED" || r.CandidateStatus != "UNKNOWN" || len(r.Targets) != 4 {
		t.Fatal(r, err)
	}
	if len(r.Gates) != 4*2*4+2 {
		t.Fatal("missing per-copy gates", len(r.Gates))
	}
	for i, target := range r.Targets {
		if target.Copy.ID != g.Targets[i].Copy || target.Source != g.Targets[i].SourceAfter || len(target.Inspection.Tools) != 2 {
			t.Fatal(target)
		}
		for _, tool := range target.Inspection.Tools {
			if tool.L1 != "NOT_APPLIED" || tool.L2 != "UNKNOWN" || tool.L3 != "UNKNOWN" || tool.L4 != "UNKNOWN" {
				t.Fatal(tool)
			}
			if _, err = os.Stat(tool.Target); !os.IsNotExist(err) {
				t.Fatal("check created actual target", err)
			}
		}
	}
	unchanged(t, s, originals)
	generatedEqual(t, m, m.Control.Root, control)
	fresh, err := New(s[0])
	if err != nil {
		t.Fatal(err)
	}
	rows, err := fresh.GenerationCheckHistory()
	if err != nil || len(rows) != 1 || rows[0].ID != r.ID {
		t.Fatal(rows, err)
	}
	shown, err := fresh.GenerationCheckShow(ctx, r.ID)
	if err != nil || shown.Stale {
		t.Fatal(shown, err)
	}
	// Offline original projects cannot remove the historical observations.
	parent := filepath.Dir(b[2].Root)
	if err = os.Rename(parent, parent+"-offline"); err != nil {
		t.Fatal(err)
	}
	shown, err = fresh.GenerationCheckShow(ctx, r.ID)
	if err != nil || !shown.Stale || len(shown.Targets) != 4 {
		t.Fatal(shown, err)
	}
	if err = os.Rename(parent+"-offline", parent); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationCheckStaleSourceOutputPolicyAndExpiry(t *testing.T) {
	for _, change := range []string{"source", "publisher", "output", "policy", "expiry", "manifest", "inspection-policy"} {
		t.Run(change, func(t *testing.T) {
			m, b, s, g := checkedFixture(t, 2)
			ctx := context.Background()
			r, err := m.CheckGeneration(ctx, GenerationCheckRequest{Plan: g.ID, Digest: g.Digest})
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "source", "publisher":
				i := 1
				if change == "publisher" {
					i = 0
				}
				applySource(t, s[i], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "new.txt", Content: "changed"}}})
			case "output":
				generatedFixtureWrite(t, g.Targets[0].Outputs[0].Root, "manual/new.txt", []byte("external"))
			case "policy":
				setFixtureExcludes(t, m, b[1].ID, []string{"skills/guide"})
			case "expiry":
				r.Expires = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
				r.Digest = generationCheckDigest(r)
				if err = saveLocal(filepath.Join(m.generationChecksDir(), r.ID+".json"), r); err != nil {
					t.Fatal(err)
				}
			case "manifest":
				if err = os.Remove(m.generationFile(g.ID)); err != nil {
					t.Fatal(err)
				}
			case "inspection-policy":
				r.Policy = "previous-inspection-policy"
				r.Digest = generationCheckDigest(r)
				if err = saveLocal(filepath.Join(m.generationChecksDir(), r.ID+".json"), r); err != nil {
					t.Fatal(err)
				}
			}
			shown, err := m.GenerationCheckShow(ctx, r.ID)
			if err != nil || !shown.Stale || shown.WriteSupported || shown.Digest != r.Digest {
				t.Fatal(shown, err)
			}
			rows, err := m.GenerationCheckHistory()
			if err != nil || len(rows) != 1 {
				t.Fatal(rows, err)
			}
		})
	}
}

func TestGenerationCheckRecomputedTamperedManifestBlockedBeforeProbe(t *testing.T) {
	m, _, _, g := checkedFixture(t, 2)
	g.Targets[0].Outputs[0].Packages[0].Name = "../../outside"
	g.Digest = generationDigest(g)
	if err := saveLocal(m.generationFile(g.ID), g); err != nil {
		t.Fatal(err)
	}
	_, err := m.CheckGeneration(context.Background(), GenerationCheckRequest{Plan: g.ID, Digest: g.Digest, Probe: true})
	code(t, err, "GENERATOR_MANIFEST_INVALID")
	rows, err := m.GenerationCheckHistory()
	if err != nil || len(rows) != 0 {
		t.Fatal("tampered generation produced evidence", rows, err)
	}
}

func TestGenerationCheckNoopExclusionsAndBlockedBatch(t *testing.T) {
	m, b, _, _ := checkedFixture(t, 2)
	setFixtureExcludes(t, m, b[1].ID, []string{"skills"})
	g, err := m.StageRepair(context.Background(), repairRequest(t, m, b))
	if err != nil || !g.NoChanges {
		t.Fatal(g, err)
	}
	r, err := m.CheckGeneration(context.Background(), GenerationCheckRequest{Plan: g.ID, Digest: g.Digest})
	if err != nil || r.CandidateStatus != "UNKNOWN" || !r.NoChanges || r.Targets[0].Preserved == nil || !r.Targets[0].Preserved.All || len(r.Targets[0].Inspection.Tools[0].Skills) != 0 {
		t.Fatal(r, err)
	}
	// Blocked generated inputs retain useful diagnostics without launching any
	// tools even when the caller explicitly requests a batch probe.
	m2, b2, _, _ := checkedFixture(t, 2)
	generatedFixtureWrite(t, filepath.Join(filepath.Dir(b2[1].Root), ".agents/skills"), "guide/SKILL.md", []byte("handwritten collision"))
	g, err = m2.StageRepair(context.Background(), repairRequest(t, m2, b2))
	if err != nil || !g.Blocked {
		t.Fatal(g, err)
	}
	r, err = m2.CheckGeneration(context.Background(), GenerationCheckRequest{Plan: g.ID, Digest: g.Digest, Probe: true})
	if err != nil || r.CandidateStatus != "FAIL" || r.Targets[0].Inspection.Probe {
		t.Fatal(r, err)
	}
	for _, tool := range r.Targets[0].Inspection.Tools {
		if tool.L2 != "UNKNOWN" {
			t.Fatal(tool)
		}
	}
	_, err = m2.CheckGeneration(context.Background(), GenerationCheckRequest{Plan: workstore.ID(), Digest: g.Digest})
	if err == nil {
		t.Fatal("foreign manifest accepted")
	}
}

func TestGenerationCheckRejectsChangesDuringBatchAndCancellation(t *testing.T) {
	for _, change := range []string{"source", "output", "cancel"} {
		t.Run(change, func(t *testing.T) {
			m, _, s, g := checkedFixture(t, 3)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			m.inspectDraft = func(ctx context.Context, n *native.Manager, targets []native.Target, snapshot string, probe bool) (native.CheckReport, error) {
				calls++
				r, err := n.InspectDraft(ctx, targets, snapshot, false)
				if err == nil && calls == 2 {
					switch change {
					case "source":
						applySource(t, s[1], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "during.txt", Content: "edited during later copy inspection"}}})
					case "output":
						generatedFixtureWrite(t, g.Targets[0].Outputs[0].Root, "during.txt", []byte("external edit"))
					case "cancel":
						cancel()
					}
				}
				return r, err
			}
			_, err := m.CheckGeneration(ctx, GenerationCheckRequest{Plan: g.ID, Digest: g.Digest, Probe: true})
			if change == "cancel" {
				if err != context.Canceled {
					t.Fatal(err)
				}
			} else {
				code(t, err, "PLAN_STALE")
			}
			rows, err := m.GenerationCheckHistory()
			if err != nil || len(rows) != 0 {
				t.Fatal("stale batch saved as evidence", rows, err)
			}
		})
	}
}
