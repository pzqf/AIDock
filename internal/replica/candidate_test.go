package replica

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/pluginartifact"
	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func replicaCandidateFixture(t *testing.T) (*Manager, []Binding, []*source.Manager, CandidateRequest, string) {
	t.Helper()
	m, bindings, copies := sharedPluginCopies(t, 2)
	src := copies[0]
	ctx := context.Background()
	for _, s := range copies {
		state, err := s.MCP()
		if err != nil {
			t.Fatal(err)
		}
		state.Config["servers"].([]any)[0].(map[string]any)["enabled"] = true
		p, err := s.MCPPlan(ctx, source.MCPEdit{ExpectedSHA256: state.SHA256, Config: state.Config})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.ApplyMCP(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
			t.Fatal(err)
		}
	}
	old := filepath.Join(src.Store.Local, "mcp-artifacts/shared-fixture")
	root := filepath.Join(src.Store.Local, "mcp-artifacts/publisher-v2")
	tree, err := src.Store.Capture(old)
	if err != nil {
		t.Fatal(err)
	}
	for name, e := range tree.Entries {
		if e.Dir {
			if err = os.MkdirAll(filepath.Join(root, name), 0755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		body, err := src.Store.Data(e)
		if err != nil {
			t.Fatal(err)
		}
		if name == "plugin.yaml" {
			var manifest map[string]any
			if err = json.Unmarshal(body, &manifest); err != nil {
				t.Fatal(err)
			}
			manifest["version"] = "2.0.0"
			body = data(manifest)
		}
		generatedFixtureWrite(t, root, name, body)
		if err = os.Chmod(filepath.Join(root, name), os.FileMode(e.Mode)); err != nil {
			t.Fatal(err)
		}
	}
	lock, err := src.InspectMCPLock(ctx, pluginlock.Preparation{Name: "project-memory", ArtifactPath: "mcp-artifacts/publisher-v2", Version: "2.0.0", Runtime: pluginlock.Runtime{Kind: "native", Version: "2.0.0", Constraint: "=2.0.0", Executable: "runtime/helper"}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := src.MCPLockPlan(ctx, source.MCPLockEdit{ExpectedSHA256: lock.ExpectedSHA256, Document: lock.Document})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = src.ApplyMCPLock(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	proposal, err := m.Resolve(ctx, configurationRequest(t, m, bindings, map[string]string{bindings[1].ID + "/" + source.SharedMCPPrefix + "project-memory": "source"}))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := pluginlock.Parse([]byte(lock.Document))
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "reviewed.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, e := range parsed.Plugins[0].Artifact.Entries {
		h := &zip.FileHeader{Name: e.Path, Method: zip.Deflate}
		h.SetMode(os.FileMode(e.Mode))
		if e.Directory {
			h.Name += "/"
			h.SetMode(os.ModeDir | os.FileMode(e.Mode))
		}
		entry, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if !e.Directory {
			body, err := os.ReadFile(filepath.Join(root, e.Path))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = entry.Write(body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return m, bindings, copies, CandidateRequest{Plan: proposal.ID, Digest: proposal.Digest, Copy: bindings[1].ID}, archive
}

func TestReplicaCandidateMCPDefinitionCannotPublishThroughSkillsRules(t *testing.T) {
	m, _, _, req, archive := replicaCandidateFixture(t)
	ctx := context.Background()
	p, err := m.loadResolution(req.Plan, req.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if !resolutionChangesMCP(p) {
		t.Fatal("fixture did not change MCP source")
	}
	code(t, m.publicationQualification(PublicationPlan{Resolution: p}), "PUBLICATION_MCP_CONFIGURATION_REQUIRED")
	state, err := m.Candidate(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	importPlan, err := m.CandidateArtifactPlan(ctx, req, pluginartifact.Edit{Action: "import", Name: "project-memory", ArchivePath: archive, ExpectedSHA256: state.Artifacts.Artifacts[0].SHA256, ExpectedLockSHA256: state.Artifacts.LockSHA256})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.CandidateArtifactApply(ctx, req, "project-memory", importPlan.Plan.ID, importPlan.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	g, err := m.StageGeneration(ctx, GenerationRequest{Plan: p.ID, Digest: p.Digest, Tools: []string{"codex-cli"}, ApplicationScope: native.SkillsRulesScope})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Boundary, "本候选修改 MCP 定义或精确锁") {
		t.Fatal("generation implied formal MCP publication", g.Boundary)
	}
	check, err := m.CheckGeneration(ctx, GenerationCheckRequest{Plan: g.ID, Digest: g.Digest})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, gate := range check.Gates {
		found = found || gate.Code == "MCP_CONFIGURATION_PUBLICATION" && gate.Status == "UNAVAILABLE" && gate.Required
	}
	if !found || check.WriteSupported || check.PublicationStatus != "BLOCKED" {
		t.Fatal("MCP definition candidate entered skill/rule publication", check)
	}
}

func TestReplicaCandidatePreparesFuturePackageAndInstallationWithoutChangingResolution(t *testing.T) {
	m, b, copies, req, archive := replicaCandidateFixture(t)
	ctx := context.Background()
	before := captureSources(t, copies)
	control := generatedObservation(t, m, m.Control.Root)
	state, err := m.Candidate(ctx, req)
	if err != nil || state.Artifacts.Artifacts[0].Status != "MISSING" || state.Candidate == "" {
		t.Fatal(state, err)
	}
	futureDigest := state.Candidate
	artifact := pluginartifact.Edit{Action: "import", Name: "project-memory", ArchivePath: archive, ExpectedSHA256: state.Artifacts.Artifacts[0].SHA256, ExpectedLockSHA256: state.Artifacts.LockSHA256}
	p, err := m.CandidateArtifactPlan(ctx, req, artifact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.CandidateArtifactApply(ctx, req, artifact.Name, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	state, err = m.Candidate(ctx, req)
	if err != nil || state.Candidate != futureDigest || state.Artifacts.Artifacts[0].Status != "MATCHED" {
		t.Fatal("candidate expired during its own package preparation", state.Candidate, err)
	}
	install := plugininstall.Edit{Action: "install", Name: artifact.Name, ExpectedSHA256: state.Installation.SHA256, AllowUnverified: true, RiskReason: "独立目标自有夹具，明确接受 L0，仅登记不运行"}
	i, err := m.CandidateInstallPlan(ctx, req, install)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.CandidateInstallApply(ctx, req, install.Name, i.Plan.ID, i.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	state, err = m.Candidate(ctx, req)
	if err != nil || state.Candidate != futureDigest || state.Installation.Installations[0].Status != "CURRENT" {
		t.Fatal("candidate expired during its own installation", state.Candidate, err)
	}
	wrong := req
	wrong.Copy = b[0].ID
	_, err = m.Candidate(ctx, wrong)
	code(t, err, "REPLICA_TARGET_NOT_SELECTED")
	wrong.Copy = workstore.ID()
	_, err = m.Candidate(ctx, wrong)
	code(t, err, "REPLICA_TARGET_NOT_SELECTED")
	unchanged(t, copies, before)
	generatedEqual(t, m, m.Control.Root, control)
	generatedFixtureWrite(t, copies[0].Store.Root, "publisher-external.txt", []byte("late external edit"))
	_, err = m.Candidate(ctx, req)
	code(t, err, "PLAN_STALE")
}
