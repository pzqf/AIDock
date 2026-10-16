package native

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/nativeconfig"
	"github.com/pzqf/AIDock/internal/privatefs"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestMCPConnectionProcessHelper(t *testing.T) {
	phase := os.Getenv("AIDOCK_CONNECTION_PROCESS_PHASE")
	if phase == "" {
		return
	}
	s, err := source.New(filepath.Join(os.Getenv("AIDOCK_CONNECTION_PROJECT"), ".aidock"), os.Getenv("AIDOCK_CONNECTION_LOCAL"), owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	s.Copy = "22222222-2222-4222-8222-222222222222"
	m, err := New(s, os.Getenv("AIDOCK_CONNECTION_PROJECT"))
	if err != nil {
		t.Fatal(err)
	}
	m.connectionHook = func(stage string, n int) error {
		if stage == phase && (phase != "target-complete" || n == 0) {
			os.Exit(31)
		}
		return nil
	}
	if phase == "recovered-target" {
		_, err = m.RecoverMCPConnections()
	} else {
		_, err = m.connectionBatch().Apply(context.Background(), os.Getenv("AIDOCK_CONNECTION_PLAN"), os.Getenv("AIDOCK_CONNECTION_DIGEST"))
	}
	t.Fatalf("child did not interrupt: %v", err)
}

func interruptConnection(t *testing.T, m *Manager, p workstore.BatchPlan, phase string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestMCPConnectionProcessHelper$")
	cmd.Env = append(os.Environ(), "AIDOCK_CONNECTION_PROCESS_PHASE="+phase, "AIDOCK_CONNECTION_PROJECT="+m.Root, "AIDOCK_CONNECTION_LOCAL="+m.Source.Store.Local, "AIDOCK_CONNECTION_PLAN="+p.ID, "AIDOCK_CONNECTION_DIGEST="+p.Digest)
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 31 {
		t.Fatalf("expected process interruption: %v", err)
	}
}

func TestMCPConnectionProcessExitRecoveryAndConflict(t *testing.T) {
	for _, phase := range []string{"prepared", "staged", "target-complete", "recovered-target", "conflict"} {
		t.Run(phase, func(t *testing.T) {
			m, req, c := connectionFixture(t)
			before := configurationTree(t, m.Root)
			p := compiledConnection(t, m, req, &c)
			firstPhase := phase
			if phase == "recovered-target" || phase == "conflict" {
				firstPhase = "target-complete"
			}
			interruptConnection(t, m, p, firstPhase)
			code(t, m.connectionBatch().Pending(), "NATIVE_RECOVERY_REQUIRED")
			// All native transactions share the same project marker even when
			// their private backup buckets and target sets differ.
			other := &workstore.Batch{Boundary: m.Root, Local: m.Source.Store.Local, Identity: "other-native-binding", Paths: []string{".agents/skills"}}
			code(t, other.Pending(), "RECOVERY_CONFLICT")
			if phase == "recovered-target" {
				interruptConnection(t, m, p, phase)
			}
			if phase == "conflict" {
				path := filepath.Join(m.Root, ".codex/config.toml")
				original, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				write(t, path, append(append([]byte{}, original...), []byte("\n# external recovery edit\n")...))
				conflicted := configurationTree(t, m.Root)
				_, err = m.RecoverMCPConnections()
				code(t, err, "RECOVERY_CONFLICT")
				if !reflect.DeepEqual(conflicted, configurationTree(t, m.Root)) {
					t.Fatal("recovery overwrote a file before validating every target")
				}
				write(t, path, original)
			}
			// Reopen the manager with the same identity; recovery must never
			// execute a launcher or need the removed plugin authorization.
			reopened, err := New(m.Source, m.Root)
			if err != nil {
				t.Fatal(err)
			}
			n, err := reopened.RecoverMCPConnections()
			if err != nil || n != 1 {
				t.Fatal(n, err)
			}
			if !reflect.DeepEqual(before, configurationTree(t, m.Root)) {
				t.Fatal("process recovery did not restore both configs and ownership absence")
			}
			if n, err = reopened.RecoverMCPConnections(); err != nil || n != 0 {
				t.Fatal("recovery not idempotent", n, err)
			}
		})
	}
}

func connectionFixture(t *testing.T) (*Manager, MCPConnectionRequest, source.MCPClientConfiguration) {
	t.Helper()
	m, _ := fixture(t)
	m.Source.Copy = "22222222-2222-4222-8222-222222222222"
	write(t, filepath.Join(m.Root, ".mcp.json"), []byte("{\r\n \"mcpServers\":{\"personal\": {\"command\":\"not-executed\",\"env\":{\"TOKEN\":\"JSON_BACKUP_PRIVATE\"}}},\r\n \"custom\" : [1,2]\r\n}\r\n"))
	write(t, filepath.Join(m.Root, ".codex/config.toml"), []byte("# keep all handwritten bytes\r\nmodel='custom'\r\n[mcp_servers.personal]\r\ncommand='not-executed'\r\nenv={TOKEN='TOML_BACKUP_PRIVATE'}\r\n"))
	req := MCPConnectionRequest{Action: "install", Tools: []string{"claude-code", "codex-cli"}, Session: source.MCPSessionRequest{Name: "owned-service", ExpectedSHA256: strings.Repeat("a", 64), ExpectedReviewSHA256: strings.Repeat("b", 64), ExpectedRuntimeAttempt: workstore.ID()}}
	c := source.MCPClientConfiguration{Command: filepath.Join(t.TempDir(), "aidock.exe"), Args: []string{"mcp", "exec", "--plugin", "owned-service"}}
	if err := privatefs.Ensure(m.connectionArea()); err != nil {
		t.Fatal(err)
	}
	return m, req, c
}

func compiledConnection(t *testing.T, m *Manager, req MCPConnectionRequest, c *source.MCPClientConfiguration) workstore.BatchPlan {
	t.Helper()
	targets, err := m.compileConnection(req, c, "", "")
	if err != nil {
		t.Fatal(err)
	}
	// The ledger is local metadata. A real install supplies absolute executable
	// and home paths; this low-level fixture also needs a valid bound ledger.
	if c != nil {
		targets, err = m.compileConnection(req, c, c.Command, filepath.Dir(m.Source.Store.Local))
		if err != nil {
			t.Fatal(err)
		}
	}
	p, err := m.connectionBatch().Preview("apply", targets)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMCPConnectionPreservesUnmanagedBytesAndRestoresBothFiles(t *testing.T) {
	m, req, c := connectionFixture(t)
	ctx := context.Background()
	before := configurationTree(t, m.Root)
	p := compiledConnection(t, m, req, &c)
	if !reflect.DeepEqual(before, configurationTree(t, m.Root)) {
		t.Fatal("preview modified project")
	}
	j, err := m.connectionBatch().Apply(ctx, p.ID, p.Digest)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range Adapters() {
		b, err := os.ReadFile(filepath.Join(m.Root, a.MCPConfig.Path))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(b, []byte("BACKUP_PRIVATE")) || !bytes.Contains(b, []byte("aidock-owned-service")) {
			t.Fatal("lost handwritten field or managed launcher")
		}
		if _, err := nativeconfig.Inspect(*a.MCPConfig, b); err != nil {
			t.Fatal(err)
		}
	}
	ledger, err := m.connectionLedger()
	if err != nil || len(ledger.Tools) != 2 {
		t.Fatalf("ledger: %v", err)
	}
	report, err := m.Configuration(ctx, req.Tools)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range report.Files {
		if !file.WriteSupported || file.Fields[0].Ownership != "MANAGED" {
			t.Fatal(file)
		}
	}
	// Recompilation is byte-identical and retains the other owned fields.
	targets, err := m.compileConnection(req, &c, c.Command, filepath.Dir(m.Source.Store.Local))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		if len(workstore.Changes(target.Before, target.After)) != 0 {
			t.Fatal("connection not idempotent", target.Path)
		}
	}
	reverse, err := m.ReverseMCPConnection(ctx, j.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPConnectionReverse(ctx, reverse.ID, reverse.Digest); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, configurationTree(t, m.Root)) {
		t.Fatal("rollback did not restore original bytes and ledger absence")
	}
	// Reinstating a launcher has an independent current authorization gate.
	if _, err = m.ReverseMCPConnection(ctx, reverse.ID); err == nil {
		t.Fatal("undo restored an unapproved launcher")
	}
	if !reflect.DeepEqual(before, configurationTree(t, m.Root)) {
		t.Fatal("failed authorization changed project")
	}
	// Sensitive raw backup bytes remain only in the protected local bucket.
	err = filepath.WalkDir(m.Source.Store.Local, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() || workstore.Within(m.connectionArea(), path) {
			return nil
		}
		data, e := os.ReadFile(path)
		if e == nil && bytes.Contains(data, []byte("BACKUP_PRIVATE")) {
			t.Fatal("backup escaped private area", path)
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMCPConnectionSecondTargetFailureRestoresFirstAndLedger(t *testing.T) {
	m, req, c := connectionFixture(t)
	before := configurationTree(t, m.Root)
	p := compiledConnection(t, m, req, &c)
	m.connectionHook = func(stage string, n int) error {
		if stage == "target" && n == 1 {
			return errors.New("injected second target failure")
		}
		return nil
	}
	if _, err := m.connectionBatch().Apply(context.Background(), p.ID, p.Digest); err == nil {
		t.Fatal("expected failure")
	}
	if !reflect.DeepEqual(before, configurationTree(t, m.Root)) {
		t.Fatal("failed second file left partial install")
	}
	history, err := m.MCPConnectionHistory()
	if err != nil || len(history) != 1 || history[0].State != "recovered" {
		t.Fatalf("%+v %v", history, err)
	}
}

func TestMCPConnectionRefusesUnownedCollisionDriftAndStalePreview(t *testing.T) {
	for _, scenario := range []string{"collision", "drift", "stale"} {
		t.Run(scenario, func(t *testing.T) {
			m, req, c := connectionFixture(t)
			ctx := context.Background()
			if scenario == "collision" {
				write(t, filepath.Join(m.Root, ".mcp.json"), []byte(`{"mcpServers":{"aidock-owned-service":{"command":"personal"}}}`))
				_, err := m.compileConnection(req, &c, c.Command, filepath.Dir(m.Source.Store.Local))
				var classified *nativeconfig.Error
				if !errors.As(err, &classified) || classified.Code != "CONFIG_UNMANAGED_COLLISION" {
					t.Fatal(err)
				}
				return
			}
			p := compiledConnection(t, m, req, &c)
			if _, err := m.connectionBatch().Apply(ctx, p.ID, p.Digest); err != nil {
				t.Fatal(err)
			}
			remove := MCPConnectionRequest{Action: "remove", Tools: req.Tools, Session: source.MCPSessionRequest{Name: req.Session.Name}}
			preview, err := m.MCPConnectionPlan(ctx, remove, "", "")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(m.Root, ".mcp.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "drift" {
				data = bytes.ReplaceAll(data, []byte("owned-service"), []byte("owned-altered"))
			} else {
				data = append(data, '\n')
			}
			write(t, path, data)
			before := configurationTree(t, m.Root)
			_, err = m.ApplyMCPConnection(ctx, preview.ID, preview.Digest)
			if err == nil {
				t.Fatal("external modification was overwritten")
			}
			if !reflect.DeepEqual(before, configurationTree(t, m.Root)) {
				t.Fatal("modified stale files")
			}
		})
	}
}

func TestMCPConnectionRemoveAfterAuthorizationUnavailableAndRejectTampering(t *testing.T) {
	m, req, c := connectionFixture(t)
	ctx := context.Background()
	p := compiledConnection(t, m, req, &c)
	if _, err := m.connectionBatch().Apply(ctx, p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	remove := MCPConnectionRequest{Action: "remove", Tools: req.Tools, Session: source.MCPSessionRequest{Name: req.Session.Name}}
	preview, err := m.MCPConnectionPlan(ctx, remove, "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Rehashing a modified preview is not authority to write arbitrary bytes.
	for i := range preview.Transaction.Targets {
		preview.Transaction.Targets[i].After = preview.Transaction.Targets[i].Before
	}
	preview.Digest = connectionDigest(preview)
	if err = save(filepath.Join(m.connectionArea(), "connection-plans", preview.ID+".json"), jsonData(preview)); err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPConnection(ctx, preview.ID, preview.Digest); err == nil {
		t.Fatal("forged candidate was accepted")
	}
	preview, err = m.MCPConnectionPlan(ctx, remove, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPConnection(ctx, preview.ID, preview.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(m.Root, connectionLedgerPath)); !os.IsNotExist(err) {
		t.Fatal("empty ownership ledger retained", err)
	}
	for _, a := range Adapters() {
		data, err := os.ReadFile(filepath.Join(m.Root, a.MCPConfig.Path))
		if err != nil || bytes.Contains(data, []byte("aidock-owned-service")) || !bytes.Contains(data, []byte("BACKUP_PRIVATE")) {
			t.Fatalf("remove lost handwritten bytes: %v", err)
		}
	}
}

func TestMCPConnectionRequestRejectsAmbiguousInputWithoutEcho(t *testing.T) {
	for _, raw := range []string{`{"action":"install","action":"remove"}`, `{"Action":"install"}`, `{"tools":null}`, `{"session":{"probe":true,"probe":false}}`, `{"session":{"command":"PRIVATE_SENTINEL"}}`, `{"command":"PRIVATE_SENTINEL"}`, `{"tools":[{}]}`, `{} {}`} {
		_, err := ParseMCPConnectionRequest([]byte(raw))
		if err == nil || strings.Contains(err.Error(), "PRIVATE_SENTINEL") {
			t.Fatal(raw, err)
		}
	}
	req, err := ParseMCPConnectionRequest([]byte(`{"action":"remove","tools":["codex-cli"],"session":{"name":"owned-service"}}`))
	if err != nil || req.Session.Name != "owned-service" {
		t.Fatal(req, err)
	}
	if _, err = json.Marshal(req); err != nil {
		t.Fatal(err)
	}
}

func TestMCPConnectionVersionReceiptUsesCurrentBinaryAndNewestFailure(t *testing.T) {
	m, req, _ := connectionFixture(t)
	paths := map[string]string{}
	for _, a := range Adapters() {
		paths[a.ID] = filepath.Join(t.TempDir(), a.ID+".exe")
		write(t, paths[a.ID], []byte("test version program"))
	}
	ops := &probeOps{resolve: func(id string) (toolBinary, error) {
		data, err := os.ReadFile(paths[id])
		return toolBinary{Path: paths[id], Digest: workstore.Hash(data)}, err
	}, version: func(_ context.Context, _ toolBinary, id, project, home string) (string, error) {
		if len(configurationTree(t, project)) != 1 || len(configurationTree(t, home)) != 1 {
			t.Fatal("version check did not use empty directories")
		}
		for _, a := range Adapters() {
			if a.ID == id {
				return a.Verified[0], nil
			}
		}
		return "", errors.New("unknown tool")
	}}
	m.probe = ops
	r, err := m.CheckMCPConnectionVersions(context.Background(), req.Tools)
	if err != nil || len(r.Tools) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	as, _ := selectAdapters(req.Tools)
	if err = m.connectionVersion(r.ID, as); err != nil {
		t.Fatal(err)
	}
	write(t, paths["codex-cli"], []byte("changed program"))
	code(t, m.connectionVersion(r.ID, as), "NATIVE_CHECK_REQUIRED")
	ops.version = func(context.Context, toolBinary, string, string, string) (string, error) {
		return "", errors.New("PRIVATE_TOOL_STDERR")
	}
	failed, err := m.CheckMCPConnectionVersions(context.Background(), req.Tools)
	if err != nil || len(failed.Tools) != 0 {
		t.Fatal(failed, err)
	}
	if bytes.Contains(jsonData(failed), []byte("PRIVATE_TOOL_STDERR")) {
		t.Fatal("captured private process output")
	}
	code(t, m.connectionVersion(r.ID, as), "CHECK_STALE")
	before := configurationTree(t, m.Root)
	req.CheckID = failed.ID
	if _, err = m.MCPConnectionPlan(context.Background(), req, paths["codex-cli"], filepath.Dir(m.Source.Store.Local)); err == nil {
		t.Fatal("version receipt bypassed plugin authorization")
	}
	if !reflect.DeepEqual(before, configurationTree(t, m.Root)) {
		t.Fatal("failed authority gate changed target")
	}
}
