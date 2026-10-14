package native

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func writableFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	m, id := fixture(t)
	applySource(t, m.Source, source.Request{Action: "edit", ID: id, Files: []source.Patch{{Path: "SKILL.md", Content: "---\nname: guide\ndescription: example\n---\n[details](references/notes.md)\n"}}})
	return m, id
}
func qualifiedPlan(t *testing.T, m *Manager) Plan {
	t.Helper()
	r, err := m.Check(context.Background(), CheckRequest{Tools: []string{"claude-code", "codex-cli"}, Probe: true})
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Plan(context.Background(), Request{Tools: []string{"claude-code", "codex-cli"}, CheckID: r.ID})
	if err != nil || !p.WriteSupported || p.Qualification == nil {
		t.Fatal(p, err)
	}
	return p
}

func TestNativeQualifiedInstallRollbackUndoAndIdempotence(t *testing.T) {
	m, _ := writableFixture(t)
	ctx := context.Background()
	fakeProbes(m, discoveredGuide)
	write(t, filepath.Join(m.Root, ".claude/skills/manual/notes.txt"), []byte("before"))
	sourceBefore := observe(t, m, m.Source.Store.Root)
	p := qualifiedPlan(t, m)
	if err := m.Apply(ctx, p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	for _, a := range Adapters() {
		b, err := os.ReadFile(filepath.Join(m.Root, filepath.FromSlash(a.Directory), "guide/assets/data.bin"))
		if err != nil || string(b) != string([]byte{0, 1, 255}) {
			t.Fatal(err)
		}
		if _, err = os.Stat(filepath.Join(m.Root, filepath.FromSlash(a.Directory), "guide/empty")); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(m.Root, ".claude/skills/unrelated.txt"), []byte("after"))
	history, err := m.History()
	if err != nil || len(history) != 1 {
		t.Fatal(history, err)
	}
	q, err := m.Reverse(history[0].Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	code(t, func() error { _, err := m.ApplyReverse(ctx, q.ID, "wrong"); return err }(), "PLAN_CONFIRMATION_REQUIRED")
	if _, err = m.ApplyReverse(ctx, q.ID, q.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(m.Root, ".agents")); !os.IsNotExist(err) {
		t.Fatal("rollback retained generated parents", err)
	}
	if b, err := os.ReadFile(filepath.Join(m.Root, ".claude/skills/unrelated.txt")); err != nil || string(b) != "after" {
		t.Fatal("lost handwritten file", err)
	}
	history, _ = m.History()
	q, err = m.Reverse(history[0].Plan.ID)
	if err != nil || q.Action != "undo-rollback" {
		t.Fatal(q, err)
	}
	if _, err = m.ApplyReverse(ctx, q.ID, q.Digest); err != nil {
		t.Fatal(err)
	}
	p = qualifiedPlan(t, m)
	for _, target := range p.Targets {
		if len(target.Changes) != 0 {
			t.Fatal("not idempotent", target.Changes)
		}
	}
	if err = m.Apply(ctx, p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	check, err := m.Check(ctx, CheckRequest{Tools: []string{"claude-code", "codex-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range check.Tools {
		if tool.L1 != "PASS" || tool.L3 != "UNKNOWN" {
			t.Fatal(tool)
		}
	}
	unchanged(t, m, m.Source.Store.Root, sourceBefore)
}

func TestQualificationRejectsStaleUnsupportedAndSemanticConditions(t *testing.T) {
	for _, change := range []string{"static", "expiry", "binary", "version", "source", "selection", "metadata"} {
		t.Run(change, func(t *testing.T) {
			m, id := writableFixture(t)
			fakeProbes(m, discoveredGuide)
			ctx := context.Background()
			if change == "metadata" {
				applySource(t, m.Source, source.Request{Action: "edit", ID: id, Files: []source.Patch{{Path: "SKILL.md", Content: "---\nname: guide\ndescription: example\nallowed-tools: Read\n---\nbody"}}})
			}
			if change == "version" {
				m.probe.version = func(context.Context, toolBinary, string, string, string) (string, error) { return "9.9.9", nil }
			}
			r, err := m.Check(ctx, CheckRequest{Tools: []string{"claude-code", "codex-cli"}, Probe: change != "static"})
			if err != nil {
				t.Fatal(err)
			}
			want := "NATIVE_CHECK_REQUIRED"
			tools := []string{"claude-code", "codex-cli"}
			switch change {
			case "expiry":
				r.Expires = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
				r.Digest = checkDigest(r)
				write(t, filepath.Join(m.checksDir(), "reports", r.ID+".json"), jsonData(r))
				want = "CHECK_STALE"
			case "binary":
				m.probe.resolve = func(id string) (toolBinary, error) { return toolBinary{id + ".exe", strings.Repeat("b", 64)}, nil }
				want = "CHECK_STALE"
			case "source":
				applySource(t, m.Source, source.Request{Action: "disable", ID: id})
				want = "CHECK_STALE"
			case "selection":
				tools = []string{"codex-cli"}
				want = "CHECK_STALE"
			case "metadata":
				want = "NATIVE_SEMANTICS_UNVERIFIED"
			}
			_, err = m.Plan(ctx, Request{Tools: tools, CheckID: r.ID})
			code(t, err, want)
			if _, err = os.Stat(filepath.Join(m.Root, ".agents")); !os.IsNotExist(err) {
				t.Fatal("failed qualification wrote target")
			}
		})
	}
}

func TestQualificationExpiresBeforeApplyAndPreservesExternalEdits(t *testing.T) {
	m, _ := writableFixture(t)
	fakeProbes(m, discoveredGuide)
	ctx := context.Background()
	p := qualifiedPlan(t, m)
	m.probe.resolve = func(id string) (toolBinary, error) { return toolBinary{id + ".exe", strings.Repeat("b", 64)}, nil }
	code(t, m.Apply(ctx, p.ID, p.Digest), "CHECK_STALE")
	fakeProbes(m, discoveredGuide)
	p = qualifiedPlan(t, m)
	if err := m.Apply(ctx, p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	history, _ := m.History()
	write(t, filepath.Join(m.Root, ".agents/skills/guide/SKILL.md"), []byte("user edit"))
	_, err := m.Reverse(history[0].Plan.ID)
	code(t, err, "RECOVERY_CONFLICT")
	if b, _ := os.ReadFile(filepath.Join(m.Root, ".agents/skills/guide/SKILL.md")); string(b) != "user edit" {
		t.Fatal("overwrote external edit")
	}
}

func TestLocalQualifiedNativeIntegration(t *testing.T) {
	if os.Getenv("AIDOCK_NATIVE_PROBE_TEST") != "1" {
		t.Skip("opt-in installed CLI acceptance on temporary project")
	}
	m, _ := writableFixture(t)
	ctx := context.Background()
	before := observe(t, m, m.Source.Store.Root)
	p := qualifiedPlan(t, m)
	if err := m.Apply(ctx, p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	r, err := m.Check(ctx, CheckRequest{Tools: []string{"claude-code", "codex-cli"}, Probe: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range r.Tools {
		if tool.L1 != "PASS" || tool.L2 != "PASS" || tool.L3 != "UNKNOWN" {
			t.Fatal(tool)
		}
		t.Logf("%s %s: installed full package PASS, fresh isolated discovery PASS, L3 UNKNOWN", tool.Tool, tool.Version)
	}
	h, err := m.History()
	if err != nil {
		t.Fatal(err)
	}
	q, err := m.Reverse(h[0].Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyReverse(ctx, q.ID, q.Digest); err != nil {
		t.Fatal(err)
	}
	h, _ = m.History()
	q, err = m.Reverse(h[0].Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyReverse(ctx, q.ID, q.Digest); err != nil {
		t.Fatal(err)
	}
	unchanged(t, m, m.Source.Store.Root, before)
	for _, a := range Adapters() {
		tree := observe(t, m, filepath.Join(m.Root, filepath.FromSlash(a.Directory)))
		if tree.Entries["guide/assets/data.bin"].Hash != workstore.Hash([]byte{0, 1, 255}) {
			t.Fatal("binary changed")
		}
	}
}
