package auditlog

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestObserveRejectsPartialDamagedChainWithoutPublishingValues(t *testing.T) {
	s := &workstore.Store{Root: t.TempDir(), Local: t.TempDir()}
	c := Observe(context.Background(), "install", "source", s, func(workstore.Tree) ([]Event, string, error) {
		return []Event{{Subject: "PRIVATE_VALUE"}}, "unverified-head", result.Fail("CHAIN_INVALID", "PRIVATE_VALUE", 5)
	})
	if c.Status != "FAIL" || c.Code != "CHAIN_INVALID" || c.Count != nil || c.Head != "" || len(c.Events) != 0 {
		t.Fatal(c)
	}
	if got := Unavailable("session", "copy", result.Fail("MCP_SESSION_RECOVERY_REQUIRED", "private", 4)); got.Status != "RECOVERY_REQUIRED" {
		t.Fatal(got)
	}
}

func TestAuditPaginationFiltersStableMetadataAndComparesInstants(t *testing.T) {
	events := []Event{}
	for i := 1; i <= 101; i++ {
		events = append(events, Event{Category: "install", Sequence: i, ID: fmt.Sprint(i), At: "2026-10-16T00:00:00Z", Action: "install", Subject: "fixture"})
	}
	chains := []Chain{{Category: "install", Status: "PASS", Events: events}, {Category: "session", Status: "PASS", Events: []Event{{Category: "session", Sequence: 1, At: "2026-10-16T08:00:01+08:00", ID: "later"}}}, {Category: "permission", Status: "FAIL", Events: []Event{{Category: "permission", ID: "never-publish"}}}}
	q := Query{Page: 1}
	if err := q.Validate(); err != nil {
		t.Fatal(err)
	}
	p := Paginate(chains, q)
	if p.Total != 102 || len(p.Events) != 50 || p.Pages != 3 || p.Events[0].ID != "later" || p.Events[1].Sequence != 101 {
		t.Fatal(p)
	}
	p = Paginate(chains, Query{Page: 3, Category: "install", Search: "FIXTURE"})
	if p.Total != 101 || len(p.Events) != 1 || p.Events[0].Sequence != 1 {
		t.Fatal(p)
	}
	for _, q := range []Query{{Category: "private-value"}, {Page: -1}, {Search: strings.Repeat("x", 201)}, {Search: "bad\nquery"}} {
		if q.Validate() == nil {
			t.Fatal(q)
		}
	}
}
