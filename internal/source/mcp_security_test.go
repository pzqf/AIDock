package source

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/pluginsecurity"
	"github.com/pzqf/AIDock/internal/workstore"
)

func securityDocument(t *testing.T, m *Manager) string {
	t.Helper()
	s, err := m.MCPSecurity(context.Background())
	if err != nil || len(s.Subjects) != 1 {
		t.Fatal(s, err)
	}
	v := s.Subjects[0]
	v.Provider = "Isolated fixture reference"
	v.ReferenceURL = "https://example.invalid/isolated-reference"
	for i := range v.Packages {
		a := []pluginsecurity.Advisory{}
		v.Packages[i].Advisories = &a
	}
	return string(pluginsecurity.JSON(v))
}
func securitySave(t *testing.T, m *Manager, r pluginsecurity.Edit) pluginsecurity.Preview {
	t.Helper()
	ctx := context.Background()
	s, err := m.MCPSecurity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.ExpectedSHA256 = s.SHA256
	p, err := m.MCPSecurityPlan(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPSecurity(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestMCPSecurityPreviewSaveRestartRollbackUndoAndBytes(t *testing.T) {
	m, req := installFixture(t)
	ctx := context.Background()
	before, _ := m.Store.Capture(m.Store.Root)
	s, err := m.MCPSecurity(ctx)
	if err != nil || s.Results[0].CVEStatus != "MISSING" {
		t.Fatal(s, err)
	}
	doc := "\ufeff# preserve raw reference\r\n" + securityDocument(t, m) + "\r\n"
	p, err := m.MCPSecurityPlan(ctx, pluginsecurity.Edit{Action: "import", ExpectedSHA256: s.SHA256, Document: doc})
	if err != nil {
		t.Fatal(err)
	}
	if p.Candidate.Results[0].CVEStatus != "NO_KNOWN_MATCHES" {
		t.Fatal(p)
	}
	s, _ = m.MCPSecurity(ctx)
	if s.Results[0].CVEStatus != "MISSING" {
		t.Fatal("preview wrote cache")
	}
	_, err = m.ApplyMCPSecurity(ctx, p.Plan.ID, "wrong")
	code(t, err, "PLAN_STALE")
	if _, err = m.ApplyMCPSecurity(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	_, err = m.ApplyMCPSecurity(ctx, p.Plan.ID, p.Plan.Digest)
	code(t, err, "PLAN_CONSUMED")
	fresh, _ := New(m.Store.Root, m.Store.Local, m.Owner, m.Scope)
	s, err = fresh.MCPSecurity(ctx)
	if err != nil || s.Results[0].CVEStatus != "NO_KNOWN_MATCHES" || s.Results[0].TrustLevel != "L0" || s.Documents[s.Results[0].SubjectSHA256] != doc || s.ExecutionReady {
		t.Fatal(s, err)
	}
	rollback := securitySave(t, fresh, pluginsecurity.Edit{Action: "rollback", Snapshot: p.Plan.ID})
	s, _ = fresh.MCPSecurity(ctx)
	if s.Results[0].CVEStatus != "MISSING" {
		t.Fatal(s)
	}
	securitySave(t, fresh, pluginsecurity.Edit{Action: "rollback", Snapshot: rollback.Plan.ID})
	s, _ = fresh.MCPSecurity(ctx)
	if s.Results[0].CVEStatus != "NO_KNOWN_MATCHES" || len(s.History) != 3 {
		t.Fatal(s)
	}
	other, _ := New(m.Store.Root, filepath.Join(t.TempDir(), "other-home"), m.Owner, m.Scope)
	os.MkdirAll(other.Store.Local, 0700)
	otherState, _ := other.MCPSecurity(ctx)
	if len(otherState.Documents) != 0 {
		t.Fatal("cache crossed home")
	}
	after, _ := m.Store.Capture(m.Store.Root)
	if workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("cache wrote portable source")
	}
	if _, err = os.Stat(filepath.Join(m.Store.Local, filepath.FromSlash(req.ArtifactPath), "runtime", "helper")); err != nil {
		t.Fatal(err)
	}
}
func TestMCPSecurityKnownRiskBlocksInstallAndInvalidatesOldConfirmation(t *testing.T) {
	m, req := installFixture(t)
	ctx := context.Background()
	s, _ := m.MCPInstall(ctx)
	r := allowInstall(req.Name)
	r.ExpectedSHA256 = s.SHA256
	old, err := m.MCPInstallPlan(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	var snap pluginsecurity.Snapshot
	json.Unmarshal([]byte(securityDocument(t, m)), &snap)
	advisories := []pluginsecurity.Advisory{{ID: "CVE-2026-54321", Severity: "critical", Summary: "Test only finding", URL: "https://example.invalid/advisory"}}
	snap.Packages[0].Advisories = &advisories
	securitySave(t, m, pluginsecurity.Edit{Action: "import", Document: string(pluginsecurity.JSON(snap))})
	state, _ := m.MCPSecurity(ctx)
	if state.Results[0].Status != "FAIL" || state.Results[0].AdvisoryCount != 1 {
		t.Fatal(state)
	}
	_, err = m.ApplyMCPInstall(ctx, old.Plan.ID, old.Plan.Digest)
	code(t, err, "PLAN_STALE")
	s, _ = m.MCPInstall(ctx)
	r.ExpectedSHA256 = s.SHA256
	_, err = m.MCPInstallPlan(ctx, r)
	code(t, err, "INSTALL_CHECK_FAILED")
	lock, _ := m.MCPLock(ctx)
	if lock.Plugins[0].Status != "FAIL" {
		t.Fatal(lock)
	}
	// Expiry is not a remedy for a recorded risk.
	snap.CollectedAt = time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	snap.ExpiresAt = time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	securitySave(t, m, pluginsecurity.Edit{Action: "import", Document: string(pluginsecurity.JSON(snap))})
	state, _ = m.MCPSecurity(ctx)
	if state.Results[0].Status != "FAIL" || state.Results[0].Current {
		t.Fatal(state)
	}
}
func TestMCPSecurityBindingSourceArtifactExpiryAndTamperGuards(t *testing.T) {
	m, req := installFixture(t)
	ctx := context.Background()
	doc := securityDocument(t, m)
	s, _ := m.MCPSecurity(ctx)
	p, err := m.MCPSecurityPlan(ctx, pluginsecurity.Edit{Action: "import", ExpectedSHA256: s.SHA256, Document: doc})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(m.Store.Local, filepath.FromSlash(req.ArtifactPath), ".hidden")
	write(t, file, []byte("external change"))
	_, err = m.ApplyMCPSecurity(ctx, p.Plan.ID, p.Plan.Digest)
	code(t, err, "SECURITY_REFERENCE_UNBOUND")
	write(t, file, []byte("preserve"))
	var snap pluginsecurity.Snapshot
	json.Unmarshal([]byte(doc), &snap)
	snap.DefinitionSHA256 = workstore.Hash([]byte("foreign"))
	_, err = m.MCPSecurityPlan(ctx, pluginsecurity.Edit{Action: "import", ExpectedSHA256: s.SHA256, Document: string(pluginsecurity.JSON(snap))})
	code(t, err, "SECURITY_REFERENCE_UNBOUND")
	saved := securitySave(t, m, pluginsecurity.Edit{Action: "import", Document: doc})
	state, _ := m.MCPSecurity(ctx)
	path := filepath.Join(state.Root, "reports", state.Results[0].SubjectSHA256+".json")
	json.Unmarshal([]byte(doc), &snap)
	snap.Provider = "Externally edited"
	write(t, path, pluginsecurity.JSON(snap))
	state, _ = m.MCPSecurity(ctx)
	if len(state.Results) != 0 || len(state.Checks) == 0 {
		t.Fatal("cache external change not detected", state)
	}
	_, err = m.MCPInstallPlan(ctx, allowInstall(req.Name))
	if err == nil {
		t.Fatal("invalid cache allowed installation")
	}
	securitySave(t, m, pluginsecurity.Edit{Action: "rollback", Snapshot: saved.Plan.ID})
	state, _ = m.MCPSecurity(ctx)
	if len(state.Checks) != 0 || state.Results[0].CVEStatus != "MISSING" {
		t.Fatal(state)
	}
}
func TestMCPSecurityConfirmRechecksTTLAndCannotSmuggleFiles(t *testing.T) {
	m, _ := installFixture(t)
	ctx := context.Background()
	err := m.withSecurity(ctx, true, func(i *pluginsecurity.Manager, c []pluginsecurity.Candidate, h string, _ []inventory.Finding) error {
		now := time.Now().UTC()
		i.Now = func() time.Time { return now }
		s := pluginsecurity.Template(c[0], now.Add(-time.Minute))
		s.Provider = "Fixture"
		s.ReferenceURL = "https://example.invalid/reference"
		s.ExpiresAt = now.Add(time.Minute).Format(time.RFC3339)
		for idx := range s.Packages {
			a := []pluginsecurity.Advisory{}
			s.Packages[idx].Advisories = &a
		}
		state, _ := i.State(c)
		p, err := i.Plan(ctx, pluginsecurity.Edit{Action: "import", ExpectedSHA256: state.SHA256, Document: string(pluginsecurity.JSON(s))}, c, h)
		if err != nil {
			return err
		}
		i.Now = func() time.Time { return now.Add(2 * time.Minute) }
		_, err = i.Apply(ctx, p.Plan.ID, p.Plan.Digest, c, h)
		code(t, err, "PLAN_STALE")
		i.Now = func() time.Time { return now }
		if err = i.Store.PutFile(&p.Plan.After, "foreign.json", []byte("smuggled"), 0600); err != nil {
			return err
		}
		forged, err := i.Store.PreviewBound(p.Plan.Action, p.Plan.Before, p.Plan.After, p.Plan.Inputs)
		if err != nil {
			return err
		}
		_, err = i.Apply(ctx, forged.ID, forged.Digest, c, h)
		code(t, err, "INVALID_SECURITY_PLAN")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestMCPSecurityRecoveryAfterProcessExit(t *testing.T) {
	if os.Getenv("AIDOCK_SECURITY_CRASH_HELPER") == "1" {
		var input struct{ Root, Local, Owner, Scope, Plan, Digest string }
		json.Unmarshal([]byte(os.Getenv("AIDOCK_SECURITY_CRASH_INPUT")), &input)
		m, err := New(input.Root, input.Local, input.Owner, input.Scope)
		if err != nil {
			t.Fatal(err)
		}
		err = m.withSecurity(context.Background(), true, func(i *pluginsecurity.Manager, c []pluginsecurity.Candidate, h string, _ []inventory.Finding) error {
			i.Store.Hook = func(_ string, n int) error {
				if n == 1 {
					os.Exit(94)
				}
				return nil
			}
			_, err := i.Apply(context.Background(), input.Plan, input.Digest, c, h)
			return err
		})
		t.Fatal("helper failed to exit", err)
		return
	}
	m, _ := installFixture(t)
	ctx := context.Background()
	s, _ := m.MCPSecurity(ctx)
	p, err := m.MCPSecurityPlan(ctx, pluginsecurity.Edit{Action: "import", ExpectedSHA256: s.SHA256, Document: securityDocument(t, m)})
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"Root": m.Store.Root, "Local": m.Store.Local, "Owner": m.Owner, "Scope": m.Scope, "Plan": p.Plan.ID, "Digest": p.Plan.Digest})
	cmd := exec.Command(os.Args[0], "-test.run=^TestMCPSecurityRecoveryAfterProcessExit$")
	cmd.Env = append(os.Environ(), "AIDOCK_SECURITY_CRASH_HELPER=1", "AIDOCK_SECURITY_CRASH_INPUT="+string(input))
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 94 {
		t.Fatal(string(output), err)
	}
	fresh, _ := New(m.Store.Root, m.Store.Local, m.Owner, m.Scope)
	state, err := fresh.MCPSecurity(ctx)
	if err != nil || !state.Recovery {
		t.Fatal(state, err)
	}
	write(t, filepath.Join(m.Store.Root, "plugins.lock"), []byte("invalid: ["))
	n, err := fresh.RecoverMCPSecurity(ctx)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	b, _ := os.ReadFile(filepath.Join(m.Store.Root, "plugins.lock"))
	if string(b) != "invalid: [" {
		t.Fatal("recovery changed portable lock")
	}
}
