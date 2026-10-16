package replica

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/pzqf/AIDock/internal/workstore"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/source"
)

func TestPublicationRequiresExplicitScopeAndExactCandidateEvidence(t *testing.T) {
	m, b, _ := scopedReplicaFixture(t, 2)
	g := scopedRepair(t, m, b)
	r, err := m.CheckGeneration(context.Background(), GenerationCheckRequest{Plan: g.ID, Digest: g.Digest})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.PlanPublication(context.Background(), PublicationRequest{Generation: g.ID, Digest: g.Digest, Check: r.ID, CheckDigest: r.Digest})
	code(t, err, "PUBLICATION_CHECK_REQUIRED")
	rows, err := m.PublicationHistory()
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
}

type publicationChild struct{ Root, Local, Owner, Scope, Copy, ID, Digest, Phase string }

func TestPublicationCrashHelper(t *testing.T) {
	file := os.Getenv("AIDOCK_PUBLICATION_CHILD")
	if file == "" {
		return
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var v publicationChild
	if err = json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	s, err := source.New(v.Root, v.Local, v.Owner, v.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if v.Copy != "" {
		if err = s.BindBridge("", v.Copy); err != nil {
			t.Fatal(err)
		}
	}
	m, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	m.PublicationHook = func(stage string, i int) error {
		if stage == v.Phase && i == 0 {
			os.Exit(29)
		}
		return nil
	}
	if v.Phase == "recovered-target" {
		_, err = m.RecoverPublication(v.ID)
	} else {
		_, err = m.ApplyPublication(context.Background(), v.ID, v.Digest)
	}
	t.Fatalf("child failed to interrupt: %v", err)
}

// This opt-in suite runs actual installed CLI discovery and rule-source probes
// once, then reuses the immutable evidence for transaction fault scenarios.
func TestLocalPublicationFiveCopiesCommitReverseFailureAndCrash(t *testing.T) {
	if os.Getenv("AIDOCK_NATIVE_PROBE_TEST") != "1" {
		t.Skip("opt-in actual installed CLI acceptance against temporary copies")
	}
	m, b, copies := scopedReplicaFixture(t, 5)
	ctx := context.Background()
	baseline(t, m, b)
	applySource(t, copies[0], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "release.txt", Content: "formal publication"}}})
	publisher := generatedObservation(t, m, b[0].Root)
	resolution, err := m.Resolve(ctx, resolveRequest(t, m, b, map[string]string{b[1].ID: "source", b[2].ID: "source", b[3].ID: "source", b[4].ID: "source"}))
	if err != nil {
		t.Fatal(err)
	}
	g, err := m.StageGeneration(ctx, GenerationRequest{Plan: resolution.ID, Digest: resolution.Digest, Tools: []string{"claude-code", "codex-cli"}, ApplicationScope: native.SkillsRulesScope})
	if err != nil {
		t.Fatal(err)
	}
	check, err := m.CheckGeneration(ctx, GenerationCheckRequest{Plan: g.ID, Digest: g.Digest, Probe: true, RuleProbe: true})
	if err != nil {
		t.Fatal(err)
	}
	request := PublicationRequest{g.ID, g.Digest, check.ID, check.Digest}
	plan := func() PublicationPlan {
		t.Helper()
		p, err := m.PlanPublication(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	verify := func(p PublicationPlan, after bool) {
		t.Helper()
		c := m.publicationCoordinator(p)
		for _, target := range p.Transaction.Targets {
			root := ""
			for _, bound := range c.Roots {
				if bound.Path == target.Path {
					root = bound.Root
				}
			}
			want := target.Before
			if after {
				want = target.After
			}
			if target.File {
				replicaFileEqual(t, m, root, want)
			} else {
				generatedEqual(t, m, root, want)
			}
		}
		generatedEqual(t, m, b[0].Root, publisher)
	}
	p := plan()
	other, err := New(copies[1])
	if err != nil {
		t.Fatal(err)
	}
	rows, err := other.PublicationHistory()
	if err != nil || len(rows) != 0 {
		t.Fatal("another initiating copy must have an independent history", rows, err)
	}
	_, err = other.PublicationShow(p.ID)
	code(t, err, "BACKUP_INVALID")
	rows, err = m.PublicationHistory()
	if err != nil || len(rows) != 1 || rows[0].ID != p.ID {
		t.Fatal("original initiating copy lost its publication", rows, err)
	}
	for _, change := range []string{"unowned-file", "source", "baseline", "duplicate-input"} {
		t.Run("tamper-"+change, func(t *testing.T) {
			var altered PublicationPlan
			if err := decode(data(p), &altered); err != nil {
				t.Fatal(err)
			}
			index := 0
			name := "unexpected"
			switch change {
			case "unowned-file":
				index = 2
				name = "settings.json"
			case "source":
				index = 1
			case "baseline":
				index = 0
			case "duplicate-input":
				altered.Inputs[0] = altered.Inputs[1]
			}
			if change != "duplicate-input" {
				if err := m.Control.PutFile(&altered.Transaction.Targets[index].After, name, []byte("not a selected change"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			altered.Digest = publicationDigest(altered)
			code(t, m.validatePublicationTransaction(altered), "PUBLICATION_PLAN_INVALID")
		})
	}
	_, err = m.ApplyPublication(ctx, p.ID, "wrong")
	code(t, err, "PLAN_CONFIRMATION_REQUIRED")
	r, err := m.ApplyPublication(ctx, p.ID, p.Digest)
	if err != nil || r.Status != "APPLIED" || len(r.Roots) != 21 {
		t.Fatal(r, err)
	}
	verify(p, true)
	// Recovery and reverse use the self-contained publication descriptor.
	if err = os.Remove(m.generationFile(g.ID)); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(m.resolutionDir(), "plans", resolution.ID+".json")); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(m.generationChecksDir(), check.ID+".json")); err != nil {
		t.Fatal(err)
	}
	for _, after := range []bool{false, true, false} {
		reverse, err := m.ReversePublication(r.ID, r.Transactions[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		r, err = m.ApplyPublicationReverse(ctx, r.ID, reverse.ID, reverse.Digest)
		if err != nil {
			t.Fatal(err)
		}
		verify(p, after)
	}
	if err = saveLocal(m.generationFile(g.ID), g); err != nil {
		t.Fatal(err)
	}
	if err = saveLocal(filepath.Join(m.resolutionDir(), "plans", resolution.ID+".json"), resolution); err != nil {
		t.Fatal(err)
	}
	if err = saveLocal(filepath.Join(m.generationChecksDir(), check.ID+".json"), check); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"second-copy", "publisher-change", "local-first-save"} {
		t.Run(scenario, func(t *testing.T) {
			p := plan()
			publisherEdit := filepath.Join(b[0].Root, "external-after-check.txt")
			localSource, err := m.copySource(b[1])
			if err != nil {
				t.Fatal(err)
			}
			localState, err := localSource.Profile()
			if err != nil {
				t.Fatal(err)
			}
			m.PublicationHook = func(stage string, i int) error {
				if stage == "target-complete" && i == 6 {
					switch scenario {
					case "second-copy":
						return errors.New("injected second copy failure")
					case "publisher-change":
						return os.WriteFile(publisherEdit, []byte("preserve publisher edit"), 0644)
					case "local-first-save":
						_, err := localSource.ProfilePlan(ctx, source.ProfileEdit{Target: "local", ExpectedSHA256: localState.Layers.LocalSHA256, Profile: map[string]any{"schema_version": 2, "name": "local"}})
						code(t, err, "SOURCE_BUSY")
					}
				}
				return nil
			}
			_, err = m.ApplyPublication(ctx, p.ID, p.Digest)
			m.PublicationHook = nil
			if scenario == "local-first-save" {
				if err != nil {
					t.Fatal(err)
				}
				r, err := m.PublicationShow(p.ID)
				if err != nil {
					t.Fatal(err)
				}
				reverse, err := m.ReversePublication(p.ID, r.Transactions[0].ID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = m.ApplyPublicationReverse(ctx, p.ID, reverse.ID, reverse.Digest); err != nil {
					t.Fatal(err)
				}
			} else {
				code(t, err, "COORDINATED_APPLY_FAILED")
			}
			if scenario == "publisher-change" {
				body, err := os.ReadFile(publisherEdit)
				if err != nil || string(body) != "preserve publisher edit" {
					t.Fatal("publisher was rolled back", err)
				}
				if err = os.Remove(publisherEdit); err != nil {
					t.Fatal(err)
				}
			}
			verify(p, false)
		})
	}
	for _, phase := range []string{"prepared", "barrier", "target-complete", "committed", "cleared-barrier"} {
		t.Run("crash-"+phase, func(t *testing.T) {
			p := plan()
			v := publicationChild{m.Source.Store.Root, m.Source.Store.Local, m.Source.Owner, m.Source.Scope, m.Source.Copy, p.ID, p.Digest, phase}
			file := filepath.Join(t.TempDir(), "child.json")
			bytes, _ := json.Marshal(v)
			if err := os.WriteFile(file, bytes, 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestPublicationCrashHelper$")
			cmd.Env = append(os.Environ(), "AIDOCK_PUBLICATION_CHILD="+file)
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 29 {
				t.Fatal(string(output), err)
			}
			fresh, err := New(m.Source)
			if err != nil {
				t.Fatal(err)
			}
			r, err := fresh.RecoverPublication(p.ID)
			if err != nil || r.Pending {
				t.Fatal(r, err)
			}
			after := phase == "committed" || phase == "cleared-barrier"
			verify(p, after)
			if after {
				reverse, err := fresh.ReversePublication(p.ID, r.Transactions[0].ID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = fresh.ApplyPublicationReverse(ctx, p.ID, reverse.ID, reverse.Digest); err != nil {
					t.Fatal(err)
				}
				verify(p, false)
			}
		})
	}
	// Pure repair uses each target's own saved source and must also validate
	// under the control lock without reentering Compare's public lock.
	repairRequest := repairRequest(t, m, b)
	repairRequest.ApplicationScope = native.SkillsRulesScope
	repair, err := m.StageRepair(ctx, repairRequest)
	if err != nil {
		t.Fatal(err)
	}
	repairCheck, err := m.CheckGeneration(ctx, GenerationCheckRequest{Plan: repair.ID, Digest: repair.Digest, Probe: true, RuleProbe: true})
	if err != nil {
		t.Fatal(err)
	}
	repairPlan, err := m.PlanPublication(ctx, PublicationRequest{repair.ID, repair.Digest, repairCheck.ID, repairCheck.Digest})
	if err != nil {
		t.Fatal(err)
	}
	repaired, err := m.ApplyPublication(ctx, repairPlan.ID, repairPlan.Digest)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range repairPlan.Transaction.Targets {
		if target.Path == "control" || strings.HasSuffix(target.Path, "/.aidock") {
			if workstore.Digest(target.Before) != workstore.Digest(target.After) {
				t.Fatal("repair changed authority", target.Path)
			}
		}
	}
	reverse, err := m.ReversePublication(repaired.ID, repaired.Transactions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyPublicationReverse(ctx, repaired.ID, reverse.ID, reverse.Digest); err != nil {
		t.Fatal(err)
	}
	verify(repairPlan, false)
}
