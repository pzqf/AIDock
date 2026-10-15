package replica

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/source"
)

func TestReopenGenerationAfterRestartKeepsExactCandidateAndOriginalInputs(t *testing.T) {
	m, b, s, g := checkedFixture(t, 5)
	ctx := context.Background()
	originals := captureSources(t, s)
	control := generatedObservation(t, m, m.Control.Root)
	r, err := m.CheckGeneration(ctx, GenerationCheckRequest{Plan: g.ID, Digest: g.Digest})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(m.generationFile(g.ID))
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := New(s[0])
	if err != nil {
		t.Fatal(err)
	}
	v, err := fresh.ReopenGeneration(ctx, r.ID)
	if err != nil || !v.Current || v.Check.Stale || v.Check.ID != r.ID || v.Generation.ID != g.ID || v.Generation.Digest != g.Digest || v.Source.ID != b[0].ID || len(v.Targets) != 4 {
		t.Fatal(v, err)
	}
	for i, target := range v.Targets {
		var original Binding
		for _, copy := range b {
			if copy.ID == target.ID {
				original = copy
			}
		}
		if target.ID != g.Targets[i].Copy || target.Name != original.Name || target.Root != original.Root {
			t.Fatal("lost bound target identity", target)
		}
	}
	after, err := os.ReadFile(m.generationFile(g.ID))
	if err != nil || string(after) != string(manifest) {
		t.Fatal("reopen rewrote saved generation", err)
	}
	history, err := fresh.GenerationCheckHistory()
	if err != nil || len(history) != 1 || history[0].ID != r.ID {
		t.Fatal("reopen generated new evidence", history, err)
	}
	unchanged(t, s, originals)
	generatedEqual(t, m, m.Control.Root, control)
}

func TestReopenGenerationSeparatesExpiredCheckFromChangedCandidate(t *testing.T) {
	for _, change := range []string{"expiry", "inspection-policy", "source", "output", "excluded", "offline"} {
		t.Run(change, func(t *testing.T) {
			m, b, s, g := checkedFixture(t, 2)
			ctx := context.Background()
			r, err := m.CheckGeneration(ctx, GenerationCheckRequest{Plan: g.ID, Digest: g.Digest})
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "expiry", "inspection-policy":
				if change == "expiry" {
					r.Expires = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
				} else {
					r.Policy = "previous-policy"
				}
				r.Digest = generationCheckDigest(r)
				if err = saveLocal(filepath.Join(m.generationChecksDir(), r.ID+".json"), r); err != nil {
					t.Fatal(err)
				}
			case "source":
				applySource(t, s[1], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "changed.txt", Content: "new content"}}})
			case "output":
				generatedFixtureWrite(t, g.Targets[0].Outputs[0].Root, "manual.txt", []byte("manual"))
			case "excluded":
				setFixtureExcludes(t, m, b[1].ID, []string{"skills/guide"})
			case "offline":
				parent := filepath.Dir(b[1].Root)
				if err = os.Rename(parent, parent+"-offline"); err != nil {
					t.Fatal(err)
				}
				defer os.Rename(parent+"-offline", parent)
			}
			v, err := m.ReopenGeneration(ctx, r.ID)
			wantCurrent := change == "expiry" || change == "inspection-policy"
			if err != nil || v.Current != wantCurrent || !v.Check.Stale || v.Generation.Digest != g.Digest || v.Check.ID != r.ID || !v.Current && v.Reason == "" {
				t.Fatal(v, err)
			}
		})
	}
}

func TestReopenGenerationRejectsCorruptOrUnrelatedHistoryAndCancellation(t *testing.T) {
	for _, change := range []string{"copy", "source-digest", "count", "repair", "manifest", "missing", "cancel"} {
		t.Run(change, func(t *testing.T) {
			m, _, _, g := checkedFixture(t, 2)
			ctx := context.Background()
			r, err := m.CheckGeneration(ctx, GenerationCheckRequest{Plan: g.ID, Digest: g.Digest})
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "copy":
				r.Targets[0].Copy.Name = "unrelated project name"
			case "source-digest":
				r.Targets[0].Source = "unrelated digest"
			case "count":
				r.Targets = append(r.Targets, r.Targets[0])
			case "repair":
				r.Repair = !r.Repair
			case "manifest":
				g.Targets[0].Copy = "another-copy"
				if err = saveLocal(m.generationFile(g.ID), g); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err = os.Remove(m.generationFile(g.ID)); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			r.Digest = generationCheckDigest(r)
			if err = saveLocal(filepath.Join(m.generationChecksDir(), r.ID+".json"), r); err != nil {
				t.Fatal(err)
			}
			_, err = m.ReopenGeneration(ctx, r.ID)
			if err == nil {
				t.Fatal("accepted unrelated, corrupt, missing or cancelled candidate")
			}
		})
	}
}
