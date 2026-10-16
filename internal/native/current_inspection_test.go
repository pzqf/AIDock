package native

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCurrentInspectionKeepsExplicitEvidenceAndNeverProbes(t *testing.T) {
	m, _ := fixture(t)
	fakeProbes(m, discoveredGuide)
	old, err := m.Check(context.Background(), CheckRequest{Tools: []string{"codex-cli"}, Probe: true})
	if err != nil {
		t.Fatal(err)
	}
	m.probe.version = func(context.Context, toolBinary, string, string, string) (string, error) {
		t.Fatal("overview ran a version process")
		return "", nil
	}
	m.probe.discover = func(context.Context, toolBinary, string, string, string) (discovery, error) {
		t.Fatal("overview ran discovery")
		return discovery{}, nil
	}
	before := observe(t, m, m.Source.Store.Root)
	r, err := m.InspectCurrent(context.Background())
	if err != nil || r.Probe || len(r.Tools) != 2 {
		t.Fatal(r, err)
	}
	for _, row := range r.Tools {
		if row.L1 != "NOT_APPLIED" || row.L2 != "UNKNOWN" || row.L3 != "UNKNOWN" || row.L4 != "UNKNOWN" {
			t.Fatal(row)
		}
	}
	last, err := m.Latest(context.Background())
	if err != nil || last == nil || last.ID != old.ID || last.Stale || last.Tools[0].L2 != "PASS" {
		t.Fatal(last, err)
	}
	files, err := os.ReadDir(filepath.Join(m.checksDir(), "reports"))
	if err != nil || len(files) != 1 {
		t.Fatal("overview saved another record", err, len(files))
	}
	unchanged(t, m, m.Source.Store.Root, before)
	for _, dir := range []string{".agents", ".claude"} {
		if _, err := os.Stat(filepath.Join(m.Root, dir)); !os.IsNotExist(err) {
			t.Fatal("overview created native output", err)
		}
	}
}
