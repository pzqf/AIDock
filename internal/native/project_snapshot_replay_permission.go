package native

import (
	"context"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/privatefs"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

type SnapshotReplayPermissionReview struct {
	Snapshot   string                       `json:"snapshot_id"`
	Project    string                       `json:"project"`
	Scope      string                       `json:"scope"`
	Copy       string                       `json:"copy"`
	SourceRoot string                       `json:"source_root"`
	Restore    *SnapshotReplayRestoreTarget `json:"restore,omitempty"`
	source.ReplayPermissionReview
}

type SnapshotReplayPermissionRequest struct {
	Restore       *SnapshotReplayRestoreTarget `json:"restore,omitempty"`
	Snapshot      string                       `json:"snapshot_id"`
	Candidate     string                       `json:"candidate_digest"`
	Authorization pluginpermission.Edit        `json:"authorization"`
}

func ParseSnapshotReplayPermissionRequest(raw []byte) (SnapshotReplayPermissionRequest, error) {
	var out SnapshotReplayPermissionRequest
	bad := func() (SnapshotReplayPermissionRequest, error) {
		return SnapshotReplayPermissionRequest{}, result.Fail("INVALID_INPUT", "需要明确快照、候选摘要和本次授权请求；不接受重复键、别名、空值或未知字段", 2)
	}
	if len(raw) > 65536 {
		return bad()
	}
	parsed, err := config.Parse(raw)
	if err != nil {
		return bad()
	}
	fields, ok := parsed.(map[string]any)
	expected := 3
	if _, exists := fields["restore"]; exists {
		expected++
	}
	if !ok || len(fields) != expected {
		return bad()
	}
	for key, value := range fields {
		switch key {
		case "snapshot_id", "candidate_digest":
			if _, ok := value.(string); !ok {
				return bad()
			}
		case "restore":
			out.Restore, err = parseReplayRestoreTarget(value)
			if err != nil {
				return bad()
			}
		case "authorization":
			if _, ok := value.(map[string]any); !ok {
				return bad()
			}
		default:
			return bad()
		}
	}
	out.Snapshot, _ = fields["snapshot_id"].(string)
	out.Candidate, _ = fields["candidate_digest"].(string)
	if !validID.MatchString(out.Snapshot) || !validDigest.MatchString(out.Candidate) {
		return bad()
	}
	out.Authorization, err = pluginpermission.ParseEdit(jsonData(fields["authorization"]))
	if err != nil || out.Authorization.Action != "grant" {
		return bad()
	}
	return out, nil
}

type SnapshotReplayPermissionPlan struct {
	Restore       *SnapshotReplayRestoreTarget `json:"restore,omitempty"`
	Version       int                          `json:"schema_version"`
	ID            string                       `json:"id"`
	Snapshot      string                       `json:"snapshot_id"`
	Candidate     string                       `json:"candidate_digest"`
	Name          string                       `json:"name"`
	ReplacesGrant bool                         `json:"replaces_grant"`
	Digest        string                       `json:"digest"`
	Boundary      string                       `json:"boundary"`
}

type snapshotReplayPermissionPlan struct {
	Preview          SnapshotReplayPermissionPlan `json:"preview"`
	Commit           string                       `json:"commit"`
	PermissionDigest string                       `json:"permission_digest"`
}

func replayPermissionDigest(p snapshotReplayPermissionPlan) string {
	p.Preview.Digest = ""
	return workstore.Hash(jsonData(p))
}

func (m *Manager) replayPermissionFile(id string) string {
	key := workstore.Hash(jsonData([]string{m.Source.Owner, m.Source.Scope, m.Source.Copy, m.Source.Store.Root}))
	return filepath.Join(m.Source.Store.Local, "snapshot-replay-permission-reviews", key, id+".json")
}

// Rebuild from immutable archived bytes on every read/plan/confirm. No request
// may supply a source tree, raw profile, root, authority or permission record.
func (m *Manager) snapshotReplayCandidate(ctx context.Context, id string) (source.ReplayDraft, string, error) {
	var d source.ReplayDraft
	if !validID.MatchString(id) {
		return d, "", result.Fail("INVALID_INPUT", "需要明确的应用快照 UUID", 2)
	}
	a, err := m.projectSnapshotArchive()
	if err != nil {
		return d, "", err
	}
	r, err := a.Materialize(ctx, id, m.Source.Store)
	if err != nil {
		return d, "", err
	}
	if r.Configuration == nil {
		return d, "", result.Fail("SNAPSHOT_CONFIGURATION_MISSING", "旧快照没有原始配置层，不能推测回放候选", 4)
	}
	d, err = m.Source.DraftReplay(ctx, r.Source, r.Effective, *r.Configuration)
	return d, r.Commit, err
}

func (m *Manager) ReviewSnapshotReplayPermissions(ctx context.Context, id string) (SnapshotReplayPermissionReview, error) {
	return m.ReviewSnapshotReplayTarget(ctx, id, nil)
}
func (m *Manager) ReviewSnapshotReplayTarget(ctx context.Context, id string, restore *SnapshotReplayRestoreTarget) (SnapshotReplayPermissionReview, error) {
	out := SnapshotReplayPermissionReview{Snapshot: id, Restore: restore, Project: m.Source.Owner, Scope: m.Source.Scope, Copy: m.Source.Copy, SourceRoot: m.Source.Store.Root}
	d, _, err := m.snapshotReplayCandidateFor(ctx, id, restore)
	if err != nil {
		return out, err
	}
	out.ReplayPermissionReview, err = m.Source.ReplayPermissions(ctx, d)
	return out, err
}

func (m *Manager) PlanSnapshotReplayPermission(ctx context.Context, req SnapshotReplayPermissionRequest) (SnapshotReplayPermissionPlan, error) {
	var out SnapshotReplayPermissionPlan
	if !validDigest.MatchString(req.Candidate) || req.Authorization.Action != "grant" {
		return out, result.Fail("INVALID_INPUT", "需要已审查候选的完整摘要及本次授权", 2)
	}
	d, commit, err := m.snapshotReplayCandidateFor(ctx, req.Snapshot, req.Restore)
	if err != nil {
		return out, err
	}
	if d.Digest != req.Candidate {
		return out, result.Fail("PLAN_STALE", "回放候选或当前草稿已变化，请重新审查", 3)
	}
	p, err := m.Source.PlanReplayPermission(ctx, d, req.Authorization)
	if err != nil {
		return out, err
	}
	replaces := false
	for _, entry := range p.Before.Authorizations {
		if entry.Record.Input.Name == req.Authorization.Name {
			replaces = true
		}
	}
	impact := "新增候选授权"
	if replaces {
		impact = "替换同名现有授权，被替换的旧授权与运行时证据会失效"
	}
	out = SnapshotReplayPermissionPlan{Version: 1, ID: p.Plan.ID, Snapshot: req.Snapshot, Restore: req.Restore, Candidate: d.Digest, Name: req.Authorization.Name, ReplacesGrant: replaces, Boundary: "本次将为 " + req.Authorization.Name + " " + impact + "，记录项目授权及审计。授权摘要会变化，现有连接需重新检查和生成。项目源、本机覆盖和工具文件不变，不启动程序。新授权仅匹配冻结候选；共同回放须另行检查完整候选并预览确认。撤销使用项目授权入口，恢复旧配置仍需本次重新授权。"}
	stored := snapshotReplayPermissionPlan{Preview: out, Commit: commit, PermissionDigest: p.Plan.Digest}
	out.Digest = replayPermissionDigest(stored)
	stored.Preview = out
	file := m.replayPermissionFile(out.ID)
	if err = privatefs.Ensure(filepath.Dir(file)); err != nil {
		return out, err
	}
	err = save(file, jsonData(stored))
	return out, err
}

func (m *Manager) ApplySnapshotReplayPermission(ctx context.Context, id, digest string) (workstore.Journal, error) {
	var out workstore.Journal
	if !validID.MatchString(id) || !validDigest.MatchString(digest) {
		return out, result.Fail("INVALID_INPUT", "需要候选授权计划 UUID 和完整确认摘要", 2)
	}
	raw, err := workstore.ReadFile(m.replayPermissionFile(id))
	if err != nil {
		return out, err
	}
	var p snapshotReplayPermissionPlan
	if strict(raw, &p) != nil || p.Preview.Version != 1 || p.Preview.ID != id || p.Preview.Digest != digest || replayPermissionDigest(p) != digest || !validID.MatchString(p.Preview.Snapshot) || !validDigest.MatchString(p.PermissionDigest) {
		return out, result.Fail("PLAN_STALE", "候选授权确认、快照或关联记录不一致", 3)
	}
	d, commit, err := m.snapshotReplayCandidateFor(ctx, p.Preview.Snapshot, p.Preview.Restore)
	if err != nil {
		return out, err
	}
	if d.Digest != p.Preview.Candidate || commit != p.Commit {
		return out, result.Fail("PLAN_STALE", "确认前候选、快照或当前草稿变化；未更改授权", 3)
	}
	return m.Source.ApplyReplayPermission(ctx, d, p.Preview.Name, id, p.PermissionDigest)
}
