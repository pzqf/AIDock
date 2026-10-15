package source

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/workstore"
)

func mcpLocalArtifact(t *testing.T, m *Manager) pluginlock.Preparation {
	t.Helper()
	apply(t, m, Request{Action: "create", Name: "guide", Description: "test"})
	state, err := m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	conf := mcpFixture()
	conf["servers"].([]any)[0].(map[string]any)["source"] = map[string]any{"type": "local", "path": "source-not-read"}
	p, err := m.MCPPlan(context.Background(), MCPEdit{ExpectedSHA256: state.SHA256, Config: conf})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(m.Store.Local, "mcp-artifacts", "memory")
	write(t, filepath.Join(root, "runtime", "helper"), []byte("never execute fixture"))
	if err = os.Chmod(filepath.Join(root, "runtime", "helper"), 0755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "dist", "index.js"), []byte("// do not execute"))
	write(t, filepath.Join(root, ".hidden"), []byte("preserve"))
	return pluginlock.Preparation{Name: "project-memory", ArtifactPath: "mcp-artifacts/memory", Version: "1.2.3", Runtime: pluginlock.Runtime{Kind: "native", Version: "1.2.3", Constraint: "=1.2.3", Executable: "runtime/helper"}}
}

func TestMCPLockInspectSaveRestartRollbackUndoAndOriginalBytes(t *testing.T) {
	m := manager(t)
	req := mcpLocalArtifact(t, m)
	ctx := context.Background()
	before, err := m.Store.Capture(m.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	inspected, err := m.InspectMCPLock(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if inspected.Candidate.Plugins[0].ArtifactStatus != "PASS" || inspected.Candidate.Plugins[0].TrustLevel != "L0" || inspected.Candidate.ExecutionReady {
		t.Fatal(inspected)
	}
	after, _ := m.Store.Capture(m.Store.Root)
	if workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("inspection wrote source")
	}
	// The submitted original format is retained, not reserialized on apply.
	document := "\ufeff# keep lock comments\r\n" + inspected.Document + "\r\n"
	p, err := m.MCPLockPlan(ctx, MCPLockEdit{ExpectedSHA256: inspected.ExpectedSHA256, Document: document})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Plan.Changes) != 1 || p.Plan.Changes[0].Path != "plugins.lock" || p.Candidate.ExecutionReady {
		t.Fatal(p)
	}
	_, err = m.ApplyMCPLock(ctx, p.Plan.ID, "")
	code(t, err, "INVALID_INPUT")
	_, err = m.ApplyMCPLock(ctx, p.Plan.ID, "wrong")
	code(t, err, "PLAN_STALE")
	if _, err = m.ApplyMCPLock(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	fresh, err := New(m.Store.Root, m.Store.Local, m.Owner, m.Scope)
	if err != nil {
		t.Fatal(err)
	}
	state, err := fresh.MCPLock(ctx)
	if err != nil || !state.Present || state.Document != document || state.Plugins[0].ArtifactStatus != "PASS" || state.ExecutionReady {
		t.Fatal(state, err)
	}
	rolled := apply(t, fresh, Request{Action: "rollback", Snapshot: p.Plan.ID})
	state, err = fresh.MCPLock(ctx)
	if err != nil || state.Present {
		t.Fatal(state, err)
	}
	apply(t, fresh, Request{Action: "rollback", Snapshot: rolled.Plan.ID})
	state, err = fresh.MCPLock(ctx)
	if err != nil || state.Document != document {
		t.Fatal(state, err)
	}
	bytes, err := os.ReadFile(filepath.Join(m.Store.Local, "mcp-artifacts", "memory", ".hidden"))
	if err != nil || string(bytes) != "preserve" {
		t.Fatal("artifact modified", err)
	}
	// Repair malformed lock with its observed hash; rollback restores those bytes.
	broken := []byte("\ufeff# raw broken lock\r\ninvalid: [")
	write(t, filepath.Join(m.Store.Root, "plugins.lock"), broken)
	state, err = m.MCPLock(ctx)
	if err != nil || state.Status != "FAIL" || state.Document != string(broken) {
		t.Fatal(state, err)
	}
	p, err = m.MCPLockPlan(ctx, MCPLockEdit{ExpectedSHA256: state.SHA256, Document: inspected.Document})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPLock(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	apply(t, m, Request{Action: "rollback", Snapshot: p.Plan.ID})
	got, _ := os.ReadFile(filepath.Join(m.Store.Root, "plugins.lock"))
	if string(got) != string(broken) {
		t.Fatal("lost original malformed bytes")
	}
}

func TestMCPLockArtifactSourceAndLocalBindingDriftBlockConfirmation(t *testing.T) {
	for _, kind := range []string{"artifact", "definition", "local binding", "scope", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			m := manager(t)
			req := mcpLocalArtifact(t, m)
			ctx := context.Background()
			inspected, err := m.InspectMCPLock(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			p, err := m.MCPLockPlan(ctx, MCPLockEdit{ExpectedSHA256: inspected.ExpectedSHA256, Document: inspected.Document})
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "artifact":
				write(t, filepath.Join(m.Store.Local, "mcp-artifacts", "memory", ".hidden"), []byte("changed"))
			case "definition":
				write(t, filepath.Join(m.Store.Root, "mcp.yaml"), []byte("schema_version: 1\nservers: []\n"))
			case "local binding":
				p.Plan.Inputs["local_root"] = "another-local-root"
				err = m.validateMCPLockPlan(ctx, p.Plan)
				code(t, err, "PLAN_STALE")
				return
			case "scope":
				if err = m.Store.PutFile(&p.Plan.After, "unrelated.txt", []byte("forbidden"), 0644); err != nil {
					t.Fatal(err)
				}
				err = m.validateMCPLockPlan(ctx, p.Plan)
				code(t, err, "MCP_LOCK_INVALID")
				return
			case "cancel":
				cancelCtx, cancel := context.WithCancel(ctx)
				cancel()
				_, err = m.ApplyMCPLock(cancelCtx, p.Plan.ID, p.Plan.Digest)
				if err != context.Canceled {
					t.Fatal(err)
				}
				return
			}
			_, err = m.ApplyMCPLock(ctx, p.Plan.ID, p.Plan.Digest)
			code(t, err, "PLAN_STALE")
			if _, err = os.Stat(filepath.Join(m.Store.Root, "plugins.lock")); !os.IsNotExist(err) {
				t.Fatal("stale plan wrote lock", err)
			}
		})
	}
}

func TestMCPLockInterruptedSaveRecoversWholeSource(t *testing.T) {
	m := manager(t)
	req := mcpLocalArtifact(t, m)
	ctx := context.Background()
	inspected, err := m.InspectMCPLock(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.MCPLockPlan(ctx, MCPLockEdit{ExpectedSHA256: inspected.ExpectedSHA256, Document: inspected.Document})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := m.Store.Capture(m.Store.Root)
	m.Store.Hook = func(phase string, _ int) error {
		if phase == "replaced" {
			return fmt.Errorf("injected lock interruption")
		}
		return nil
	}
	if _, err = m.ApplyMCPLock(ctx, p.Plan.ID, p.Plan.Digest); err == nil {
		t.Fatal("expected interruption")
	}
	m.Store.Hook = nil
	if _, err = m.Recover(); err != nil {
		t.Fatal(err)
	}
	after, _ := m.Store.Capture(m.Store.Root)
	if workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("recovery lost source bytes")
	}
}
