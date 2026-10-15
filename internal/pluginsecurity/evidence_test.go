package pluginsecurity

import (
	"strings"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/pluginmanifest"
	"github.com/pzqf/AIDock/internal/workstore"
)

func fixture() Candidate {
	deps := []pluginmanifest.Dependency{{Ecosystem: "npm", Name: "direct", Version: "2.0.0"}}
	p := pluginlock.Plugin{Name: "fixture", DefinitionSHA256: workstore.Hash([]byte("definition")), Source: map[string]any{"type": "npm", "package": "@fixture/root", "version": "1.2.3"}, Version: "1.2.3", Platform: "windows-amd64", Artifact: pluginlock.Artifact{SHA256: workstore.Hash([]byte("artifact"))}}
	return Candidate{Plugin: p, Manifest: pluginmanifest.Report{Status: "PASS", ArtifactStatus: "PASS", ManifestStatus: "PASS", PermissionStatus: "PASS", ManifestSHA256: workstore.Hash([]byte("manifest")), Manifest: &pluginmanifest.Manifest{Name: "publisher", Dependencies: &deps}}}
}
func snapshot(c Candidate, now time.Time) Snapshot {
	s := Template(c, now.Add(-time.Hour))
	s.ExpiresAt = now.Add(time.Hour).Format(time.RFC3339)
	s.Provider = "Isolated reference fixture"
	s.ReferenceURL = "https://reference.example.invalid/snapshot"
	for i := range s.Packages {
		a := []Advisory{}
		s.Packages[i].Advisories = &a
	}
	return s
}
func TestReferenceCoverageKnownRisksExpiryAndTrustBoundary(t *testing.T) {
	c := fixture()
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	s := snapshot(c, now)
	if _, err := Parse(JSON(s)); err != nil {
		t.Fatal(err)
	}
	if r := Verify(c, nil, now); r.CVEStatus != "MISSING" || r.Status != "UNKNOWN" {
		t.Fatal(r)
	}
	if r := Verify(c, &s, now); r.CVEStatus != "NO_KNOWN_MATCHES" || r.Status != "UNKNOWN" || r.TrustLevel != "L0" || r.Covered != 2 {
		t.Fatal(r)
	}
	partial := s
	partial.Packages = s.Packages[:1]
	if r := Verify(c, &partial, now); r.CVEStatus != "PARTIAL" || r.Covered != 1 {
		t.Fatal(r)
	}
	a := []Advisory{{ID: "CVE-2026-12345", Severity: "high", Summary: "Fixture vulnerability", URL: "https://reference.example.invalid/advisory"}}
	s.Packages[0].Advisories = &a
	for _, at := range []time.Time{now, now.Add(2 * time.Hour)} {
		r := Verify(c, &s, at)
		if r.Status != "FAIL" || r.CVEStatus != "FOUND" || r.AdvisoryCount != 1 || r.TrustLevel != "L0" {
			t.Fatal(r)
		}
	}
	clean := snapshot(c, now)
	if r := Verify(c, &clean, now.Add(2*time.Hour)); r.CVEStatus != "EXPIRED" || r.Current || r.Status != "UNKNOWN" {
		t.Fatal(r)
	}
	changed := c
	changed.Plugin.DefinitionSHA256 = workstore.Hash([]byte("permissions changed"))
	if r := Verify(changed, &s, now); r.CVEStatus != "STALE" || r.Status != "UNKNOWN" {
		t.Fatal(r)
	}
	s.Packages[0].Name = "unrelated"
	if err := Bound(s, c); err == nil {
		t.Fatal("unrelated package accepted")
	}
}
func TestReferenceStrictInputsAndUncheckedTemplates(t *testing.T) {
	c := fixture()
	now := time.Now().UTC()
	template := Template(c, now)
	template.Provider = "Fixture"
	template.ReferenceURL = "https://example.invalid/fixture"
	if _, err := Parse(JSON(template)); err == nil {
		t.Fatal("null advisory template became a negative result")
	}
	for name, mutate := range map[string]func(*Snapshot){"schema": func(s *Snapshot) { s.Version = 2 }, "format": func(s *Snapshot) { s.Format = "other" }, "longTTL": func(s *Snapshot) { s.ExpiresAt = now.Add(90 * 24 * time.Hour).Format(time.RFC3339) }, "credentials": func(s *Snapshot) { s.ReferenceURL = "https://user:password@example.invalid/private" }, "duplicate": func(s *Snapshot) { s.Packages = append(s.Packages, s.Packages[0]) }, "query": func(s *Snapshot) { s.ReferenceURL = "https://example.invalid/?token=secret" }} {
		t.Run(name, func(t *testing.T) {
			s := snapshot(c, now)
			mutate(&s)
			if _, err := Parse(JSON(s)); err == nil {
				t.Fatal("accepted invalid snapshot")
			}
		})
	}
	s := snapshot(c, now)
	b := strings.TrimSpace(string(JSON(s)))
	b = strings.TrimSuffix(b, "}") + `,"trust_level":"L4"}`
	if _, err := Parse([]byte(b)); err == nil {
		t.Fatal("unknown rating accepted")
	}
	if _, err := Parse([]byte(strings.Repeat(" ", 2<<20+1))); err == nil {
		t.Fatal("oversized input accepted")
	}
}
