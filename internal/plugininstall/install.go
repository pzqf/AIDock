// Package plugininstall records already prepared local artifacts. Registration
// never downloads, copies, launches, or grants access to third-party code.
package plugininstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/pluginmanifest"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type Binding struct {
	Version    int    `json:"schema_version"`
	Project    string `json:"project"`
	Scope      string `json:"scope"`
	SourceRoot string `json:"source_root"`
}
type Candidate struct {
	Plugin   pluginlock.Plugin     `json:"plugin"`
	Check    pluginlock.Check      `json:"check"`
	Manifest pluginmanifest.Report `json:"manifest"`
}
type Record struct {
	Version          int               `json:"schema_version"`
	ID               string            `json:"id"`
	InstalledAt      string            `json:"installed_at"`
	Plugin           pluginlock.Plugin `json:"plugin"`
	ManifestDocument string            `json:"manifest_document"`
	ReviewSHA256     string            `json:"review_sha256"`
	TrustLevel       string            `json:"trust_level"`
	MinimumTrust     string            `json:"minimum_trust"`
	RiskReason       string            `json:"risk_reason"`
}
type SBOM struct {
	Version        int                         `json:"schema_version"`
	Format         string                      `json:"format"`
	InstallationID string                      `json:"installation_id"`
	InstalledAt    string                      `json:"installed_at"`
	Name           string                      `json:"name"`
	PublisherName  string                      `json:"manifest_name"`
	PluginVersion  string                      `json:"version"`
	Source         map[string]any              `json:"source"`
	ArtifactSHA256 string                      `json:"artifact_sha256"`
	ManifestSHA256 string                      `json:"manifest_sha256"`
	Dependencies   []pluginmanifest.Dependency `json:"dependencies"`
	Boundary       string                      `json:"boundary"`
}
type Entry struct {
	Record         Record `json:"record"`
	Status         string `json:"status"`
	SBOM           *SBOM  `json:"sbom,omitempty"`
	SBOMSHA256     string `json:"sbom_sha256,omitempty"`
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
	SHA256         string              `json:"sha256"`
	Root           string              `json:"root"`
	Installations  []Entry             `json:"installations"`
	Candidates     []Candidate         `json:"candidates"`
	Checks         []inventory.Finding `json:"checks"`
	History        []History           `json:"history"`
	AuditCount     int                 `json:"audit_count"`
	AuditHead      string              `json:"audit_head"`
	Recovery       bool                `json:"recovery_required"`
	Boundary       string              `json:"boundary"`
	ExecutionReady bool                `json:"execution_ready"`
}
type Edit struct {
	Action          string `json:"action"`
	ExpectedSHA256  string `json:"expected_sha256"`
	Name            string `json:"name,omitempty"`
	Snapshot        string `json:"snapshot,omitempty"`
	AllowUnverified bool   `json:"allow_unverified,omitempty"`
	RiskReason      string `json:"risk_reason,omitempty"`
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
		return nil, result.Fail("INVALID_INSTALL_BINDING", "安装登记需要明确本机根、项目、作用域和源目录", 2)
	}
	s := &workstore.Store{Root: filepath.Join(local, "plugin-install-"+workstore.Hash(bytesOf(b))), Local: filepath.Join(local, "plugin-install-transactions"), PlanKind: "plugin-install"}
	for _, p := range []string{s.Root, s.Local} {
		if err := workstore.CheckLinks(p); err != nil {
			return nil, err
		}
	}
	return &Manager{Store: s, Binding: b}, nil
}
func recordPath(name string) string { return "installed/" + workstore.Hash([]byte(name)) + ".json" }
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
		return result.Fail("INSTALL_OWNERSHIP_REQUIRED", "非空安装登记目录缺少绑定，保留并阻断", 4)
	}
	b, err := m.Store.Data(e)
	if err != nil {
		return err
	}
	var bound Binding
	if err = decode(b, &bound); err != nil || bound != m.Binding {
		return result.Fail("INSTALL_BINDING_MISMATCH", "安装登记属于其它项目或源目录", 3)
	}
	for p, e := range t.Entries {
		valid := p == "binding.json" && !e.Dir || p == "installed" && e.Dir || p == "audit" && e.Dir || strings.HasPrefix(p, "installed/") && !e.Dir && strings.HasSuffix(p, ".json") && hash(strings.TrimSuffix(strings.TrimPrefix(p, "installed/"), ".json")) || strings.HasPrefix(p, "audit/") && !e.Dir && strings.HasSuffix(p, ".json")
		if !valid {
			return result.Fail("INSTALL_CONTENT_UNKNOWN", "安装登记包含未知内容: "+p, 5)
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
		if !strings.HasPrefix(p, "installed/") {
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
		if r.Version != 1 || !uuid(r.ID) || !hash(r.ReviewSHA256) || r.TrustLevel != "L0" || r.MinimumTrust != "L2" || strings.TrimSpace(r.RiskReason) == "" || len(r.RiskReason) > 2000 || p != recordPath(r.Plugin.Name) {
			return out, result.Fail("INSTALL_RECORD_INVALID", "安装登记格式不匹配", 5)
		}
		if _, err = time.Parse(time.RFC3339Nano, r.InstalledAt); err != nil {
			return out, err
		}
		if _, err = pluginlock.Parse(bytesOf(pluginlock.Lock{Version: 1, Plugins: []pluginlock.Plugin{r.Plugin}})); err != nil {
			return out, err
		}
		manifest, err := pluginmanifest.Parse([]byte(r.ManifestDocument))
		if err != nil || manifest.Dependencies == nil {
			return out, result.Fail("INSTALL_RECORD_INVALID", "安装登记清单或直接依赖无效", 5)
		}
		manifestBound := false
		for _, entry := range r.Plugin.Artifact.Entries {
			if entry.Path == "plugin.yaml" && !entry.Directory && entry.SHA256 == workstore.Hash([]byte(r.ManifestDocument)) && entry.Bytes == int64(len(r.ManifestDocument)) {
				manifestBound = true
			}
		}
		if !manifestBound {
			return out, result.Fail("INSTALL_RECORD_INVALID", "登记清单未绑定产物原字节", 5)
		}
		out[r.Plugin.Name] = r
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
		if a.Version != 1 || a.Sequence != i+1 || !uuid(a.ID) || a.Previous != head || !hash(a.Before) || !hash(a.After) || !hash(a.ReviewSHA256) || p != fmt.Sprintf("audit/%08d-%s.json", a.Sequence, a.ID) || a.Action != a.Request.Action || !validEdit(a.Request) {
			return out, head, result.Fail("INSTALL_AUDIT_INVALID", "安装审计链或动作不匹配", 5)
		}
		if _, err = time.Parse(time.RFC3339Nano, a.CreatedAt); err != nil {
			return out, head, err
		}
		if i > 0 && a.Action != "rollback" && a.Before != out[i-1].After {
			return out, head, result.Fail("INSTALL_AUDIT_INVALID", "安装审计状态不连续", 5)
		}
		out = append(out, a)
		head = workstore.Hash(b)
	}
	if len(t.Entries) > 0 && len(out) == 0 {
		return out, head, result.Fail("INSTALL_AUDIT_INVALID", "安装登记缺少审计", 5)
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
				return out, head, result.Fail("INSTALL_AUDIT_INVALID", "安装审计与已提交事务锚点不符", 5)
			}
		}
	}
	if len(out) > 0 && out[len(out)-1].After != authority(t) {
		return out, head, result.Fail("INSTALL_AUTHORITY_DRIFT", "安装登记在审计外发生变化；可预览历史回退修复", 5)
	}
	return out, head, nil
}
func drift(err error) bool {
	var e *result.Error
	return errors.As(err, &e) && e.Code == "INSTALL_AUTHORITY_DRIFT"
}
func review(c Candidate) string {
	c.Manifest.InstallationState = "PREPARED"
	c.Manifest.SBOMStatus = "UNKNOWN"
	return workstore.Hash(bytesOf(c))
}
func reviews(c []Candidate) string {
	v := []string{}
	for _, entry := range c {
		v = append(v, review(entry))
	}
	return workstore.Hash(bytesOf(v))
}
func Gate(c Candidate) error {
	if c.Check.Status == "FAIL" || c.Manifest.Status == "FAIL" {
		return result.Fail("INSTALL_CHECK_FAILED", "当前产物或清单检查存在失败项", 5)
	}
	if c.Check.Name != c.Plugin.Name || c.Manifest.Name != c.Plugin.Name || c.Check.ArtifactStatus != "PASS" || c.Manifest.ManifestStatus != "PASS" || c.Manifest.PermissionStatus != "PASS" || c.Manifest.Manifest == nil || c.Manifest.Manifest.Dependencies == nil {
		return result.Fail("INSTALL_CHECK_REQUIRED", "需要匹配的完整产物、有效清单、相同权限和明确直接依赖声明", 5)
	}
	for _, f := range append(append([]inventory.Finding{}, c.Check.Checks...), c.Manifest.Checks...) {
		if f.Status == "FAIL" {
			return result.Fail("INSTALL_CHECK_FAILED", f.Code+": "+f.Detail, 5)
		}
	}
	return nil
}
func risk(req Edit) error {
	if !req.AllowUnverified || strings.TrimSpace(req.RiskReason) == "" || len(req.RiskReason) > 2000 {
		return result.Fail("INSTALL_RISK_CONFIRMATION_REQUIRED", "当前仅 L0，低于默认 L2；需明确接受未验证风险并填写理由。此确认不授予运行权限", 4)
	}
	return nil
}
func validEdit(r Edit) bool {
	return hash(r.ExpectedSHA256) && len(r.RiskReason) <= 2000 && (r.Action == "install" && r.Name != "" && r.Snapshot == "" || r.Action == "remove" && r.Name != "" && r.Snapshot == "" && !r.AllowUnverified && r.RiskReason == "" || r.Action == "rollback" && r.Name == "" && uuid(r.Snapshot))
}
func sbom(r Record) SBOM {
	manifest, _ := pluginmanifest.Parse([]byte(r.ManifestDocument))
	return SBOM{Version: 1, Format: "aidock-direct-sbom-v1", InstallationID: r.ID, InstalledAt: r.InstalledAt, Name: r.Plugin.Name, PublisherName: manifest.Name, PluginVersion: r.Plugin.Version, Source: r.Plugin.Source, ArtifactSHA256: r.Plugin.Artifact.SHA256, ManifestSHA256: workstore.Hash([]byte(r.ManifestDocument)), Dependencies: append([]pluginmanifest.Dependency{}, (*manifest.Dependencies)...), Boundary: "仅为本机登记安装生成 plugin.yaml 直接声明清单；不是完整传递依赖或漏洞扫描报告"}
}
func (m *Manager) state(t workstore.Tree, candidates []Candidate, installed bool) State {
	out := State{SHA256: workstore.Digest(t), Root: m.Store.Root, Installations: []Entry{}, Candidates: candidates, Checks: []inventory.Finding{}, History: []History{}, Boundary: "仅登记已备好的本地产物并生成直接声明 SBOM；不下载、执行、授予项目权限或提升 L0。回退移除登记时保留产物与追加审计。"}
	records, err := m.records(t)
	if err == nil {
		var audits []audit
		audits, out.AuditHead, err = m.audits(t)
		out.AuditCount = len(audits)
	}
	if err != nil {
		out.Checks = append(out.Checks, inventory.Finding{Status: "FAIL", Code: "MCP_INSTALL_STORE_INVALID", Detail: err.Error()})
		return out
	}
	names := []string{}
	for name := range records {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		r := records[name]
		entry := Entry{Record: r, Status: "STALE"}
		for _, c := range candidates {
			if c.Plugin.Name == name && review(c) == r.ReviewSHA256 && Gate(c) == nil {
				entry.Status = "CURRENT"
			}
		}
		if installed {
			document := sbom(r)
			entry.SBOM = &document
			entry.SBOMSHA256 = workstore.Hash(JSON(entry.SBOM))
		}
		out.Installations = append(out.Installations, entry)
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
			out.Checks = append(out.Checks, inventory.Finding{Status: "FAIL", Code: "MCP_INSTALL_RECOVERY_REQUIRED", Detail: recovery(err).Error()})
			for i := range out.Installations {
				out.Installations[i].Status = "RECOVERY_REQUIRED"
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
func (m *Manager) build(before workstore.Tree, req Edit, candidates []Candidate, id, created string) (workstore.Tree, error) {
	after := workstore.Clone(before)
	if err := m.initial(&after); err != nil {
		return after, err
	}
	switch req.Action {
	case "install":
		if err := risk(req); err != nil {
			return after, err
		}
		var candidate *Candidate
		for i := range candidates {
			if candidates[i].Plugin.Name == req.Name {
				candidate = &candidates[i]
				break
			}
		}
		if candidate == nil {
			return after, result.Fail("MCP_LOCK_ENTRY_MISSING", "安装锁未登记此 MCP", 4)
		}
		if err := Gate(*candidate); err != nil {
			return after, err
		}
		record := Record{Version: 1, ID: id, InstalledAt: created, Plugin: candidate.Plugin, ManifestDocument: candidate.Manifest.Document, ReviewSHA256: review(*candidate), TrustLevel: "L0", MinimumTrust: "L2", RiskReason: strings.TrimSpace(req.RiskReason)}
		if err := m.Store.PutFile(&after, recordPath(req.Name), JSON(record), 0600); err != nil {
			return after, err
		}
	case "remove":
		p := recordPath(req.Name)
		if _, ok := after.Entries[p]; !ok {
			return after, result.Fail("INSTALL_NOT_FOUND", "没有此安装登记", 4)
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
				if p == "installed" || strings.HasPrefix(p, "installed/") {
					delete(after.Entries, p)
				}
			}
			for p, e := range j.Plan.Before.Entries {
				if p == "installed" || strings.HasPrefix(p, "installed/") {
					after.Entries[p] = e
				}
			}
			break
		}
		if !found {
			return after, result.Fail("SNAPSHOT_NOT_FOUND", "没有此安装登记的已提交恢复点", 4)
		}
		restored, err := m.records(after)
		if err != nil {
			return after, err
		}
		for name, r := range restored {
			if before.Entries[recordPath(name)] == after.Entries[recordPath(name)] {
				continue
			}
			if err = risk(req); err != nil {
				return after, err
			}
			found := false
			for _, c := range candidates {
				if c.Plugin.Name == name && review(c) == r.ReviewSHA256 {
					if err = Gate(c); err != nil {
						return after, err
					}
					found = true
				}
			}
			if !found {
				return after, result.Fail("SNAPSHOT_DEPENDENCY_MISSING", "恢复所需产物、定义或安全证据已变化；保留当前登记，先恢复依赖并重新审查", 5)
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
		return out, result.Fail("INVALID_INPUT", "安装动作、已读摘要或输入不匹配", 2)
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
		if req.ExpectedSHA256 != workstore.Digest(before) {
			return result.Fail("PLAN_STALE", "本机安装登记已变化，保留草稿并重读", 3)
		}
		if err = m.binding(before); err != nil {
			return err
		}
		audits, head, err := m.audits(before)
		if err != nil && (req.Action != "rollback" || !drift(err)) {
			return err
		}
		id, created := workstore.ID(), time.Now().UTC().Format(time.RFC3339Nano)
		after, err := m.build(before, req, candidates, id, created)
		if err != nil {
			return err
		}
		a := audit{Version: 1, Sequence: len(audits) + 1, ID: id, Action: req.Action, CreatedAt: created, Previous: head, Before: authority(before), After: authority(after), ReviewSHA256: reviews(candidates), Request: req}
		if err = m.Store.PutFile(&after, fmt.Sprintf("audit/%08d-%s.json", a.Sequence, a.ID), JSON(a), 0600); err != nil {
			return err
		}
		out.Before = m.state(before, candidates, true)
		out.Candidate = m.state(after, candidates, false)
		out.Plan, err = m.Store.PreviewBound("install-"+req.Action, before, after, map[string]string{"source_sha256": sourceHash, "review_sha256": reviews(candidates), "request": string(bytesOf(req)), "event_id": id, "created_at": created})
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
		if len(p.Inputs) != 5 || p.Digest != digest || p.Inputs["source_sha256"] != sourceHash || p.Inputs["review_sha256"] != reviews(candidates) {
			return result.Fail("PLAN_STALE", "确认、源或当前产物/安全证据已变化；重新预览", 3)
		}
		var req Edit
		if err := decode([]byte(p.Inputs["request"]), &req); err != nil || !validEdit(req) || p.Action != "install-"+req.Action || req.ExpectedSHA256 != workstore.Digest(p.Before) || !uuid(p.Inputs["event_id"]) {
			return result.Fail("INVALID_INSTALL_PLAN", "安装动作输入不匹配", 5)
		}
		if _, err := time.Parse(time.RFC3339Nano, p.Inputs["created_at"]); err != nil {
			return err
		}
		audits, head, err := m.audits(p.Before)
		if err != nil && (req.Action != "rollback" || !drift(err)) {
			return err
		}
		after, err := m.build(p.Before, req, candidates, p.Inputs["event_id"], p.Inputs["created_at"])
		if err != nil {
			return err
		}
		a := audit{Version: 1, Sequence: len(audits) + 1, ID: p.Inputs["event_id"], Action: req.Action, CreatedAt: p.Inputs["created_at"], Previous: head, Before: authority(p.Before), After: authority(after), ReviewSHA256: reviews(candidates), Request: req}
		if err = m.Store.PutFile(&after, fmt.Sprintf("audit/%08d-%s.json", a.Sequence, a.ID), JSON(a), 0600); err != nil {
			return err
		}
		if workstore.Digest(after) != workstore.Digest(p.After) {
			return result.Fail("INVALID_INSTALL_PLAN", "安装候选与动作、历史或当前输入不符", 5)
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
		return &result.Error{Code: e.Code, Message: "安装登记事务需要恢复或人工核对: " + strings.ReplaceAll(e.Message, "skill recover", "mcp install recover"), Exit: e.Exit, Recovery: map[string]string{"command": "mcp install recover"}}
	}
	return err
}
