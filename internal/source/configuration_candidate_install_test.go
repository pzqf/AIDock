package source

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginartifact"
	"github.com/pzqf/AIDock/internal/workstore"
)

func candidateUpgradeFixture(t *testing.T) (*Manager, ConfigurationCandidate, string, string) {
	t.Helper()
	src, request := installFixture(t)
	dst, old := installFixture(t)
	dst.Copy = workstore.ID()
	installEdit(t, dst, allowInstall(old.Name))
	copyPreparedArtifact(t, src, request.ArtifactPath, "mcp-artifacts/publisher-upgrade")
	request.ArtifactPath = "mcp-artifacts/publisher-upgrade"
	request.Version, request.Runtime.Version, request.Runtime.Constraint = "2.0.0", "2.0.0", "=2.0.0"
	manifest, err := configArtifactManifest(t, src, request.ArtifactPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest["version"] = "2.0.0"
	write(t, filepath.Join(src.Store.Local, request.ArtifactPath, "plugin.yaml"), jsonBytes(manifest))
	refreshInstallLock(t, src, request)
	unit := mcpSharedGroup(t, src)
	before := capture(t, dst)
	after, _, err := dst.DraftSharedConfiguration(before, nil, []SharedConfiguration{unit}, src.Store)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := dst.Profile()
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := dst.DraftConfigurationCandidate(context.Background(), before, after, profile.Layers.Digest)
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "reviewed-upgrade.zip")
	archiveEntries(t, archive, *unit.MCP.Lock, filepath.Join(src.Store.Local, request.ArtifactPath), "")
	return dst, candidate, archive, old.ArtifactPath
}

func candidateArtifactEdit(t *testing.T, m *Manager, c ConfigurationCandidate, archive string) pluginartifact.Edit {
	t.Helper()
	state, err := m.CandidateArtifacts(context.Background(), c)
	if err != nil || len(state.Artifacts) != 1 {
		t.Fatal(state, err)
	}
	return pluginartifact.Edit{Action: "import", Name: state.Artifacts[0].Name, ArchivePath: archive, ExpectedSHA256: state.Artifacts[0].SHA256, ExpectedLockSHA256: state.LockSHA256}
}

func TestConfigurationCandidateUpgradeImportInstallationAndLiveRouteSeparation(t *testing.T) {
	m, c, archive, oldPath := candidateUpgradeFixture(t)
	ctx := context.Background()
	before := capture(t, m)
	old, err := m.Store.Capture(filepath.Join(m.Store.Local, oldPath))
	if err != nil {
		t.Fatal(err)
	}
	state, err := m.CandidateArtifacts(ctx, c)
	if err != nil || state.Artifacts[0].Status != "MISSING" {
		t.Fatal(state, err)
	}
	installations, err := m.CandidateInstallations(ctx, c)
	if err != nil || len(installations.Installations) != 1 || installations.Installations[0].Status == "CURRENT" {
		t.Fatal(installations, err)
	}
	install := allowInstall("project-memory")
	install.ExpectedSHA256 = installations.SHA256
	_, err = m.PlanCandidateInstallation(ctx, c, install)
	code(t, err, "INSTALL_CHECK_FAILED")
	importRequest := candidateArtifactEdit(t, m, c, archive)
	preview, err := m.PlanCandidateArtifact(ctx, c, importRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(state.Artifacts[0].Root); !os.IsNotExist(err) {
		t.Fatal("candidate ZIP preview materialized package", err)
	}
	if _, err = m.ApplyMCPArtifact(ctx, importRequest.Name, preview.Plan.ID, preview.Plan.Digest); err == nil {
		t.Fatal("live route consumed candidate artifact preview")
	}
	if _, err = m.ApplyCandidateArtifact(ctx, c, importRequest.Name, preview.Plan.ID, preview.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	state, err = m.CandidateArtifacts(ctx, c)
	if err != nil || state.Artifacts[0].Status != "MATCHED" {
		t.Fatal(state, err)
	}
	installations, err = m.CandidateInstallations(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	install.ExpectedSHA256 = installations.SHA256
	install.AllowUnverified = false
	_, err = m.PlanCandidateInstallation(ctx, c, install)
	code(t, err, "INSTALL_RISK_CONFIRMATION_REQUIRED")
	install.AllowUnverified = true
	installation, err := m.PlanCandidateInstallation(ctx, c, install)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPInstall(ctx, installation.Plan.ID, installation.Plan.Digest); err == nil {
		t.Fatal("live route consumed candidate installation")
	}
	_, err = m.ApplyCandidateInstallation(ctx, c, "foreign-plugin", installation.Plan.ID, installation.Plan.Digest)
	code(t, err, "INVALID_INPUT")
	if _, err = m.ApplyCandidateInstallation(ctx, c, install.Name, installation.Plan.ID, installation.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	installations, err = m.CandidateInstallations(ctx, c)
	if err != nil || installations.AuditCount != 2 || installations.Installations[0].Status != "CURRENT" || installations.Installations[0].Record.Plugin.Version != "2.0.0" {
		t.Fatal(installations, err)
	}
	live, err := m.MCPInstall(ctx)
	if err != nil || live.Installations[0].Status == "CURRENT" {
		t.Fatal("candidate installation matched old source", live, err)
	}
	review, err := m.CandidatePermissions(ctx, c)
	if err != nil || review.Entries[0].Installation != "CURRENT" || review.Entries[0].Authorization == "CURRENT" {
		t.Fatal("installation bypassed independent approval", review, err)
	}
	currentOld, err := m.Store.Capture(filepath.Join(m.Store.Local, oldPath))
	if err != nil || workstore.Digest(currentOld) != workstore.Digest(old) {
		t.Fatal("old package bytes changed", err)
	}
	unchangedManager(t, m, before)
}

func TestConfigurationCandidateArtifactRejectsWrongLockAndChangedInputs(t *testing.T) {
	for _, changed := range []string{"archive", "source", "copy", "target"} {
		t.Run(changed, func(t *testing.T) {
			m, c, archive, _ := candidateUpgradeFixture(t)
			ctx := context.Background()
			req := candidateArtifactEdit(t, m, c, archive)
			wrong := req
			wrong.ExpectedLockSHA256 = workstore.Hash([]byte("other lock"))
			_, err := m.PlanCandidateArtifact(ctx, c, wrong)
			code(t, err, "PLAN_STALE")
			p, err := m.PlanCandidateArtifact(ctx, c, req)
			if err != nil {
				t.Fatal(err)
			}
			switch changed {
			case "archive":
				f, err := os.OpenFile(archive, os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.Write([]byte("changed after preview"))
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "source":
				write(t, filepath.Join(m.Store.Root, "external.txt"), []byte("retain external edit"))
			case "copy":
				m.Copy = workstore.ID()
			case "target":
				write(t, filepath.Join(p.Candidate.Root, "foreign.txt"), []byte("retain target bytes"))
			}
			_, err = m.ApplyCandidateArtifact(ctx, c, req.Name, p.Plan.ID, p.Plan.Digest)
			code(t, err, "PLAN_STALE")
			if changed == "target" {
				body, err := os.ReadFile(filepath.Join(p.Candidate.Root, "foreign.txt"))
				if err != nil || string(body) != "retain target bytes" {
					t.Fatal("candidate overwrote occupied path", err)
				}
			} else if _, err = os.Stat(p.Candidate.Root); !os.IsNotExist(err) {
				t.Fatal("stale candidate wrote artifact", err)
			}
		})
	}
}
