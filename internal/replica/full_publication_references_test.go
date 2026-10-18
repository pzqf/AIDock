package replica

import (
	"context"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func TestFullPublicationCommitPinsPublisherAndTargetRemovalAndRetainsArchive(t *testing.T) {
	m, p := storedFullPublicationFixture(t)
	c, err := m.fullPublicationCoordinator(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range append([]Binding{p.Resolution.Source}, fullPublicationBindings(p)...) {
		refs, err := m.fullPublicationReferences(b)
		if err != nil || len(refs) != 0 {
			t.Fatal("unexecuted preview pinned binding", refs, err)
		}
	}
	// Deliberately unqualified kernel fixture, not owner publication acceptance.
	if _, err = c.Apply(context.Background(), p.Proof.Transaction.ID, p.Proof.Transaction.Digest); err != nil {
		t.Fatal(err)
	}
	shown, err := m.ReadFullPublication(context.Background(), p.ID, p.Digest)
	if err != nil || shown.Status != "APPLIED" || shown.Execution == nil || shown.Fresh || shown.CanApply || len(shown.Execution.LatestMembers) != len(p.Proof.Members) {
		t.Fatal("actual committed history was presented as stale source qualification", shown.Status, err)
	}
	history, err := m.FullPublicationHistory()
	if err != nil || len(history) != 1 || history[0].Status != "APPLIED" || history[0].Execution == nil {
		t.Fatal("common history lost the actual successful transaction", err)
	}
	for _, member := range shown.Members {
		if len(member.Changes) != 0 || member.AppliedKey != "" {
			t.Fatal("old compiler qualification shown as current in executed history")
		}
	}
	for _, b := range append([]Binding{p.Resolution.Source}, fullPublicationBindings(p)...) {
		refs, err := m.fullPublicationReferences(b)
		if err != nil || len(refs) != 1 || refs[0].Kind != "publication" || refs[0].ID != p.Proof.Transaction.ID || refs[0].State != "committed" {
			t.Fatal("real common recovery no longer pins original binding", refs, err)
		}
		_, _, _, err = m.RemovalCandidate(b.ID, workstore.ID())
		code(t, err, "BINDING_REFERENCED")
		_, _, after, err := m.MembershipCandidate(b.ID, workstore.ID(), false)
		if err != nil {
			t.Fatal("archive did not retain common recovery reference", err)
		}
		registry, err := m.registry(after)
		if err != nil || len(registry.Archived) != 1 {
			t.Fatal(err)
		}
		found := false
		for _, ref := range registry.Archived[0].References {
			found = found || ref.Kind == "publication" && ref.ID == p.Proof.Transaction.ID
		}
		if !found {
			t.Fatal("archive silently dropped actual common recovery reference")
		}
	}
	// Scans from another member see the same real common reference, even though
	// only the original initiator may use the execution/recovery owner API.
	other, err := m.copySource(p.Resolution.Targets[0].Copy)
	if err != nil {
		t.Fatal(err)
	}
	clone := *m
	clone.Source = other
	refs, err := clone.fullPublicationReferences(p.Resolution.Source)
	if err != nil || len(refs) != 1 {
		t.Fatal("foreign initiating binding hid common references", refs, err)
	}
}
