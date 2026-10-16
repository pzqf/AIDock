package runtimecheck

import (
	"context"
	"testing"
)

func TestAuditViewIncludesRuntimeIntentAndResultWithoutExecution(t *testing.T) {
	m, input := fixture(t)
	if _, err := m.Probe(context.Background(), input, func() (string, string) { return "RUNTIME_VERSION_MATCH", "v1.2.3" }); err != nil {
		t.Fatal(err)
	}
	c := m.Audit(context.Background())
	if c.Status != "PASS" || *c.Count != 2 || c.Events[0].Action != "INTENT" || c.Events[1].Code != "RUNTIME_VERSION_MATCH" || c.Events[1].SHA256 != c.Head {
		t.Fatal(c)
	}
}
