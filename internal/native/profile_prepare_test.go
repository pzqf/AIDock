package native

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func TestProfilePreparationStaticCannotProbeAndKeepsMCPGate(t *testing.T) {
	m, _ := writableFixture(t)
	m.Source.Copy = "22222222-2222-4222-8222-222222222222"
	enableScopeMCP(t, m)
	fakeProbes(m, func(context.Context, toolBinary, string, string, string) (discovery, error) {
		t.Fatal("static preparation started discovery")
		return discovery{}, nil
	})
	m.probe.version = func(context.Context, toolBinary, string, string, string) (string, error) {
		t.Fatal("static preparation started a version probe")
		return "", nil
	}
	executable := filepath.Join(t.TempDir(), "aidock.exe")
	write(t, executable, []byte("unexecuted test program"))
	before := configurationTree(t, m.Root)
	r, err := m.PrepareProfile(context.Background(), CheckRequest{Tools: []string{"codex-cli"}}, executable, filepath.Dir(m.Source.Store.Local))
	if err != nil || r.Plan != nil || r.MCP.Status != "NEEDS_ATTENTION" || r.Status != "NEEDS_ATTENTION" || r.Check.Probe {
		t.Fatal(r.Status, r.MCP.Status, err)
	}
	if !reflectTreesEqual(before, configurationTree(t, m.Root)) {
		t.Fatal("static preparation wrote native files")
	}
}

func reflectTreesEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestProfilePreparationMainApplyDiffHistoryAndReversal(t *testing.T) {
	m, _ := writableFixture(t)
	m.Source.Copy = "22222222-2222-4222-8222-222222222222"
	fakeProbes(m, discoveredGuide)
	executable := filepath.Join(t.TempDir(), "aidock.exe")
	write(t, executable, []byte("fixed unexecuted test host"))
	before := configurationTree(t, m.Root)
	r, err := m.PrepareProfile(context.Background(), CheckRequest{Tools: []string{"claude-code", "codex-cli"}, Probe: true}, executable, filepath.Dir(m.Source.Store.Local))
	if err != nil || r.Status != "READY" || r.Plan == nil {
		t.Fatal(r.Status, err)
	}
	diff, err := m.DiffApplication(context.Background(), r.Plan.ID, r.Plan.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := diff.(ProfileApplicationPlan); !ok {
		t.Fatal("main diff returned wrong plan kind")
	}
	if err = m.ApplyApplication(context.Background(), r.Plan.ID, r.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	h, err := m.ApplicationHistory()
	if err != nil || len(h) != 1 || h[0].ApplicationKind != ProfileApplicationKind || h[0].State != "committed" {
		t.Fatal(h, err)
	}
	p, err := m.ReverseApplication(context.Background(), h[0].Plan.ID)
	if err != nil || p.ApplicationKind != ProfileApplicationKind {
		t.Fatal(p.ApplicationKind, err)
	}
	if _, err = m.ApplyApplicationReverse(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	if !reflectTreesEqual(before, configurationTree(t, m.Root)) {
		t.Fatal("main reversal did not restore all bytes")
	}
	if n, err := m.RecoverApplication(); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	for _, name := range []string{".mcp.json", ".codex/config.toml"} {
		if _, err := os.Stat(filepath.Join(m.Root, name)); !os.IsNotExist(err) {
			t.Fatal("empty MCP created native file", name)
		}
	}
	_, err = m.PrepareProfile(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, ApplicationScope: SkillsRulesScope}, executable, filepath.Dir(m.Source.Store.Local))
	code(t, err, "INVALID_INPUT")
}

func TestProfilePreparationPreservesUnsupportedFieldsAndInvalidation(t *testing.T) {
	m, _ := writableFixture(t)
	m.Source.Copy = "22222222-2222-4222-8222-222222222222"
	fakeProbes(m, discoveredGuide)
	executable := filepath.Join(t.TempDir(), "aidock.exe")
	write(t, executable, []byte("fixed unexecuted test host"))
	s, err := m.Source.Profile()
	if err != nil {
		t.Fatal(err)
	}
	s.Profile["config_overrides"] = map[string]any{"unknown_setting": "must-not-be-ignored"}
	p, err := m.Source.ProfilePlan(context.Background(), source.ProfileEdit{ExpectedSHA256: s.SHA256, Profile: s.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Source.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	r, err := m.PrepareProfile(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true}, executable, filepath.Dir(m.Source.Store.Local))
	if err != nil || r.Status != "BLOCKED" || r.Plan != nil {
		t.Fatal(r.Status, err)
	}
	if !has(r.NativePlan, "PROFILE_CONFIGURATION_UNKNOWN_PLUGIN") {
		t.Fatal("unsupported field silently omitted")
	}
}

func TestProfilePreparationRequestRejectsAmbiguousProbeWithoutEcho(t *testing.T) {
	for _, raw := range []string{`null`, `{"probe":true,"probe":false}`, `{"Probe":true}`, `{"probe":null}`, `{"tools":null}`, `{"command":"PRIVATE_PREPARE_INPUT"}`, `{"rule_resolutions":[{"Tool":"codex-cli"}]}`} {
		_, err := ParseProfilePreparationRequest([]byte(raw))
		if err == nil {
			t.Fatal("ambiguous preparation accepted", raw)
		}
	}
}
