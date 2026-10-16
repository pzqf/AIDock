package native

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func enableScopeMCP(t *testing.T, m *Manager) {
	t.Helper()
	b, err := config.ReadFile(filepath.Join("..", "config", "testdata", "mcp.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := config.Validate("mcp", b)
	if err != nil {
		t.Fatal(err)
	}
	conf := v.(map[string]any)
	conf["servers"].([]any)[0].(map[string]any)["enabled"] = true
	state, err := m.Source.MCP()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Source.MCPPlan(context.Background(), source.MCPEdit{ExpectedSHA256: state.SHA256, Config: conf})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
}

func TestApplicationScopeKeepsMCPBytesThroughInstallRollbackUndo(t *testing.T) {
	m, _ := writableFixture(t)
	enableScopeMCP(t, m)
	saveRules(t, m, []any{"preserve manual content"})
	fakeProbes(m, discoveredGuide)
	m.probe.rules = fakeRuleLoad
	m.probe.claudeRules = func(_ context.Context, _ toolBinary, cwd, _ string) ([]string, error) {
		return []string{filepath.Join(cwd, "CLAUDE.md")}, nil
	}
	originals := map[string][]byte{
		".mcp.json":                  []byte("{\r\n  \"mcpServers\": {\"manual\":{\"command\":\"keep\"}}\r\n}\r\n"),
		".codex/config.toml":         []byte("# manual\r\nmodel = \"keep\"\r\n[mcp_servers.manual]\r\ncommand = \"keep\"\r\n"),
		".aidock-mcp-ownership.json": []byte("unrelated fixture sidecar"),
	}
	for path, b := range originals {
		write(t, filepath.Join(m.Root, path), b)
	}
	assertMCP := func() {
		t.Helper()
		for path, b := range originals {
			got, err := os.ReadFile(filepath.Join(m.Root, path))
			if err != nil || !bytes.Equal(b, got) {
				t.Fatal("MCP bytes changed", path, err)
			}
		}
	}
	ctx := context.Background()
	full, err := m.Prepare(ctx, CheckRequest{Tools: []string{"claude-code", "codex-cli"}, Probe: true, RuleProbe: true})
	if err != nil || full.Status != "BLOCKED" || !has(full.Plan, "MCP_EXECUTION_PENDING") {
		t.Fatal(full, err)
	}
	if len(full.Gates) < 2 || full.Gates[1].Code != "MCP_APPLICATION_SEPARATE" {
		t.Fatal(full.Gates)
	}
	before := observe(t, m, m.Root)
	req := CheckRequest{Tools: []string{"claude-code", "codex-cli"}, ApplicationScope: SkillsRulesScope, Probe: true, RuleProbe: true}
	r, err := m.Prepare(ctx, req)
	if err != nil || r.Status != "READY" || r.Plan.ApplicationScope != SkillsRulesScope || r.Check.ApplicationScope != SkillsRulesScope || r.Plan.Qualification.ApplicationScope != SkillsRulesScope || len(r.Plan.ExcludedChecks) == 0 {
		t.Fatal(r, err)
	}
	for _, f := range r.Plan.ExcludedChecks {
		if f.Component != "mcp" {
			t.Fatal("excluded other component", f)
		}
	}
	if has(r.Plan, "MCP_EXECUTION_PENDING") {
		t.Fatal("MCP check still gates content application")
	}
	originalExport, err := m.Source.Export()
	if err != nil || workstore.Hash(jsonData(originalExport)) != workstore.Hash(jsonData(r.Plan.Source)) {
		t.Fatal("plan changed its authoritative source export", err)
	}
	unchanged(t, m, m.Root, before)
	last, err := m.Latest(ctx)
	if err != nil || last == nil || last.Stale || last.ID != r.Check.ID || last.ApplicationScope != SkillsRulesScope || len(last.ExcludedChecks) != len(r.Check.ExcludedChecks) {
		t.Fatal(last, err)
	}
	if err = m.Apply(ctx, r.Plan.ID, r.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	assertMCP()
	for _, dir := range []string{".agents/skills", ".claude/skills"} {
		b, err := os.ReadFile(filepath.Join(m.Root, dir, "guide/assets/data.bin"))
		if err != nil || !bytes.Equal(b, []byte{0, 1, 255}) {
			t.Fatal(err)
		}
	}
	next, err := m.Prepare(ctx, req)
	if err != nil || next.Status != "READY" {
		t.Fatal(next, err)
	}
	for _, target := range next.Plan.Targets {
		if len(target.Changes) > 0 || target.Rule == nil || len(target.Rule.Changes) > 0 {
			t.Fatal("not idempotent", target)
		}
	}
	history, err := m.History()
	if err != nil || len(history) != 1 {
		t.Fatal(history, err)
	}
	reverse, err := m.Reverse(history[0].Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	rollback, err := m.ApplyReverse(ctx, reverse.ID, reverse.Digest)
	if err != nil {
		t.Fatal(err)
	}
	unchanged(t, m, m.Root, before)
	assertMCP()
	undo, err := m.Reverse(rollback.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyReverse(ctx, undo.ID, undo.Digest); err != nil {
		t.Fatal(err)
	}
	assertMCP()
	if _, err = os.Stat(filepath.Join(m.Root, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
}

func TestApplicationScopeEvidenceCannotCrossScopeAndFullIsDefault(t *testing.T) {
	m, _ := writableFixture(t)
	fakeProbes(m, discoveredGuide)
	ctx := context.Background()
	scoped, err := m.Check(ctx, CheckRequest{Tools: []string{"codex-cli"}, Probe: true, ApplicationScope: SkillsRulesScope})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Plan(ctx, Request{Tools: []string{"codex-cli"}, CheckID: scoped.ID})
	code(t, err, "CHECK_STALE")
	full, err := m.Check(ctx, CheckRequest{Tools: []string{"codex-cli"}, Probe: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Plan(ctx, Request{Tools: []string{"codex-cli"}, CheckID: full.ID, ApplicationScope: SkillsRulesScope})
	code(t, err, "CHECK_STALE")
	p, err := m.Plan(ctx, Request{Tools: []string{"codex-cli"}, CheckID: full.ID, ApplicationScope: "full"})
	if err != nil || !p.WriteSupported || p.ApplicationScope != "" {
		t.Fatal(p, err)
	}
	for _, scope := range []string{"mcp", "skills", "SKILLS-RULES", " skills-rules", "none"} {
		_, err = m.Prepare(ctx, CheckRequest{Tools: []string{"codex-cli"}, ApplicationScope: scope})
		code(t, err, "INVALID_INPUT")
	}
}

func TestApplicationScopeStillExpiresOnMCPSourceChanges(t *testing.T) {
	m, _ := writableFixture(t)
	enableScopeMCP(t, m)
	fakeProbes(m, discoveredGuide)
	ctx := context.Background()
	r, err := m.Prepare(ctx, CheckRequest{Tools: []string{"codex-cli"}, Probe: true, ApplicationScope: SkillsRulesScope})
	if err != nil || r.Status != "READY" {
		t.Fatal(r, err)
	}
	state, err := m.Source.MCP()
	if err != nil {
		t.Fatal(err)
	}
	// Use the saved definition and normal source transaction, not tool output.
	b, err := os.ReadFile(filepath.Join(m.Source.Store.Root, "mcp.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var conf map[string]any
	if err = json.Unmarshal(b, &conf); err != nil {
		t.Fatal(err)
	}
	conf["servers"].([]any)[0].(map[string]any)["enabled"] = false
	p, err := m.Source.MCPPlan(ctx, source.MCPEdit{ExpectedSHA256: state.SHA256, Config: conf})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.ApplyMCP(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	err = m.Apply(ctx, r.Plan.ID, r.Plan.Digest)
	code(t, err, "CHECK_STALE")
	last, err := m.Latest(ctx)
	if err != nil || last == nil || !last.Stale {
		t.Fatal(last, err)
	}
	if _, err = os.Stat(filepath.Join(m.Root, ".agents")); !os.IsNotExist(err) {
		t.Fatal("stale plan wrote targets", err)
	}
}

func TestApplicationScopeDoesNotWaiveSkillAndProfileGates(t *testing.T) {
	for _, scenario := range []string{"collision", "metadata", "profile", "dependency", "requirements", "settings"} {
		t.Run(scenario, func(t *testing.T) {
			m, id := writableFixture(t)
			enableScopeMCP(t, m)
			fakeProbes(m, discoveredGuide)
			want := "BLOCKED"
			switch scenario {
			case "collision":
				write(t, filepath.Join(m.Root, ".agents/skills/guide/SKILL.md"), []byte("manual"))
			case "metadata":
				applySource(t, m.Source, source.Request{Action: "edit", ID: id, Files: []source.Patch{{Path: "SKILL.md", Content: "---\nname: guide\ndescription: example\nallowed-tools: Read\n---\nbody"}}})
				want = "NEEDS_ATTENTION"
			case "dependency", "requirements":
				bundle, err := m.Source.Export()
				if err != nil {
					t.Fatal(err)
				}
				g := &source.Governance{Dependencies: map[string]any{}, Compatibility: map[string]any{}, Requirements: map[string]any{}}
				if scenario == "dependency" {
					g.Dependencies["plugins"] = []any{map[string]any{"id": "example", "lock_digest": "sha256:" + string(bytes.Repeat([]byte("a"), 64)), "required": true}}
				} else {
					g.Requirements["exec"] = true
				}
				applySource(t, m.Source, source.Request{Action: "governance", ID: id, Revision: bundle.Packages[0].Skill.Revision, Governance: g})
				want = "NEEDS_ATTENTION"
			case "settings":
				profile, err := m.Source.Profile()
				if err != nil {
					t.Fatal(err)
				}
				profile.Profile["config_overrides"] = map[string]any{"model": "uncompiled"}
				p, err := m.Source.ProfilePlan(context.Background(), source.ProfileEdit{ExpectedSHA256: profile.SHA256, Profile: profile.Profile})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = m.Source.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
					t.Fatal(err)
				}
			case "profile":
				saveRules(t, m, []any{"rule"}) // Without rule-probe, qualification must still fail.
				want = "NEEDS_ATTENTION"
			}
			before := observe(t, m, m.Root)
			r, err := m.Prepare(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true, ApplicationScope: SkillsRulesScope})
			if err != nil || r.Status != want || r.Plan.WriteSupported {
				t.Fatal(r, err)
			}
			unchanged(t, m, m.Root, before)
		})
	}
	// An MCP-looking code is not an exclusion; only source-owned component marking is.
	bundle := source.Export{Preflight: source.Preflight{ProfileChecks: []inventory.Finding{{Status: "FAIL", Code: "MCP_GENERIC_PROFILE_FAILURE"}, {Status: "FAIL", Code: "MCP_REAL", Component: "mcp"}}}}
	projected, excluded := applicationBundle(bundle, SkillsRulesScope)
	if len(projected.Preflight.ProfileChecks) != 1 || len(excluded) != 1 || projected.Preflight.ProfileChecks[0].Code != "MCP_GENERIC_PROFILE_FAILURE" || len(bundle.Preflight.ProfileChecks) != 2 {
		t.Fatal(projected, excluded)
	}
}
