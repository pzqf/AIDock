package native

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/rules"
)

func TestRuleOverwriteRequiresCurrentWholeFileAndPreservesManualBytes(t *testing.T) {
	m, _ := writableFixture(t)
	saveRules(t, m, []any{"canonical"})
	seedRulePlan(t, m, rulesPlan(t, m))
	file := filepath.Join(m.Root, "AGENTS.md")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	drift := []byte("# manual\r\n" + strings.Replace(string(data), "canonical", "hand edited", 1) + "\nsuffix\r\n")
	write(t, file, drift)
	p := rulesPlan(t, m)
	if !p.Blocked || !has(p, "RULES_MANAGED_DRIFT") {
		t.Fatal(p)
	}
	r := p.Targets[1].Rule
	if r.ObservedBody == nil || *r.ObservedBody != "hand edited" {
		t.Fatal(r)
	}
	decision := RuleResolution{Tool: "codex-cli", Action: "overwrite", ObservedDigest: r.ObservedDigest}
	req := Request{Tools: []string{"codex-cli"}, RuleResolutions: []RuleResolution{decision}}
	p, err = m.Plan(context.Background(), req)
	if err != nil || p.Blocked || len(p.RuleResolutions) != 1 {
		t.Fatal(p, err)
	}
	r = p.Targets[0].Rule
	observed, err := rules.Inspect(drift)
	if err != nil || r.Prefix != observed.Prefix || r.Suffix != observed.Suffix || !strings.Contains(r.Managed, "canonical") {
		t.Fatal(r, err)
	}
	write(t, file, append(drift, []byte("late manual")...))
	_, err = m.Diff(context.Background(), p.ID, p.Digest)
	code(t, err, "PLAN_STALE")
	_, err = m.Plan(context.Background(), req)
	code(t, err, "PLAN_STALE")
	req.RuleResolutions = append(req.RuleResolutions, decision)
	_, err = m.Plan(context.Background(), req)
	code(t, err, "INVALID_INPUT")
}
