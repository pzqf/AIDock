package replica

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/privatefs"
	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// References only. A client cannot supply source trees, native bodies, roots,
// programs, permissions or the transaction it wants the server to save.
type FullPublicationCheck struct {
	Copy   string `json:"copy_id"`
	ID     string `json:"preparation_id"`
	Digest string `json:"preparation_digest"`
}
type FullPublicationRequest struct {
	Plan   string                 `json:"plan_id"`
	Digest string                 `json:"digest"`
	Checks []FullPublicationCheck `json:"checks"`
}
type FullPublicationReadRequest struct {
	ID     string `json:"publication_id"`
	Digest string `json:"digest"`
}

func ParseFullPublicationRead(raw []byte) (FullPublicationReadRequest, error) {
	var req FullPublicationReadRequest
	if len(raw) > 65536 {
		return req, result.Fail("INVALID_INPUT", "完整批次重读请求过大", 2)
	}
	v, err := config.Parse(raw)
	fields, ok := v.(map[string]any)
	if err != nil || !ok || len(fields) != 2 || decode(data(fields), &req) != nil || !uuid.MatchString(req.ID) || !fullPublicationSHA.MatchString(req.Digest) {
		return FullPublicationReadRequest{}, result.Fail("INVALID_INPUT", "重读需要唯一的完整批次 UUID 和完整摘要", 2)
	}
	return req, nil
}

type fullPublicationPlan struct {
	Version       int                               `json:"schema_version"`
	ID            string                            `json:"id"`
	Created       string                            `json:"created"`
	InitiatorRoot string                            `json:"initiator_root"`
	InitiatorCopy string                            `json:"initiator_copy"`
	Executable    string                            `json:"executable"`
	Home          string                            `json:"home"`
	Resolution    Resolution                        `json:"resolution"`
	Checks        []FullPublicationCheck            `json:"checks"`
	Proof         projectsnapshot.PublicationCommit `json:"proof"`
	Digest        string                            `json:"digest"`
}

type FullPublicationMember struct {
	Copy       Binding                     `json:"copy"`
	Check      FullPublicationCheck        `json:"check"`
	Tools      []string                    `json:"tools"`
	MCP        []string                    `json:"mcp_names"`
	Changes    []native.ReplayNativeChange `json:"changes"`
	Source     string                      `json:"source_digest"`
	Local      string                      `json:"local_digest"`
	AppliedKey string                      `json:"applied_binding"`
}
type FullPublicationPreview struct {
	ID            string                    `json:"id"`
	Digest        string                    `json:"digest"`
	Created       string                    `json:"created"`
	InitiatorRoot string                    `json:"initiator_root"`
	Resolution    string                    `json:"resolution_id"`
	Source        Binding                   `json:"source"`
	Members       []FullPublicationMember   `json:"members"`
	Status        string                    `json:"status"`
	Fresh         bool                      `json:"fresh"`
	CanApply      bool                      `json:"can_apply"`
	Code          string                    `json:"code,omitempty"`
	Boundary      string                    `json:"boundary"`
	Execution     *FullPublicationExecution `json:"execution,omitempty"`
}

var fullPublicationSHA = regexp.MustCompile("^[0-9a-f]{64}$")

const fullPublicationBoundary = "完整批次预览核对全部目标的技能、规则、启用 MCP、当前授权及固定启动材料。预览只保存私有提案；另行确认后，以真实共同事务处理源、工具文件和共同基线，保留本机覆盖与只读发布者，失败整批恢复。不启动 MCP，不恢复旧授权。回退与撤销须明确选择成员并分别确认；文件提交不代表实际会话或能力已验证。"

func fullPublicationDigest(p fullPublicationPlan) string {
	p.Digest = ""
	return workstore.Hash(data(p))
}
func fullBinding(owner, scope string, b Binding) projectsnapshot.Binding {
	return projectsnapshot.Binding{Project: owner, Scope: scope, Copy: b.ID, SourceRoot: b.Root, NativeRoot: filepath.Dir(b.Root)}
}
func (m *Manager) fullPublicationDir() string {
	return filepath.Join(m.Source.Store.Local, "replica-publications-private", workstore.Hash([]byte(m.Source.Owner+"/"+m.Source.Scope)))
}
func (m *Manager) fullPublicationFile(id string) string {
	return filepath.Join(m.fullPublicationDir(), "previews", id+".json")
}

func ParseFullPublicationRequest(raw []byte) (FullPublicationRequest, error) {
	var req FullPublicationRequest
	bad := func() (FullPublicationRequest, error) {
		return req, result.Fail("INVALID_INPUT", "需要保存方案及每个目标唯一的完整检查引用；拒绝重复键、空值、别名、未知字段或自定义材料", 2)
	}
	if len(raw) > 65536 {
		return bad()
	}
	v, err := config.Parse(raw)
	if err != nil {
		return bad()
	}
	fields, ok := v.(map[string]any)
	if !ok || len(fields) != 3 || decode(data(fields), &req) != nil || !uuid.MatchString(req.Plan) || !fullPublicationSHA.MatchString(req.Digest) || len(req.Checks) == 0 || len(req.Checks) > 31 {
		return bad()
	}
	checks, ok := fields["checks"].([]any)
	if !ok || len(checks) != len(req.Checks) {
		return bad()
	}
	seen := map[string]bool{}
	for i, c := range req.Checks {
		f, ok := checks[i].(map[string]any)
		if !ok || len(f) != 3 || !uuid.MatchString(c.Copy) || !uuid.MatchString(c.ID) || !fullPublicationSHA.MatchString(c.Digest) || seen[c.Copy] {
			return bad()
		}
		seen[c.Copy] = true
	}
	return req, nil
}

// This assembles private compiler material but never archives an applied view,
// creates a committed journal or exposes a write permit.
func (m *Manager) compileFullPublication(ctx context.Context, p fullPublicationPlan) ([]native.ConfigurationPublicationMaterial, []FullPublicationMember, error) {
	materials := []native.ConfigurationPublicationMaterial{}
	members := []FullPublicationMember{}
	checkingCopy := ""
	c, err := p.Proof.Coordinator(m.Source.Store.Local)
	if err != nil {
		return nil, nil, err
	}
	err = m.Control.Locked(func() error {
		if err := m.Control.Pending(); err != nil {
			return err
		}
		if err := m.revalidateWith(ctx, p.Resolution, true); err != nil {
			return err
		}
		for i, t := range p.Resolution.Targets {
			checkingCopy = t.Copy.ID
			s, err := m.copySource(t.Copy)
			if err != nil {
				return err
			}
			if err = transfer(m.Control, s.Store, t.Before, t.After); err != nil {
				return err
			}
			candidate, err := s.DraftConfigurationCandidate(ctx, t.Before, t.After, t.ConfigurationDigest)
			if err != nil {
				return err
			}
			n, err := native.New(s, filepath.Dir(t.Copy.Root))
			if err != nil {
				return err
			}
			ref := p.Checks[i]
			checked, err := n.ReadConfigurationPreparation(ctx, candidate, ref.ID)
			if err != nil {
				return err
			}
			if checked.Digest != ref.Digest {
				return result.Fail("PLAN_CONFIRMATION_REQUIRED", "完整批次需要每个原检查的完整摘要", 4)
			}
			material, err := n.CompileConfigurationPublication(ctx, candidate, ref.ID, ref.Digest, p.ID, p.Executable, p.Home, c.Blobs)
			if err != nil {
				return err
			}
			materials = append(materials, material)
			members = append(members, FullPublicationMember{Copy: t.Copy, Check: ref, Tools: append([]string{}, checked.Tools...), MCP: append([]string{}, checked.MCP...), Changes: []native.ReplayNativeChange{}, Source: workstore.Digest(t.After), Local: workstore.Digest(material.Targets()[1].After), AppliedKey: material.ActiveKey()})
		}
		return nil
	})
	if err != nil && checkingCopy != "" {
		var failure *result.Error
		if errors.As(err, &failure) {
			err = result.Fail(failure.Code, "完整批次目标 "+checkingCopy+" 未通过核对："+failure.Code, failure.Exit)
		} else {
			err = result.Fail("FULL_PUBLICATION_CHECK_UNAVAILABLE", "完整批次目标 "+checkingCopy+" 的检查或材料不可读；未采用部分结果", 4)
		}
	}
	return materials, members, err
}

func (m *Manager) PlanFullPublication(ctx context.Context, req FullPublicationRequest, executable, home string) (FullPublicationPreview, error) {
	// Validate direct Go callers by the same public contract.
	if _, err := ParseFullPublicationRequest(data(req)); err != nil {
		return FullPublicationPreview{}, err
	}
	r, err := m.loadResolution(req.Plan, req.Digest)
	if err != nil {
		return FullPublicationPreview{}, err
	}
	if r.Version != 2 || r.Repair || len(r.Targets) != len(req.Checks) {
		return FullPublicationPreview{}, result.Fail("FULL_PUBLICATION_CHECKS_REQUIRED", "需要完整候选和每个明确目标的检查，不能漏选或增加目标", 4)
	}
	refs := map[string]FullPublicationCheck{}
	for _, c := range req.Checks {
		refs[c.Copy] = c
	}
	p := fullPublicationPlan{Version: 1, ID: workstore.ID(), Created: time.Now().UTC().Format(time.RFC3339Nano), InitiatorRoot: m.Source.Store.Root, InitiatorCopy: m.Source.Copy, Executable: executable, Home: home, Resolution: r, Checks: []FullPublicationCheck{}}
	p.Proof = projectsnapshot.PublicationCommit{Version: 1, Publication: p.ID, Publisher: fullBinding(r.Owner, r.Scope, r.Source), Members: []projectsnapshot.Binding{}}
	for _, t := range r.Targets {
		ref, ok := refs[t.Copy.ID]
		if !ok {
			return FullPublicationPreview{}, result.Fail("FULL_PUBLICATION_CHECKS_REQUIRED", "检查引用必须与原方案目标逐一相符", 4)
		}
		p.Checks = append(p.Checks, ref)
		p.Proof.Members = append(p.Proof.Members, fullBinding(r.Owner, r.Scope, t.Copy))
	}
	if err = privatefs.Ensure(m.fullPublicationDir()); err != nil {
		return FullPublicationPreview{}, err
	}
	materials, members, err := m.compileFullPublication(ctx, p)
	if err != nil {
		return FullPublicationPreview{}, err
	}
	c, err := p.Proof.Coordinator(m.Source.Store.Local)
	if err != nil {
		return FullPublicationPreview{}, err
	}
	if err = transfer(m.Control, c.Blobs, r.ControlBefore, r.ControlAfter); err != nil {
		return FullPublicationPreview{}, err
	}
	targets := []workstore.BatchTarget{{Path: "control", Before: r.ControlBefore, After: r.ControlAfter}}
	for _, material := range materials {
		for _, t := range material.Targets() {
			t.Path = projectsnapshot.PublicationMemberPrefix(material.Binding().Copy) + t.Path
			targets = append(targets, t)
		}
	}
	p.Proof.Transaction, err = c.PreviewObserved("apply", targets)
	if err != nil {
		return FullPublicationPreview{}, err
	}
	if err = m.validateFullPublication(p); err != nil {
		return FullPublicationPreview{}, err
	}
	if err = m.revalidate(ctx, r); err != nil {
		return FullPublicationPreview{}, err
	}
	p.Digest = fullPublicationDigest(p)
	if err = saveLocal(m.fullPublicationFile(p.ID), p); err != nil {
		return FullPublicationPreview{}, err
	}
	return fullPublicationReport(p, members, true), nil
}

func (m *Manager) validateFullPublication(p fullPublicationPlan) error {
	bad := func() error {
		return result.Fail("FULL_PUBLICATION_INVALID", "完整批次的身份、方案、检查或共同事务不一致", 5)
	}
	r := p.Resolution
	if p.Version != 1 || !uuid.MatchString(p.ID) || p.Proof.Publication != p.ID || p.Proof.View != nil || r.Owner != m.Source.Owner || r.Scope != m.Source.Scope || r.Version != 2 || r.Repair || r.Digest != resolutionDigest(r) || r.Source.ID != r.Request.From || len(r.Targets) == 0 || len(p.Checks) != len(r.Targets) || len(p.Proof.Members) != len(r.Targets) || p.Proof.Publisher != fullBinding(r.Owner, r.Scope, r.Source) || !filepath.IsAbs(p.Executable) || filepath.Clean(p.Executable) != p.Executable || filepath.Join(p.Home, "local") != m.Source.Store.Local {
		return bad()
	}
	if err := m.validateBinding(r.Source); err != nil {
		return err
	}
	registry, err := m.registry(r.ControlBefore)
	if err != nil {
		return err
	}
	initiator := false
	for _, b := range registry.Copies {
		initiator = initiator || b.ID == p.InitiatorCopy && b.Root == p.InitiatorRoot
	}
	if !initiator {
		return bad()
	}
	if err := p.Proof.Validate(m.Source.Store.Local); err != nil {
		return err
	}
	// The compared roster also contains keep/skip and already-equal copies.
	// Only resolved source changes become transaction members. Keep the full
	// request for deterministic revalidation; never rewrite it to the footprint.
	if p.Proof.Transaction.Action != "apply" || len(r.Request.Targets) < len(r.Targets) || len(r.Request.Targets) > 31 {
		return bad()
	}
	requested := map[string]bool{}
	registered := map[string]bool{}
	for _, b := range registry.Copies {
		registered[b.ID] = true
	}
	for _, id := range r.Request.Targets {
		if !uuid.MatchString(id) || id == r.Source.ID || requested[id] || !registered[id] {
			return bad()
		}
		requested[id] = true
	}
	byPath := map[string]workstore.BatchTarget{}
	for _, t := range p.Proof.Transaction.Targets {
		byPath[t.Path] = t
	}
	control := byPath["control"]
	if workstore.Digest(control.Before) != workstore.Digest(r.ControlBefore) || workstore.Digest(control.After) != workstore.Digest(r.ControlAfter) {
		return bad()
	}
	seen := map[string]bool{}
	for i, t := range r.Targets {
		ref := p.Checks[i]
		if err := m.validateBinding(t.Copy); err != nil {
			return err
		}
		if seen[t.Copy.ID] || !containsCopy(r.Request.Targets, t.Copy.ID) || p.Proof.Members[i] != fullBinding(r.Owner, r.Scope, t.Copy) || ref.Copy != t.Copy.ID || !uuid.MatchString(ref.ID) || !fullPublicationSHA.MatchString(ref.Digest) {
			return bad()
		}
		seen[t.Copy.ID] = true
		current := byPath[projectsnapshot.PublicationMemberPrefix(t.Copy.ID)+"source"]
		if workstore.Digest(current.Before) != workstore.Digest(t.Before) || workstore.Digest(current.After) != workstore.Digest(t.After) {
			return bad()
		}
	}
	return nil
}

func (m *Manager) loadFullPublication(id, digest string) (fullPublicationPlan, error) {
	p, err := m.readFullPublicationDescriptor(id, digest)
	if err != nil {
		return p, err
	}
	if p.InitiatorRoot != m.Source.Store.Root || p.InitiatorCopy != m.Source.Copy {
		return p, result.Fail("FULL_PUBLICATION_INVALID", "完整批次不属于原发起绑定", 5)
	}
	return p, nil
}

// Reference scans validate ALL stored common records without impersonating a
// different source manager or requiring its current draft to be operational.
func (m *Manager) readFullPublicationDescriptor(id, digest string) (fullPublicationPlan, error) {
	var p fullPublicationPlan
	if !uuid.MatchString(id) || digest != "" && !fullPublicationSHA.MatchString(digest) {
		return p, result.Fail("INVALID_INPUT", "需要完整批次 UUID 与完整摘要", 2)
	}
	raw, err := workstore.ReadFile(m.fullPublicationFile(id))
	if err != nil {
		return p, err
	}
	if decode(raw, &p) != nil || p.ID != id || p.Digest != fullPublicationDigest(p) || !fullPublicationSHA.MatchString(p.Digest) {
		return p, result.Fail("FULL_PUBLICATION_INVALID", "完整批次提案损坏或不属于原发起绑定", 5)
	}
	if digest != "" && digest != p.Digest {
		return p, result.Fail("PLAN_CONFIRMATION_REQUIRED", "需要原完整批次提案摘要", 4)
	}
	if err = m.validateFullPublication(p); err != nil {
		return p, err
	}
	c, _ := p.Proof.Coordinator(m.Source.Store.Local)
	stored, err := c.LoadPlan(p.Proof.Transaction.ID)
	if err != nil {
		return p, err
	}
	if string(data(stored)) != string(data(p.Proof.Transaction)) {
		return p, result.Fail("FULL_PUBLICATION_INVALID", "完整批次与原共同事务提案不同", 5)
	}
	return p, nil
}

func fullPublicationReport(p fullPublicationPlan, members []FullPublicationMember, fresh bool) FullPublicationPreview {
	out := FullPublicationPreview{ID: p.ID, Digest: p.Digest, Created: p.Created, InitiatorRoot: p.InitiatorRoot, Resolution: p.Resolution.ID, Source: p.Resolution.Source, Members: members, Status: "SAVED", Fresh: fresh, CanApply: fresh, Boundary: fullPublicationBoundary}
	if fresh {
		out.Status = "CHECKED"
		for i := range out.Members {
			prefix := projectsnapshot.PublicationMemberPrefix(out.Members[i].Copy.ID)
			for _, t := range p.Proof.Transaction.Targets {
				if len(t.Path) < len(prefix) || t.Path[:len(prefix)] != prefix || t.Path == prefix+"local" {
					continue
				}
				out.Members[i].Changes = append(out.Members[i].Changes, native.ReplayNativeChange{Path: t.Path[len(prefix):], Before: workstore.Digest(t.Before), After: workstore.Digest(t.After), Changes: workstore.Changes(t.Before, t.After)})
			}
		}
	}
	return out
}

func (m *Manager) ReadFullPublication(ctx context.Context, id, digest string) (FullPublicationPreview, error) {
	if !fullPublicationSHA.MatchString(digest) {
		return FullPublicationPreview{}, result.Fail("PLAN_CONFIRMATION_REQUIRED", "重读需要原完整批次提案摘要", 4)
	}
	p, err := m.loadFullPublication(id, digest)
	if err != nil {
		return FullPublicationPreview{}, err
	}
	observed, err := m.fullPublicationHistoryReport(p)
	if err != nil {
		return FullPublicationPreview{}, err
	}
	if observed.Execution != nil {
		return observed, nil
	}
	materials, members, err := m.compileFullPublication(ctx, p)
	if err == nil {
		byPath := map[string]workstore.BatchTarget{}
		for _, t := range p.Proof.Transaction.Targets {
			byPath[t.Path] = t
		}
		for _, material := range materials {
			for _, t := range material.Targets() {
				t.Path = projectsnapshot.PublicationMemberPrefix(material.Binding().Copy) + t.Path
				if string(data(t)) != string(data(byPath[t.Path])) {
					err = result.Fail("PLAN_STALE", "重读完整批次时目标原生或配置材料变化", 3)
				}
			}
		}
	}
	if err == nil {
		err = m.revalidate(ctx, p.Resolution)
	}
	if err != nil {
		out := fullPublicationReport(p, fullPublicationStoredMembers(p), false)
		out.Status, out.Code = "STALE", "FULL_PUBLICATION_RECHECK_REQUIRED"
		var failure *result.Error
		if errors.As(err, &failure) {
			out.Code = failure.Code
		}
		return out, nil
	}
	return fullPublicationReport(p, members, true), nil
}

func fullPublicationStoredMembers(p fullPublicationPlan) []FullPublicationMember {
	out := []FullPublicationMember{}
	for i, t := range p.Resolution.Targets {
		out = append(out, FullPublicationMember{Copy: t.Copy, Check: p.Checks[i], Changes: []native.ReplayNativeChange{}})
	}
	return out
}

func (m *Manager) fullPublicationHistoryReport(p fullPublicationPlan) (FullPublicationPreview, error) {
	out := fullPublicationReport(p, fullPublicationStoredMembers(p), false)
	execution, err := m.fullPublicationExecution(p)
	if err != nil {
		return FullPublicationPreview{}, err
	}
	if len(execution.Transactions) > 0 {
		out.Status, out.Execution, out.Boundary = execution.Status, &execution, execution.Boundary
	}
	return out, nil
}

func (m *Manager) FullPublicationHistory() ([]FullPublicationPreview, error) {
	dir := filepath.Join(m.fullPublicationDir(), "previews")
	if err := workstore.CheckLinks(dir); err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []FullPublicationPreview{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []FullPublicationPreview{}
	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".json" {
			continue
		}
		id := f.Name()[:len(f.Name())-5]
		// Other initiating bindings are checked without opening their source.
		p, err := m.readFullPublicationDescriptor(id, "")
		if err != nil {
			return nil, err
		}
		if p.InitiatorRoot == m.Source.Store.Root && p.InitiatorCopy == m.Source.Copy {
			report, err := m.fullPublicationHistoryReport(p)
			if err != nil {
				return nil, err
			}
			out = append(out, report)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out, nil
}
