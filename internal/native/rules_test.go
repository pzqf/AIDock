package native

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/rules"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func saveRules(t *testing.T, m *Manager, values []any) {
	t.Helper()
	s, err := m.Source.Profile()
	if err != nil {
		t.Fatal(err)
	}
	s.Profile["rules"] = values
	p, err := m.Source.ProfilePlan(context.Background(), source.ProfileEdit{ExpectedSHA256: s.SHA256, Profile: s.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
}
func rulesPlan(t *testing.T, m *Manager) Plan {
	t.Helper()
	p, err := m.Plan(context.Background(), Request{Tools: []string{"claude-code", "codex-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func seedRulePlan(t *testing.T, m *Manager, p Plan) {
	t.Helper()
	for _, target := range p.Targets {
		materialize(t, m, target)
		if target.Rule != nil {
			data, err := m.Source.Store.Data(target.Rule.After.Entries[workstore.FileContent])
			if err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(m.Root, target.Rule.Path), data)
		}
	}
}
func TestRulesPreviewAndRehearsalRestoreWholeFilesAndPackages(t *testing.T) {
	m, _ := writableFixture(t)
	ctx := context.Background()
	manual := []byte("\xef\xbb\xbf# 团队手写\r\n保留空行\r\n\r\n")
	write(t, filepath.Join(m.Root, "CLAUDE.md"), manual)
	saveRules(t, m, []any{"修改前检查范围。\n交付说明回退。", "有序第二条"})
	before := observe(t, m, m.Source.Store.Root)
	p := rulesPlan(t, m)
	if p.Blocked || p.WriteSupported || len(p.Targets) != 2 || p.Targets[0].Rule == nil || p.Targets[1].Rule == nil {
		t.Fatal(p)
	}
	if p.Targets[0].Rule.Prefix != string(manual) || !strings.Contains(p.Targets[1].Rule.Managed, "有序第二条") {
		t.Fatal(p.Targets)
	}
	code(t, m.Apply(ctx, p.ID, p.Digest), "NATIVE_WRITE_UNAVAILABLE")
	if _, err := m.Diff(ctx, p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	r, err := m.Rehearse(ctx, p.ID, p.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if r.Transactions[0].Targets != 4 {
		t.Fatal(r)
	}
	for _, target := range p.Targets {
		data, e := os.ReadFile(filepath.Join(r.Root, target.Rule.Path))
		if e != nil || string(data) != target.Rule.Prefix+target.Rule.Managed+target.Rule.Suffix {
			t.Fatal(target.Rule.Path, e)
		}
	}
	reverse, err := m.RehearsalReverse(r.ID, r.Transactions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	r, err = m.RehearsalApply(ctx, r.ID, reverse.ID, reverse.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if data, e := os.ReadFile(filepath.Join(r.Root, "CLAUDE.md")); e != nil || string(data) != string(manual) {
		t.Fatal("manual rollback", e)
	}
	if _, e := os.Stat(filepath.Join(r.Root, "AGENTS.md")); !os.IsNotExist(e) {
		t.Fatal("created file not removed", e)
	}
	u, err := m.RehearsalReverse(r.ID, r.Transactions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.RehearsalApply(ctx, r.ID, u.ID, u.Digest); err != nil {
		t.Fatal(err)
	}
	unchanged(t, m, m.Source.Store.Root, before)
	if data, e := os.ReadFile(filepath.Join(m.Root, "CLAUDE.md")); e != nil || string(data) != string(manual) {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(m.Root, "AGENTS.md")); !os.IsNotExist(e) {
		t.Fatal("bound file changed", e)
	}
}
func TestRulesOwnershipDriftIdempotenceAndEmptyList(t *testing.T) {
	m, _ := writableFixture(t)
	saveRules(t, m, []any{"first"})
	p := rulesPlan(t, m)
	seedRulePlan(t, m, p)
	p = rulesPlan(t, m)
	for _, target := range p.Targets {
		if len(target.Changes) != 0 || len(target.Rule.Changes) != 0 {
			t.Fatal("non-idempotent", target)
		}
	}
	// Editing outside the block is allowed and survives the next compile.
	path := filepath.Join(m.Root, "CLAUDE.md")
	data, _ := os.ReadFile(path)
	write(t, path, append([]byte("new manual\r\n"), data...))
	saveRules(t, m, []any{})
	p = rulesPlan(t, m)
	if p.Blocked || !strings.HasPrefix(p.Targets[0].Rule.Prefix, "new manual\r\n") || p.Targets[0].Rule.Managed != rules.Begin+"\n\n"+rules.End {
		t.Fatal(p)
	}
	seedRulePlan(t, m, p)
	data, _ = os.ReadFile(path)
	write(t, path, []byte(strings.Replace(string(data), rules.Begin+"\n", rules.Begin+"\nmanual in block", 1)))
	p = rulesPlan(t, m)
	if !p.Blocked || !has(p, "RULES_MANAGED_DRIFT") {
		t.Fatal(p)
	}
	code(t, m.Apply(context.Background(), p.ID, p.Digest), "NATIVE_PREFLIGHT_FAILED")
}
func TestRulesPreviewBindsFileAndRetainsInstallationGate(t *testing.T) {
	m, _ := writableFixture(t)
	saveRules(t, m, []any{"rule"})
	fakeProbes(m, discoveredGuide)
	p := rulesPlan(t, m)
	write(t, filepath.Join(m.Root, "AGENTS.md"), []byte("later manual"))
	_, err := m.Diff(context.Background(), p.ID, p.Digest)
	code(t, err, "PLAN_STALE")
	r, err := m.Check(context.Background(), CheckRequest{Tools: []string{"claude-code", "codex-cli"}, Probe: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Tools[0].L2 != "PASS" || r.Tools[0].L3 != "UNKNOWN" {
		t.Fatal(r)
	}
	_, err = m.Plan(context.Background(), Request{Tools: []string{"claude-code", "codex-cli"}, CheckID: r.ID})
	code(t, err, "NATIVE_SEMANTICS_UNVERIFIED")
	// The replica skill-only compiler must not silently drop rules.
	bundle, err := m.Source.Export()
	if err != nil {
		t.Fatal(err)
	}
	observations := map[string]workstore.Tree{"codex-cli": {Entries: map[string]workstore.Entry{}}}
	targets, err := m.DraftTargets(context.Background(), []string{"codex-cli"}, bundle, observations)
	if err != nil || len(targets) != 1 || targets[0].Status != "FAIL" {
		t.Fatal(targets, err)
	}
}

func TestRulesOnlyProfileCreatesOwnershipWithoutAnyEnabledSkills(t *testing.T) {
	m, id := writableFixture(t)
	applySource(t, m.Source, source.Request{Action: "disable", ID: id})
	saveRules(t, m, []any{"rules without skills"})
	p := rulesPlan(t, m)
	if p.Blocked || p.Targets[0].Rule == nil || len(p.Targets[0].Packages) != 0 {
		t.Fatal(p)
	}
	r, err := m.Rehearse(context.Background(), p.ID, p.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if data, e := os.ReadFile(filepath.Join(r.Root, "AGENTS.md")); e != nil || !strings.Contains(string(data), "rules without skills") {
		t.Fatal(e)
	}
}
