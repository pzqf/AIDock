//go:build windows

package isolation

import (
	"context"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsActualBoundaryAndCleanup(t *testing.T) {
	local := filepath.Join(t.TempDir(), "local")
	m, err := New(local)
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.Check(context.Background(), true)
	for _, f := range r.Findings {
		t.Log(f.Code, f.Status, f.Detail)
	}
	if err != nil || r.Status == "FAIL" || !r.Cleanup || r.GeneratorReady {
		t.Fatalf("boundary failed: %+v %v", r, err)
	}
	// Exact refusal is required for each network row. Windows can silently
	// drop loopback/UDP; those observations must remain UNKNOWN, never PASS.
	passed := map[string]bool{}
	for _, f := range r.Findings {
		passed[f.Code] = f.Status == "PASS"
	}
	for _, code := range []string{"APPCONTAINER_TOKEN", "DECLARED_INPUT_READ", "DECLARED_OUTPUT_WRITE", "READ_ONLY_INPUT", "READ_ONLY_BINARY", "PROJECT_READ_DENIED", "PROJECT_WRITE_DENIED", "LOCAL_READ_DENIED", "LOCAL_WRITE_DENIED", "JUNCTION_READ_DENIED", "JUNCTION_WRITE_DENIED", "CHILD_PROCESS_DENIED", "HOST_OUTPUT_VERIFIED", "HOST_SENTINELS_UNCHANGED", "TIMEOUT_JOB_CLEANUP", "FIXTURE_CLEANUP"} {
		if !passed[code] {
			t.Fatal("required fixed fixture evidence missing", code)
		}
	}
	entries, err := os.ReadDir(filepath.Join(local, "isolation-probes"))
	if err != nil || len(entries) != 0 {
		t.Fatal("probe leaked", entries, err)
	}
	s, err := m.Latest()
	if err != nil || s == nil || s.Stale || s.Status != r.Status {
		t.Fatal(s, err)
	}
}

func init() {
	if len(os.Args) == 3 && os.Args[1] == "__test_isolation_crash" {
		afterResume = func() { time.Sleep(100 * time.Millisecond); os.Exit(92) }
		m, err := New(os.Args[2])
		if err != nil {
			os.Exit(94)
		}
		_, _ = m.Check(context.Background(), true)
		os.Exit(95)
	}
}
func TestWindowsAbruptParentExitCanRecover(t *testing.T) {
	local := filepath.Join(t.TempDir(), "local")
	exe, _ := os.Executable()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := exec.CommandContext(ctx, exe, "__test_isolation_crash", local).Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 92 {
		t.Fatal("did not interrupt live AppContainer parent", err)
	}
	m, _ := New(local)
	rows, err := m.Pending()
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	started, err := regularFile(filepath.Join(rows[0].Root, "stage", "output", "check-started"))
	if err != nil || string(started) != "started" {
		t.Fatal("interrupted before child ran", err)
	}
	if latest, err := m.Latest(); err != nil || latest != nil {
		t.Fatal("abrupt run saved evidence", latest, err)
	}
	// A new process may only clean the protocol-owned staging resources.
	for i := 0; i < 50; i++ {
		n, err := m.Recover()
		if err == nil {
			if n != 1 {
				t.Fatal(n)
			}
			break
		}
		if i == 49 {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	rows, err = m.Pending()
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	if n, err := m.Recover(); err != nil || n != 0 {
		t.Fatal("recover not idempotent", n, err)
	}
}

func TestWindowsCancellationCleansFixtureWithoutEvidence(t *testing.T) {
	local := filepath.Join(t.TempDir(), "local")
	m, _ := New(local)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.run = func(ctx context.Context, local string, platform Platform) ([]Finding, bool) {
		timer := time.AfterFunc(300*time.Millisecond, cancel)
		defer timer.Stop()
		return probePlatform(ctx, local, platform)
	}
	r, err := m.Check(ctx, true)
	if err != context.Canceled || !r.Cleanup {
		t.Fatal("cancel did not clean", r, err)
	}
	if saved, e := m.Latest(); e != nil || saved != nil {
		t.Fatal("cancel saved evidence", saved, e)
	}
	entries, e := os.ReadDir(filepath.Join(local, "isolation-probes"))
	if e != nil || len(entries) != 0 {
		t.Fatal("cancel leaked fixture", entries, e)
	}
}

func TestWindowsRecoveryPreservesMarkerOnLockedFileAndRefusesExtraContent(t *testing.T) {
	local := filepath.Join(t.TempDir(), "local")
	m, _ := New(local)
	id := "d26f53ce-62ce-4d6b-b25a-1cbdca310bb0"
	root := filepath.Join(local, "isolation-probes", "probe-"+id)
	_ = os.MkdirAll(filepath.Join(root, "stage"), 0700)
	p := Pending{Version: 1, ID: id, Root: root, Profile: "AIDock.Probe." + id, Host: platformInfo().Host}
	if err := writePending(p); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(root, "keep.txt"), []byte("unrelated"), 0600)
	if _, err := m.Recover(); err == nil {
		t.Fatal("extra top-level content deleted")
	}
	_ = os.Remove(filepath.Join(root, "keep.txt"))
	file := filepath.Join(root, "stage", "locked.bin")
	_ = os.WriteFile(file, []byte("recover later"), 0600)
	path, _ := windows.UTF16PtrFromString(file)
	h, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Recover(); err == nil {
		windows.CloseHandle(h)
		t.Fatal("locked cleanup falsely passed")
	}
	rows, err := m.Pending()
	if err != nil || len(rows) != 1 {
		windows.CloseHandle(h)
		t.Fatal("failed cleanup lost durable marker", rows, err)
	}
	windows.CloseHandle(h)
	if n, err := m.Recover(); err != nil || n != 1 {
		t.Fatal("retry failed", n, err)
	}
	if err := lockPlatform(local, func() error {
		if _, err := m.Recover(); err == nil {
			t.Fatal("active directory lock ignored")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
