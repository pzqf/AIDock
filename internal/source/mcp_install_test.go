package source

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/workstore"
)

func installFixture(t *testing.T) (*Manager, pluginlock.Preparation) {
	t.Helper()
	m := manager(t)
	req := mcpLocalArtifact(t, m)
	s, err := m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	server := s.Config["servers"].([]any)[0].(map[string]any)
	manifest := map[string]any{"schema_version": 1, "name": "publisher-package", "type": "mcp-server", "version": "1.2.3", "source": server["source"], "permissions": server["permissions"], "dependencies": []any{map[string]any{"ecosystem": "generic", "name": "direct-fixture", "version": "2.0.0"}}}
	write(t, filepath.Join(m.Store.Local, filepath.FromSlash(req.ArtifactPath), "plugin.yaml"), jsonBytes(manifest))
	refreshInstallLock(t, m, req)
	return m, req
}
func refreshInstallLock(t *testing.T, m *Manager, req pluginlock.Preparation) {
	t.Helper()
	ctx := context.Background()
	v, err := m.InspectMCPLock(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.MCPLockPlan(ctx, MCPLockEdit{ExpectedSHA256: v.ExpectedSHA256, Document: v.Document})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPLock(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
}
func installEdit(t *testing.T, m *Manager, req plugininstall.Edit) plugininstall.Preview {
	t.Helper()
	ctx := context.Background()
	s, err := m.MCPInstall(ctx)
	if err != nil {
		t.Fatal(err)
	}
	req.ExpectedSHA256 = s.SHA256
	p, err := m.MCPInstallPlan(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPInstall(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	return p
}
func allowInstall(name string) plugininstall.Edit {
	return plugininstall.Edit{Action: "install", Name: name, AllowUnverified: true, RiskReason: "独立测试夹具，只登记，不运行"}
}

func TestMCPInstallPreviewRiskConfirmationSBOMRestartRollbackUndoAndRemoval(t *testing.T) {
	m, req := installFixture(t)
	ctx := context.Background()
	sourceBefore, _ := m.Store.Capture(m.Store.Root)
	artifactBefore, _, _ := pluginlock.Observe(ctx, filepath.Join(m.Store.Local, filepath.FromSlash(req.ArtifactPath)))
	state, err := m.MCPInstall(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.MCPInstalledSBOM(ctx, req.Name)
	code(t, err, "INSTALL_NOT_FOUND")
	edit := allowInstall(req.Name)
	edit.ExpectedSHA256 = state.SHA256
	edit.AllowUnverified = false
	_, err = m.MCPInstallPlan(ctx, edit)
	code(t, err, "INSTALL_RISK_CONFIRMATION_REQUIRED")
	edit.AllowUnverified = true
	edit.RiskReason = " "
	_, err = m.MCPInstallPlan(ctx, edit)
	code(t, err, "INSTALL_RISK_CONFIRMATION_REQUIRED")
	edit.RiskReason = "明确保留 L0，仅登记本地夹具"
	preview, err := m.MCPInstallPlan(ctx, edit)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Candidate.Installations[0].SBOM != nil {
		t.Fatal("preview generated an official installed SBOM")
	}
	afterPreview, _ := m.MCPInstall(ctx)
	if len(afterPreview.Installations) != 0 || afterPreview.AuditCount != 0 {
		t.Fatal("preview registered installation", afterPreview)
	}
	for _, digest := range []string{"", "wrong"} {
		_, err = m.ApplyMCPInstall(ctx, preview.Plan.ID, digest)
		if err == nil {
			t.Fatal("accepted bad confirmation")
		}
	}
	if _, err = m.ApplyMCPInstall(ctx, preview.Plan.ID, preview.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	_, err = m.ApplyMCPInstall(ctx, preview.Plan.ID, preview.Plan.Digest)
	code(t, err, "PLAN_CONSUMED")
	fresh, err := New(m.Store.Root, m.Store.Local, m.Owner, m.Scope)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := fresh.MCPInstalledSBOM(ctx, req.Name)
	if err != nil || entry.Status != "CURRENT" || entry.SBOM == nil || len(entry.SBOM.Dependencies) != 1 || entry.SBOM.Dependencies[0].Name != "direct-fixture" || entry.Record.TrustLevel != "L0" || entry.ExecutionReady {
		t.Fatal(entry, err)
	}
	sbomBytes := plugininstall.JSON(entry.SBOM)
	if workstore.Hash(sbomBytes) != entry.SBOMSHA256 {
		t.Fatal("SBOM not deterministic")
	}
	again, _ := fresh.MCPInstalledSBOM(ctx, req.Name)
	if again.SBOMSHA256 != entry.SBOMSHA256 {
		t.Fatal("SBOM changed between reads")
	}
	manifest, err := fresh.MCPManifest(ctx, req.Name)
	if err != nil || manifest.Plugins[0].InstallationState != "INSTALLED" || manifest.Plugins[0].SBOMStatus != "PASS" {
		t.Fatal(manifest, err)
	}
	rolled := installEdit(t, fresh, plugininstall.Edit{Action: "rollback", Snapshot: preview.Plan.ID})
	state, _ = fresh.MCPInstall(ctx)
	if len(state.Installations) != 0 || state.AuditCount != 2 {
		t.Fatal(state)
	}
	state, _ = fresh.MCPInstall(ctx)
	undo := plugininstall.Edit{Action: "rollback", Snapshot: rolled.Plan.ID, ExpectedSHA256: state.SHA256}
	_, err = fresh.MCPInstallPlan(ctx, undo)
	code(t, err, "INSTALL_RISK_CONFIRMATION_REQUIRED")
	undo.AllowUnverified = true
	undo.RiskReason = "重新接受恢复登记的未验证风险"
	installEdit(t, fresh, undo)
	removed := installEdit(t, fresh, plugininstall.Edit{Action: "remove", Name: req.Name})
	state, _ = fresh.MCPInstall(ctx)
	if len(state.Installations) != 0 || state.AuditCount != 4 {
		t.Fatal(state)
	}
	installEdit(t, fresh, plugininstall.Edit{Action: "rollback", Snapshot: removed.Plan.ID, AllowUnverified: true, RiskReason: "核对后恢复移除登记"})
	sourceAfter, _ := m.Store.Capture(m.Store.Root)
	artifactAfter, _, _ := pluginlock.Observe(ctx, filepath.Join(m.Store.Local, filepath.FromSlash(req.ArtifactPath)))
	if workstore.Digest(sourceBefore) != workstore.Digest(sourceAfter) || pluginlock.Digest(artifactBefore) != pluginlock.Digest(artifactAfter) {
		t.Fatal("registration modified source or artifacts")
	}
	other, err := New(m.Store.Root, t.TempDir(), m.Owner, m.Scope)
	if err != nil {
		t.Fatal(err)
	}
	otherState, err := other.MCPInstall(ctx)
	if err != nil || len(otherState.Installations) != 0 {
		t.Fatal("local installation leaked to another home", otherState, err)
	}
}

func TestMCPInstallCurrentBytesAndSourceChangesInvalidateConfirmation(t *testing.T) {
	for _, kind := range []string{"artifact", "permission", "source"} {
		t.Run(kind, func(t *testing.T) {
			m, req := installFixture(t)
			ctx := context.Background()
			state, _ := m.MCPInstall(ctx)
			edit := allowInstall(req.Name)
			edit.ExpectedSHA256 = state.SHA256
			p, err := m.MCPInstallPlan(ctx, edit)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "artifact":
				write(t, filepath.Join(m.Store.Local, filepath.FromSlash(req.ArtifactPath), ".hidden"), []byte("changed"))
			case "permission":
				s, _ := m.MCP()
				s.Config["servers"].([]any)[0].(map[string]any)["permissions"].(map[string]any)["exec"] = true
				mp, err := m.MCPPlan(ctx, MCPEdit{ExpectedSHA256: s.SHA256, Config: s.Config})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = m.ApplyMCP(ctx, mp.Plan.ID, mp.Plan.Digest); err != nil {
					t.Fatal(err)
				}
			case "source":
				apply(t, m, Request{Action: "create", Name: "unrelated-guide", Description: "source binding changes"})
			}
			_, err = m.ApplyMCPInstall(ctx, p.Plan.ID, p.Plan.Digest)
			code(t, err, "PLAN_STALE")
			state, _ = m.MCPInstall(ctx)
			if len(state.Installations) != 0 || state.AuditCount != 0 {
				t.Fatal("stale plan changed registration")
			}
		})
	}
}

func TestMCPInstallKnownFailureMissingManifestAndUndeclaredDependenciesBlock(t *testing.T) {
	for _, kind := range []string{"missing", "permissions", "dependencies", "platform", "transport"} {
		t.Run(kind, func(t *testing.T) {
			m, req := installFixture(t)
			ctx := context.Background()
			root := filepath.Join(m.Store.Local, filepath.FromSlash(req.ArtifactPath))
			file := filepath.Join(root, "plugin.yaml")
			b, _ := os.ReadFile(file)
			var manifest map[string]any
			json.Unmarshal(b, &manifest)
			switch kind {
			case "missing":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				manifest["permissions"].(map[string]any)["exec"] = true
				write(t, file, jsonBytes(manifest))
			case "dependencies":
				delete(manifest, "dependencies")
				write(t, file, jsonBytes(manifest))
			case "platform":
			case "transport":
				s, _ := m.MCP()
				s.Config["servers"].([]any)[0].(map[string]any)["transport"] = "sse"
				p, err := m.MCPPlan(ctx, MCPEdit{ExpectedSHA256: s.SHA256, Config: s.Config})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = m.ApplyMCP(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
					t.Fatal(err)
				}
			}
			refreshInstallLock(t, m, req)
			if kind == "platform" {
				s, _ := m.MCPLock(ctx)
				lock, _ := pluginlock.Parse([]byte(s.Document))
				originalPlatform := lock.Plugins[0].Platform
				lock.Plugins[0].Platform = "windows-amd64"
				if originalPlatform == "windows-amd64" {
					lock.Plugins[0].Platform = "linux-amd64"
				}
				p, err := m.MCPLockPlan(ctx, MCPLockEdit{ExpectedSHA256: s.SHA256, Document: string(jsonBytes(lock))})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = m.ApplyMCPLock(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
					t.Fatal(err)
				}
			}
			state, err := m.MCPInstall(ctx)
			if err != nil {
				t.Fatal(err)
			}
			edit := allowInstall(req.Name)
			edit.ExpectedSHA256 = state.SHA256
			_, err = m.MCPInstallPlan(ctx, edit)
			if err == nil {
				t.Fatal("invalid installation accepted", kind)
			}
			state, _ = m.MCPInstall(ctx)
			if len(state.Installations) != 0 {
				t.Fatal(state)
			}
		})
	}
}

func TestMCPInstallStaleRegistrationHistoricalSBOMAndDependencyRestoreGuard(t *testing.T) {
	m, req := installFixture(t)
	ctx := context.Background()
	installEdit(t, m, allowInstall(req.Name))
	original, _ := m.MCPInstalledSBOM(ctx, req.Name)
	removed := installEdit(t, m, plugininstall.Edit{Action: "remove", Name: req.Name})
	file := filepath.Join(m.Store.Local, filepath.FromSlash(req.ArtifactPath), ".hidden")
	before, _ := os.ReadFile(file)
	write(t, file, []byte("changed"))
	state, _ := m.MCPInstall(ctx)
	undo := plugininstall.Edit{Action: "rollback", Snapshot: removed.Plan.ID, ExpectedSHA256: state.SHA256, AllowUnverified: true, RiskReason: "核对恢复"}
	_, err := m.MCPInstallPlan(ctx, undo)
	code(t, err, "SNAPSHOT_DEPENDENCY_MISSING")
	write(t, file, before)
	installEdit(t, m, undo)
	write(t, file, []byte("changed again"))
	entry, err := m.MCPInstalledSBOM(ctx, req.Name)
	if err != nil || entry.Status != "STALE" || entry.SBOMSHA256 != original.SBOMSHA256 {
		t.Fatal(entry, err)
	}
	manifest, err := m.MCPManifest(ctx, req.Name)
	if err != nil || manifest.Plugins[0].InstallationState != "STALE" {
		t.Fatal(manifest, err)
	}
	installEdit(t, m, plugininstall.Edit{Action: "remove", Name: req.Name})
	state, _ = m.MCPInstall(ctx)
	if len(state.Installations) != 0 {
		t.Fatal("damaged artifact blocked removing registration")
	}
}

func TestMCPInstallRecoveryAfterProcessExit(t *testing.T) {
	if os.Getenv("AIDOCK_INSTALL_CRASH_HELPER") == "1" {
		var input struct{ Root, Local, Owner, Scope, Plan, Digest string }
		if err := json.Unmarshal([]byte(os.Getenv("AIDOCK_INSTALL_CRASH_INPUT")), &input); err != nil {
			t.Fatal(err)
		}
		m, err := New(input.Root, input.Local, input.Owner, input.Scope)
		if err != nil {
			t.Fatal(err)
		}
		err = m.withInstall(context.Background(), true, func(i *plugininstall.Manager, c []plugininstall.Candidate, hash string, _ []inventory.Finding) error {
			i.Store.Hook = func(_ string, n int) error {
				if n == 1 {
					os.Exit(94)
				}
				return nil
			}
			_, err := i.Apply(context.Background(), input.Plan, input.Digest, c, hash)
			return err
		})
		t.Fatal("helper failed to exit", err)
		return
	}
	m, req := installFixture(t)
	ctx := context.Background()
	state, _ := m.MCPInstall(ctx)
	edit := allowInstall(req.Name)
	edit.ExpectedSHA256 = state.SHA256
	p, err := m.MCPInstallPlan(ctx, edit)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"Root": m.Store.Root, "Local": m.Store.Local, "Owner": m.Owner, "Scope": m.Scope, "Plan": p.Plan.ID, "Digest": p.Plan.Digest})
	cmd := exec.Command(os.Args[0], "-test.run=^TestMCPInstallRecoveryAfterProcessExit$")
	cmd.Env = append(os.Environ(), "AIDOCK_INSTALL_CRASH_HELPER=1", "AIDOCK_INSTALL_CRASH_INPUT="+string(input))
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 94 {
		t.Fatalf("crash helper: %s %v", output, err)
	}
	fresh, err := New(m.Store.Root, m.Store.Local, m.Owner, m.Scope)
	if err != nil {
		t.Fatal(err)
	}
	state, err = fresh.MCPInstall(ctx)
	if err != nil || !state.Recovery {
		t.Fatal(state, err)
	}
	// Recovery remains available with an invalid portable lock, and never writes it.
	write(t, filepath.Join(m.Store.Root, "plugins.lock"), []byte("malformed: ["))
	n, err := fresh.RecoverMCPInstall(ctx)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	state, err = fresh.MCPInstall(ctx)
	if err != nil || state.Recovery || len(state.Installations) != 0 || state.AuditCount != 0 {
		t.Fatal(state, err)
	}
	b, _ := os.ReadFile(filepath.Join(m.Store.Root, "plugins.lock"))
	if string(b) != "malformed: [" {
		t.Fatal("recovery overwrote portable lock")
	}
}

func TestMCPInstallAuditTamperingAndUnknownContentAreDetected(t *testing.T) {
	m, req := installFixture(t)
	ctx := context.Background()
	installEdit(t, m, allowInstall(req.Name))
	i, _ := m.installManager()
	tree, err := i.Store.Capture(i.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	var auditPath string
	for path := range tree.Entries {
		if strings.HasPrefix(path, "audit/") {
			auditPath = path
			break
		}
	}
	file := filepath.Join(i.Store.Root, filepath.FromSlash(auditPath))
	original, _ := os.ReadFile(file)
	write(t, file, []byte("{}"))
	state, err := m.MCPInstall(ctx)
	if err != nil || len(state.Checks) == 0 {
		t.Fatal(state, err)
	}
	_, err = m.MCPInstalledSBOM(ctx, req.Name)
	code(t, err, "MCP_INSTALL_STORE_INVALID")
	write(t, file, original)
	// A valid older authority prefix cannot erase a committed audit anchor.
	installEdit(t, m, plugininstall.Edit{Action: "remove", Name: req.Name})
	newTree, _ := i.Store.Capture(i.Store.Root)
	for path := range newTree.Entries {
		if strings.HasPrefix(path, "audit/") && path != auditPath {
			if err = os.Remove(filepath.Join(i.Store.Root, filepath.FromSlash(path))); err != nil {
				t.Fatal(err)
			}
		}
	}
	state, err = m.MCPInstall(ctx)
	if err != nil || len(state.Checks) == 0 {
		t.Fatal("truncated audit accepted", state, err)
	}
}

func TestMCPInstallRehashedPlanCannotSmuggleOtherFiles(t *testing.T) {
	m, req := installFixture(t)
	ctx := context.Background()
	state, _ := m.MCPInstall(ctx)
	edit := allowInstall(req.Name)
	edit.ExpectedSHA256 = state.SHA256
	p, err := m.MCPInstallPlan(ctx, edit)
	if err != nil {
		t.Fatal(err)
	}
	i, _ := m.installManager()
	forged := workstore.Clone(p.Plan.After)
	if err = i.Store.PutFile(&forged, "unowned.txt", []byte("must not write"), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := i.Store.PreviewBound(p.Plan.Action, p.Plan.Before, forged, p.Plan.Inputs)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.ApplyMCPInstall(ctx, plan.ID, plan.Digest)
	code(t, err, "INVALID_INSTALL_PLAN")
	state, _ = m.MCPInstall(ctx)
	if len(state.Installations) != 0 {
		t.Fatal(state)
	}
}
