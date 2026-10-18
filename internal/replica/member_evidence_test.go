package replica

import (
	"context"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func TestMemberEvidenceRejectsUncommittedForeignAndUnqualifiedRecords(t *testing.T) {
	m, p := storedFullPublicationFixture(t)
	ctx := context.Background()
	for _, ids := range [][]string{{"invalid"}, {p.ID, p.ID}} {
		rows, err := m.MemberPublicationChecks(ctx, ids)
		code(t, err, "INVALID_INPUT")
		if rows != nil {
			t.Fatal("invalid request returned partial history")
		}
	}
	_, err := m.MemberPublicationChecks(ctx, []string{p.ID})
	code(t, err, "PUBLICATION_COMMIT_REQUIRED")
	// Commit unqualified kernel images solely to test proof and member roles.
	// Public apply still refuses this deliberately fake discovery reference.
	c, err := m.fullPublicationCoordinator(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(ctx, p.Proof.Transaction.ID, p.Proof.Transaction.Digest); err != nil {
		t.Fatal(err)
	}
	_, err = m.MemberPublicationChecks(ctx, []string{p.ID})
	code(t, err, "PUBLICATION_MEMBER_MISMATCH")
	target, err := m.copySource(p.Resolution.Targets[0].Copy)
	if err != nil {
		t.Fatal(err)
	}
	member, err := New(target)
	if err != nil {
		t.Fatal(err)
	}
	before, err := m.Control.Capture(m.Control.Root)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := member.MemberPublicationChecks(ctx, []string{p.ID})
	if err == nil || rows != nil {
		t.Fatal("committed bytes manufactured a missing discovery record", rows, err)
	}
	after, err := m.Control.Capture(m.Control.Root)
	if err != nil || workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("member history read changed common control", err)
	}
}
