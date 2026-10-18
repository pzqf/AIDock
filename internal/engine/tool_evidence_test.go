package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestNativeEvidenceObservesCurrentBytesWithoutCreatingQualification(t *testing.T) {
	e, s, id := diagnosisFixture(t)
	ctx := context.Background()
	n, err := e.Native()
	if err != nil {
		t.Fatal(err)
	}
	p, err := n.Plan(ctx, native.Request{Tools: []string{"claude-code", "codex-cli"}, ApplicationScope: native.SkillsRulesScope})
	if err != nil {
		t.Fatal(err)
	}
	// Static comparison fixture only; not vendor discovery or formal proof.
	for _, target := range p.Targets {
		for name, entry := range target.After.Entries {
			file := filepath.Join(target.Root, filepath.FromSlash(name))
			if entry.Dir {
				if err := os.MkdirAll(file, 0755); err != nil {
					t.Fatal(err)
				}
				continue
			}
			body, err := s.Store.Data(entry)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, body, os.FileMode(entry.Mode)); err != nil {
				t.Fatal(err)
			}
		}
	}
	before, err := s.Store.Capture(e.NativeRoot)
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.NativeEvidence(ctx)
	if err != nil || r.Last != nil || len(r.Publications) != 0 || r.Applied == nil || r.ApplicationStatus != "OBSERVED" || r.Current.Probe {
		t.Fatal(r, err)
	}
	for _, tool := range r.Current.Tools {
		if len(tool.Skills) != 1 || tool.Skills[0].ID != id || tool.Skills[0].L1 != "PASS" || tool.Skills[0].L2 != "UNKNOWN" || tool.Skills[0].L3 != "UNKNOWN" || tool.Skills[0].L4 != "UNKNOWN" {
			t.Fatal(tool)
		}
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), "PRIVATE_BODY_DO_NOT_EXPORT") {
		t.Fatal("read-only evidence leaked source bodies")
	}
	latest, err := n.Latest(ctx)
	if err != nil || latest != nil {
		t.Fatal("evidence observation created a native qualification", latest, err)
	}
	after, err := s.Store.Capture(e.NativeRoot)
	if err != nil || workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("evidence read changed business source or native files", err)
	}
	withoutCopy := *e
	withoutCopy.SourceCopy = ""
	r, err = withoutCopy.NativeEvidence(ctx)
	if err != nil || r.Applied != nil || r.ApplicationStatus != "COPY_BINDING_REQUIRED" || len(r.Publications) != 0 {
		t.Fatal("an absent copy identity was invented", r.ApplicationStatus, err)
	}
}

func TestNativeEvidenceKeepsCurrentObservationWhenStandaloneHistoryIsCorrupt(t *testing.T) {
	e, _, _ := diagnosisFixture(t)
	n, err := e.Native()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := n.Check(context.Background(), native.CheckRequest{Tools: []string{"codex-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	identity := e.SourceOwner + "\x00" + e.SourceScope + "\x00" + e.SourceCopy
	file := filepath.Join(e.Roots.Local, "native-checks", workstore.Hash([]byte(strings.ToLower(e.SourceRoot))), workstore.Hash([]byte(identity)), "reports", saved.ID+".json")
	broken := []byte("PRIVATE_HISTORY_BODY_MUST_NOT_APPEAR")
	if err := os.WriteFile(file, broken, 0600); err != nil {
		t.Fatal(err)
	}
	r, err := e.NativeEvidence(context.Background())
	if err != nil || r.Status != "PARTIAL" || r.Last != nil || r.LastStatus != "UNAVAILABLE" || len(r.Current.Tools) != 2 || r.Current.Digest != "" || r.PublicationsStatus != "AVAILABLE" || r.PublicationsTotal == nil || *r.PublicationsTotal != 0 {
		t.Fatal(r, err)
	}
	if len(r.Warnings) != 1 || r.Warnings[0].Area != "last" || r.Warnings[0].Code != "CHECK_CORRUPT" {
		t.Fatal(r.Warnings)
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), string(broken)) {
		t.Fatal("partial evidence exported corrupt history content")
	}
	actual, err := os.ReadFile(file)
	if err != nil || string(actual) != string(broken) {
		t.Fatal("history read modified the corrupt record", err)
	}
}
