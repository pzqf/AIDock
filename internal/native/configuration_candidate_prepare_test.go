package native

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func nativeConfigurationFixture(t *testing.T) (*Manager, source.ConfigurationCandidate, string, string) {
	t.Helper()
	m, _ := writableFixture(t)
	m.Source.Copy = workstore.ID()
	write(t, filepath.Join(m.Source.Store.Root, "project.yaml"), jsonData(map[string]any{"schema_version": 1, "id": m.Source.Owner, "name": "owned future target"}))
	saveSnapshotLocal(t, m, "TARGET_LOCAL_PRIVATE")
	state, err := m.Source.Profile()
	if err != nil {
		t.Fatal(err)
	}
	state.Profile["rules"] = []any{"FUTURE_PRIVATE_RULE"}
	p, err := m.Source.ProfilePlan(context.Background(), source.ProfileEdit{ExpectedSHA256: state.SHA256, Profile: state.Profile})
	if err != nil {
		t.Fatal(err)
	}
	c, err := m.Source.DraftConfigurationCandidate(context.Background(), p.Plan.Before, p.Plan.After, state.Layers.Digest)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(m.Root, "AGENTS.md"), []byte("# NATIVE_PRIVATE_SENTINEL\r\nKeep exact current native bytes.\r\n"))
	write(t, filepath.Join(m.Root, ".codex/config.toml"), []byte("model='MCP_NATIVE_PRIVATE_SENTINEL'\r\n"))
	executable := filepath.Join(t.TempDir(), "aidock.exe")
	write(t, executable, []byte("unexecuted fixed aidock test host"))
	return m, c, executable, filepath.Dir(m.Source.Store.Local)
}

func TestConfigurationPreparationRetainsWholeEnabledSetWhenMCPDependencyMissing(t *testing.T) {
	m, _, executable, home := nativeConfigurationFixture(t)
	ctx := context.Background()
	raw, err := config.ReadFile(filepath.Join("..", "config", "testdata", "mcp.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := config.Validate("mcp", raw)
	if err != nil {
		t.Fatal(err)
	}
	conf := v.(map[string]any)
	first := conf["servers"].([]any)[0].(map[string]any)
	first["enabled"] = true
	second := map[string]any{}
	for k, v := range first {
		second[k] = v
	}
	second["name"] = "second-service"
	conf["servers"] = []any{first, second}
	state, err := m.Source.MCP()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Source.MCPPlan(ctx, source.MCPEdit{ExpectedSHA256: state.SHA256, Config: conf})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := m.Source.Profile()
	if err != nil {
		t.Fatal(err)
	}
	c, err := m.Source.DraftConfigurationCandidate(ctx, p.Plan.Before, p.Plan.After, profile.Layers.Digest)
	if err != nil {
		t.Fatal(err)
	}
	beforeSource, beforeLocal := snapshotDraftTrees(t, m)
	beforeNative := snapshotNativeTrees(t, m)
	fakeProbes(m, discoveredGuide)
	m.probe.rules = fakeRuleLoad
	r, err := m.PrepareConfigurationCandidate(ctx, c, CheckRequest{Tools: []string{"codex-cli"}, Probe: true, RuleProbe: true}, executable, home)
	if err != nil || r.Status != "NEEDS_ATTENTION" || r.MCPReady || r.CanApply || len(r.MCP) != 2 || r.MCP[0] != "fixture" || r.MCP[1] != "second-service" || len(r.Gates) == 0 {
		t.Fatal("missing MCP silently omitted from full checked set", r.Status, r.MCP, r.Gates, err)
	}
	for _, tool := range r.Check.Tools {
		if tool.L2 != "PASS" {
			t.Fatal("fixture did not independently pass skill discovery")
		}
	}
	read, err := m.ReadConfigurationPreparation(ctx, c, r.ID)
	if err != nil || !read.Fresh || read.Status != "NEEDS_ATTENTION" || read.MCPReady {
		t.Fatal("unchanged blocked observation was lost", read.Status, err)
	}
	s, l := snapshotDraftTrees(t, m)
	if workstore.Digest(s) != workstore.Digest(beforeSource) || workstore.Digest(l) != workstore.Digest(beforeLocal) || !reflectTreesEqual(beforeNative, snapshotNativeTrees(t, m)) {
		t.Fatal("blocked full preparation modified current source, Local or native files")
	}
	write(t, filepath.Join(m.Root, ".codex/config.toml"), []byte("[mcp_servers.manual]\ncommand='keep'\n"))
	read, err = m.ReadConfigurationPreparation(ctx, c, r.ID)
	if err != nil || read.Fresh || read.Status != "STALE" {
		t.Fatal("blocked dependency hid native drift", read.Status, err)
	}
}

func TestConfigurationPreparationStaticExplicitProbesPrivateMaterialAndStaleRead(t *testing.T) {
	m, c, executable, home := nativeConfigurationFixture(t)
	ctx := context.Background()
	sourceBefore, localBefore := snapshotDraftTrees(t, m)
	nativeBefore := snapshotNativeTrees(t, m)
	fakeProbes(m, discoveredGuide)
	m.probe.version = func(context.Context, toolBinary, string, string, string) (string, error) {
		t.Fatal("static full candidate launched a process")
		return "", nil
	}
	req := CheckRequest{Tools: []string{"codex-cli"}}
	static, err := m.PrepareConfigurationCandidate(ctx, c, req, executable, home)
	if err != nil || static.Status != "NEEDS_CHECK" || !static.Fresh || !static.MCPReady || static.CanApply || static.Check.Probe {
		t.Fatal(static.Status, static.Gates, err)
	}
	if strings.Contains(string(jsonData(static)), "PRIVATE_") || strings.Contains(string(jsonData(static)), "PRIVATE_SENTINEL") {
		t.Fatal("safe candidate report included source/Local/native body")
	}
	saved, err := m.loadConfigurationPreparation(static.ID)
	if err != nil {
		t.Fatal(err)
	}
	rule := saved.Native.Targets[0].Rule
	if rule == nil || !strings.Contains(rule.Managed, "FUTURE_PRIVATE_RULE") {
		t.Fatal("did not compile future rules")
	}
	entry := rule.Before.Entries[workstore.FileContent]
	if _, err = m.Source.Store.Data(entry); err == nil {
		t.Fatal("native before-image entered normal source blobs")
	}
	if body, err := m.connectionStore().Data(entry); err != nil || !strings.Contains(string(body), "NATIVE_PRIVATE_SENTINEL") {
		t.Fatal("private native backup missing", err)
	}
	mcpEntry := saved.MCPInputs[".codex/config.toml"].Entries[workstore.FileContent]
	if _, err = m.Source.Store.Data(mcpEntry); err == nil {
		t.Fatal("native MCP before-image entered ordinary source blobs")
	}
	if body, err := m.connectionStore().Data(mcpEntry); err != nil || !strings.Contains(string(body), "MCP_NATIVE_PRIVATE_SENTINEL") {
		t.Fatal("private MCP observation missing", err)
	}
	fakeProbes(m, discoveredGuide)
	m.probe.rules = fakeRuleLoad
	req.Probe, req.RuleProbe = true, true
	checked, err := m.PrepareConfigurationCandidate(ctx, c, req, executable, home)
	if err != nil || checked.Status != "CHECKED" || !checked.MCPReady || checked.CanApply || !checked.Check.Probe || !checked.Check.RuleProbe {
		t.Fatal(checked.Status, checked.Gates, err)
	}
	read, err := m.ReadConfigurationPreparation(ctx, c, checked.ID)
	if err != nil || !read.Fresh || read.Status != "CHECKED" {
		t.Fatal(read.Status, read.Gates, err)
	}
	if err := m.Apply(ctx, checked.ID, checked.Digest); err == nil {
		t.Fatal("ordinary apply consumed candidate preparation")
	}
	s, l := snapshotDraftTrees(t, m)
	if workstore.Digest(s) != workstore.Digest(sourceBefore) || workstore.Digest(l) != workstore.Digest(localBefore) || !reflectTreesEqual(nativeBefore, snapshotNativeTrees(t, m)) {
		t.Fatal("candidate preparation changed live source, Local or native files")
	}
	write(t, filepath.Join(m.Root, "AGENTS.md"), []byte("retain late manual edit"))
	read, err = m.ReadConfigurationPreparation(ctx, c, checked.ID)
	if err != nil || read.Fresh || read.Status != "STALE" || read.MCPReady || len(read.Changes) != 0 || !read.Check.Stale {
		t.Fatal("stale native file retained usable evidence", read.Status, err)
	}
}

func TestConfigurationPreparationCannotForgeSavedNativeMaterialOrForeignBinding(t *testing.T) {
	m, c, executable, home := nativeConfigurationFixture(t)
	ctx := context.Background()
	r, err := m.PrepareConfigurationCandidate(ctx, c, CheckRequest{Tools: []string{"codex-cli"}}, executable, home)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.loadConfigurationPreparation(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.connectionStore().PutFile(&p.Native.Targets[0].After, "guide/foreign.txt", []byte("unchosen source bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	p.Digest = configurationPreparedDigest(p)
	p.Report.Digest = p.Digest
	if err := save(m.configurationPreparedFile(p.Report.ID), jsonData(p)); err != nil {
		t.Fatal(err)
	}
	_, err = m.ReadConfigurationPreparation(ctx, c, r.ID)
	code(t, err, "CHECK_CORRUPT")
	other := *m
	otherSource := *m.Source
	otherSource.Copy = workstore.ID()
	other.Source = &otherSource
	_, err = other.PrepareConfigurationCandidate(ctx, c, CheckRequest{Tools: []string{"codex-cli"}}, executable, home)
	code(t, err, "PLAN_STALE")
	_, err = m.PrepareConfigurationCandidate(ctx, c, CheckRequest{Tools: []string{"codex-cli"}, ApplicationScope: SkillsRulesScope}, executable, home)
	code(t, err, "INVALID_INPUT")
}
