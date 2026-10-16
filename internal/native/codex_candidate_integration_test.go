package native

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Explicit compatibility admission experiment. It does not use or modify the
// supported-version list, qualify a plan, install files or send a model turn.
func TestCodexProtocolCandidateIntegration(t *testing.T) {
	version := os.Getenv("AIDOCK_NATIVE_CANDIDATE_VERSION")
	if version == "" {
		t.Skip("opt-in exact-version protocol admission")
	}
	m, _ := writableFixture(t)
	ctx := context.Background()
	b, err := resolveBinary("codex-cli")
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.compile(ctx, Request{Tools: []string{"codex-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	project, home := filepath.Join(base, "project"), filepath.Join(base, "home")
	if err = m.prepareProbe(project, home, p.Targets[0]); err != nil {
		t.Fatal(err)
	}
	observed, err := toolVersion(ctx, b, "codex-cli", project, home)
	if err != nil || observed != version {
		t.Fatal("candidate binary version", observed, err)
	}
	d, err := probeProcess(ctx, b, "codex-cli", project, home)
	if err != nil || d.Errors != 0 {
		t.Fatal("candidate discovery", d.Errors, err)
	}
	found := false
	for _, skill := range d.Skills {
		if skill.Name == "guide" && skill.Enabled && samePath(skill.Path, filepath.Join(project, ".agents/skills/guide/SKILL.md")) {
			found = true
		}
	}
	if !found {
		t.Fatal("candidate did not discover exact fixture package")
	}
	for _, kind := range []string{"present", "absent", "override"} {
		base := t.TempDir()
		cwd, home := filepath.Join(base, "project"), filepath.Join(base, "home")
		if err = m.prepareProbe(cwd, home, Target{Adapter: Adapters()[1]}); err != nil {
			t.Fatal(err)
		}
		if kind != "absent" {
			write(t, filepath.Join(cwd, "AGENTS.md"), []byte("# Disposable candidate instructions\n"))
		}
		expected := filepath.Join(cwd, "AGENTS.md")
		if kind == "override" {
			expected = filepath.Join(cwd, "AGENTS.override.md")
			write(t, expected, []byte("# Override fixture\n"))
		}
		sources, err := probeRuleProcess(ctx, b, cwd, home)
		if err != nil {
			t.Fatal(kind, err)
		}
		if kind == "absent" {
			if len(sources) != 0 {
				t.Fatal("absent negative control")
			}
		} else if len(sources) != 1 || !samePath(sources[0], expected) {
			t.Fatal(kind, "instruction source mismatch")
		}
	}
	t.Logf("Codex %s (%s): real skills/list and ephemeral instructionSources PASS; absent/override controls PASS; no turn/start or model request", version, b.Digest)
}
