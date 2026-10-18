package native

import (
	"context"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/privatefs"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type PublicationRestorationPermissionPlan struct {
	Version       int    `json:"schema_version"`
	ID            string `json:"id"`
	Candidate     string `json:"candidate_digest"`
	Name          string `json:"name"`
	ReplacesGrant bool   `json:"replaces_grant"`
	Digest        string `json:"digest"`
	Boundary      string `json:"boundary"`
}

type publicationRestorationPermission struct {
	Preview          PublicationRestorationPermissionPlan `json:"preview"`
	PermissionDigest string                               `json:"permission_digest"`
}

func publicationRestorationPermissionDigest(p publicationRestorationPermission) string {
	p.Preview.Digest = ""
	return workstore.Hash(jsonData(p))
}

func (m *Manager) publicationRestorationPermissionFile(id string) string {
	return filepath.Join(m.connectionArea(), "publication-restore-permissions", id+".json")
}

func (c PublicationRestorationCandidate) planPermission(ctx context.Context, edit pluginpermission.Edit) (PublicationRestorationPermissionPlan, error) {
	var out PublicationRestorationPermissionPlan
	p, err := c.manager.Source.PlanReplayPermission(ctx, c.draft, edit)
	if err != nil {
		return out, err
	}
	out = PublicationRestorationPermissionPlan{Version: 1, ID: p.Plan.ID, Candidate: c.digest, Name: edit.Name, Boundary: "只为本次明确的原应用与真实共同恢复提案授权。确认会替换同名现有授权并使旧参数失效；不恢复旧权限，不启动 MCP，也不应用项目或工具。随后须重新核验运行时、检查完整候选、生成新参数并单独确认共同恢复。"}
	for _, a := range p.Before.Authorizations {
		out.ReplacesGrant = out.ReplacesGrant || a.Record.Input.Name == edit.Name
	}
	stored := publicationRestorationPermission{Preview: out, PermissionDigest: p.Plan.Digest}
	out.Digest = publicationRestorationPermissionDigest(stored)
	stored.Preview = out
	file := c.manager.publicationRestorationPermissionFile(out.ID)
	if err = privatefs.Ensure(filepath.Dir(file)); err != nil {
		return PublicationRestorationPermissionPlan{}, err
	}
	if err = save(file, jsonData(stored)); err != nil {
		return PublicationRestorationPermissionPlan{}, err
	}
	return out, nil
}

func (c PublicationRestorationCandidate) applyPermission(ctx context.Context, name, id, digest string) (workstore.Journal, error) {
	if !validID.MatchString(id) || !validDigest.MatchString(digest) {
		return workstore.Journal{}, result.Fail("INVALID_INPUT", "需要本次原应用授权计划 UUID 与完整摘要", 2)
	}
	raw, err := workstore.ReadFile(c.manager.publicationRestorationPermissionFile(id))
	if err != nil {
		return workstore.Journal{}, err
	}
	var p publicationRestorationPermission
	if strict(raw, &p) != nil || p.Preview.Version != 1 || p.Preview.ID != id || p.Preview.Name != name || p.Preview.Candidate != c.digest || p.Preview.Digest != digest || publicationRestorationPermissionDigest(p) != digest || !validDigest.MatchString(p.PermissionDigest) {
		return workstore.Journal{}, result.Fail("PLAN_STALE", "授权确认与原应用、共同恢复提案或当前草稿不一致", 3)
	}
	return c.manager.Source.ApplyReplayPermission(ctx, c.draft, name, id, p.PermissionDigest)
}
