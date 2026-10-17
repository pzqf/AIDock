package workstore

import (
	"context"
	"testing"
)

func TestCoordinatorRevisedReversalPreservesOriginalReferenceAndBeforeScope(t *testing.T) {
	c, p := coordinatedFixture(t)
	if _, err := c.Apply(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	reverse, err := c.Reverse(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	targets := append([]BatchTarget{}, reverse.Targets...)
	targets[0].After = Clone(targets[0].After)
	if err = c.Blobs.PutFile(&targets[0].After, "body", []byte("regenerated exact-contract output"), 0644); err != nil {
		t.Fatal(err)
	}
	revised, err := c.ReviseReversal(reverse.ID, reverse.Digest, targets)
	if err != nil {
		t.Fatal(err)
	}
	if revised.ID == reverse.ID || revised.Reference != p.ID || revised.Action != "rollback" {
		t.Fatal("lost original committed reference", revised)
	}
	if err = c.ObservePlan(revised.ID, revised.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(context.Background(), revised.ID, revised.Digest); err != nil {
		t.Fatal(err)
	}
	if err = c.ObservePlan(reverse.ID, reverse.Digest); err == nil {
		t.Fatal("old proposal survived newer committed revision")
	}
	undo, err := c.Reverse(revised.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(context.Background(), undo.ID, undo.Digest); err != nil {
		t.Fatal(err)
	}
	coordinatedEqual(t, c, p.Targets, true)
	changed := append([]BatchTarget{}, undo.Targets...)
	changed[0].Before = Tree{Entries: map[string]Entry{}}
	if _, err = c.ReviseReversal(undo.ID, undo.Digest, changed); err == nil {
		t.Fatal("consumed or shifted before-image accepted")
	}
}
