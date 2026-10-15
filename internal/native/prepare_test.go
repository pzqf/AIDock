package native

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func TestPrepareStaticCannotLaunchOrInstall(t *testing.T) {
	m, _ := writableFixture(t)
	fakeProbes(m, func(context.Context, toolBinary, string, string, string) (discovery, error) {
		t.Fatal("static preparation started tool discovery")
		return discovery{}, nil
	})
	m.probe.version = func(context.Context, toolBinary, string, string, string) (string, error) {
		t.Fatal("static preparation executed a version command")
		return "", nil
	}
	before := observe(t, m, m.Source.Store.Root)
	r, err := m.Prepare(context.Background(), CheckRequest{Tools: []string{"codex-cli"}})
	if err != nil || r.Status != "NEEDS_CHECK" || r.Plan.WriteSupported || r.Plan.Qualification != nil || r.Plan.ID == "" || r.Check.ID == "" {
		t.Fatal(r, err)
	}
	code(t, m.Apply(context.Background(), r.Plan.ID, r.Plan.Digest), "NATIVE_WRITE_UNAVAILABLE")
	if _, err := os.Stat(filepath.Join(m.Root, ".agents")); !os.IsNotExist(err) {
		t.Fatal("preparation created tool targets", err)
	}
	unchanged(t, m, m.Source.Store.Root, before)
}

func TestPrepareRulesInstallsBothToolsOnlyAfterConfirmationAndCanReverse(t *testing.T) {
	m, _ := writableFixture(t)
	saveRules(t, m, []any{"preserve handwritten files"})
	fakeProbes(m, discoveredGuide)
	m.probe.rules = fakeRuleLoad
	m.probe.claudeRules = func(_ context.Context, _ toolBinary, cwd, _ string) ([]string, error) {
		return []string{filepath.Join(cwd, "CLAUDE.md")}, nil
	}
	manual := []byte("# original manual rules\r\n")
	write(t, filepath.Join(m.Root, "AGENTS.md"), manual)
	write(t, filepath.Join(m.Root, ".claude/skills/manual/notes.txt"), []byte("keep"))
	before := observe(t, m, m.Root)
	r, err := m.Prepare(context.Background(), CheckRequest{Tools: []string{"claude-code", "codex-cli"}, Probe: true, RuleProbe: true})
	if err != nil || r.Status != "READY" || !r.Plan.WriteSupported || r.Plan.Qualification.CheckID != r.Check.ID || len(r.Plan.Targets) != 2 {
		t.Fatal(r, err)
	}
	unchanged(t, m, m.Root, before)
	for _, tool := range r.Check.Tools {
		if tool.L3 != "UNKNOWN" || tool.L4 != "UNKNOWN" || tool.Rules.Status != "PASS" {
			t.Fatal("promoted evidence beyond isolated candidates", tool)
		}
	}
	if err = m.Apply(context.Background(), r.Plan.ID, r.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".agents/skills", ".claude/skills"} {
		data, err := os.ReadFile(filepath.Join(m.Root, dir, "guide/assets/data.bin"))
		if err != nil || string(data) != string([]byte{0, 1, 255}) {
			t.Fatal("lost complete package", err)
		}
	}
	next, err := m.Prepare(context.Background(), CheckRequest{Tools: []string{"claude-code", "codex-cli"}, Probe: true, RuleProbe: true})
	if err != nil || next.Status != "READY" {
		t.Fatal(next, err)
	}
	for _, target := range next.Plan.Targets {
		if len(target.Changes) != 0 || len(target.Rule.Changes) != 0 {
			t.Fatal("preparation after apply was not idempotent", target)
		}
	}
	history, err := m.History()
	if err != nil || len(history) != 1 {
		t.Fatal(history, err)
	}
	q, err := m.Reverse(history[0].Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyReverse(context.Background(), q.ID, q.Digest); err != nil {
		t.Fatal(err)
	}
	unchanged(t, m, m.Root, before)
}

func TestPrepareKeepsUsefulPreviewWhenQualificationIsUnavailable(t *testing.T) {
	for _, scenario := range []string{"missing", "version", "rules", "semantics", "collision"} {
		t.Run(scenario, func(t *testing.T) {
			m, id := writableFixture(t)
			fakeProbes(m, discoveredGuide)
			want := "NEEDS_ATTENTION"
			switch scenario {
			case "missing":
				m.probe.resolve = func(string) (toolBinary, error) { return toolBinary{}, fmt.Errorf("not installed") }
			case "version":
				m.probe.version = func(context.Context, toolBinary, string, string, string) (string, error) { return "9.9.9", nil }
			case "rules":
				saveRules(t, m, []any{"rule needs explicit source check"})
			case "semantics":
				applySource(t, m.Source, source.Request{Action: "edit", ID: id, Files: []source.Patch{{Path: "SKILL.md", Content: "---\nname: guide\ndescription: example\nallowed-tools: Read\n---\nbody"}}})
			case "collision":
				write(t, filepath.Join(m.Root, ".agents/skills/guide/SKILL.md"), []byte("unmanaged manual file"))
				want = "BLOCKED"
			}
			before := observe(t, m, m.Root)
			r, err := m.Prepare(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true})
			if err != nil || r.Status != want || r.Plan.WriteSupported || r.Plan.Qualification != nil || r.Plan.ID == "" || len(r.Gates) == 0 {
				t.Fatal(r, err)
			}
			if _, err = m.Diff(context.Background(), r.Plan.ID, r.Plan.Digest); err != nil {
				t.Fatal("diagnostic preview could not be read", err)
			}
			unchanged(t, m, m.Root, before)
		})
	}
}

func TestPreparePropagatesChangesAndCancellation(t *testing.T) {
	m, id := writableFixture(t)
	fakeProbes(m, func(ctx context.Context, b toolBinary, tool, cwd, home string) (discovery, error) {
		applySource(t, m.Source, source.Request{Action: "disable", ID: id})
		return discoveredGuide(ctx, b, tool, cwd, home)
	})
	_, err := m.Prepare(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true})
	code(t, err, "PLAN_STALE")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = m.Prepare(ctx, CheckRequest{Tools: []string{"codex-cli"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation", err)
	}
}
