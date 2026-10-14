package native

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/source"
)

func TestInspectDraftUsesImmutableCandidateAndDoesNotGrantInstall(t *testing.T) {
	m, id := writableFixture(t)
	ctx := context.Background()
	p, err := m.compile(ctx, Request{Tools: []string{"claude-code", "codex-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	applySource(t, m.Source, source.Request{Action: "edit", ID: id, Files: []source.Patch{{Path: "SKILL.md", Content: "---\nname: guide\ndescription: saved source changed\n---\nnew body\n"}}})
	before := observe(t, m, m.Source.Store.Root)
	calls := 0
	fakeProbes(m, func(ctx context.Context, b toolBinary, tool, project, home string) (discovery, error) {
		calls++
		dir := ".agents/skills"
		if tool == "claude-code" {
			dir = ".claude/skills"
		}
		pkg := filepath.Join(project, filepath.FromSlash(dir), "guide")
		bytes, err := os.ReadFile(filepath.Join(pkg, "SKILL.md"))
		if err != nil || !strings.Contains(string(bytes), "description: example") || strings.Contains(string(bytes), "saved source changed") {
			t.Fatal("probe used saved source", err)
		}
		bytes, err = os.ReadFile(filepath.Join(pkg, "assets/data.bin"))
		if err != nil || string(bytes) != string([]byte{0, 1, 255}) {
			t.Fatal("candidate binary changed", err)
		}
		if st, err := os.Stat(filepath.Join(pkg, "empty")); err != nil || !st.IsDir() {
			t.Fatal("empty directory lost", err)
		}
		return discoveredGuide(ctx, b, tool, project, home)
	})
	r, err := m.InspectDraft(ctx, p.Targets, p.Source.Snapshot, true)
	if err != nil || calls != 2 || r.SourceSnapshot != p.Source.Snapshot {
		t.Fatal(r, err, calls)
	}
	for _, tool := range r.Tools {
		if tool.L2 != "PASS" || tool.L3 != "UNKNOWN" || tool.L4 != "UNKNOWN" {
			t.Fatal(tool)
		}
	}
	unchanged(t, m, m.Source.Store.Root, before)
	_, err = m.Plan(ctx, Request{Tools: []string{"claude-code", "codex-cli"}, CheckID: r.ID})
	code(t, err, "CHECK_NOT_FOUND")
	if _, err = os.Stat(m.checksDir()); !os.IsNotExist(err) {
		t.Fatal("draft check saved native qualification", err)
	}
	if err = m.InspectionFresh(r); err != nil {
		t.Fatal(err)
	}
	r.Expires = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	r.Digest = checkDigest(r)
	code(t, m.InspectionFresh(r), "CHECK_STALE")
}

func TestInspectDraftStaticAndCompilerPathGuards(t *testing.T) {
	m, _ := writableFixture(t)
	ctx := context.Background()
	p, err := m.compile(ctx, Request{Tools: []string{"codex-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	fakeProbes(m, func(context.Context, toolBinary, string, string, string) (discovery, error) {
		t.Fatal("static discovery executed")
		return discovery{}, nil
	})
	m.probe.version = func(context.Context, toolBinary, string, string, string) (string, error) {
		t.Fatal("static version executed")
		return "", nil
	}
	r, err := m.InspectDraft(ctx, p.Targets, p.Source.Snapshot, false)
	if err != nil || r.Probe || r.Tools[0].L2 != "UNKNOWN" {
		t.Fatal(r, err)
	}
	m.probe.resolve = func(string) (toolBinary, error) { return toolBinary{}, fmt.Errorf("uninstalled") }
	code(t, m.InspectionFresh(r), "CHECK_STALE")
	p.Targets[0].Packages[0].Name = "../../outside"
	_, err = m.InspectDraft(ctx, p.Targets, p.Source.Snapshot, true)
	code(t, err, "GENERATOR_MANIFEST_INVALID")
}
