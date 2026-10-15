package native

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func fakeRuleLoad(_ context.Context, _ toolBinary, cwd, home string) ([]string, error) {
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		return nil, fmt.Errorf("home not empty")
	}
	data, err := os.ReadFile(filepath.Join(cwd, "AGENTS.md"))
	if err != nil || !bytes.Contains(data, []byte("aidock:begin")) {
		return nil, fmt.Errorf("missing candidate")
	}
	return []string{filepath.Join(cwd, "AGENTS.md")}, nil
}

func TestRuleLoadingIsExplicitSeparateAndBoundToExactCandidate(t *testing.T) {
	m, _ := writableFixture(t)
	saveRules(t, m, []any{"preserve source"})
	fakeProbes(m, discoveredGuide)
	m.probe.rules = func(context.Context, toolBinary, string, string) ([]string, error) {
		t.Fatal("unexpected rule session")
		return nil, nil
	}
	_, err := m.Check(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, RuleProbe: true})
	code(t, err, "INVALID_INPUT")
	for _, probe := range []bool{false, true} {
		r, e := m.Check(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: probe})
		if e != nil || r.Tools[0].Rules.Status != "UNKNOWN" || r.RuleProbe {
			t.Fatal(r, e)
		}
	}
	m.probe.rules = fakeRuleLoad
	before := observe(t, m, m.Source.Store.Root)
	r, err := m.Check(context.Background(), CheckRequest{Tools: []string{"claude-code", "codex-cli"}, Probe: true, RuleProbe: true})
	if err != nil || r.Tools[0].Rules.Code != "RULES_PROTOCOL_UNSUPPORTED" || r.Tools[1].Rules.Status != "PASS" || r.Tools[1].L3 != "UNKNOWN" || r.Tools[1].L4 != "UNKNOWN" {
		t.Fatal(r, err)
	}
	_, err = m.Plan(context.Background(), Request{Tools: []string{"claude-code", "codex-cli"}, CheckID: r.ID})
	code(t, err, "NATIVE_SEMANTICS_UNVERIFIED")
	unchanged(t, m, m.Source.Store.Root, before)
	if _, e := os.Stat(filepath.Join(m.Root, "AGENTS.md")); !os.IsNotExist(e) {
		t.Fatal("wrote bound rule")
	}
	last, err := m.Latest(context.Background())
	if err != nil || last.Stale || !last.RuleProbe || last.Tools[1].Rules.Status != "PASS" {
		t.Fatal(last, err)
	}
}

func TestRuleLoadingRejectsMissingUnexpectedChangedAndOverBudgetCandidates(t *testing.T) {
	for _, kind := range []string{"missing", "duplicate", "unexpected", "changed", "error", "budget", "version"} {
		t.Run(kind, func(t *testing.T) {
			m, _ := writableFixture(t)
			saveRules(t, m, []any{"rule"})
			fakeProbes(m, discoveredGuide)
			m.probe.rules = func(ctx context.Context, b toolBinary, cwd, home string) ([]string, error) {
				switch kind {
				case "missing":
					return nil, nil
				case "duplicate":
					return []string{filepath.Join(cwd, "AGENTS.md"), filepath.Join(cwd, "AGENTS.md")}, nil
				case "unexpected":
					return []string{filepath.Join(cwd, "AGENTS.override.md")}, nil
				case "changed":
					write(t, filepath.Join(cwd, "AGENTS.md"), []byte("outside change"))
					return []string{filepath.Join(cwd, "AGENTS.md")}, nil
				case "error":
					return nil, fmt.Errorf("PRIVATE captured protocol payload")
				default:
					t.Fatal("budget or unsupported version started session")
					return nil, nil
				}
			}
			if kind == "budget" {
				saveRules(t, m, []any{strings.Repeat("long", ruleDocumentBudget/4)})
			}
			if kind == "version" {
				m.probe.version = func(context.Context, toolBinary, string, string, string) (string, error) { return "0.162.2", nil }
			}
			r, e := m.Check(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true, RuleProbe: true})
			if e != nil || r.Tools[0].Rules.Status == "PASS" || strings.Contains(string(jsonData(r)), "PRIVATE") {
				t.Fatal(r, e)
			}
			_, err := m.Plan(context.Background(), Request{Tools: []string{"codex-cli"}, CheckID: r.ID})
			if err == nil {
				t.Fatal("invalid evidence qualified installation")
			}
		})
	}
}

func TestRuleQualifiedInstallJointRollbackUndoAndStaleChecks(t *testing.T) {
	m, _ := writableFixture(t)
	saveRules(t, m, []any{"first\nsecond", "third"})
	fakeProbes(m, discoveredGuide)
	m.probe.rules = fakeRuleLoad
	ctx := context.Background()
	manual := []byte("\xef\xbb\xbf# handwritten\r\n\r\n")
	write(t, filepath.Join(m.Root, "AGENTS.md"), manual)
	sourceBefore := observe(t, m, m.Source.Store.Root)
	r, err := m.Check(ctx, CheckRequest{Tools: []string{"codex-cli"}, Probe: true, RuleProbe: true})
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(ctx, Request{Tools: []string{"codex-cli"}, CheckID: r.ID})
	if err != nil || !p.WriteSupported || p.Evidence["L3"] != "UNKNOWN" || p.Evidence["rules"] != "PASS_ISOLATED_LOAD" {
		t.Fatal(p, err)
	}
	if err = m.Apply(ctx, p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(m.Root, "AGENTS.md"))
	if err != nil || !bytes.HasPrefix(data, manual) || !bytes.Contains(data, []byte("first\nsecond")) {
		t.Fatal(err)
	}
	h, err := m.History()
	if err != nil || len(h[0].Plan.Targets) != 2 {
		t.Fatal(h, err)
	}
	reverse, err := m.Reverse(h[0].Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyReverse(ctx, reverse.ID, reverse.Digest); err != nil {
		t.Fatal(err)
	}
	if got, e := os.ReadFile(filepath.Join(m.Root, "AGENTS.md")); e != nil || !bytes.Equal(got, manual) {
		t.Fatal("rollback lost manual", e)
	}
	undo, err := m.Reverse(reverse.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyReverse(ctx, undo.ID, undo.Digest); err != nil {
		t.Fatal(err)
	}
	if got, e := os.ReadFile(filepath.Join(m.Root, "AGENTS.md")); e != nil || !bytes.Equal(got, data) {
		t.Fatal("undo lost exact output", e)
	}
	unchanged(t, m, m.Source.Store.Root, sourceBefore)
	_, err = m.Plan(ctx, Request{Tools: []string{"codex-cli"}, CheckID: r.ID})
	code(t, err, "CHECK_STALE")
	// A current report still cannot survive changes to the native rule file.
	r, err = m.Check(ctx, CheckRequest{Tools: []string{"codex-cli"}, Probe: true, RuleProbe: true})
	if err != nil {
		t.Fatal(err)
	}
	p, err = m.Plan(ctx, Request{Tools: []string{"codex-cli"}, CheckID: r.ID})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(m.Root, "AGENTS.md"), append([]byte("new handwritten\n"), data...))
	code(t, m.Apply(ctx, p.ID, p.Digest), "CHECK_STALE")
}

func TestRuleOnlyLoadingAndConcurrentSourceChange(t *testing.T) {
	m, id := writableFixture(t)
	applySource(t, m.Source, source.Request{Action: "disable", ID: id})
	saveRules(t, m, []any{"rule only"})
	fakeProbes(m, func(context.Context, toolBinary, string, string, string) (discovery, error) {
		t.Fatal("no skills to discover")
		return discovery{}, nil
	})
	m.probe.rules = fakeRuleLoad
	r, err := m.Check(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true, RuleProbe: true})
	if err != nil || r.Tools[0].L2 != "SKIPPED" || r.Tools[0].Rules.Status != "PASS" {
		t.Fatal(r, err)
	}
	p, err := m.Plan(context.Background(), Request{Tools: []string{"codex-cli"}, CheckID: r.ID})
	if err != nil || !p.WriteSupported {
		t.Fatal(p, err)
	}
	m.probe.rules = func(ctx context.Context, b toolBinary, cwd, home string) ([]string, error) {
		saveRules(t, m, []any{"concurrent change"})
		return fakeRuleLoad(ctx, b, cwd, home)
	}
	_, err = m.Check(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true, RuleProbe: true})
	code(t, err, "PLAN_STALE")
}

func TestRuleShadowingIsDiagnosedAndInvalidatesPriorEvidence(t *testing.T) {
	m, _ := writableFixture(t)
	saveRules(t, m, []any{"rule"})
	fakeProbes(m, discoveredGuide)
	m.probe.rules = fakeRuleLoad
	ctx := context.Background()
	r, err := m.Check(ctx, CheckRequest{Tools: []string{"codex-cli"}, Probe: true, RuleProbe: true})
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(ctx, Request{Tools: []string{"codex-cli"}, CheckID: r.ID})
	if err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(m.Root, "AGENTS.override.md")
	write(t, override, []byte("handwritten override"))
	code(t, m.Apply(ctx, p.ID, p.Digest), "CHECK_STALE")
	blocked, err := m.Plan(ctx, Request{Tools: []string{"codex-cli"}})
	if err != nil || !blocked.Blocked || !has(blocked, "RULES_SHADOWED") {
		t.Fatal(blocked, err)
	}
	if b, e := os.ReadFile(override); e != nil || string(b) != "handwritten override" {
		t.Fatal("changed override", e)
	}
	if _, e := os.Stat(filepath.Join(m.Root, "AGENTS.md")); !os.IsNotExist(e) {
		t.Fatal("wrote shadowed rule")
	}
}

func TestRuleOverrideChangedAfterQualificationAbortsBeforeWriting(t *testing.T) {
	m, _ := writableFixture(t)
	saveRules(t, m, []any{"rule"})
	fakeProbes(m, discoveredGuide)
	m.probe.rules = fakeRuleLoad
	ctx := context.Background()
	r, err := m.Check(ctx, CheckRequest{Tools: []string{"codex-cli"}, Probe: true, RuleProbe: true})
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(ctx, Request{Tools: []string{"codex-cli"}, CheckID: r.ID})
	if err != nil {
		t.Fatal(err)
	}
	resolve := m.probe.resolve
	calls := 0
	m.probe.resolve = func(id string) (toolBinary, error) {
		calls++
		if calls == 2 {
			write(t, filepath.Join(m.Root, "AGENTS.override.md"), []byte("late override"))
		}
		return resolve(id)
	}
	code(t, m.Apply(ctx, p.ID, p.Digest), "PLAN_STALE")
	if _, e := os.Stat(filepath.Join(m.Root, "AGENTS.md")); !os.IsNotExist(e) {
		t.Fatal("wrote rule before guard")
	}
	h, e := m.History()
	if e != nil || len(h) != 0 {
		t.Fatal(h, e)
	}
}

func TestLocalRuleLoadingIntegration(t *testing.T) {
	if os.Getenv("AIDOCK_NATIVE_PROBE_TEST") != "1" {
		t.Skip("opt-in exact-version local CLI; disposable projects only")
	}
	m, _ := writableFixture(t)
	saveRules(t, m, []any{"Do not change business files.\nDescribe verification."})
	r, err := m.Check(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true, RuleProbe: true})
	if err != nil || r.Tools[0].Rules.Status != "PASS" || r.Tools[0].L3 != "UNKNOWN" {
		t.Fatal(r, err)
	}
	p, err := m.Plan(context.Background(), Request{Tools: []string{"codex-cli"}, CheckID: r.ID})
	if err != nil || !p.WriteSupported {
		t.Fatal(p, err)
	}
	// Negative controls use another empty home and explicit repository boundary:
	// an absent file is not reported, and AGENTS.override.md takes precedence.
	b, err := resolveBinary("codex-cli")
	if err != nil {
		t.Fatal(err)
	}
	for _, override := range []bool{false, true} {
		base := t.TempDir()
		project, home := filepath.Join(base, "p"), filepath.Join(base, "h")
		if err = m.prepareProbe(project, home, Target{Adapter: Adapters()[1]}); err != nil {
			t.Fatal(err)
		}
		if override {
			write(t, filepath.Join(project, "AGENTS.md"), []byte("base"))
			write(t, filepath.Join(project, "AGENTS.override.md"), []byte("override"))
		}
		sources, err := probeRuleProcess(context.Background(), b, project, home)
		if err != nil {
			t.Fatal(err)
		}
		if !override && len(sources) != 0 || override && (len(sources) != 1 || !samePath(sources[0], filepath.Join(project, "AGENTS.override.md"))) {
			t.Fatal("negative control", sources)
		}
	}
	t.Log("Codex 0.162.1: candidate instruction source PASS; absent and override controls PASS; actual project L3/L4 UNKNOWN")
	if _, e := os.Stat(filepath.Join(m.Root, "AGENTS.md")); !os.IsNotExist(e) {
		t.Fatal("integration wrote bound project")
	}
}
