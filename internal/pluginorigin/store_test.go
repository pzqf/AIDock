package pluginorigin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

func originStore(t *testing.T) *Manager {
	t.Helper()
	d := t.TempDir()
	local := filepath.Join(d, "local")
	if err := os.MkdirAll(local, 0755); err != nil {
		t.Fatal(err)
	}
	m, err := New(local, Binding{1, "project", "server", filepath.Join(d, "source")})
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func errorCode(t *testing.T, err error, want string) {
	t.Helper()
	var e *result.Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("want %s: %v", want, err)
	}
}

var sourceHash = workstore.Hash([]byte("source bytes"))

func saveOrigin(t *testing.T, m *Manager, req Edit, plugins []pluginlock.Plugin) Preview {
	t.Helper()
	s, err := m.State(plugins)
	if err != nil {
		t.Fatal(err)
	}
	req.ExpectedSHA256 = s.SHA256
	p, err := m.Plan(context.Background(), req, plugins, sourceHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest, plugins, sourceHash); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOriginStorePreviewSaveRestartRollbackUndoKeepAuditAndExactEvidence(t *testing.T) {
	m := originStore(t)
	p, keys, e, _, now := fixture(t)
	m.Now = func() time.Time { return now }
	plugins := []pluginlock.Plugin{p}
	initial, _ := m.State(plugins)
	plan, err := m.Plan(context.Background(), Edit{Action: "keys", ExpectedSHA256: initial.SHA256, Keys: &keys}, plugins, sourceHash)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := m.State(plugins); s.AuditCount != 0 || len(s.Keys.Keys) != 0 {
		t.Fatal("preview wrote authority", s)
	}
	_, err = m.Apply(context.Background(), plan.Plan.ID, "", plugins, sourceHash)
	errorCode(t, err, "INVALID_INPUT")
	_, err = m.Apply(context.Background(), plan.Plan.ID, "wrong", plugins, sourceHash)
	errorCode(t, err, "PLAN_STALE")
	if _, err = m.Apply(context.Background(), plan.Plan.ID, plan.Plan.Digest, plugins, sourceHash); err != nil {
		t.Fatal(err)
	}
	document := "\ufeff# original exact statement\r\n" + string(JSON(e)) + "\r\n"
	added := saveOrigin(t, m, Edit{Action: "evidence", Evidence: document}, plugins)
	fresh, err := New(filepath.Dir(m.Store.Root), m.Binding)
	if err != nil {
		t.Fatal(err)
	}
	fresh.Now = m.Now
	if s, err := fresh.State(plugins); err != nil || s.Results[0].Status != "PASS" || s.AuditCount != 2 || s.ExecutionReady {
		t.Fatal(s, err)
	}
	got, err := fresh.Show(For(p).SHA256)
	if err != nil || got != document {
		t.Fatal("original bytes lost", err)
	}
	rolled := saveOrigin(t, fresh, Edit{Action: "rollback", Snapshot: added.Plan.ID}, plugins)
	if s, _ := fresh.State(plugins); len(s.Reports) != 0 || s.AuditCount != 3 {
		t.Fatal(s)
	}
	saveOrigin(t, fresh, Edit{Action: "rollback", Snapshot: rolled.Plan.ID}, plugins)
	if s, _ := fresh.State(plugins); s.Results[0].Status != "PASS" || s.AuditCount != 4 {
		t.Fatal(s)
	}
	got, _ = fresh.Show(For(p).SHA256)
	if got != document {
		t.Fatal("undo changed evidence bytes")
	}
	keys.Keys[0].Enabled = false
	saveOrigin(t, fresh, Edit{Action: "keys", Keys: &keys}, plugins)
	if s, _ := fresh.State(plugins); s.Results[0].Status != "FAIL" || s.AuditCount != 5 {
		t.Fatal(s)
	}
}

func TestOriginStoreDriftExpiryBindingAndUnknownFiles(t *testing.T) {
	for _, kind := range []string{"source", "expiry", "authority", "binding", "unknown", "audit"} {
		t.Run(kind, func(t *testing.T) {
			m := originStore(t)
			p, keys, e, _, now := fixture(t)
			m.Now = func() time.Time { return now }
			plugins := []pluginlock.Plugin{p}
			first := saveOrigin(t, m, Edit{Action: "keys", Keys: &keys}, plugins)
			s, _ := m.State(plugins)
			plan, err := m.Plan(context.Background(), Edit{Action: "evidence", ExpectedSHA256: s.SHA256, Evidence: string(JSON(e))}, plugins, sourceHash)
			if err != nil {
				t.Fatal(err)
			}
			hash := sourceHash
			switch kind {
			case "source":
				hash = workstore.Hash([]byte("changed source"))
			case "expiry":
				m.Now = func() time.Time { return now.Add(3 * time.Hour) }
			case "authority":
				if err = os.WriteFile(filepath.Join(m.Store.Root, "keys.json"), []byte("broken ["), 0600); err != nil {
					t.Fatal(err)
				}
			case "binding":
				if err = os.WriteFile(filepath.Join(m.Store.Root, "binding.json"), JSON(Binding{1, "another", "server", m.Binding.SourceRoot}), 0600); err != nil {
					t.Fatal(err)
				}
			case "unknown":
				if err = os.WriteFile(filepath.Join(m.Store.Root, "user-notes.txt"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			case "audit":
				files, _ := os.ReadDir(filepath.Join(m.Store.Root, "audit"))
				if err = os.WriteFile(filepath.Join(m.Store.Root, "audit", files[0].Name()), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err = m.Apply(context.Background(), plan.Plan.ID, plan.Plan.Digest, plugins, hash)
			errorCode(t, err, "PLAN_STALE")
			if kind == "authority" {
				s, _ = m.State(plugins)
				if s.Results[0].Status != "FAIL" || len(s.History) != 1 {
					t.Fatal(s)
				}
				saveOrigin(t, m, Edit{Action: "rollback", Snapshot: first.Plan.ID}, plugins)
				s, _ = m.State(plugins)
				if len(s.Checks) != 0 || s.AuditCount != 2 {
					t.Fatal(s)
				}
			}
			if kind == "binding" || kind == "unknown" || kind == "audit" {
				s, _ = m.State(plugins)
				_, err = m.Plan(context.Background(), Edit{Action: "rollback", Snapshot: first.Plan.ID, ExpectedSHA256: s.SHA256}, plugins, sourceHash)
				if err == nil {
					t.Fatal("damaged ownership/chain was auto overwritten")
				}
			}
		})
	}
}

func TestOriginStoreProjectAndLocalRootDoNotShareTrust(t *testing.T) {
	m := originStore(t)
	p, keys, e, _, now := fixture(t)
	m.Now = func() time.Time { return now }
	plugins := []pluginlock.Plugin{p}
	saveOrigin(t, m, Edit{Action: "keys", Keys: &keys}, plugins)
	saveOrigin(t, m, Edit{Action: "evidence", Evidence: string(JSON(e))}, plugins)
	for _, kind := range []string{"project", "scope", "source", "local"} {
		t.Run(kind, func(t *testing.T) {
			b := m.Binding
			local := filepath.Dir(m.Store.Root)
			switch kind {
			case "project":
				b.Project = "other"
			case "scope":
				b.Scope = "client"
			case "source":
				b.SourceRoot = filepath.Join(filepath.Dir(b.SourceRoot), "copy")
			case "local":
				local = t.TempDir()
			}
			other, err := New(local, b)
			if err != nil {
				t.Fatal(err)
			}
			s, err := other.State(plugins)
			if err != nil || len(s.Keys.Keys) != 0 || s.Results[0].Status == "PASS" {
				t.Fatal(s, err)
			}
		})
	}
}

func TestOriginStoreInterruptedSaveRecoveryConflictAndMutationValidation(t *testing.T) {
	m := originStore(t)
	p, keys, _, _, now := fixture(t)
	m.Now = func() time.Time { return now }
	plugins := []pluginlock.Plugin{p}
	s, _ := m.State(plugins)
	plan, err := m.Plan(context.Background(), Edit{Action: "keys", Keys: &keys, ExpectedSHA256: s.SHA256}, plugins, sourceHash)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate process death, so Apply's ordinary error recovery cannot run.
	m.Store.Hook = func(string, int) error { panic("power loss") }
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("missing interruption")
			}
		}()
		m.Apply(context.Background(), plan.Plan.ID, plan.Plan.Digest, plugins, sourceHash)
	}()
	m.Store.Hook = nil
	s, err = m.State(plugins)
	if err != nil || !s.Recovery || s.Results[0].Status != "FAIL" {
		t.Fatal(s, err)
	}
	n, err := m.Recover()
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	s, _ = m.State(plugins)
	if s.Recovery || s.AuditCount != 0 || len(s.Keys.Keys) != 0 {
		t.Fatal(s)
	}
	plan = saveOrigin(t, m, Edit{Action: "keys", Keys: &keys}, plugins)
	bad := plan.Plan
	bad.Inputs = map[string]string{}
	for k, v := range plan.Plan.Inputs {
		bad.Inputs[k] = v
	}
	bad.Inputs["target"] = "reports/" + For(p).SHA256 + ".json"
	if err = m.validateChanges(bad); err == nil {
		t.Fatal("key edit accepted as report edit")
	}
	if err = os.WriteFile(filepath.Join(m.Store.Root, "notes.txt"), []byte("keep user work"), 0600); err != nil {
		t.Fatal(err)
	}
	s, _ = m.State(plugins)
	_, err = m.Plan(context.Background(), Edit{Action: "keys", Keys: &keys, ExpectedSHA256: s.SHA256}, plugins, sourceHash)
	errorCode(t, err, "ORIGIN_CONTENT_UNKNOWN")
}

func TestOriginAuditCannotBeTruncatedToAnOlderCommittedPrefix(t *testing.T) {
	m := originStore(t)
	p, keys, e, _, now := fixture(t)
	m.Now = func() time.Time { return now }
	plugins := []pluginlock.Plugin{p}
	first := saveOrigin(t, m, Edit{Action: "keys", Keys: &keys}, plugins)
	saveOrigin(t, m, Edit{Action: "evidence", Evidence: string(JSON(e))}, plugins)
	files, err := os.ReadDir(filepath.Join(m.Store.Root, "audit"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(m.Store.Root, "audit", files[len(files)-1].Name())); err != nil {
		t.Fatal(err)
	}
	s, err := m.State(plugins)
	if err != nil || s.Results[0].Status != "FAIL" {
		t.Fatal(s, err)
	}
	_, err = m.Plan(context.Background(), Edit{Action: "rollback", Snapshot: first.Plan.ID, ExpectedSHA256: s.SHA256}, plugins, sourceHash)
	errorCode(t, err, "ORIGIN_AUDIT_INVALID")
}

func TestOriginRecoveryPreservesExternalEditsAndResumesAfterConflictResolved(t *testing.T) {
	m := originStore(t)
	p, keys, _, _, now := fixture(t)
	m.Now = func() time.Time { return now }
	plugins := []pluginlock.Plugin{p}
	saveOrigin(t, m, Edit{Action: "keys", Keys: &keys}, plugins)
	keys.Keys[0].Enabled = false
	s, _ := m.State(plugins)
	plan, err := m.Plan(context.Background(), Edit{Action: "keys", Keys: &keys, ExpectedSHA256: s.SHA256}, plugins, sourceHash)
	if err != nil {
		t.Fatal(err)
	}
	m.Store.Hook = func(_ string, count int) error {
		if count == 2 {
			panic("power loss after keys")
		}
		return nil
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("missing interruption")
			}
		}()
		m.Apply(context.Background(), plan.Plan.ID, plan.Plan.Digest, plugins, sourceHash)
	}()
	m.Store.Hook = nil
	keyPath := filepath.Join(m.Store.Root, "keys.json")
	external := []byte("# externally edited, keep\ninvalid: [")
	if err = os.WriteFile(keyPath, external, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = m.Recover()
	errorCode(t, err, "RECOVERY_CONFLICT")
	var recoveryError *result.Error
	if !errors.As(err, &recoveryError) || strings.Contains(err.Error(), "skill recover") || recoveryError.Recovery.(map[string]string)["command"] != "mcp origin recover" {
		t.Fatal("wrong recovery command", err)
	}
	b, _ := os.ReadFile(keyPath)
	if string(b) != string(external) {
		t.Fatal("recovery overwrote external edit")
	}
	approved, err := m.Store.Data(plan.Plan.After.Entries["keys.json"])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyPath, approved, 0600); err != nil {
		t.Fatal(err)
	}
	n, err := m.Recover()
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	s, err = m.State(plugins)
	if err != nil || s.Recovery || !s.Keys.Keys[0].Enabled || s.AuditCount != 1 {
		t.Fatal(s, err)
	}
}
