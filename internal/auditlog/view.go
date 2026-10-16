// Package auditlog defines a redacted, read-only view over existing audit chains.
// It does not create a new authority, write events or certify an external anchor.
package auditlog

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type Event struct {
	Category string `json:"category"`
	Sequence int    `json:"sequence"`
	ID       string `json:"id"`
	At       string `json:"at"`
	Action   string `json:"action"`
	Code     string `json:"code,omitempty"`
	Subject  string `json:"subject,omitempty"`
	Actor    string `json:"actor,omitempty"`
	Attempt  string `json:"attempt,omitempty"`
	Method   string `json:"method,omitempty"`
	Tool     string `json:"tool,omitempty"`
	Request  string `json:"request_id,omitempty"`
	Previous string `json:"previous,omitempty"`
	SHA256   string `json:"sha256"`
}

type Chain struct {
	Category string `json:"category"`
	Scope    string `json:"scope"`
	Status   string `json:"status"`
	Count    *int   `json:"count"`
	Head     string `json:"head,omitempty"`
	Code     string `json:"code,omitempty"`
	Anchor   string `json:"anchor"`
	// Events are redacted inside the owning store, before they reach the engine.
	Events []Event `json:"-"`
}

func Unavailable(category, scope string, err error) Chain {
	c := Chain{Category: category, Scope: scope, Status: "FAIL", Code: "AUDIT_READ_UNAVAILABLE", Anchor: "UNVERIFIED", Events: []Event{}}
	var e *result.Error
	if errors.As(err, &e) {
		c.Code = e.Code
		if strings.Contains(e.Code, "RECOVERY_REQUIRED") {
			c.Status = "RECOVERY_REQUIRED"
		}
	}
	return c
}

// Observe validates under the same store lock used by event writers. A damaged
// chain exposes no partial events, count or head. Local cache/lock writes may
// occur through Capture; authority bytes and journals are never changed.
func Observe(ctx context.Context, category, scope string, store *workstore.Store, read func(workstore.Tree) ([]Event, string, error)) Chain {
	out := Chain{Category: category, Scope: scope, Anchor: "LOCAL_JOURNALS_ONLY", Events: []Event{}}
	err := store.Locked(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := store.Pending(); err != nil {
			return err
		}
		t, err := store.Capture(store.Root)
		if err != nil {
			return err
		}
		out.Events, out.Head, err = read(t)
		return err
	})
	if err != nil {
		return Unavailable(category, scope, err)
	}
	count := len(out.Events)
	out.Count, out.Status = &count, "PASS"
	if count == 0 {
		out.Status = "EMPTY"
	}
	for i := range out.Events {
		out.Events[i].Category = category
	}
	return out
}

const PageSize = 50

type Query struct {
	Category string `json:"category"`
	Search   string `json:"search"`
	Page     int    `json:"page"`
}

func (q *Query) Validate() error {
	if q.Page == 0 {
		q.Page = 1
	}
	if q.Page < 1 || q.Page > 1000000 || len(q.Search) > 200 || strings.ContainsAny(q.Search, "\x00\r\n") {
		return result.Fail("INVALID_INPUT", "审计页码或搜索条件无效", 2)
	}
	switch q.Category {
	case "", "origin", "install", "permission", "runtime", "session":
		return nil
	}
	return result.Fail("INVALID_INPUT", "审计类别不受支持", 2)
}

type Page struct {
	Query    Query   `json:"query"`
	Total    int     `json:"total"`
	Pages    int     `json:"pages"`
	PageSize int     `json:"page_size"`
	Events   []Event `json:"events"`
}

// Paginate operates only on validated, redacted metadata. A search never reads
// permissions, free-form reasons, tool arguments, results or configuration values.
func Paginate(chains []Chain, q Query) Page {
	events := []Event{}
	for _, c := range chains {
		if c.Status != "PASS" || q.Category != "" && c.Category != q.Category {
			continue
		}
		for _, e := range c.Events {
			text := strings.Join([]string{e.Category, e.ID, e.Action, e.Code, e.Subject, e.Actor, e.Attempt, e.Method, e.Tool, e.Request}, " ")
			if q.Search == "" || strings.Contains(strings.ToLower(text), strings.ToLower(q.Search)) {
				events = append(events, e)
			}
		}
	}
	sort.Slice(events, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, events[i].At)
		b, _ := time.Parse(time.RFC3339Nano, events[j].At)
		if !a.Equal(b) {
			return a.After(b)
		}
		if events[i].Category != events[j].Category {
			return events[i].Category < events[j].Category
		}
		return events[i].Sequence > events[j].Sequence
	})
	out := Page{Query: q, Total: len(events), Pages: (len(events) + PageSize - 1) / PageSize, PageSize: PageSize, Events: []Event{}}
	start := (q.Page - 1) * PageSize
	if start < len(events) {
		end := start + PageSize
		if end > len(events) {
			end = len(events)
		}
		out.Events = events[start:end]
	}
	return out
}
