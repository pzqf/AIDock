package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/inventory"
)

const project = "11111111-1111-4111-8111-111111111111"

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func packageFixture(t *testing.T, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "skills", "api", "SKILL.md"), "---\nname: api\ndescription: external\n---\n\nbody\n")
	writeFile(t, filepath.Join(root, "skills", "api", "references", "guide.md"), "guide\n")
	writeFile(t, filepath.Join(root, "skills", "api", "assets", "data.bin"), string([]byte{0, 1, 255}))
}

func registration(root string) Registration {
	return Registration{ID: "22222222-2222-4222-8222-222222222222", Project: project, Scope: "server", Copy: "one", Root: root, SourcePath: "skills", SourceRole: "external-source", Format: "native-skill", Generator: "fixture-generator", Packages: []string{"api"}}
}

func TestLoadResolvesRelativeRootAndRejectsOverlap(t *testing.T) {
	dir := t.TempDir()
	data := `schema_version: 1
bridges:
  - id: 22222222-2222-4222-8222-222222222222
    project: 11111111-1111-4111-8111-111111111111
    scope: server
    copy: one
    root: external
    source_path: skills
    source_role: external-source
    format: native-skill
    packages: [api]
`
	path := filepath.Join(dir, "bridge.yaml")
	writeFile(t, path, data)
	idx, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if idx.Bridges[0].Root != filepath.Join(dir, "external") {
		t.Fatal(idx.Bridges[0].Root)
	}
	data = strings.Replace(data, "packages: [api]", "packages: [api, api/docs]", 1)
	writeFile(t, path, data)
	if _, err = Load(path); err == nil {
		t.Fatal("overlapping package registrations accepted")
	}
}

func TestCheckProducesCandidateRevisionWithoutWritingExternalSource(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "external")
	packageFixture(t, root)
	before := fileBytes(t, root)
	idx := Index{Version: 1, Bridges: []Registration{registration(root)}}
	report, err := Check(context.Background(), idx)
	if err != nil {
		t.Fatal(err)
	}
	if report.ReadOnly != true || len(report.Bridges) != 1 || len(report.Bridges[0].Candidates) != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	b := report.Bridges[0]
	if b.CurrentRevision == "" || !strings.HasPrefix(b.CurrentRevision, "sha256:") || !strings.HasPrefix(b.Candidates[0].Revision, "sha256:") {
		t.Fatalf("missing candidate revision: %+v", b)
	}
	if b.Status != "UNKNOWN" && b.Status != "WARN" {
		t.Fatalf("unverified bridge should not pass: %+v", b)
	}
	if got := fileBytes(t, root); string(got) != string(before) {
		t.Fatal("bridge check modified external source")
	}
	reg := registration(root)
	reg.ApprovedRevision = b.CurrentRevision
	writeFile(t, filepath.Join(root, "skills", "api", "assets", "data.bin"), string([]byte{0, 1, 2}))
	changed, err := Check(context.Background(), Index{Version: 1, Bridges: []Registration{reg}})
	if err != nil {
		t.Fatal(err)
	}
	if !changed.Bridges[0].Drift || changed.Bridges[0].CurrentRevision == b.CurrentRevision {
		t.Fatalf("package change did not change candidate revision: old=%s new=%s candidates=%+v", b.CurrentRevision, changed.Bridges[0].CurrentRevision, changed.Bridges[0].Candidates)
	}
}

func TestCheckReportsApprovedDriftAndMissingPackage(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "external")
	packageFixture(t, root)
	reg := registration(root)
	first, err := Check(context.Background(), Index{Version: 1, Bridges: []Registration{reg}})
	if err != nil {
		t.Fatal(err)
	}
	reg.ApprovedRevision = first.Bridges[0].CurrentRevision
	reg.Packages = []string{"missing"}
	report, err := Check(context.Background(), Index{Version: 1, Bridges: []Registration{reg}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Bridges[0].Status != "FAIL" || !hasFinding(report.Bridges[0], "EXTERNAL_PACKAGE_MISSING") {
		t.Fatalf("missing package was not blocking: %+v", report.Bridges[0])
	}
	reg.Packages = []string{"api"}
	writeFile(t, filepath.Join(root, "skills", "api", "SKILL.md"), "---\nname: api\ndescription: changed\n---\n\nbody\n")
	report, err = Check(context.Background(), Index{Version: 1, Bridges: []Registration{reg}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Bridges[0].Status != "STALE" || !hasFinding(report.Bridges[0], "EXTERNAL_SOURCE_DRIFT") {
		t.Fatalf("drift was not explicit: %+v", report.Bridges[0])
	}
}

func hasFinding(report BridgeReport, code string) bool {
	for _, finding := range report.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func fileBytes(t *testing.T, root string) []byte {
	t.Helper()
	all := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if info.IsDir() {
			all[rel] = "directory"
		} else {
			b, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			all[rel] = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSelectedPackagesBoundScanAndRevision(t *testing.T) {
	root := t.TempDir()
	packageFixture(t, root)
	writeFile(t, filepath.Join(root, "skills/other/SKILL.md"), "must not parse this unrelated package")
	reg := registration(root)
	first, err := Check(context.Background(), Index{Version: 1, Bridges: []Registration{reg}})
	if err != nil || first.Status == "FAIL" || len(first.Bridges[0].Candidates) != 1 {
		t.Fatal(first, err)
	}
	writeFile(t, filepath.Join(root, "skills/other/SKILL.md"), "unregistered package changed")
	second, err := Check(context.Background(), Index{Version: 1, Bridges: []Registration{reg}})
	if err != nil || first.Bridges[0].CurrentRevision != second.Bridges[0].CurrentRevision {
		t.Fatal("unregistered package changed selected revision", err)
	}
	reg.Packages = nil
	all, err := Check(context.Background(), Index{Version: 1, Bridges: []Registration{reg}})
	if err != nil || all.Status != "FAIL" || all.Bridges[0].CurrentRevision != "" {
		t.Fatal("failed package was usable as complete candidate", all, err)
	}
}

func TestIncompletePackagesNeverProduceSourceRevision(t *testing.T) {
	for _, issue := range []string{"missing reference", "attachment reference", "secret", "cache", "no document"} {
		t.Run(issue, func(t *testing.T) {
			root := t.TempDir()
			packageFixture(t, root)
			switch issue {
			case "missing reference":
				writeFile(t, filepath.Join(root, "skills/api/SKILL.md"), "---\nname: api\ndescription: fixture\n---\n[missing](missing.md)\n")
			case "attachment reference":
				writeFile(t, filepath.Join(root, "skills/api/references/guide.md"), "[missing](missing.md)")
			case "secret":
				writeFile(t, filepath.Join(root, "skills/api/.env"), "MUST_NOT_LEAK=secret")
			case "cache":
				writeFile(t, filepath.Join(root, "skills/api/node_modules/ignored.md"), "ignored")
			case "no document":
				if err := os.Remove(filepath.Join(root, "skills/api/SKILL.md")); err != nil {
					t.Fatal(err)
				}
			}
			report, err := Check(context.Background(), Index{Version: 1, Bridges: []Registration{registration(root)}})
			if err != nil || report.Status != "FAIL" || report.Bridges[0].CurrentRevision != "" {
				t.Fatal(report, err)
			}
			encoded, _ := json.Marshal(report)
			if strings.Contains(string(encoded), "MUST_NOT_LEAK=secret") {
				t.Fatal("secret file content leaked into report")
			}
		})
	}
}

func TestSourceDisappearsAndCancellationHaveNoWrites(t *testing.T) {
	root := filepath.Join(t.TempDir(), "never-created")
	report, err := Check(context.Background(), Index{Version: 1, Bridges: []Registration{registration(root)}})
	if err != nil || report.Status != "FAIL" || !hasFinding(report.Bridges[0], "EXTERNAL_SOURCE_MISSING") {
		t.Fatal(report, err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("missing external directory was created")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Check(ctx, Index{Version: 1, Bridges: []Registration{registration(root)}}); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestRolesPathsAndDuplicateIDsAreRejectedBeforeScanning(t *testing.T) {
	for _, issue := range []string{"generated", "plugin", "unknown", "source escape", "package escape", "duplicate id", "relative root", "edit"} {
		t.Run(issue, func(t *testing.T) {
			reg := registration(filepath.Join(t.TempDir(), "never-created"))
			idx := Index{Version: 1, Bridges: []Registration{reg}}
			switch issue {
			case "generated", "plugin", "unknown":
				idx.Bridges[0].SourceRole = issue
			case "source escape":
				idx.Bridges[0].SourcePath = "../escape"
			case "package escape":
				idx.Bridges[0].Packages = []string{"../../escape"}
			case "duplicate id":
				idx.Bridges = append(idx.Bridges, reg)
			case "relative root":
				idx.Bridges[0].Root = "relative"
			case "edit":
				idx.Bridges[0].AllowEdit = true
			}
			if _, err := Check(context.Background(), idx); err == nil {
				t.Fatal("unsafe direct registration accepted")
			}
		})
	}
}

func TestNativeCollisionAcrossSeparateSources(t *testing.T) {
	root := t.TempDir()
	packageFixture(t, root)
	writeFile(t, filepath.Join(root, "extra/api/SKILL.md"), "---\nname: api\ndescription: other source\n---\n")
	first := registration(root)
	second := first
	second.ID = "33333333-3333-4333-8333-333333333333"
	second.SourcePath = "extra"
	report, err := Check(context.Background(), Index{Version: 1, Bridges: []Registration{first, second}})
	if err != nil || report.Status != "FAIL" {
		t.Fatal(report, err)
	}
	for _, b := range report.Bridges {
		if b.Status != "FAIL" || b.CurrentRevision != "" || !hasFinding(b, "NATIVE_NAME_COLLISION") {
			t.Fatal(b)
		}
	}
}

func TestDirectoryEntriesParticipateInCandidateRevision(t *testing.T) {
	root := t.TempDir()
	packageFixture(t, root)
	idx := Index{Version: 1, Bridges: []Registration{registration(root)}}
	first, err := Check(context.Background(), idx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "skills/api/empty"), 0700); err != nil {
		t.Fatal(err)
	}
	second, err := Check(context.Background(), idx)
	if err != nil || first.Bridges[0].CurrentRevision == second.Bridges[0].CurrentRevision {
		t.Fatal("empty directory omitted from revision", err)
	}
}

func TestChangedPackageCannotReuseScannedFileManifest(t *testing.T) {
	root := t.TempDir()
	packageFixture(t, root)
	dir := filepath.Join(root, "skills/api")
	scanned := inventory.AnalyzePackage(context.Background(), dir)
	writeFile(t, filepath.Join(dir, "assets/data.bin"), "changed bytes")
	if revision, err := packageRevision(context.Background(), dir, scanned); err == nil || revision != "" {
		t.Fatal("stale scan accepted", revision, err)
	}
}
