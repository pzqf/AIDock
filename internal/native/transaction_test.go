package native

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRehearsalUsesCompletePackagesAndNeverChangesBoundProject(t *testing.T) {
	m, _ := fixture(t)
	ctx := context.Background()
	write(t, filepath.Join(m.Root, ".claude/skills/manual/note.txt"), []byte("handwritten"))
	sourceBefore := observe(t, m, m.Source.Store.Root)
	nativeBefore := observe(t, m, filepath.Join(m.Root, ".claude/skills"))
	p, err := m.Plan(ctx, Request{Tools: []string{"claude-code", "codex-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.Rehearse(ctx, p.ID, p.Digest)
	if err != nil || r.Status != "APPLIED" || len(r.Transactions) != 1 {
		t.Fatal(r, err)
	}
	for _, a := range Adapters() {
		root := filepath.Join(r.Root, filepath.FromSlash(a.Directory))
		data, err := os.ReadFile(filepath.Join(root, "guide/assets/data.bin"))
		if err != nil || string(data) != string([]byte{0, 1, 255}) {
			t.Fatal("binary changed", err)
		}
		if _, err = os.Stat(filepath.Join(root, "guide/empty")); err != nil {
			t.Fatal(err)
		}
	}
	q, err := m.RehearsalReverse(r.ID, r.Transactions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	code(t, func() error { _, err := m.RehearsalApply(ctx, r.ID, q.ID, "bad"); return err }(), "PLAN_CONFIRMATION_REQUIRED")
	r, err = m.RehearsalApply(ctx, r.ID, q.ID, q.Digest)
	if err != nil || r.Status != "ROLLED_BACK" {
		t.Fatal(r, err)
	}
	if _, err = os.Stat(filepath.Join(r.Root, ".agents")); !os.IsNotExist(err) {
		t.Fatal("created parent retained")
	}
	if data, err := os.ReadFile(filepath.Join(r.Root, ".claude/skills/manual/note.txt")); err != nil || string(data) != "handwritten" {
		t.Fatal("manual baseline lost")
	}
	q, err = m.RehearsalReverse(r.ID, r.Transactions[0].ID)
	if err != nil || q.Action != "undo-rollback" {
		t.Fatal(q, err)
	}
	r, err = m.RehearsalApply(ctx, r.ID, q.ID, q.Digest)
	if err != nil || r.Status != "UNDONE" {
		t.Fatal(r, err)
	}
	reopened, _ := New(m.Source, m.Root)
	latest, err := reopened.RehearsalLatest()
	if err != nil || latest == nil || latest.ID != r.ID || latest.Status != "UNDONE" {
		t.Fatal(latest, err)
	}
	unchanged(t, m, m.Source.Store.Root, sourceBefore)
	unchanged(t, m, filepath.Join(m.Root, ".claude/skills"), nativeBefore)
	if _, err = os.Stat(filepath.Join(m.Root, ".agents")); !os.IsNotExist(err) {
		t.Fatal("bound target modified")
	}
	history, err := m.History()
	if err != nil || len(history) != 0 {
		t.Fatal("rehearsal leaked to actual history", err)
	}
	code(t, m.Apply(ctx, p.ID, p.Digest), "NATIVE_WRITE_UNAVAILABLE")
}
func TestRehearsalRejectsStalePlansAndForeignSessions(t *testing.T) {
	m, _ := fixture(t)
	ctx := context.Background()
	p, err := m.Plan(ctx, Request{Tools: []string{"codex-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(m.Root, ".agents/skills/manual.txt"), []byte("change"))
	_, err = m.Rehearse(ctx, p.ID, p.Digest)
	code(t, err, "PLAN_STALE")
	p, err = m.Plan(ctx, Request{Tools: []string{"codex-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.Rehearse(ctx, p.ID, p.Digest)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := fixture(t)
	if _, err = other.RehearsalReverse(r.ID, r.Transactions[0].ID); err == nil {
		t.Fatal("foreign session accepted")
	}
	_, err = m.RehearsalApply(ctx, r.ID, "../../outside", "digest")
	code(t, err, "INVALID_INPUT")
	b := m.rehearsalBatch(r.ID)
	js, err := b.History()
	if err != nil {
		t.Fatal(err)
	}
	seed := js[len(js)-1].Plan
	_, err = m.RehearsalApply(ctx, r.ID, seed.ID, seed.Digest)
	code(t, err, "PLAN_KIND_MISMATCH")
	write(t, filepath.Join(r.Root, ".agents/skills/guide/SKILL.md"), []byte("user edit"))
	_, err = m.RehearsalReverse(r.ID, r.Transactions[0].ID)
	code(t, err, "RECOVERY_CONFLICT")
}

func TestPendingOlderRehearsalCannotBeHiddenByNewerSession(t *testing.T) {
	m, _ := fixture(t)
	ctx := context.Background()
	p, err := m.Plan(ctx, Request{Tools: []string{"claude-code", "codex-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.Rehearse(ctx, p.ID, p.Digest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Rehearse(ctx, p.ID, p.Digest)
	if err != nil {
		t.Fatal(err)
	}
	b := m.rehearsalBatch(first.ID)
	q, err := b.Reverse(first.Transactions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("missing interruption")
			}
		}()
		b.Hook = func(stage string, i int) error {
			if stage == "target-complete" && i == 0 {
				panic("interruption")
			}
			return nil
		}
		_, _ = b.Apply(ctx, q.ID, q.Digest)
	}()
	latest, err := m.RehearsalLatest()
	if err != nil || latest == nil || latest.ID != first.ID || !latest.Pending {
		t.Fatal(latest, err)
	}
	_, err = m.Rehearse(ctx, p.ID, p.Digest)
	code(t, err, "NATIVE_RECOVERY_REQUIRED")
	r, err := m.RehearsalRecover(first.ID)
	if err != nil || r.Pending {
		t.Fatal(r, err)
	}
	// Recovery restores the successful apply, so another rollback is available.
	if _, err = m.RehearsalReverse(first.ID, first.Transactions[0].ID); err != nil {
		t.Fatal(err)
	}
	latest, err = m.RehearsalLatest()
	if err != nil || latest.ID != second.ID {
		t.Fatal(latest, err)
	}
}
