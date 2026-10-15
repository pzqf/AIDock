package source

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginartifact"
	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/workstore"
)

func artifactFixture(t *testing.T, large bool) (*Manager, pluginlock.Plugin, string) {
	t.Helper()
	m, req := installFixture(t)
	root := filepath.Join(m.Store.Local, filepath.FromSlash(req.ArtifactPath))
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0755); err != nil {
		t.Fatal(err)
	}
	if large {
		write(t, filepath.Join(root, "runtime", "large-binary"), bytes.Repeat([]byte{0, 255, 42, 13, 10}, (11<<20)/5))
	}
	refreshInstallLock(t, m, req)
	state, err := m.MCPLock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lock, err := pluginlock.Parse([]byte(state.Document))
	if err != nil {
		t.Fatal(err)
	}
	p := lock.Plugins[0]
	archive := filepath.Join(filepath.Dir(m.Store.Root), "complete.zip")
	archiveEntries(t, archive, p, root, "")
	if err = os.Rename(root, root+"-fixture-backup"); err != nil {
		t.Fatal(err)
	}
	return m, p, archive
}
func archiveEntries(t *testing.T, archive string, p pluginlock.Plugin, root, mutation string) {
	t.Helper()
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for i, e := range p.Artifact.Entries {
		if mutation == "missing" && i == 0 {
			continue
		}
		header := &zip.FileHeader{Name: e.Path, Method: zip.Deflate}
		header.SetMode(os.FileMode(e.Mode))
		if e.Directory {
			header.Name += "/"
			header.SetMode(os.ModeDir | os.FileMode(e.Mode))
		}
		if mutation == "symlink" && i == 0 {
			header.SetMode(os.ModeSymlink | 0755)
		}
		if mutation == "traversal" && i == 0 {
			header.Name = "../escape"
		}
		if mutation == "duplicate" && i == 1 {
			header.Name = p.Artifact.Entries[0].Path
		}
		entry, err := w.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if !e.Directory {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(e.Path)))
			if err != nil {
				t.Fatal(err)
			}
			if mutation == "bytes" {
				data = bytes.Repeat([]byte("x"), len(data))
			}
			if _, err = entry.Write(data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if mutation == "extra" {
		w.Create("unknown.txt")
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
}
func artifactEdit(t *testing.T, m *Manager, name, archive string) pluginartifact.Edit {
	t.Helper()
	s, err := m.MCPArtifacts(context.Background())
	if err != nil || len(s.Artifacts) != 1 {
		t.Fatal(s, err)
	}
	return pluginartifact.Edit{Action: "import", Name: name, ArchivePath: archive, ExpectedSHA256: s.Artifacts[0].SHA256, ExpectedLockSHA256: s.LockSHA256}
}
func applyArtifact(t *testing.T, m *Manager, req pluginartifact.Edit) pluginartifact.Preview {
	t.Helper()
	p, err := m.MCPArtifactPlan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCPArtifact(context.Background(), req.Name, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestMCPArtifactLargeCompleteImportRestartRollbackUndoAndSourcePreservation(t *testing.T) {
	m, plugin, archive := artifactFixture(t, true)
	ctx := context.Background()
	sourceBefore, _ := m.Store.Capture(m.Store.Root)
	req := artifactEdit(t, m, plugin.Name, archive)
	p, err := m.MCPArtifactPlan(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if p.Candidate.Status != "MATCHED" || p.ArchiveSHA256 == "" || p.Candidate.ExecutionReady {
		t.Fatal(p)
	}
	if _, err = os.Stat(p.Candidate.Root); !os.IsNotExist(err) {
		t.Fatal("preview wrote artifact", err)
	}
	_, err = m.ApplyMCPArtifact(ctx, req.Name, p.Plan.ID, "wrong")
	code(t, err, "PLAN_STALE")
	if _, err = m.ApplyMCPArtifact(ctx, req.Name, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	_, err = m.ApplyMCPArtifact(ctx, req.Name, p.Plan.ID, p.Plan.Digest)
	code(t, err, "PLAN_CONSUMED")
	fresh, err := New(m.Store.Root, m.Store.Local, m.Owner, m.Scope)
	if err != nil {
		t.Fatal(err)
	}
	s, err := fresh.MCPArtifacts(ctx)
	if err != nil || s.Artifacts[0].Status != "MATCHED" || len(s.Artifacts[0].History) != 1 {
		t.Fatal(s, err)
	}
	verified, err := fresh.MCPLock(ctx)
	if err != nil || verified.Plugins[0].ArtifactStatus != "PASS" || verified.Plugins[0].TrustLevel != "L0" {
		t.Fatal(verified, err)
	}
	installed, err := fresh.MCPInstall(ctx)
	if err != nil || len(installed.Installations) != 0 || installed.AuditCount != 0 {
		t.Fatal("import registered an installation", installed, err)
	}
	reverse := pluginartifact.Edit{Action: "rollback", Name: req.Name, Snapshot: p.Plan.ID, ExpectedSHA256: s.Artifacts[0].SHA256, ExpectedLockSHA256: s.LockSHA256}
	rolled := applyArtifact(t, fresh, reverse)
	if _, err = os.Stat(p.Candidate.Root); !os.IsNotExist(err) {
		t.Fatal("rollback retained artifact root", err)
	}
	s, _ = fresh.MCPArtifacts(ctx)
	reverse.ExpectedSHA256 = s.Artifacts[0].SHA256
	reverse.Snapshot = rolled.Plan.ID
	applyArtifact(t, fresh, reverse)
	s, err = fresh.MCPArtifacts(ctx)
	if err != nil || s.Artifacts[0].Status != "MATCHED" {
		t.Fatal(s, err)
	}
	_, err = fresh.MCPArtifactPlan(ctx, artifactEdit(t, fresh, plugin.Name, archive))
	code(t, err, "ARTIFACT_TARGET_OCCUPIED")
	after, _ := fresh.Store.Capture(fresh.Store.Root)
	if workstore.Digest(sourceBefore) != workstore.Digest(after) {
		t.Fatal("artifact workflow modified portable source")
	}
}
func TestMCPArtifactRejectsMalformedArchivesAndPreservesUnknownTarget(t *testing.T) {
	for _, mutation := range []string{"missing", "extra", "traversal", "duplicate", "symlink", "bytes"} {
		t.Run(mutation, func(t *testing.T) {
			m, p, archive := artifactFixture(t, false)
			archiveEntries(t, archive, p, filepath.Join(m.Store.Local, filepath.FromSlash(p.Artifact.Path))+"-fixture-backup", mutation)
			_, err := m.MCPArtifactPlan(context.Background(), artifactEdit(t, m, p.Name, archive))
			if err == nil {
				t.Fatal("accepted invalid archive")
			}
			if _, err = os.Stat(filepath.Join(m.Store.Local, filepath.FromSlash(p.Artifact.Path))); !os.IsNotExist(err) {
				t.Fatal("invalid preview wrote target", err)
			}
		})
	}
	m, p, archive := artifactFixture(t, false)
	req := artifactEdit(t, m, p.Name, archive)
	target := filepath.Join(m.Store.Local, filepath.FromSlash(p.Artifact.Path))
	write(t, filepath.Join(target, "unknown"), []byte("user data"))
	_, err := m.MCPArtifactPlan(context.Background(), req)
	code(t, err, "PLAN_STALE")
	req = artifactEdit(t, m, p.Name, archive)
	_, err = m.MCPArtifactPlan(context.Background(), req)
	code(t, err, "ARTIFACT_TARGET_OCCUPIED")
	b, _ := os.ReadFile(filepath.Join(target, "unknown"))
	if string(b) != "user data" {
		t.Fatal("overwrote unknown target")
	}
}
func TestMCPArtifactArchiveLockSourceAndTargetDriftInvalidateConfirmation(t *testing.T) {
	for _, kind := range []string{"archive", "lock", "source", "target"} {
		t.Run(kind, func(t *testing.T) {
			m, p, archive := artifactFixture(t, false)
			req := artifactEdit(t, m, p.Name, archive)
			preview, err := m.MCPArtifactPlan(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "archive":
				f, err := os.OpenFile(archive, os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				f.Write([]byte("changed ZIP bytes"))
				f.Close()
			case "lock":
				b, err := os.ReadFile(filepath.Join(m.Store.Root, "plugins.lock"))
				if err != nil {
					t.Fatal(err)
				}
				write(t, filepath.Join(m.Store.Root, "plugins.lock"), append(b, '\n'))
			case "source":
				apply(t, m, Request{Action: "create", Name: "new-guide", Description: "new source"})
			case "target":
				write(t, filepath.Join(m.Store.Local, filepath.FromSlash(p.Artifact.Path), "user.txt"), []byte("external"))
			}
			_, err = m.ApplyMCPArtifact(context.Background(), p.Name, preview.Plan.ID, preview.Plan.Digest)
			code(t, err, "PLAN_STALE")
		})
	}
}
func TestMCPArtifactRegisteredInstallationBlocksRollbackAndLateConfirmation(t *testing.T) {
	m, p, archive := artifactFixture(t, false)
	imported := applyArtifact(t, m, artifactEdit(t, m, p.Name, archive))
	s, _ := m.MCPArtifacts(context.Background())
	reverse := pluginartifact.Edit{Action: "rollback", Name: p.Name, Snapshot: imported.Plan.ID, ExpectedSHA256: s.Artifacts[0].SHA256, ExpectedLockSHA256: s.LockSHA256}
	preview, err := m.MCPArtifactPlan(context.Background(), reverse)
	if err != nil {
		t.Fatal(err)
	}
	installEdit(t, m, allowInstall(p.Name))
	_, err = m.MCPArtifactPlan(context.Background(), reverse)
	code(t, err, "ARTIFACT_IN_USE")
	_, err = m.ApplyMCPArtifact(context.Background(), p.Name, preview.Plan.ID, preview.Plan.Digest)
	code(t, err, "ARTIFACT_IN_USE")
	installEdit(t, m, plugininstall.Edit{Action: "remove", Name: p.Name})
	applyArtifact(t, m, reverse)
}

func TestMCPArtifactOtherProjectReferencesAndActualRootLocksPreventInterference(t *testing.T) {
	m, p, archive := artifactFixture(t, false)
	imported := applyArtifact(t, m, artifactEdit(t, m, p.Name, archive))
	ctx := context.Background()
	secondProject := filepath.Join(filepath.Dir(m.Store.Root), "second-project")
	if err := os.MkdirAll(secondProject, 0755); err != nil {
		t.Fatal(err)
	}
	second, err := New(filepath.Join(secondProject, ".aidock"), m.Store.Local, "22222222-2222-4222-8222-222222222222", m.Scope)
	if err != nil {
		t.Fatal(err)
	}
	apply(t, second, Request{Action: "create", Name: "second-guide", Description: "separate project"})
	config, err := m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	state, err := second.MCP()
	if err != nil {
		t.Fatal(err)
	}
	mp, err := second.MCPPlan(ctx, MCPEdit{ExpectedSHA256: state.SHA256, Config: config.Config})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = second.ApplyMCP(ctx, mp.Plan.ID, mp.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	lock, _ := m.MCPLock(ctx)
	emptyLock, _ := second.MCPLock(ctx)
	lp, err := second.MCPLockPlan(ctx, MCPLockEdit{ExpectedSHA256: emptyLock.SHA256, Document: lock.Document})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = second.ApplyMCPLock(ctx, lp.Plan.ID, lp.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	s, _ := m.MCPArtifacts(ctx)
	reverse := pluginartifact.Edit{Action: "rollback", Name: p.Name, Snapshot: imported.Plan.ID, ExpectedSHA256: s.Artifacts[0].SHA256, ExpectedLockSHA256: s.LockSHA256}
	preview, err := m.MCPArtifactPlan(ctx, reverse)
	if err != nil {
		t.Fatal(err)
	}
	var a *pluginartifact.Manager
	m.withArtifact(ctx, p.Name, func(x *pluginartifact.Manager, _ string) error { a = x; return nil })
	if err = a.Store.Locked(func() error { _, err := second.MCPInstall(ctx); code(t, err, "SOURCE_BUSY"); return nil }); err != nil {
		t.Fatal(err)
	}
	installEdit(t, second, allowInstall(p.Name))
	_, err = m.ApplyMCPArtifact(ctx, p.Name, preview.Plan.ID, preview.Plan.Digest)
	code(t, err, "ARTIFACT_IN_USE")
	_, err = m.MCPArtifactPlan(ctx, reverse)
	code(t, err, "ARTIFACT_IN_USE")
	installEdit(t, second, plugininstall.Edit{Action: "remove", Name: p.Name})
	applyArtifact(t, m, reverse)
}
func TestMCPArtifactInterruptedImportBlocksInstallAndRecoversEvenWithoutSourceLock(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprint(conflict), func(t *testing.T) {
			m, p, archive := artifactFixture(t, true)
			req := artifactEdit(t, m, p.Name, archive)
			err := m.withArtifact(context.Background(), p.Name, func(a *pluginartifact.Manager, hash string) error {
				preview, err := a.Plan(context.Background(), req, hash)
				if err != nil {
					return err
				}
				a.Store.Hook = func(phase string, _ int) error {
					if phase == "replaced" {
						panic("simulated crash")
					}
					return nil
				}
				func() {
					defer func() {
						if recover() == nil {
							t.Error("expected interrupted writer")
						}
					}()
					a.Apply(context.Background(), preview.Plan.ID, preview.Plan.Digest, hash)
				}()
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			s, err := m.MCPArtifacts(context.Background())
			if err != nil || !s.Artifacts[0].Recovery {
				t.Fatal(s, err)
			}
			lock, err := m.MCPLock(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, c := range lock.Plugins[0].Checks {
				if c.Code == "MCP_ARTIFACT_RECOVERY_REQUIRED" {
					found = true
				}
			}
			if !found {
				t.Fatal("partial artifact did not block installation", lock)
			}
			wrong, err := New(m.Store.Root, m.Store.Local, m.Owner, "client")
			if err != nil {
				t.Fatal(err)
			}
			_, err = wrong.RecoverMCPArtifact(context.Background(), p.Artifact.Path)
			code(t, err, "ARTIFACT_BINDING_MISMATCH")
			_, err = m.MCPArtifactPlan(context.Background(), req)
			code(t, err, "RECOVERY_CONFLICT")
			root := filepath.Join(m.Store.Local, filepath.FromSlash(p.Artifact.Path))
			if conflict {
				write(t, filepath.Join(root, ".hidden"), []byte("external user edit"))
			}
			write(t, filepath.Join(m.Store.Root, "plugins.lock"), []byte("broken: ["))
			n, err := m.RecoverMCPArtifact(context.Background(), p.Artifact.Path)
			if conflict {
				code(t, err, "RECOVERY_CONFLICT")
				b, _ := os.ReadFile(filepath.Join(root, ".hidden"))
				if string(b) != "external user edit" {
					t.Fatal("recovery overwrote external edit")
				}
			} else if err != nil || n != 1 {
				t.Fatal(n, err)
			}
		})
	}
}
func TestMCPArtifactInstallLockObservationAndCancellationAreRequired(t *testing.T) {
	m, p, archive := artifactFixture(t, false)
	req := artifactEdit(t, m, p.Name, archive)
	req.ExpectedLockSHA256 = "old"
	_, err := m.MCPArtifactPlan(context.Background(), req)
	code(t, err, "PLAN_STALE")
	req = artifactEdit(t, m, p.Name, archive)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = m.MCPArtifactPlan(ctx, req)
	if err != context.Canceled {
		t.Fatal(err)
	}
	for _, path := range []string{"../outside", "mcp-artifacts/../outside", "other/cache", "mcp-artifacts/CON"} {
		_, err = m.RecoverMCPArtifact(context.Background(), path)
		if err == nil {
			t.Fatal("accepted unsafe recovery path", path)
		}
	}
	// Recomputing the storage plan digest cannot approve a different payload.
	preview, err := m.MCPArtifactPlan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var a *pluginartifact.Manager
	var sourceHash string
	m.withArtifact(context.Background(), p.Name, func(x *pluginartifact.Manager, hash string) error { a = x; sourceHash = hash; return nil })
	if a == nil {
		t.Fatal("missing manager")
	}
	after := workstore.Clone(preview.Plan.After)
	delete(after.Entries, ".hidden")
	tampered, err := a.Store.PreviewBound(preview.Plan.Action, preview.Plan.Before, after, preview.Plan.Inputs)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Apply(context.Background(), tampered.ID, tampered.Digest, sourceHash)
	code(t, err, "PLAN_STALE")
}

func TestMCPArtifactActualProcessExitAndLargeStagedFileRecovery(t *testing.T) {
	for _, phase := range []string{"replaced", "staged-large"} {
		t.Run(phase, func(t *testing.T) {
			m, p, archive := artifactFixture(t, true)
			req := artifactEdit(t, m, p.Name, archive)
			preview, err := m.MCPArtifactPlan(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(map[string]string{"root": m.Store.Root, "local": m.Store.Local, "name": p.Name, "id": preview.Plan.ID, "digest": preview.Plan.Digest, "phase": phase})
			command := exec.Command(os.Args[0], "-test.run=^TestMCPArtifactExitHelper$")
			command.Env = append(os.Environ(), "AIDOCK_ARTIFACT_EXIT_HELPER="+string(data))
			output, err := command.CombinedOutput()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 94 {
				t.Fatalf("exit=%v output=%s", err, output)
			}
			state, err := m.MCPArtifacts(context.Background())
			if err != nil || !state.Artifacts[0].Recovery {
				t.Fatal(state, err)
			}
			fresh, err := New(m.Store.Root, m.Store.Local, m.Owner, m.Scope)
			if err != nil {
				t.Fatal(err)
			}
			n, err := fresh.RecoverMCPArtifact(context.Background(), p.Artifact.Path)
			if err != nil || n != 1 {
				t.Fatal(n, err)
			}
			if _, err = os.Stat(preview.Candidate.Root); !os.IsNotExist(err) {
				t.Fatal("process recovery retained target", err)
			}
		})
	}
}
func TestMCPArtifactExitHelper(t *testing.T) {
	value := os.Getenv("AIDOCK_ARTIFACT_EXIT_HELPER")
	if value == "" {
		t.Skip("subprocess helper")
	}
	var data map[string]string
	if err := json.Unmarshal([]byte(value), &data); err != nil {
		t.Fatal(err)
	}
	m, err := New(data["root"], data["local"], owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	err = m.withArtifact(context.Background(), data["name"], func(a *pluginartifact.Manager, hash string) error {
		staged := 0
		a.Store.Hook = func(phase string, _ int) error {
			if phase == "staged" {
				staged++
			}
			if data["phase"] == "replaced" && phase == "replaced" || data["phase"] == "staged-large" && phase == "staged" && staged == 5 {
				os.Exit(94)
			}
			return nil
		}
		_, err := a.Apply(context.Background(), data["id"], data["digest"], hash)
		return err
	})
	t.Fatal("expected helper exit", err)
}
