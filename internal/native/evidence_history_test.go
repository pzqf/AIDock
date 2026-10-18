package native

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func checkedHistoryFixture(t *testing.T) (*Manager, ConfigurationPreparation, string) {
	t.Helper()
	m, candidate, executable, home := nativeConfigurationFixture(t)
	fakeProbes(m, discoveredGuide)
	m.probe.rules = fakeRuleLoad
	r, err := m.PrepareConfigurationCandidate(context.Background(), candidate, CheckRequest{Tools: []string{"codex-cli"}, Probe: true, RuleProbe: true}, executable, home)
	if err != nil || r.Status != "CHECKED" {
		t.Fatal(r.Status, err)
	}
	return m, r, executable
}

func TestConfigurationHistoryIsReadOnlyHistoricalAndNotQualification(t *testing.T) {
	m, prepared, executable := checkedHistoryFixture(t)
	ctx := context.Background()
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(m.Root, "AGENTS.md"), []byte("retained later native draft"))
	state, err := m.Source.State()
	if err != nil {
		t.Fatal(err)
	}
	applySource(t, m.Source, source.Request{Action: "edit", ID: state.Index.Skills[0].ID, Files: []source.Patch{{Path: "later-draft.txt", Content: "LATER_PRIVATE_SOURCE_BODY"}}})
	m.probe.version = func(context.Context, toolBinary, string, string, string) (string, error) {
		t.Fatal("historical read ran a version process")
		return "", nil
	}
	s, l := snapshotDraftTrees(t, m)
	native := snapshotNativeTrees(t, m)
	history, err := m.ConfigurationCheckHistory(ctx, prepared.ID, prepared.Digest)
	if err != nil || !history.Check.Stale || history.Check.Digest != prepared.Check.Digest || history.Check.Tools[0].L2 != "PASS" || history.Check.Tools[0].L3 != "UNKNOWN" || history.Check.Tools[0].L4 != "UNKNOWN" {
		t.Fatal(history, err)
	}
	if strings.Contains(string(jsonData(history)), "PRIVATE_") || strings.Contains(string(jsonData(history)), "PRIVATE_SENTINEL") {
		t.Fatal("historical projection leaked compiler or configuration bodies")
	}
	if _, err := m.loadCheck(history.Check.ID); err == nil {
		t.Fatal("historical candidate became a stored native qualification")
	}
	if _, err := os.Stat(m.checksDir()); !os.IsNotExist(err) {
		t.Fatal("history read created a native check record", err)
	}
	afterSource, afterLocal := snapshotDraftTrees(t, m)
	if workstore.Digest(s) != workstore.Digest(afterSource) || workstore.Digest(l) != workstore.Digest(afterLocal) || !reflectTreesEqual(native, snapshotNativeTrees(t, m)) {
		t.Fatal("historical read changed source, Local or native draft")
	}
	_, err = m.ConfigurationCheckHistory(ctx, prepared.ID, workstore.Hash([]byte("wrong")))
	code(t, err, "CHECK_CORRUPT")
}

func TestConfigurationHistoryRejectsRehashedForeignNestedCheck(t *testing.T) {
	m, prepared, _ := checkedHistoryFixture(t)
	original, err := m.loadConfigurationPreparation(prepared.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*CheckReport){
		"owner":     func(c *CheckReport) { c.Owner = workstore.ID() },
		"scope":     func(c *CheckReport) { c.Scope = "client" },
		"copy":      func(c *CheckReport) { c.Copy = workstore.ID() },
		"root":      func(c *CheckReport) { c.Root = t.TempDir() },
		"candidate": func(c *CheckReport) { c.SourceSnapshot = workstore.Hash([]byte("foreign candidate")) },
	} {
		t.Run(name, func(t *testing.T) {
			var forged configurationPrepared
			if err := strict(jsonData(original), &forged); err != nil {
				t.Fatal(err)
			}
			mutate(&forged.Report.Check)
			forged.Report.Check.Digest = checkDigest(forged.Report.Check)
			forged.Digest = configurationPreparedDigest(forged)
			forged.Report.Digest = forged.Digest
			if err := save(m.configurationPreparedFile(prepared.ID), jsonData(forged)); err != nil {
				t.Fatal(err)
			}
			_, err := m.ConfigurationCheckHistory(context.Background(), prepared.ID, forged.Digest)
			code(t, err, "CHECK_CORRUPT")
		})
	}
}
