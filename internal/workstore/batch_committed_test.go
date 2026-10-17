package workstore

import (
	"context"
	"testing"
)

func TestBatchCommittedGuardRequiresJournalAndHoldsNativeLock(t *testing.T) {
	b, p := batchFixture(t)
	called := false
	err := b.WithCommitted(p.ID, p.Digest, func(BatchJournal) error { called = true; return nil })
	batchCode(t, err, "APPLIED_VIEW_NOT_COMMITTED")
	if called {
		t.Fatal("intent entered committed guard")
	}
	if _, err = b.Apply(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	err = b.WithCommitted(p.ID, p.Digest, func(j BatchJournal) error {
		called = true
		if j.State != "committed" {
			t.Fatal(j)
		}
		batchCode(t, b.Pending(), "NATIVE_BUSY")
		return nil
	})
	if err != nil || !called {
		t.Fatal(err)
	}
}

func TestBatchCurrentGuardHasNoImplicitCommitAuthority(t *testing.T) {
	b, _ := batchFixture(t)
	called := false
	if err := b.WithCurrent(func() error { called = true; batchCode(t, b.Pending(), "NATIVE_BUSY"); return nil }); err != nil || !called {
		t.Fatal(called, err)
	}
	if err := b.WithCurrent(nil); err == nil {
		t.Fatal("nil consumer accepted")
	}
}
