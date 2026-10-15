// Package pluginpermission stores explicit project approvals. A current approval
// alone never proves isolation, call authorization, or permission to launch.
package pluginpermission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type Binding struct {
	Version    int    `json:"schema_version"`
	Project    string `json:"project"`
	Scope      string `json:"scope"`
	SourceRoot string `json:"source_root"`
	Copy       string `json:"copy"`
}
type Input struct {
	Name                string         `json:"name"`
	InstallationID      string         `json:"installation_id"`
	InstallationSHA256  string         `json:"installation_sha256"`
	LockSHA256          string         `json:"lock_sha256"`
	ConfigurationSHA256 string         `json:"configuration_sha256"`
	ArtifactSHA256      string         `json:"artifact_sha256"`
	Permissions         map[string]any `json:"permissions"`
	Tier                int            `json:"tier"`
}
type Candidate struct {
	Input  Input  `json:"input"`
	Status string `json:"status"`
}
type Record struct {
	Version     int    `json:"schema_version"`
	ID          string `json:"id"`
	ApprovedAt  string `json:"approved_at"`
	Actor       string `json:"actor"`
	Input       Input  `json:"input"`
	Purpose     string `json:"purpose"`
	DataLevel   string `json:"data_level"`
	Destination string `json:"destination"`
}
type Entry struct {
	Record         Record `json:"record"`
	Status         string `json:"status"`
	ExecutionReady bool   `json:"execution_ready"`
}

type History struct {
	ID      string `json:"id"`
	Action  string `json:"action"`
	State   string `json:"state"`
	Updated string `json:"updated"`
	Request Edit   `json:"request"`
}
type State struct {
	Binding        Binding             `json:"binding"`
	SHA256         string              `json:"sha256"`
	Root           string              `json:"root"`
	Authorizations []Entry             `json:"authorizations"`
	Candidates     []Candidate         `json:"candidates"`
	Checks         []inventory.Finding `json:"checks"`
	History        []History           `json:"history"`
	AuditCount     int                 `json:"audit_count"`
	ReviewSHA256   string              `json:"review_sha256"`
	AuditHead      string              `json:"audit_head"`
	Recovery       bool                `json:"recovery_required"`
	Boundary       string              `json:"boundary"`
	ExecutionReady bool                `json:"execution_ready"`
}
type Edit struct {
	Action               string `json:"action"`
	ExpectedSHA256       string `json:"expected_sha256"`
	ExpectedReviewSHA256 string `json:"expected_review_sha256,omitempty"`
	Name                 string `json:"name,omitempty"`
	Snapshot             string `json:"snapshot,omitempty"`
	ConfirmPermissions   bool   `json:"confirm_permissions,omitempty"`
	Purpose              string `json:"purpose,omitempty"`
	DataLevel            string `json:"data_level,omitempty"`
	Destination          string `json:"destination,omitempty"`
}

type Preview struct {
	Plan      workstore.Plan `json:"plan"`
	Before    State          `json:"before"`
	Candidate State          `json:"candidate"`
}
type audit struct {
	Version      int    `json:"schema_version"`
	Sequence     int    `json:"sequence"`
	ID           string `json:"id"`
	Action       string `json:"action"`
	CreatedAt    string `json:"created_at"`
	Previous     string `json:"previous"`
	Before       string `json:"before"`
	After        string `json:"after"`
	ReviewSHA256 string `json:"review_sha256"`
	Request      Edit   `json:"request"`
	Actor        string `json:"actor"`
}
type Manager struct {
	Store   *workstore.Store
	Binding Binding
}

func bytesOf(v any) []byte { b, _ := json.Marshal(v); return b }
func JSON(v any) []byte    { b, _ := json.MarshalIndent(v, "", "  "); return append(b, '\n') }
func hash(s string) bool   { return len(s) == 64 && strings.Trim(s, "0123456789abcdef") == "" }
func uuid(s string) bool {
	return len(s) == 36 && strings.Count(s, "-") == 4 && strings.Trim(s, "0123456789abcdef-") == ""
}
func decode(b []byte, v any) error {
	parsed, err := config.Parse(b)
	if err != nil {
		return err
	}
	d := json.NewDecoder(strings.NewReader(string(bytesOf(parsed))))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func New(local string, b Binding) (*Manager, error) {
	if !filepath.IsAbs(local) || filepath.Clean(local) != local || !filepath.IsAbs(b.SourceRoot) || filepath.Clean(b.SourceRoot) != b.SourceRoot || b.Version != 1 || b.Project == "" || b.Scope == "" {
		return nil, result.Fail("INVALID_AUTHORIZATION_BINDING", "项目授权需要明确本机根、项目、作用域和源目录", 2)
	}
	s := &workstore.Store{Root: filepath.Join(local, "plugin-permission-"+workstore.Hash(bytesOf(b))), Local: filepath.Join(local, "plugin-permission-transactions"), PlanKind: "plugin-permission"}
	for _, p := range []string{s.Root, s.Local} {
		if err := workstore.CheckLinks(p); err != nil {
			return nil, err
		}
	}
	return &Manager{Store: s, Binding: b}, nil
}
func recordPath(name string) string { return "grants/" + workstore.Hash([]byte(name)) + ".json" }
func authority(t workstore.Tree) string {
	v := map[string]workstore.Entry{}
	for p, e := range t.Entries {
		if p != "audit" && !strings.HasPrefix(p, "audit/") {
			v[p] = e
		}
	}
	return workstore.Hash(bytesOf(v))
}
func (m *Manager) binding(t workstore.Tree) error {
	if len(t.Entries) == 0 {
		return nil
	}
	e, ok := t.Entries["binding.json"]
	if !ok || e.Dir {
		return result.Fail("AUTHORIZATION_OWNERSHIP_REQUIRED", "非空项目授权目录缺少绑定，保留并阻断", 4)
	}
	b, err := m.Store.Data(e)
	if err != nil {
		return err
	}
	var bound Binding
	if err = decode(b, &bound); err != nil || bound != m.Binding {
		return result.Fail("AUTHORIZATION_BINDING_MISMATCH", "项目授权属于其它项目或源目录", 3)
	}
	for p, e := range t.Entries {
		valid := p == "binding.json" && !e.Dir || p == "grants" && e.Dir || p == "audit" && e.Dir || strings.HasPrefix(p, "grants/") && !e.Dir && strings.HasSuffix(p, ".json") && hash(strings.TrimSuffix(strings.TrimPrefix(p, "grants/"), ".json")) || strings.HasPrefix(p, "audit/") && !e.Dir && strings.HasSuffix(p, ".json")
		if !valid {
			return result.Fail("AUTHORIZATION_CONTENT_UNKNOWN", "项目授权包含未知内容: "+p, 5)
		}
	}
	return nil
}
func (m *Manager) records(t workstore.Tree) (map[string]Record, error) {
	out := map[string]Record{}
	if err := m.binding(t); err != nil {
		return out, err
	}
	for p, e := range t.Entries {
		if !strings.HasPrefix(p, "grants/") {
			continue
		}
		b, err := m.Store.Data(e)
		if err != nil {
			return out, err
		}
		var r Record
		if err = decode(b, &r); err != nil {
			return out, err
		}
		if r.Version != 1 || !uuid(r.ID) || !validInput(r.Input) || r.Actor == "" || !validApproval(r.Purpose, r.DataLevel, r.Destination) || p != recordPath(r.Input.Name) {
			return out, result.Fail("AUTHORIZATION_RECORD_INVALID", "授权记录格式或权限绑定无效", 5)
		}
		if _, err = time.Parse(time.RFC3339Nano, r.ApprovedAt); err != nil {
			return out, err
		}
		out[r.Input.Name] = r
	}
	return out, nil
}

func (m *Manager) audits(t workstore.Tree) ([]audit, string, error) {
	paths := []string{}
	for p := range t.Entries {
		if strings.HasPrefix(p, "audit/") {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	out := []audit{}
	head := ""
	for i, p := range paths {
		b, err := m.Store.Data(t.Entries[p])
		if err != nil {
			return out, head, err
		}
		var a audit
		if err = decode(b, &a); err != nil {
			return out, head, err
		}
		if a.Version != 1 || a.Sequence != i+1 || !uuid(a.ID) || a.Previous != head || !hash(a.Before) || !hash(a.After) || !hash(a.ReviewSHA256) || p != fmt.Sprintf("audit/%08d-%s.json", a.Sequence, a.ID) || a.Action != a.Request.Action || !validEdit(a.Request) || a.Actor == "" {
			return out, head, result.Fail("AUTHORIZATION_AUDIT_INVALID", "授权审计链或动作不匹配", 5)
		}
		if _, err = time.Parse(time.RFC3339Nano, a.CreatedAt); err != nil {
			return out, head, err
		}
		if i > 0 && a.Action != "rollback" && a.Before != out[i-1].After {
			return out, head, result.Fail("AUTHORIZATION_AUDIT_INVALID", "授权审计状态不连续", 5)
		}
		out = append(out, a)
		head = workstore.Hash(b)
	}
	if len(t.Entries) > 0 && len(out) == 0 {
		return out, head, result.Fail("AUTHORIZATION_AUDIT_INVALID", "项目授权缺少审计", 5)
	}
	js, err := m.Store.Journals()
	if err != nil {
		return out, head, err
	}
	for _, j := range js {
		if j.State != "committed" {
			continue
		}
		for p, e := range j.Plan.After.Entries {
			if strings.HasPrefix(p, "audit/") && t.Entries[p] != e {
				return out, head, result.Fail("AUTHORIZATION_AUDIT_INVALID", "授权审计与已提交事务锚点不符", 5)
			}
		}
	}
	if len(out) > 0 && out[len(out)-1].After != authority(t) {
		return out, head, result.Fail("AUTHORIZATION_AUTHORITY_DRIFT", "项目授权在审计外发生变化；可预览历史回退修复", 5)
	}
	return out, head, nil
}
func drift(err error) bool {
	var e *result.Error
	return errors.As(err, &e) && e.Code == "AUTHORIZATION_AUTHORITY_DRIFT"
}
func validInput(i Input) bool {
	return i.Name != "" && uuid(i.InstallationID) && hash(i.InstallationSHA256) && hash(i.LockSHA256) && hash(i.ConfigurationSHA256) && hash(i.ArtifactSHA256) && i.Permissions != nil && i.Tier >= 0 && i.Tier <= 2
}
func review(c Candidate) string    { return workstore.Hash(bytesOf(c)) }
func reviews(c []Candidate) string { return workstore.Hash(bytesOf(c)) }
func validApproval(purpose, level, destination string) bool {
	return strings.TrimSpace(purpose) != "" && len(purpose) <= 2000 && level == "Public" && destination == "unknown-external-client"
}
func confirmed(r Edit) bool {
	return r.ConfirmPermissions && validApproval(r.Purpose, r.DataLevel, r.Destination)
}
func validEdit(r Edit) bool {
	if !hash(r.ExpectedSHA256) || len(r.Purpose) > 2000 {
		return false
	}
	switch r.Action {
	case "grant":
		return r.Name != "" && r.Snapshot == "" && hash(r.ExpectedReviewSHA256) && confirmed(r)
	case "revoke":
		return r.Name != "" && r.Snapshot == "" && r.ExpectedReviewSHA256 == "" && !r.ConfirmPermissions && r.Purpose == "" && r.DataLevel == "" && r.Destination == ""
	case "rollback":
		return r.Name == "" && uuid(r.Snapshot) && hash(r.ExpectedReviewSHA256) && (confirmed(r) || !r.ConfirmPermissions && r.Purpose == "" && r.DataLevel == "" && r.Destination == "")
	}
	return false
}
func actor() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	if u.Username == "" || u.Uid == "" {
		return "", fmt.Errorf("无法确定当前授权账户")
	}
	return u.Uid + ":" + u.Username, nil
}

func (m *Manager) state(t workstore.Tree, candidates []Candidate, _ bool) State {
	out := State{Binding: m.Binding, SHA256: workstore.Digest(t), Root: m.Store.Root, Authorizations: []Entry{}, Candidates: candidates, Checks: []inventory.Finding{}, History: []History{}, ReviewSHA256: reviews(candidates), Boundary: "授权只对当前项目、作用域和本机源目录有效。绑定安装、锁、权限和有效配置；仅 Public、未知外部客户端。隔离、逐次写调用授权和受管启动另行检查，当前不启动程序。"}
	records, err := m.records(t)
	if err == nil {
		var aa []audit
		aa, out.AuditHead, err = m.audits(t)
		out.AuditCount = len(aa)
	}
	if err != nil {
		out.Checks = append(out.Checks, inventory.Finding{Status: "FAIL", Code: "MCP_AUTHORIZATION_STORE_INVALID", Detail: err.Error()})
		return out
	}
	names := []string{}
	for name := range records {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		r := records[name]
		e := Entry{Record: r, Status: "STALE"}
		for _, c := range candidates {
			if c.Status == "CURRENT" && workstore.Hash(bytesOf(c.Input)) == workstore.Hash(bytesOf(r.Input)) {
				e.Status = "CURRENT"
			}
		}
		out.Authorizations = append(out.Authorizations, e)
	}
	return out
}

func (m *Manager) State(candidates []Candidate) (State, error) {
	var out State
	err := m.Store.Locked(func() error {
		t, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		out = m.state(t, candidates, true)
		if err = m.Store.Pending(); err != nil {
			out.Recovery = true
			out.Checks = append(out.Checks, inventory.Finding{Status: "FAIL", Code: "MCP_AUTHORIZATION_RECOVERY_REQUIRED", Detail: recovery(err).Error()})
			for i := range out.Authorizations {
				out.Authorizations[i].Status = "RECOVERY_REQUIRED"
			}
		}
		js, err := m.Store.Journals()
		if err != nil {
			return err
		}
		for _, j := range js {
			var req Edit
			if err := decode([]byte(j.Plan.Inputs["request"]), &req); err != nil {
				return err
			}
			out.History = append(out.History, History{j.Plan.ID, j.Plan.Action, j.State, j.Updated, req})
		}
		return nil
	})
	return out, recovery(err)
}
func (m *Manager) initial(t *workstore.Tree) error {
	if err := m.binding(*t); err != nil {
		return err
	}
	if len(t.Entries) == 0 {
		return m.Store.PutFile(t, "binding.json", JSON(m.Binding), 0600)
	}
	return nil
}
func (m *Manager) build(before workstore.Tree, req Edit, candidates []Candidate, id, created, who string) (workstore.Tree, error) {
	after := workstore.Clone(before)
	if err := m.initial(&after); err != nil {
		return after, err
	}
	makeRecord := func(input Input) Record {
		return Record{Version: 1, ID: id, ApprovedAt: created, Actor: who, Input: input, Purpose: strings.TrimSpace(req.Purpose), DataLevel: req.DataLevel, Destination: req.Destination}
	}
	current := func(input Input) bool {
		for _, c := range candidates {
			if c.Status == "CURRENT" && validInput(c.Input) && workstore.Hash(bytesOf(c.Input)) == workstore.Hash(bytesOf(input)) {
				return true
			}
		}
		return false
	}
	switch req.Action {
	case "grant":
		var selected *Input
		for _, c := range candidates {
			if c.Input.Name == req.Name && c.Status == "CURRENT" && validInput(c.Input) {
				i := c.Input
				selected = &i
			}
		}
		if selected == nil {
			return after, result.Fail("AUTHORIZATION_INSTALL_REQUIRED", "需要与当前产物及安全证据一致的本机安装登记", 4)
		}
		if err := m.Store.PutFile(&after, recordPath(req.Name), JSON(makeRecord(*selected)), 0600); err != nil {
			return after, err
		}
	case "revoke":
		p := recordPath(req.Name)
		if _, ok := after.Entries[p]; !ok {
			return after, result.Fail("AUTHORIZATION_NOT_FOUND", "没有此项目授权记录", 4)
		}
		delete(after.Entries, p)
	case "rollback":
		js, err := m.Store.Journals()
		if err != nil {
			return after, err
		}
		found := false
		for _, j := range js {
			if j.Plan.ID != req.Snapshot || j.State != "committed" {
				continue
			}
			found = true
			for p := range after.Entries {
				if p == "grants" || strings.HasPrefix(p, "grants/") {
					delete(after.Entries, p)
				}
			}
			for p, e := range j.Plan.Before.Entries {
				if p == "grants" || strings.HasPrefix(p, "grants/") {
					after.Entries[p] = e
				}
			}
			break
		}
		if !found {
			return after, result.Fail("SNAPSHOT_NOT_FOUND", "没有此授权的已提交恢复点", 4)
		}
		restored, err := m.records(after)
		if err != nil {
			return after, err
		}
		for name, r := range restored {
			if before.Entries[recordPath(name)] == after.Entries[recordPath(name)] {
				continue
			}
			if !confirmed(req) {
				return after, result.Fail("AUTHORIZATION_CONFIRMATION_REQUIRED", "恢复或撤销吊销会重新授予权限；请明确核对并填写本次用途", 4)
			}
			if !current(r.Input) {
				return after, result.Fail("SNAPSHOT_DEPENDENCY_MISSING", "恢复所需安装、锁、权限或配置已变化；先重新审查", 5)
			}
			if err = m.Store.PutFile(&after, recordPath(name), JSON(makeRecord(r.Input)), 0600); err != nil {
				return after, err
			}
		}
	}
	if _, err := m.records(after); err != nil {
		return after, err
	}
	return after, nil
}

func (m *Manager) Plan(ctx context.Context, req Edit, candidates []Candidate, sourceHash string) (Preview, error) {
	var out Preview
	if !validEdit(req) || !hash(sourceHash) {
		return out, result.Fail("INVALID_INPUT", "授权动作、已读摘要或输入不匹配", 2)
	}
	err := m.Store.Locked(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.Store.Pending(); err != nil {
			return err
		}
		before, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		if req.Action != "revoke" && req.ExpectedReviewSHA256 != reviews(candidates) {
			return result.Fail("PLAN_STALE", "安装或权限检查在读取后变化；草稿保留，请重新核对", 3)
		}
		if req.ExpectedSHA256 != workstore.Digest(before) {
			return result.Fail("PLAN_STALE", "本机项目授权已变化，保留草稿并重读", 3)
		}
		if err = m.binding(before); err != nil {
			return err
		}
		audits, head, err := m.audits(before)
		if err != nil && (req.Action != "rollback" || !drift(err)) {
			return err
		}
		who, err := actor()
		if err != nil {
			return err
		}
		id, created := workstore.ID(), time.Now().UTC().Format(time.RFC3339Nano)
		after, err := m.build(before, req, candidates, id, created, who)
		if err != nil {
			return err
		}
		a := audit{Version: 1, Sequence: len(audits) + 1, ID: id, Action: req.Action, CreatedAt: created, Previous: head, Before: authority(before), After: authority(after), ReviewSHA256: reviews(candidates), Request: req, Actor: who}
		if err = m.Store.PutFile(&after, fmt.Sprintf("audit/%08d-%s.json", a.Sequence, a.ID), JSON(a), 0600); err != nil {
			return err
		}
		out.Before = m.state(before, candidates, true)
		out.Candidate = m.state(after, candidates, false)
		out.Plan, err = m.Store.PreviewBound("permission-"+req.Action, before, after, map[string]string{"source_sha256": sourceHash, "review_sha256": reviews(candidates), "request": string(bytesOf(req)), "event_id": id, "created_at": created, "actor": who})
		return err
	})
	return out, recovery(err)
}
func (m *Manager) Apply(ctx context.Context, id, digest string, candidates []Candidate, sourceHash string) (workstore.Journal, error) {
	if digest == "" {
		return workstore.Journal{}, result.Fail("INVALID_INPUT", "需要确认预览摘要", 2)
	}
	m.Store.Validate = func(p workstore.Plan) error {
		journals, err := m.Store.Journals()
		if err != nil {
			return err
		}
		for _, j := range journals {
			if j.Plan.ID == p.ID {
				return result.Fail("PLAN_CONSUMED", "计划已使用，请重新预览", 3)
			}
		}
		if len(p.Inputs) != 6 || p.Digest != digest || p.Inputs["source_sha256"] != sourceHash || p.Inputs["review_sha256"] != reviews(candidates) {
			return result.Fail("PLAN_STALE", "确认、源或安装、权限或有效配置已变化；重新预览", 3)
		}
		var req Edit
		if err := decode([]byte(p.Inputs["request"]), &req); err != nil || !validEdit(req) || p.Action != "permission-"+req.Action || req.ExpectedSHA256 != workstore.Digest(p.Before) || !uuid(p.Inputs["event_id"]) || req.Action != "revoke" && req.ExpectedReviewSHA256 != reviews(candidates) {
			return result.Fail("INVALID_AUTHORIZATION_PLAN", "授权动作输入不匹配", 5)
		}
		if _, err := time.Parse(time.RFC3339Nano, p.Inputs["created_at"]); err != nil {
			return err
		}
		audits, head, err := m.audits(p.Before)
		if err != nil && (req.Action != "rollback" || !drift(err)) {
			return err
		}
		after, err := m.build(p.Before, req, candidates, p.Inputs["event_id"], p.Inputs["created_at"], p.Inputs["actor"])
		if err != nil {
			return err
		}
		who, err := actor()
		if err != nil {
			return err
		}
		if who != p.Inputs["actor"] {
			return result.Fail("PLAN_STALE", "授权确认账户已变化", 3)
		}
		a := audit{Version: 1, Sequence: len(audits) + 1, ID: p.Inputs["event_id"], Action: req.Action, CreatedAt: p.Inputs["created_at"], Previous: head, Before: authority(p.Before), After: authority(after), ReviewSHA256: reviews(candidates), Request: req, Actor: who}
		if err = m.Store.PutFile(&after, fmt.Sprintf("audit/%08d-%s.json", a.Sequence, a.ID), JSON(a), 0600); err != nil {
			return err
		}
		if workstore.Digest(after) != workstore.Digest(p.After) {
			return result.Fail("INVALID_AUTHORIZATION_PLAN", "授权候选与动作、历史或当前输入不符", 5)
		}
		return nil
	}
	j, err := m.Store.Apply(ctx, id)
	return j, recovery(err)
}
func (m *Manager) Recover() (int, error) { n, err := m.Store.Recover(); return n, recovery(err) }
func recovery(err error) error {
	var e *result.Error
	if errors.As(err, &e) && e.Code == "RECOVERY_CONFLICT" {
		return &result.Error{Code: e.Code, Message: "项目授权事务需要恢复或人工核对: " + strings.ReplaceAll(e.Message, "skill recover", "mcp permission recover"), Exit: e.Exit, Recovery: map[string]string{"command": "mcp permission recover"}}
	}
	return err
}
