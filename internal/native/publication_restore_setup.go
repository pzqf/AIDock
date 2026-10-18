package native

import (
	"context"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/pluginartifact"
	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/privatefs"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

type PublicationRestorationSetupPlan struct {
	Version       int    `json:"schema_version"`
	ID            string `json:"id"`
	Digest        string `json:"digest"`
	Candidate     string `json:"candidate_digest"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	Before        string `json:"before"`
	After         string `json:"after"`
	ArchiveSHA256 string `json:"archive_sha256,omitempty"`
	RiskAccepted  bool   `json:"risk_accepted"`
	Boundary      string `json:"boundary"`
}

type publicationRestorationSetup struct {
	Preview         PublicationRestorationSetupPlan `json:"preview"`
	OperationDigest string                          `json:"operation_digest"`
}

func publicationRestorationSetupDigest(p publicationRestorationSetup) string {
	p.Preview.Digest = ""
	return workstore.Hash(jsonData(p))
}

func (m *Manager) publicationRestorationSetupFile(id string) string {
	return filepath.Join(m.connectionArea(), "publication-restore-setup", id+".json")
}

func (c PublicationRestorationCandidate) Artifacts(ctx context.Context) (source.MCPArtifactState, error) {
	if err := c.validate(ctx); err != nil {
		return source.MCPArtifactState{}, err
	}
	return c.manager.Source.ReplayArtifacts(ctx, c.draft)
}

func (c PublicationRestorationCandidate) Installations(ctx context.Context) (plugininstall.State, error) {
	if err := c.validate(ctx); err != nil {
		return plugininstall.State{}, err
	}
	return c.manager.Source.ReplayInstallations(ctx, c.draft)
}

func (c PublicationRestorationCandidate) saveSetup(preview PublicationRestorationSetupPlan, operation string) (PublicationRestorationSetupPlan, error) {
	preview.Version, preview.Candidate = 1, c.digest
	p := publicationRestorationSetup{Preview: preview, OperationDigest: operation}
	preview.Digest = publicationRestorationSetupDigest(p)
	p.Preview = preview
	file := c.manager.publicationRestorationSetupFile(preview.ID)
	if err := privatefs.Ensure(filepath.Dir(file)); err != nil {
		return PublicationRestorationSetupPlan{}, err
	}
	if err := save(file, jsonData(p)); err != nil {
		return PublicationRestorationSetupPlan{}, err
	}
	return preview, nil
}

func (c PublicationRestorationCandidate) PlanArtifact(ctx context.Context, edit pluginartifact.Edit) (PublicationRestorationSetupPlan, error) {
	if err := c.validate(ctx); err != nil {
		return PublicationRestorationSetupPlan{}, err
	}
	p, err := c.manager.Source.PlanReplayArtifact(ctx, c.draft, edit)
	if err != nil {
		return PublicationRestorationSetupPlan{}, err
	}
	return c.saveSetup(PublicationRestorationSetupPlan{ID: p.Plan.ID, Name: edit.Name, Kind: "artifact", Before: p.Before.Status, After: p.Candidate.Status, ArchiveSHA256: p.ArchiveSHA256, Boundary: "只导入原应用精确锁定的完整 ZIP 到其本机产物目录；旧版本保留，不执行包、不登记安装或授权。确认绑定本次真实共同恢复节点，随后仍须独立审查安装、权限、运行时和新参数。"}, p.Plan.Digest)
}

func (c PublicationRestorationCandidate) PlanInstallation(ctx context.Context, edit plugininstall.Edit) (PublicationRestorationSetupPlan, error) {
	if err := c.validate(ctx); err != nil {
		return PublicationRestorationSetupPlan{}, err
	}
	p, err := c.manager.Source.PlanReplayInstallation(ctx, c.draft, edit)
	if err != nil {
		return PublicationRestorationSetupPlan{}, err
	}
	preview := PublicationRestorationSetupPlan{ID: p.Plan.ID, Name: edit.Name, Kind: "install", RiskAccepted: edit.AllowUnverified, Boundary: "只将此副本的安装登记替换为原合同精确锁定的版本，保留产物与追加审计。此操作不恢复旧授权、不运行包或应用项目；当前安装对应的旧权限可能失效，须明确重新授权及核验。"}
	for _, i := range p.Before.Installations {
		if i.Record.Plugin.Name == edit.Name {
			preview.Before = i.Record.Plugin.Version
		}
	}
	for _, i := range p.Candidate.Installations {
		if i.Record.Plugin.Name == edit.Name {
			preview.After = i.Record.Plugin.Version
		}
	}
	return c.saveSetup(preview, p.Plan.Digest)
}

func (c PublicationRestorationCandidate) applySetup(ctx context.Context, kind, name, id, digest string) (workstore.Journal, error) {
	if err := c.validate(ctx); err != nil {
		return workstore.Journal{}, err
	}
	if !validID.MatchString(id) || !validDigest.MatchString(digest) {
		return workstore.Journal{}, result.Fail("INVALID_INPUT", "需要本次恢复准备计划 UUID 和完整摘要", 2)
	}
	raw, err := workstore.ReadFile(c.manager.publicationRestorationSetupFile(id))
	if err != nil {
		return workstore.Journal{}, err
	}
	var p publicationRestorationSetup
	if strict(raw, &p) != nil || p.Preview.Version != 1 || p.Preview.ID != id || p.Preview.Kind != kind || p.Preview.Name != name || p.Preview.Candidate != c.digest || p.Preview.Digest != digest || publicationRestorationSetupDigest(p) != digest || !validDigest.MatchString(p.OperationDigest) {
		return workstore.Journal{}, result.Fail("PLAN_STALE", "准备确认与原合同、共同恢复节点或操作不一致", 3)
	}
	switch kind {
	case "artifact":
		return c.manager.Source.ApplyReplayArtifact(ctx, c.draft, name, id, p.OperationDigest)
	case "install":
		return c.manager.Source.ApplyReplayInstallation(ctx, c.draft, name, id, p.OperationDigest)
	}
	return workstore.Journal{}, result.Fail("INVALID_INPUT", "未知恢复准备操作", 2)
}

func (c PublicationRestorationCandidate) ApplyArtifact(ctx context.Context, name, id, digest string) (workstore.Journal, error) {
	return c.applySetup(ctx, "artifact", name, id, digest)
}
func (c PublicationRestorationCandidate) ApplyInstallation(ctx context.Context, name, id, digest string) (workstore.Journal, error) {
	return c.applySetup(ctx, "install", name, id, digest)
}
