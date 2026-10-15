package replica

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func saveSharedConfiguration(t *testing.T, s *source.Manager, edit func(map[string]any), rules *[]source.RuleFile) {
	t.Helper()
	state, err := s.Profile()
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(state.Profile)
	}
	p, err := s.ProfilePlan(context.Background(), source.ProfileEdit{Profile: state.Profile, ExpectedSHA256: state.SHA256, RuleFiles: rules, ExpectedRulesSHA256: state.RulesSHA256})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
}

func configurationRequest(t *testing.T, m *Manager, b []Binding, choices map[string]string) ResolveRequest {
	t.Helper()
	req := resolveRequest(t, m, b, nil)
	report, err := m.Compare(context.Background(), req.Request)
	if err != nil {
		t.Fatal(err)
	}
	for i := range req.Decisions {
		if choice := choices[req.Decisions[i].Target+"/"+req.Decisions[i].Entry]; choice != "" {
			req.Decisions[i].Choice = choice
		}
	}
	for _, target := range report.Targets {
		for _, item := range target.Configurations {
			if item.Status == "SAME" {
				continue
			}
			choice := choices[target.Copy.ID+"/"+item.ID]
			if choice == "" {
				choice = "skip"
			}
			req.Decisions = append(req.Decisions, Decision{Target: target.Copy.ID, Entry: item.ID, Choice: choice})
		}
	}
	return req
}

func configurationStatusFor(t *testing.T, m *Manager, b []Binding, id, want string) {
	t.Helper()
	r, err := m.Compare(context.Background(), Request{From: b[0].ID, Targets: []string{b[1].ID}})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range r.Targets[0].Configurations {
		if item.ID == id {
			if item.Status != want {
				t.Fatalf("%s: want %s got %s", id, want, item.Status)
			}
			return
		}
	}
	t.Fatal("configuration difference missing", id)
}

func TestSharedConfigurationFiveCopiesGenerationRollbackAndUndo(t *testing.T) {
	m, b, copies := replicaRuleFixture(t, 5)
	ctx := context.Background()
	first, second, third := workstore.ID(), workstore.ID(), workstore.ID()
	baseRules := []source.RuleFile{{ID: first, Name: "first", Enabled: true, Body: "base first"}, {ID: second, Name: "second", Enabled: true, Body: "base second"}}
	for _, copy := range copies {
		saveSharedConfiguration(t, copy, func(p map[string]any) { p["description"] = "common description"; p["rules"] = []any{"base inline"} }, &baseRules)
		generatedFixtureWrite(t, copy.Store.Root, "rules/unregistered.md", []byte("keep unregistered"))
	}
	baseline(t, m, b)
	adoptedRules := []source.RuleFile{{ID: second, Name: "second", Body: "\xef\xbb\xbf# shared bytes\r\nupdated second\r\n"}, {ID: third, Name: "third", Enabled: true, Body: "new shared rule"}}
	saveSharedConfiguration(t, copies[0], func(p map[string]any) {
		delete(p, "description")
		p["rules"] = []any{"publisher inline"}
		p["rules_override"] = true
	}, &adoptedRules)
	applySource(t, copies[0], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "asset.txt", Content: "publisher skill"}}})
	applySource(t, copies[1], source.Request{Action: "disable", ID: skill})
	conflicting := []source.RuleFile{{ID: first, Name: "first", Enabled: true, Body: "target conflict"}, baseRules[1]}
	saveSharedConfiguration(t, copies[2], nil, &conflicting)
	configurationStatusFor(t, m, b, source.SharedRulesID, "SOURCE_CHANGED")
	configurationStatusFor(t, m, []Binding{b[0], b[2]}, source.SharedRulesID, "CONFLICT")
	choices := map[string]string{}
	for _, copy := range b[1:] {
		choices[copy.ID+"/"+skill] = "source"
		choices[copy.ID+"/"+source.SharedProfileID] = "source"
		choices[copy.ID+"/"+source.SharedRulesID] = "source"
	}
	choices[b[1].ID+"/"+skill] = "target"
	choices[b[2].ID+"/"+source.SharedProfileID] = "target"
	choices[b[2].ID+"/"+source.SharedRulesID] = "target"
	choices[b[3].ID+"/"+source.SharedProfileID] = "skip"
	before := captureSources(t, copies)
	control := generatedObservation(t, m, m.Control.Root)
	p, err := m.Resolve(ctx, configurationRequest(t, m, b, choices))
	if err != nil || len(p.Targets) != 4 {
		t.Fatal("combined candidate", len(p.Targets), err)
	}
	for _, candidate := range p.Targets {
		copy, err := m.copySource(candidate.Copy)
		if err != nil {
			t.Fatal(err)
		}
		if err = transfer(m.Control, copy.Store, candidate.After); err != nil {
			t.Fatal(err)
		}
		bundle, err := copy.ExportCandidate(candidate.After)
		if err != nil {
			t.Fatal(err)
		}
		isKept := candidate.Copy.ID == b[2].ID
		joined := strings.Join(bundle.Rules, "\n")
		if strings.Contains(joined, "new shared rule") == isKept || !strings.Contains(joined, "Local rule") || (candidate.Copy.ID == b[1].ID && len(bundle.Packages) != 0) {
			t.Fatal("rules or independent skill decision lost", candidate.Copy.ID, joined)
		}
		if candidate.After.Entries["rules/unregistered.md"] != candidate.Before.Entries["rules/unregistered.md"] {
			t.Fatal("unregistered rule changed")
		}
		if !isKept {
			if _, exists := candidate.After.Entries["rules/"+first+".md"]; exists {
				t.Fatal("explicit ordered-library deletion not adopted")
			}
			body, err := m.Control.Data(candidate.After.Entries["rules/"+second+".md"])
			if err != nil || string(body) != adoptedRules[0].Body {
				t.Fatal("disabled rule exact bytes lost", err)
			}
		}
	}
	g, err := m.StageGeneration(ctx, GenerationRequest{Plan: p.ID, Digest: p.Digest, Tools: []string{"claude-code", "codex-cli"}})
	if err != nil || g.Blocked {
		t.Fatal(g.Blocked, err)
	}
	checked, err := m.CheckGeneration(ctx, GenerationCheckRequest{Plan: g.ID, Digest: g.Digest})
	if err != nil {
		t.Fatal(err)
	}
	semantics := 0
	for _, gate := range checked.Gates {
		if gate.Code == "NATIVE_SEMANTICS" {
			semantics++
			if gate.Status != "PASS" {
				t.Fatal("preserved unregistered rules blocked native semantics", gate)
			}
		}
	}
	if semantics != 8 {
		t.Fatal("missing per-tool semantic gates", semantics)
	}
	r, err := m.RehearseGenerated(ctx, g.ID, g.Digest)
	if err != nil || r.Status != "APPLIED" || len(r.Roots) != 21 {
		t.Fatal(r, err)
	}
	check := func(after bool) {
		t.Helper()
		for _, candidate := range p.Targets {
			want := candidate.Before
			if after {
				want = candidate.After
			}
			generatedEqual(t, m, generatedRoot(r, "copies/"+candidate.Copy.ID+"/.aidock"), want)
		}
		for _, target := range g.Targets {
			for _, output := range target.Outputs {
				want := output.Rule.Before
				if after {
					want = output.Rule.After
				}
				replicaFileEqual(t, m, generatedRoot(r, generatedRulePath(target.Copy, output.Adapter.ID)), want)
			}
		}
		want := p.ControlBefore
		if after {
			want = p.ControlAfter
		}
		generatedEqual(t, m, generatedRoot(r, "control"), want)
		generatedEqual(t, m, m.Control.Root, control)
		unchanged(t, copies, before)
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

func TestSharedConfigurationBaselineLegacyUnknownAndStale(t *testing.T) {
	m, b, copies := fixture(t, 2)
	ctx := context.Background()
	baseline(t, m, b)
	// A historical v1 baseline with no configuration fields is still readable;
	// it never supplies an invented baseline for a newly compared config group.
	tree, _ := m.Control.Capture(m.Control.Root)
	base, _, err := m.baseline(tree, b[0].ID, b[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	base.Configurations = nil
	generatedFixtureWrite(t, m.Control.Root, pairPath(b[0].ID, b[1].ID), data(base))
	saveSharedConfiguration(t, copies[0], func(p map[string]any) { p["description"] = "publisher" }, nil)
	configurationStatusFor(t, m, b, source.SharedProfileID, "BASELINE_UNKNOWN")
	req := configurationRequest(t, m, b, map[string]string{b[1].ID + "/" + source.SharedProfileID: "source"})
	missing := req
	missing.Decisions = nil
	_, err = m.Resolve(ctx, missing)
	code(t, err, "REPLICA_DECISION_REQUIRED")
	p, err := m.Resolve(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	saveSharedConfiguration(t, copies[1], func(p map[string]any) { p["description"] = "late target edit" }, nil)
	_, err = m.StageGeneration(ctx, GenerationRequest{Plan: p.ID, Digest: p.Digest, Tools: []string{"codex-cli"}})
	code(t, err, "PLAN_STALE")
}

func TestSharedConfigurationExclusionsAndUnknownFilesAreProtected(t *testing.T) {
	for _, exclude := range []string{"profile.yaml", "rules", "rules/unregistered.md"} {
		t.Run(exclude, func(t *testing.T) {
			m, b, copies := fixture(t, 2)
			registry, err := m.List()
			if err != nil {
				t.Fatal(err)
			}
			for i := range registry.Copies {
				if registry.Copies[i].ID == b[1].ID {
					registry.Copies[i].Excludes = []string{exclude}
				}
			}
			b[1].Excludes = []string{exclude}
			generatedFixtureWrite(t, m.Control.Root, "replicas.json", data(registry))
			rules := []source.RuleFile{{ID: workstore.ID(), Name: "rule", Enabled: true, Body: "source"}}
			saveSharedConfiguration(t, copies[0], nil, &rules)
			configurationStatusFor(t, m, b, source.SharedRulesID, "LOCAL_EXCLUDED")
			_, err = m.Resolve(context.Background(), configurationRequest(t, m, b, map[string]string{b[1].ID + "/" + source.SharedRulesID: "source"}))
			code(t, err, "REPLICA_AUTHORITY_UNSUPPORTED")
		})
	}
	m, b, copies := fixture(t, 2)
	rules := []source.RuleFile{{ID: workstore.ID(), Name: "rule", Enabled: true, Body: "source"}}
	saveSharedConfiguration(t, copies[0], nil, &rules)
	generatedFixtureWrite(t, copies[1].Store.Root, fmt.Sprintf("rules/%s.md", rules[0].ID), []byte("local orphan"))
	_, err := m.Resolve(context.Background(), configurationRequest(t, m, b, map[string]string{b[1].ID + "/" + source.SharedRulesID: "source"}))
	code(t, err, "RULE_FILE_UNOWNED")
	// Unknown Profile fields cannot enter a baseline even with a recomputed hash.
	baseline(t, m, b)
	tree, _ := m.Control.Capture(m.Control.Root)
	base, _, err := m.baseline(tree, b[0].ID, b[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	v := base.Configurations[source.SharedProfileID]
	v.Profile["config_overrides"] = map[string]any{"secret": "forged"}
	v.Digest = source.SharedConfigurationDigest(v)
	base.Configurations[v.ID] = v
	generatedFixtureWrite(t, m.Control.Root, pairPath(b[0].ID, b[1].ID), data(base))
	_, err = m.Compare(context.Background(), Request{From: b[0].ID, Targets: []string{b[1].ID}})
	code(t, err, "BASELINE_INVALID")
}

func TestSharedConfigurationRecomputedResolutionCannotAdoptUnchosenBytes(t *testing.T) {
	for _, kind := range []string{"profile", "unowned", "baseline", "downgrade"} {
		t.Run(kind, func(t *testing.T) {
			m, b, copies := fixture(t, 2)
			saveSharedConfiguration(t, copies[0], func(p map[string]any) { p["description"] = "shared publisher" }, nil)
			p, err := m.Resolve(context.Background(), configurationRequest(t, m, b, map[string]string{b[1].ID + "/" + source.SharedProfileID: "source"}))
			if err != nil {
				t.Fatal(err)
			}
			want := "REPLICA_CANDIDATE_INVALID"
			switch kind {
			case "profile":
				if err = m.Control.PutFile(&p.Targets[0].After, "profile.yaml", []byte("schema_version: 2\nname: forged\n"), 0644); err != nil {
					t.Fatal(err)
				}
			case "unowned":
				if err = m.Control.PutFile(&p.Targets[0].After, "rules/unregistered.md", []byte("forged"), 0644); err != nil {
					t.Fatal(err)
				}
			case "baseline":
				if err = m.Control.PutFile(&p.ControlAfter, "baselines/forged.json", []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "downgrade":
				p.Version = 1
				want = "BACKUP_INVALID"
			}
			p.Targets[0].Changes = workstore.Changes(p.Targets[0].Before, p.Targets[0].After)
			p.Digest = resolutionDigest(p)
			if err = saveLocal(filepath.Join(m.resolutionDir(), "plans", p.ID+".json"), p); err != nil {
				t.Fatal(err)
			}
			_, err = m.StageGeneration(context.Background(), GenerationRequest{Plan: p.ID, Digest: p.Digest, Tools: []string{"codex-cli"}})
			code(t, err, want)
			_, err = m.Rehearse(context.Background(), p.ID, p.Digest)
			code(t, err, want)
		})
	}
}
