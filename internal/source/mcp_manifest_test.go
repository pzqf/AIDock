package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestMCPManifestChecksCurrentBytesWithoutInstallingOrWritingSource(t *testing.T) {
	m := manager(t)
	ctx := context.Background()
	req := mcpLocalArtifact(t, m)
	definitions, err := m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	s := definitions.Config["servers"].([]any)[0].(map[string]any)
	manifest := map[string]any{"schema_version": 1, "name": "publisher-name", "type": "mcp-server", "version": "1.2.3", "source": s["source"], "permissions": s["permissions"], "dependencies": []any{}}
	root := filepath.Join(m.Store.Local, filepath.FromSlash(req.ArtifactPath))
	raw := append([]byte("\xef\xbb\xbf# preserve manifest comment\r\n"), jsonBytes(manifest)...)
	write(t, filepath.Join(root, "plugin.yaml"), raw)
	inspected, err := m.InspectMCPLock(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := m.MCPLockPlan(ctx, MCPLockEdit{ExpectedSHA256: inspected.ExpectedSHA256, Document: inspected.Document})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPLock(ctx, plan.Plan.ID, plan.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	before, _ := m.Store.Capture(m.Store.Root)
	entries, _, _ := pluginlock.Observe(ctx, root)
	for i := 0; i < 2; i++ {
		state, err := m.MCPManifest(ctx, req.Name)
		if err != nil || len(state.Plugins) != 1 {
			t.Fatal(state, err)
		}
		r := state.Plugins[0]
		if r.ManifestStatus != "PASS" || r.PermissionStatus != "PASS" || r.DependencyStatus != "UNKNOWN" || r.Document != string(raw) || r.InstallationState != "PREPARED" || r.SBOMStatus != "UNKNOWN" || state.ExecutionReady {
			t.Fatal(state)
		}
	}
	after, _ := m.Store.Capture(m.Store.Root)
	last, _, _ := pluginlock.Observe(ctx, root)
	if workstore.Digest(before) != workstore.Digest(after) || pluginlock.Digest(entries) != pluginlock.Digest(last) {
		t.Fatal("read changed source or artifact")
	}
	for _, name := range []string{"sbom.json", "installation.json"} {
		if _, err = os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatal("read registered installation or SBOM", name, err)
		}
	}
	_, err = m.MCPManifest(ctx, "another")
	code(t, err, "MCP_LOCK_ENTRY_MISSING")
	write(t, filepath.Join(root, "plugin.yaml"), []byte(strings.Replace(string(raw), "publisher-name", "external-change", 1)))
	state, err := m.MCPManifest(ctx, req.Name)
	if err != nil || state.Plugins[0].Manifest != nil || state.Plugins[0].ManifestStatus != "UNKNOWN" {
		t.Fatal(state, err)
	}
	lock, err := m.MCPLock(ctx)
	if err != nil || lock.Plugins[0].ArtifactStatus != "FAIL" || lock.Plugins[0].TrustLevel != "L0" || lock.ExecutionReady {
		t.Fatal(lock, err)
	}
}

func TestMCPManifestEmptyLockDoesNotPassDefinedServers(t *testing.T) {
	m := manager(t)
	ctx := context.Background()
	mcpLocalArtifact(t, m)
	state, err := m.MCPLock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.MCPLockPlan(ctx, MCPLockEdit{ExpectedSHA256: state.SHA256, Document: `{"schema_version":1,"plugins":[]}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPLock(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	out, err := m.MCPManifest(ctx, "")
	if err != nil || out.Status != "UNKNOWN" || len(out.Plugins) != 0 || len(out.Checks) != 1 || out.Checks[0].Code != "MCP_MANIFEST_NO_LOCKED_ARTIFACTS" {
		t.Fatal(out, err)
	}
}
