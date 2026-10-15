package runtimecheck

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/workstore"
)

func fixture(t *testing.T) (*Manager, Input) {
	t.Helper()
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "local"), 0755); err != nil {
		t.Fatal(err)
	}
	m, err := New(filepath.Join(base, "local"), Binding{Version: 1, Project: "test-project", Scope: "server", Copy: "test-copy", SourceRoot: filepath.Join(base, "project", ".aidock")})
	if err != nil {
		t.Fatal(err)
	}
	return m, Input{Name: "fixture", AuthorizationID: workstore.ID(), AuthorizationSHA256: workstore.Hash([]byte("approval")), InputSHA256: workstore.Hash([]byte("input")), Runtime: pluginpermission.UnisolatedInput{SourceType: "local", Kind: "node", Version: "1.2.3", Executable: "runtime/node.exe", SHA256: workstore.Hash([]byte("runtime"))}}
}
func TestIntentPrecedesInvocationAndResultRequiresCurrentExactInput(t *testing.T) {
	m, input := fixture(t)
	calls := 0
	e, err := m.Probe(context.Background(), input, func() (string, string) {
		calls++
		s, err := m.State([]Input{input})
		if err != nil || s.AuditCount != 1 || s.Entries[0].Status != "INCOMPLETE" {
			t.Fatal(s, err)
		}
		return "RUNTIME_VERSION_MATCH", "v1.2.3"
	})
	if err != nil || e.Code != "RUNTIME_VERSION_MATCH" || calls != 1 {
		t.Fatal(e, err, calls)
	}
	s, err := m.State([]Input{input})
	if err != nil || s.AuditCount != 2 || len(s.Entries) != 1 || !s.Entries[0].Current || s.Entries[0].Status != "PASS" {
		t.Fatal(s, err)
	}
	fresh, err := New(filepath.Dir(m.Store.Root), m.Binding)
	if err != nil {
		t.Fatal(err)
	}
	input.AuthorizationID = workstore.ID()
	s, err = fresh.State([]Input{input})
	if err != nil || s.Entries[0].Current {
		t.Fatal(s, err)
	}
	otherBinding := m.Binding
	otherBinding.Copy = "other-copy"
	other, err := New(filepath.Dir(m.Store.Root), otherBinding)
	if err != nil {
		t.Fatal(err)
	}
	s, err = other.State([]Input{input})
	if err != nil || s.AuditCount != 0 {
		t.Fatal(s, err)
	}
}
func TestFailedAuditNeverExecutesAndCallbackDiagnosticsAreNotPersisted(t *testing.T) {
	m, input := fixture(t)
	calls := 0
	m.Store.Hook = func(string, int) error { return errors.New("disk fixture failure") }
	_, err := m.Probe(context.Background(), input, func() (string, string) { calls++; return "RUNTIME_VERSION_MATCH", "v1.2.3" })
	if err == nil || calls != 0 {
		t.Fatal(err, calls)
	}
	m.Store.Hook = nil
	for _, kind := range []string{"panic", "untrusted-diagnostic"} {
		_, err = m.Probe(context.Background(), input, func() (string, string) {
			if kind == "panic" {
				panic("private-sentinel")
			}
			return "private-sentinel", "private-sentinel"
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	s, err := m.State([]Input{input})
	if err != nil || s.AuditCount != 4 {
		t.Fatal(s, err)
	}
	for _, e := range s.Entries {
		if e.Event.Code != "RUNTIME_CHECK_FAILED" || strings.Contains(string(bytesOf(e)), "private-sentinel") {
			t.Fatal(e)
		}
	}
	tree, err := m.Store.Capture(m.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	for p, e := range tree.Entries {
		if strings.HasPrefix(p, "audit/") {
			b, err := m.Store.Data(e)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), "private-sentinel") {
				t.Fatal("diagnostic escaped to disk")
			}
		}
	}
}
func TestAuditTamperAndTruncationBlockSubsequentExecution(t *testing.T) {
	for _, kind := range []string{"tamper", "truncate"} {
		t.Run(kind, func(t *testing.T) {
			m, input := fixture(t)
			_, err := m.Probe(context.Background(), input, func() (string, string) { return "RUNTIME_VERSION_MATCH", "v1.2.3" })
			if err != nil {
				t.Fatal(err)
			}
			files, err := os.ReadDir(filepath.Join(m.Store.Root, "audit"))
			if err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(m.Store.Root, "audit", files[len(files)-1].Name())
			if kind == "tamper" {
				err = os.WriteFile(p, []byte("{}"), 0600)
			} else {
				err = os.Remove(p)
			}
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			_, err = m.Probe(context.Background(), input, func() (string, string) { calls++; return "RUNTIME_VERSION_MATCH", "v1.2.3" })
			if err == nil || calls != 0 {
				t.Fatal(err, calls)
			}
		})
	}
}
func TestRuntimeCanceledBeforeIntentAndRejectedUntrustedInput(t *testing.T) {
	m, input := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	_, err := m.Probe(ctx, input, func() (string, string) { calls++; return "RUNTIME_VERSION_MATCH", "v1.2.3" })
	if err == nil || calls != 0 {
		t.Fatal(err, calls)
	}
	input.Runtime.SourceType = "npm"
	_, err = m.Probe(context.Background(), input, func() (string, string) { calls++; return "RUNTIME_VERSION_MATCH", "v1.2.3" })
	if err == nil || calls != 0 {
		t.Fatal(err, calls)
	}
	s, err := m.State(nil)
	if err != nil || s.AuditCount != 0 {
		t.Fatal(s, err)
	}
}
func TestRuntimeAuditRecoversProcessInterruptionWithoutReplaying(t *testing.T) {
	if raw := os.Getenv("AIDOCK_RUNTIME_AUDIT_HELPER"); raw != "" {
		var args struct {
			Local   string
			Binding Binding
			Input   Input
			Phase   string
		}
		if json.Unmarshal([]byte(raw), &args) != nil {
			os.Exit(31)
		}
		m, err := New(args.Local, args.Binding)
		if err != nil {
			os.Exit(32)
		}
		if args.Phase == "intent" {
			m.Store.Hook = func(_ string, n int) error {
				if n == 1 {
					os.Exit(94)
				}
				return nil
			}
		}
		_, err = m.Probe(context.Background(), args.Input, func() (string, string) {
			if args.Phase == "after-intent" {
				os.Exit(95)
			}
			m.Store.Hook = func(_ string, n int) error {
				if n == 1 {
					os.Exit(96)
				}
				return nil
			}
			return "RUNTIME_VERSION_MATCH", "v1.2.3"
		})
		t.Fatal("helper returned", err)
		return
	}
	for _, phase := range []string{"intent", "after-intent", "complete"} {
		t.Run(phase, func(t *testing.T) {
			m, input := fixture(t)
			raw, _ := json.Marshal(map[string]any{"Local": filepath.Dir(m.Store.Root), "Binding": m.Binding, "Input": input, "Phase": phase})
			cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeAuditRecoversProcessInterruptionWithoutReplaying$")
			cmd.Env = append(os.Environ(), "AIDOCK_RUNTIME_AUDIT_HELPER="+string(raw))
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() < 94 || exit.ExitCode() > 96 {
				t.Fatalf("helper %v %s", err, out)
			}
			fresh, err := New(filepath.Dir(m.Store.Root), m.Binding)
			if err != nil {
				t.Fatal(err)
			}
			n, err := fresh.Recover()
			if err != nil || (phase == "after-intent" && n != 0) || (phase != "after-intent" && n != 1) {
				t.Fatal(n, err)
			}
			s, err := fresh.State([]Input{input})
			if err != nil {
				t.Fatal(err)
			}
			if phase == "intent" {
				if s.AuditCount != 0 {
					t.Fatal(s)
				}
			} else if s.AuditCount != 1 || s.Entries[0].Status != "INCOMPLETE" {
				t.Fatal(s)
			}
		})
	}
}
