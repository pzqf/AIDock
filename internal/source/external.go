package source

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/bridge"
	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

const externalFile = "external-skills.yaml"

// Inspect previously approved authority roots before a lock/blob write. A
// different --home must not redirect observation data inside an external root.
func (m *Manager) externalWriteBoundaries() error {
	dir := filepath.Join(m.Store.Root, ".external-approvals")
	if err := workstore.CheckLinks(dir); err != nil {
		return result.Fail("PATH_UNSAFE", err.Error(), 5)
	}
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if os.IsNotExist(err) && p == dir {
			return nil
		}
		if err != nil {
			return err
		}
		if err := workstore.CheckLinks(p); err != nil {
			return result.Fail("PATH_UNSAFE", err.Error(), 5)
		}
		if d.IsDir() {
			return nil
		}
		data, err := workstore.ReadFile(p)
		if err != nil {
			return err
		}
		if _, err = config.Validate("external-approval", data); err != nil {
			return result.Fail("REVISION_CORRUPT", err.Error(), 5)
		}
		var a ExternalApproval
		if err = json.Unmarshal(data, &a); err != nil {
			return err
		}
		if !filepath.IsAbs(a.Binding.Root) {
			return result.Fail("REVISION_CORRUPT", "批准根不是绝对路径", 5)
		}
		for _, write := range []string{m.Store.Root, m.Store.Local} {
			if workstore.Within(write, a.Binding.Root) || workstore.Within(a.Binding.Root, write) {
				return result.Fail("INVALID_ROOTS", "项目/事务写入根不能与已批准外部根重叠", 2)
			}
		}
		return nil
	})
}

// The authority is Binding.Root, never the project cache. An approval selects
// exact bytes for this project/copy; it does not grant editing or execution.
type ExternalApproval struct {
	ApprovalID        string              `json:"approval_id"`
	Binding           bridge.Registration `json:"binding"`
	PackagePath       string              `json:"package_path"`
	BridgeRevision    string              `json:"bridge_revision"`
	CandidateRevision string              `json:"candidate_revision"`
	ApprovedAt        string              `json:"approved_at"`
	Skill             Skill               `json:"skill"`
}
type ExternalIndex struct {
	Version int                `json:"schema_version"`
	Owner   string             `json:"owner"`
	Scope   string             `json:"scope_id"`
	Skills  []ExternalApproval `json:"skills"`
}

func approvalPath(a ExternalApproval) string {
	return ".external-approvals/" + a.Skill.ID + "/" + a.ApprovalID + ".json"
}
func cleanRegistration(b bridge.Registration) bridge.Registration {
	b.ApprovedRevision, b.ApprovedAt, b.ApprovedBy = "", "", ""
	return b
}

// The console reads source state before an adoption preview. Protect known
// authority roots even on that first read, before its project lock is created.
// A missing/invalid registration does not prevent reading an approved cache;
// adoption and bridge checking still validate the registration independently.
func (m *Manager) BindBridge(file, copyID string) error {
	m.BridgeFile, m.Copy = file, copyID
	if err := m.RestoreProfileLibrary(); err != nil {
		return err
	}
	return m.BindBridgeObservation(file, copyID)
}

// BindBridgeObservation protects registered authority roots without opening a
// possibly damaged Local profile binding. Only archive observation entrypoints
// use this bootstrap; ordinary application still uses BindBridge and its gates.
func (m *Manager) BindBridgeObservation(file, copyID string) error {
	m.BridgeFile, m.Copy = file, copyID
	if file == "" {
		return nil
	}
	data, err := config.ReadFile(file)
	if err != nil {
		return nil
	}
	idx, err := bridge.Decode(file, data)
	if err != nil {
		return nil
	}
	return m.externalRootBoundaries(idx)
}
func (m *Manager) externalRootBoundaries(idx bridge.Index) error {
	for _, b := range idx.Bridges {
		for _, write := range []string{m.Store.Root, m.Store.Local} {
			if workstore.Within(write, b.Root) || workstore.Within(b.Root, write) {
				return result.Fail("INVALID_ROOTS", "项目/事务写入根不能与外部登记根重叠", 2)
			}
		}
	}
	return nil
}
func (m *Manager) externalRegistration() (bridge.Index, string, error) {
	if m.Owner == "global" || m.BridgeFile == "" || strings.TrimSpace(m.Copy) == "" {
		return bridge.Index{}, "", result.Fail("BRIDGE_BINDING_REQUIRED", "外部采纳需要项目绑定、--bridge-file 和明确 --copy", 4)
	}
	file, err := filepath.Abs(m.BridgeFile)
	if err != nil {
		return bridge.Index{}, "", err
	}
	data, err := config.ReadFile(file)
	if err != nil {
		return bridge.Index{}, "", err
	}
	idx, err := bridge.Decode(file, data)
	if err != nil {
		return idx, "", result.Fail("INVALID_BRIDGE", err.Error(), 2)
	}
	// Protect the entire declared root, including generators' unselected files.
	if err = m.externalRootBoundaries(idx); err != nil {
		return idx, "", err
	}
	last, err := config.ReadFile(file)
	if err != nil || workstore.Hash(data) != workstore.Hash(last) {
		return idx, "", result.Fail("PLAN_STALE", "登记文件在读取期间变化", 3)
	}
	return idx, workstore.Hash(data), nil
}
func (m *Manager) checkApproval(t *workstore.Tree, a ExternalApproval) error {
	if _, err := config.Validate("external-approval", jsonBytes(a)); err != nil {
		return result.Fail("REVISION_CORRUPT", err.Error(), 5)
	}
	b, s := a.Binding, a.Skill
	if _, err := config.Validate("bridge", jsonBytes(bridge.Index{Version: 1, Bridges: []bridge.Registration{b}})); err != nil {
		return result.Fail("REVISION_CORRUPT", err.Error(), 5)
	}
	if _, err := config.Validate("skills", jsonBytes(Index{Version: 1, Owner: m.Owner, Scope: m.Scope, Skills: []Skill{s}})); err != nil {
		return result.Fail("REVISION_CORRUPT", err.Error(), 5)
	}
	if b.Project != m.Owner || b.Scope != m.Scope || m.Owner == "global" || (m.Copy != "" && b.Copy != m.Copy) {
		return result.Fail("SOURCE_IDENTITY_MISMATCH", "批准缓存的 project/scope/copy 与当前绑定不符", 3)
	}
	if !filepath.IsAbs(b.Root) || filepath.Clean(b.Root) != b.Root || b.ApprovedRevision != "" || b.ApprovedAt != "" || b.ApprovedBy != "" ||
		(a.PackagePath != b.SourcePath && !strings.HasPrefix(a.PackagePath, b.SourcePath+"/")) ||
		s.Source.Mode != "external-source" || s.Source.Path != a.PackagePath || s.Source.Origin != b.ID || s.Source.Format != b.Format ||
		s.RevisionFormat != "external-package-v1" || s.Revision != a.CandidateRevision || len(s.BaseSource) != 0 || len(s.Upstream) != 0 {
		return result.Fail("REVISION_CORRUPT", "批准来源/包身份不符", 5)
	}
	if len(s.Dependencies) != 0 || len(s.Compatibility) != 0 || len(s.Requirements) != 0 {
		return result.Fail("REVISION_CORRUPT", "本阶段外部治理元数据未开放；不能伪造未绑定摘要的声明", 5)
	}
	if err := workstore.SafeRelative(a.PackagePath); err != nil {
		return result.Fail("REVISION_CORRUPT", err.Error(), 5)
	}
	if _, err := time.Parse(time.RFC3339, a.ApprovedAt); err != nil {
		return result.Fail("REVISION_CORRUPT", "无效批准时间", 5)
	}
	pkg, err := m.cached(t, s)
	if err != nil {
		return err
	}
	if bridge.PackageRevision(pkg) != a.CandidateRevision {
		return result.Fail("REVISION_CORRUPT", "批准包字节/可执行位与候选摘要不符", 5)
	}
	return nil
}
func (m *Manager) loadExternal(t *workstore.Tree) (ExternalIndex, error) {
	idx := ExternalIndex{Version: 1, Owner: m.Owner, Scope: m.Scope, Skills: []ExternalApproval{}}
	b, err := m.treeRead(t, externalFile)
	if os.IsNotExist(err) {
		return idx, nil
	}
	if err != nil {
		return idx, err
	}
	v, err := config.Validate("external-skills", b)
	if err != nil {
		return idx, result.Fail("SOURCE_METADATA_INVALID", err.Error(), 5)
	}
	if err = json.Unmarshal(jsonBytes(v), &idx); err != nil {
		return idx, err
	}
	if idx.Owner != m.Owner || idx.Scope != m.Scope {
		return idx, result.Fail("SOURCE_IDENTITY_MISMATCH", "外部选择文件 owner/scope 不匹配", 3)
	}
	ids, bindings := map[string]bool{}, map[string]bool{}
	for _, a := range idx.Skills {
		key := a.Binding.ID + "/" + strings.ToLower(a.PackagePath)
		if ids[a.Skill.ID] || bindings[key] {
			return idx, result.Fail("SKILL_ID_COLLISION", "外部批准身份/包重复", 3)
		}
		ids[a.Skill.ID], bindings[key] = true, true
		if err = m.checkApproval(t, a); err != nil {
			return idx, err
		}
		record, e := m.treeRead(t, approvalPath(a))
		if e != nil || string(record) != string(jsonBytes(a)) {
			return idx, result.Fail("REVISION_CORRUPT", "不可变批准记录缺失或变化", 5)
		}
	}
	return idx, nil
}
func (m *Manager) externalHistory() ([]ExternalApproval, error) {
	out := []ExternalApproval{}
	t, err := m.Store.Capture(filepath.Join(m.Store.Root, ".external-approvals"))
	if err != nil {
		return out, err
	}
	for p, e := range t.Entries {
		if e.Dir {
			continue
		}
		data, err := m.Store.Data(e)
		if err != nil {
			return out, err
		}
		var a ExternalApproval
		if _, err = config.Validate("external-approval", data); err != nil {
			return out, result.Fail("REVISION_CORRUPT", err.Error(), 5)
		}
		if err = json.Unmarshal(data, &a); err != nil {
			return out, err
		}
		if p != strings.TrimPrefix(approvalPath(a), ".external-approvals/") {
			return out, result.Fail("REVISION_CORRUPT", "批准记录路径与身份不符", 5)
		}
		if err = m.checkApproval(nil, a); err != nil {
			return out, err
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ApprovedAt == out[j].ApprovedAt {
			return out[i].ApprovalID < out[j].ApprovalID
		}
		return out[i].ApprovedAt > out[j].ApprovedAt
	})
	return out, nil
}
func (m *Manager) externalCandidate(ctx context.Context, id, pkg, revision, bridgeRevision string) (bridge.Registration, bridge.Candidate, string, error) {
	idx, hash, err := m.externalRegistration()
	if err != nil {
		return bridge.Registration{}, bridge.Candidate{}, "", err
	}
	report, err := bridge.Check(ctx, idx)
	if err != nil {
		return bridge.Registration{}, bridge.Candidate{}, "", err
	}
	for i, r := range report.Bridges {
		if r.ID != id {
			continue
		}
		b := idx.Bridges[i]
		if b.Project != m.Owner || b.Scope != m.Scope || b.Copy != m.Copy {
			return b, bridge.Candidate{}, "", result.Fail("SOURCE_IDENTITY_MISMATCH", "候选 project/scope/copy 与当前项目绑定不符", 3)
		}
		if r.Status == "FAIL" || r.CurrentRevision == "" || r.CurrentRevision != bridgeRevision {
			return b, bridge.Candidate{}, "", result.Fail("PLAN_STALE", "完整外部候选不可用或集合 revision 已变化", 3)
		}
		for _, c := range r.Candidates {
			if c.Skill.Path == pkg && c.Revision == revision && revision != "" {
				return cleanRegistration(b), c, hash, nil
			}
		}
		return b, bridge.Candidate{}, "", result.Fail("PLAN_STALE", "所选包路径或精确 revision 已变化", 3)
	}
	return bridge.Registration{}, bridge.Candidate{}, "", result.Fail("BRIDGE_NOT_FOUND", "未找到所选外部登记", 2)
}
func (m *Manager) validateExternalInputs(ctx context.Context, inputs map[string]string) error {
	file, err := filepath.Abs(m.BridgeFile)
	if err != nil || len(inputs) != 8 || inputs["bridge_file"] != file {
		return result.Fail("PLAN_STALE", "外部采纳登记文件绑定已改变", 3)
	}
	_, _, hash, err := m.externalCandidate(ctx, inputs["bridge_id"], inputs["package_path"], inputs["revision"], inputs["bridge_revision"])
	if err != nil || hash != inputs["registration_hash"] || m.Copy != inputs["copy"] {
		return result.Fail("PLAN_STALE", "外部源、登记或副本在预览后变化；请重新检查并预览", 3)
	}
	return nil
}
func (m *Manager) externalAction(ctx context.Context, req Request, out *Preview, before workstore.Tree, after *workstore.Tree, idx Index, profile map[string]any, effective []EffectiveSkill) error {
	ext, err := m.loadExternal(&before)
	if err != nil {
		return err
	}
	var approval ExternalApproval
	at := -1
	if req.Action == "external-adopt" {
		if !uuid.MatchString(req.BridgeID) || !revisionPattern.MatchString(req.Revision) || !revisionPattern.MatchString(req.BridgeRevision) || workstore.SafeRelative(req.PackagePath) != nil {
			return result.Fail("INVALID_INPUT", "需要 bridge_id、精确包路径、包 revision 和集合 bridge_revision", 2)
		}
		binding, candidate, hash, err := m.externalCandidate(ctx, req.BridgeID, req.PackagePath, req.Revision, req.BridgeRevision)
		if err != nil {
			return err
		}
		for i, a := range ext.Skills {
			if a.Binding.ID == binding.ID && a.PackagePath == req.PackagePath {
				at = i
				// Selection policy may change, but retargeting an authority cannot
				// silently reuse an asset's identity.
				old, now := a.Binding, binding
				old.Packages, now.Packages = nil, nil
				if string(jsonBytes(old)) != string(jsonBytes(now)) {
					return result.Fail("EXTERNAL_BINDING_CHANGED", "外部原源身份已改变，不能自动重用批准资产 ID", 3)
				}
			}
		}
		pkg, scanned, err := m.validatePackage(ctx, filepath.Join(binding.Root, filepath.FromSlash(req.PackagePath)))
		if err != nil {
			return err
		}
		if bridge.PackageRevision(pkg) != candidate.Revision || scanned.Name != candidate.Skill.Name {
			return result.Fail("PLAN_STALE", "包在候选检查后变化", 3)
		}
		id := workstore.ID()
		if at >= 0 {
			id = ext.Skills[at].Skill.ID
		} else {
			// Rollback preserves approvals. Re-adoption reuses that stable
			// identity rather than creating a second asset for the same source.
			history, err := m.externalHistory()
			if err != nil {
				return err
			}
			known := ""
			for _, a := range history {
				if a.Binding.ID != binding.ID || a.PackagePath != req.PackagePath {
					continue
				}
				old, now := a.Binding, binding
				old.Packages, now.Packages = nil, nil
				if string(jsonBytes(old)) != string(jsonBytes(now)) {
					return result.Fail("EXTERNAL_BINDING_CHANGED", "保留的批准记录属于另一原源身份", 3)
				}
				if known != "" && known != a.Skill.ID {
					return result.Fail("SKILL_ID_COLLISION", "同一外部包存在不同历史资产 ID", 3)
				}
				known = a.Skill.ID
			}
			if known != "" {
				id = known
			}
		}
		s := Skill{ID: id, Name: scanned.Name, Source: Origin{Mode: "external-source", Path: req.PackagePath, Format: binding.Format, Origin: binding.ID},
			PackageFormat: map[string]string{"id": "native-skill", "version": "1"}, Files: scanned.Files, Dependencies: map[string]any{}, Compatibility: map[string]any{}, Requirements: map[string]any{}, RevisionFormat: "external-package-v1", PackageDigest: portablePackageDigest(pkg)}
		s.Revision = packageRevision(s, pkg)
		approval = ExternalApproval{ApprovalID: workstore.ID(), Binding: binding, PackagePath: req.PackagePath, BridgeRevision: req.BridgeRevision, CandidateRevision: req.Revision, ApprovedAt: time.Now().UTC().Format(time.RFC3339), Skill: s}
		if _, exists := before.Entries[revisionPath(s)]; exists {
			if _, err = m.cached(&before, s); err != nil {
				return err
			}
		} else if err = m.install(after, revisionPath(s), pkg, m); err != nil {
			return err
		}
		if err = m.Store.PutFile(after, approvalPath(approval), jsonBytes(approval), 0644); err != nil {
			return err
		}
		file, _ := filepath.Abs(m.BridgeFile)
		m.planInputs = map[string]string{"kind": "external-adopt", "bridge_file": file, "registration_hash": hash, "bridge_id": req.BridgeID, "package_path": req.PackagePath, "revision": req.Revision, "bridge_revision": req.BridgeRevision, "copy": m.Copy}
	} else {
		if !uuid.MatchString(req.ID) || !uuid.MatchString(req.Approval) || m.Copy == "" {
			return result.Fail("INVALID_INPUT", "选择历史批准版本需要稳定 id、approval 和明确 --copy", 2)
		}
		data, err := m.treeRead(&before, approvalPath(ExternalApproval{ApprovalID: req.Approval, Skill: Skill{ID: req.ID}}))
		if err != nil {
			return result.Fail("APPROVAL_NOT_FOUND", "历史批准记录不可用", 2)
		}
		if err = json.Unmarshal(data, &approval); err != nil {
			return err
		}
		if approval.ApprovalID != req.Approval || approval.Skill.ID != req.ID {
			return result.Fail("REVISION_CORRUPT", "历史批准身份不符", 5)
		}
		if err = m.checkApproval(&before, approval); err != nil {
			return err
		}
		for i, a := range ext.Skills {
			if a.Skill.ID == req.ID {
				at = i
			}
		}
		if at < 0 {
			return result.Fail("SKILL_NOT_FOUND", "先恢复包含此资产的源恢复点；历史记录不会自动重新登记资产", 2)
		}
		current := ext.Skills[at]
		if current.Binding.ID != approval.Binding.ID || current.PackagePath != approval.PackagePath {
			return result.Fail("SOURCE_IDENTITY_MISMATCH", "历史批准原源身份不符", 3)
		}
	}
	// Cached restore is independent of current external availability. Both
	// actions nevertheless require the destination copy to be explicit.
	if m.Copy == "" {
		return result.Fail("BRIDGE_BINDING_REQUIRED", "需要明确 --copy", 4)
	}
	for _, item := range effective {
		if item.Skill.ID != approval.Skill.ID && strings.EqualFold(item.Skill.Name, approval.Skill.Name) {
			return result.Fail("NATIVE_NAME_COLLISION", "外部候选原生名称与有效技能冲突", 3)
		}
	}
	if at < 0 {
		ext.Skills = append(ext.Skills, approval)
		setEnabled(profile, approval.Skill.ID, false)
	} else {
		ext.Skills[at] = approval
	}
	switches(profile)["pins"].(map[string]any)[approval.Skill.ID] = approval.Skill.Revision
	sort.Slice(ext.Skills, func(i, j int) bool { return ext.Skills[i].Skill.ID < ext.Skills[j].Skill.ID })
	if err = m.Store.PutFile(after, externalFile, jsonBytes(ext), 0644); err != nil {
		return err
	}
	out.SkillID = approval.Skill.ID
	return m.finishPlan(out, req.Action, before, *after, idx, profile)
}
