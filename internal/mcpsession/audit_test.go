package mcpsession

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/mcprelay"
)

func TestAuditViewPreservesIntentRelayAndCompletionAndDetectsDeletedTail(t *testing.T) {
	m, input := fixture(t)
	_, err := m.Probe(context.Background(), input, func(audit func(mcprelay.Event) error) (string, int) { return successful(audit) })
	if err != nil {
		t.Fatal(err)
	}
	c := m.Audit(context.Background())
	if c.Status != "PASS" || *c.Count != 6 || len(c.Events) != 6 || c.Events[0].Action != "INTENT" || c.Events[1].Action != "RELAY" || c.Events[5].Action != "COMPLETE" || c.Events[5].SHA256 != c.Head {
		t.Fatal(c)
	}
	files, _ := os.ReadDir(filepath.Join(m.Store.Root, "audit"))
	if err := os.Remove(filepath.Join(m.Store.Root, "audit", files[len(files)-1].Name())); err != nil {
		t.Fatal(err)
	}
	c = m.Audit(context.Background())
	if c.Status != "FAIL" || c.Count != nil || len(c.Events) != 0 {
		t.Fatal("deleted committed tail accepted", c)
	}
}
