package isolation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if handled, code := Helper(os.Args[1:]); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}
func TestStaticCheckDoesNotCreateFilesOrLaunch(t *testing.T) {
	local := filepath.Join(t.TempDir(), "local")
	m, err := New(local)
	if err != nil {
		t.Fatal(err)
	}
	m.run = func(context.Context, string, Platform) ([]Finding, bool) {
		t.Fatal("static launched process")
		return nil, false
	}
	r, err := m.Check(context.Background(), false)
	if err != nil || r.Probe || r.GeneratorReady || r.Status == "PASS" || r.ID != "" {
		t.Fatal(r, err)
	}
	if _, err = os.Stat(local); !os.IsNotExist(err) {
		t.Fatal("static created Local", err)
	}
}
func TestEvidenceFreshnessAndCorruption(t *testing.T) {
	m, _ := New(filepath.Join(t.TempDir(), "local"))
	p, err := current()
	if err != nil {
		t.Fatal(err)
	}
	r := Report{Version: 1, Kind: "platform-isolation-check", ID: "d26f53ce-62ce-4d6b-b25a-1cbdca310bb0", Probe: true, Created: time.Now().UTC().Format(time.RFC3339Nano), Expires: time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano), Platform: p, Status: "PASS", Cleanup: true, Boundary: boundary}
	r.Digest = digest(r)
	if err = m.save(r); err != nil {
		t.Fatal(err)
	}
	s, err := m.Latest()
	if err != nil || s == nil || s.Stale {
		t.Fatal(s, err)
	}
	for _, mutate := range []func(*Report){func(r *Report) { r.Platform.Policy = "old-policy" }, func(r *Report) { r.Platform.Host = "other-host" }, func(r *Report) { r.Platform.Executable = "different-program" }, func(r *Report) { r.Platform.Kernel = "different-os-build" }, func(r *Report) { r.Expires = time.Now().Add(-time.Second).Format(time.RFC3339Nano) }} {
		stale := r
		mutate(&stale)
		stale.Digest = digest(stale)
		if err = m.save(stale); err != nil {
			t.Fatal(err)
		}
		s, err := m.Show(r.ID)
		if err != nil || !s.Stale || s.Status != "PASS" {
			t.Fatal(s, err)
		}
	}
	r.GeneratorReady = true
	r.Digest = digest(r)
	_ = m.save(r)
	if _, err = m.Show(r.ID); err == nil {
		t.Fatal("record granted generator execution")
	}
	r.GeneratorReady = false
	r.Digest = "wrong"
	_ = m.save(r)
	if _, err = m.Show(r.ID); err == nil {
		t.Fatal("corrupt report accepted")
	}
	if _, err = m.Show("../bad"); err == nil {
		t.Fatal("path traversal accepted")
	}
}
func TestDirectHelperRefusesBeforeReadingFiles(t *testing.T) {
	if handled, code := Helper([]string{"__aidock_isolation_probe", "check"}); !handled || code != 91 {
		t.Fatal(handled, code)
	}
}

func TestAmbiguousNetworkResultsNeverPass(t *testing.T) {
	for _, tc := range []struct {
		network  string
		err      error
		expected string
	}{{"tcp4", context.DeadlineExceeded, "UNKNOWN"}, {"udp4", nil, "UNKNOWN"}, {"tcp4", nil, "FAIL"}, {"tcp4", os.ErrNotExist, "UNKNOWN"}} {
		if f := networkFinding("test", tc.network, tc.err); f.Status != tc.expected {
			t.Fatal(f)
		}
	}
}

func TestCancelledCheckDoesNotSaveEvidence(t *testing.T) {
	m, _ := New(filepath.Join(t.TempDir(), "local"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Check(ctx, true); err != context.Canceled {
		t.Fatal(err)
	}
	if r, err := m.Latest(); err != nil || r != nil {
		t.Fatal("cancel saved evidence", r, err)
	}
}
