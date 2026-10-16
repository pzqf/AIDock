package mcpsession

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/mcprelay"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestClientAuditCorrelatesReadsAndClosedDoesNotMeanPass(t *testing.T) {
	m, input := fixture(t)
	input.Mode = "stdio"
	e, err := m.Client(context.Background(), input, func(audit func(mcprelay.Event) error) (string, int) {
		for _, e := range []mcprelay.Event{
			{Method: "tools/call", Code: "FORWARD", RequestID: "aidock-1", Tool: "project_query", RequestSHA256: workstore.Hash([]byte("argument"))},
			{Method: "tools/call", Code: "RETURN", RequestID: "aidock-1", Tool: "project_query", ResultSHA256: workstore.Hash([]byte("result"))},
			{Method: "relay/reject", Code: "AUTHORIZATION_REQUIRED"},
		} {
			if err := audit(e); err != nil {
				t.Fatal(err)
			}
		}
		return "MCP_CLIENT_CLOSED", 0
	})
	if err != nil || e.Code != "MCP_CLIENT_CLOSED" {
		t.Fatal(e, err)
	}
	s, err := m.State([]Input{input})
	if err != nil || s.AuditCount != 5 || s.Entries[0].Status != "CLOSED" || s.Entries[0].ReadRequests != 1 || s.Entries[0].CompletedReads != 1 || s.Entries[0].Rejections != 1 || s.ExecutionReady {
		t.Fatal(s, err)
	}
	_, err = m.Probe(context.Background(), input, func(func(mcprelay.Event) error) (string, int) { t.Fatal("wrong execution mode entered"); return "", 0 })
	if err == nil {
		t.Fatal("bounded probe accepted client mode")
	}
}

func TestClientAuditRejectsUnmatchedReturn(t *testing.T) {
	m, input := fixture(t)
	input.Mode = "stdio"
	e, err := m.Client(context.Background(), input, func(audit func(mcprelay.Event) error) (string, int) {
		if audit(mcprelay.Event{Method: "tools/call", Code: "RETURN", RequestID: "aidock-3", Tool: "project_query", ResultSHA256: workstore.Hash([]byte("unmatched"))}) == nil {
			t.Fatal("accepted an uncorrelated result")
		}
		return "MCP_SESSION_AUDIT_FAILED", 0
	})
	if err != nil || e.Code != "MCP_SESSION_AUDIT_FAILED" {
		t.Fatal(e, err)
	}
	s, err := m.State([]Input{input})
	if err != nil || s.AuditCount != 2 || s.Entries[0].CompletedReads != 0 {
		t.Fatal(s, err)
	}
}

func TestClientInterruptedAuditRecoveryNeverReplays(t *testing.T) {
	if raw := os.Getenv("AIDOCK_CLIENT_AUDIT_HELPER"); raw != "" {
		var a struct {
			Local   string
			Binding Binding
			Input   Input
		}
		if json.Unmarshal([]byte(raw), &a) != nil {
			os.Exit(31)
		}
		m, err := New(a.Local, a.Binding)
		if err != nil {
			os.Exit(32)
		}
		m.Client(context.Background(), a.Input, func(func(mcprelay.Event) error) (string, int) { os.Exit(94); return "", 0 })
		os.Exit(33)
	}
	m, input := fixture(t)
	input.Mode = "stdio"
	raw, _ := json.Marshal(struct {
		Local   string
		Binding Binding
		Input   Input
	}{filepath.Dir(m.Store.Root), m.Binding, input})
	child := exec.Command(os.Args[0], "-test.run=^TestClientInterruptedAuditRecoveryNeverReplays$")
	child.Env = append(os.Environ(), "AIDOCK_CLIENT_AUDIT_HELPER="+string(raw))
	if err := child.Run(); err == nil {
		t.Fatal("helper unexpectedly returned")
	}
	if _, err := m.Recover(); err != nil {
		t.Fatal(err)
	}
	s, err := m.State([]Input{input})
	if err != nil || s.AuditCount != 1 || s.Entries[0].Status != "INCOMPLETE" || s.Entries[0].CompletedReads != 0 {
		t.Fatal(s, err)
	}
}
