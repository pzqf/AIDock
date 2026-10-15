// Package project manages project entry points through the canonical replica
// registry. It never scans a workspace or runs a tool when adding a project.
package project

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

var uuid = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[1-5][a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
var route = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Manager struct {
	Local string
	Hook  func(string, int) error
}
type Request struct {
	Root    string `json:"root"`
	Name    string `json:"name"`
	Create  bool   `json:"create"`
	Project string `json:"project_id,omitempty"`
	Scope   string `json:"scope_id,omitempty"`
	Copy    string `json:"copy_id,omitempty"`
}
type Binding struct {
	ID      string `json:"id"`
	Project string `json:"project_id"`
	Scope   string `json:"scope_id"`
	Copy    string `json:"copy_id"`
	Name    string `json:"name"`
	Root    string `json:"root"`
	Branch  string `json:"branch"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
}
type Plan struct {
	Version int                 `json:"schema_version"`
	Kind    string              `json:"kind"`
	ID      string              `json:"id"`
	Local   string              `json:"local"`
	Request Request             `json:"request"`
	Binding Binding             `json:"binding"`
	Batch   workstore.BatchPlan `json:"batch"`
	Digest  string              `json:"digest"`
}
type History struct {
	Plan    Plan   `json:"plan"`
	State   string `json:"state"`
	Pending bool   `json:"pending"`
}

func New(local string) (*Manager, error) {
	if local == "" || !filepath.IsAbs(local) || filepath.Clean(local) != local {
		return nil, result.Fail("INVALID_ROOTS", "项目管理需要明确的本机数据根", 2)
	}
	if err := workstore.CheckLinks(local); err != nil {
		return nil, err
	}
	return &Manager{Local: local}, nil
}
func binding(r replica.Registry, b replica.Binding) Binding {
	return Binding{ID: workstore.Hash([]byte(r.Owner + "/" + r.Scope + "/" + b.ID)), Project: r.Owner, Scope: r.Scope, Copy: b.ID, Name: b.Name, Root: b.Root, Branch: b.Branch, Status: "READY"}
}
func (m *Manager) List() ([]Binding, error) {
	registries, err := replica.Catalog(m.Local)
	if err != nil {
		return nil, err
	}
	out := []Binding{}
	for _, r := range registries {
		for _, b := range r.Copies {
			v := binding(r, b)
			s, e := source.New(v.Root, m.Local, v.Project, v.Scope)
			if e == nil {
				_, e = s.Shared()
			}
			if e != nil {
				v.Status = "UNAVAILABLE"
				v.Reason = e.Error()
			}
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
func (m *Manager) Resolve(id string) (Binding, error) {
	if !route.MatchString(id) {
		return Binding{}, result.Fail("INVALID_INPUT", "项目入口身份无效", 2)
	}
	registries, err := replica.Catalog(m.Local)
	if err != nil {
		return Binding{}, err
	}
	for _, r := range registries {
		for _, copy := range r.Copies {
			b := binding(r, copy)
			if b.ID == id {
				// Domain reads/writes check the selected source. Do not scan every
				// other project or hide history/recovery behind source availability.
				return b, nil
			}
		}
	}
	return Binding{}, result.Fail("PROJECT_NOT_REGISTERED", "项目未登记，不能切换绑定", 4)
}
func metadata(root string) (string, string, error) {
	b, err := workstore.ReadFile(filepath.Join(root, "project.yaml"))
	if err != nil {
		return "", "", err
	}
	v, err := config.Validate("project", b)
	if err != nil {
		return "", "", err
	}
	owner, _ := v.(map[string]any)["id"].(string)
	b, err = workstore.ReadFile(filepath.Join(root, "skills.yaml"))
	if err != nil {
		return "", "", err
	}
	v, err = config.Validate("skills", b)
	if err != nil {
		return "", "", err
	}
	idx := v.(map[string]any)
	if idx["owner"] != owner {
		return "", "", result.Fail("SOURCE_IDENTITY_MISMATCH", "项目与技能身份不一致", 3)
	}
	scope, _ := idx["scope_id"].(string)
	return owner, scope, nil
}
func (m *Manager) prepare(req Request) (Request, Binding, *source.Manager, *replica.Manager, workstore.Tree, workstore.Tree, workstore.Plan, error) {
	var before, after workstore.Tree
	var registration workstore.Plan
	fail := func(e error) (Request, Binding, *source.Manager, *replica.Manager, workstore.Tree, workstore.Tree, workstore.Plan, error) {
		return req, Binding{}, nil, nil, before, after, registration, e
	}
	if !filepath.IsAbs(req.Root) || !validLabel(req.Name, 120) {
		return fail(result.Fail("INVALID_INPUT", "需要项目名称和规范绝对项目目录", 2))
	}
	req.Root = filepath.Clean(req.Root)
	if err := workstore.CheckLinks(req.Root); err != nil {
		return fail(err)
	}
	if st, err := os.Stat(req.Root); err != nil || !st.IsDir() {
		return fail(result.Fail("PROJECT_DIRECTORY_REQUIRED", "请先选择一个已存在的项目目录", 2))
	}
	root := filepath.Join(req.Root, ".aidock")
	if workstore.Within(root, m.Local) || workstore.Within(m.Local, root) {
		return fail(result.Fail("INVALID_ROOTS", "项目源与本机数据根不能重叠", 2))
	}
	if req.Create {
		if req.Project == "" {
			req.Project = workstore.ID()
		}
		if req.Scope == "" {
			req.Scope = "workspace"
		}
	} else {
		owner, scope, err := metadata(root)
		if err != nil {
			return fail(err)
		}
		if req.Project != "" && req.Project != owner || req.Scope != "" && req.Scope != scope {
			return fail(result.Fail("SOURCE_IDENTITY_MISMATCH", "所选目录的项目或作用域与请求不一致", 3))
		}
		req.Project, req.Scope = owner, scope
	}
	if req.Copy == "" {
		req.Copy = workstore.ID()
	}
	if !uuid.MatchString(req.Project) || !uuid.MatchString(req.Copy) || !validLabel(req.Scope, 80) {
		return fail(result.Fail("INVALID_INPUT", "项目/副本身份或作用域无效", 2))
	}
	items, err := m.List()
	if err != nil {
		return fail(err)
	}
	for _, b := range items {
		if workstore.Within(root, b.Root) || workstore.Within(b.Root, root) {
			return fail(result.Fail("PROJECT_ALREADY_REGISTERED", "此目录已登记；请从项目列表打开", 3))
		}
	}
	s, err := source.New(root, m.Local, req.Project, req.Scope)
	if err != nil {
		return fail(err)
	}
	if req.Create {
		before, after, err = s.DraftInitialization(req.Name)
	} else {
		_, err = s.Shared()
		if err == nil {
			before, err = s.Store.Capture(root)
			after = workstore.Clone(before)
		}
	}
	if err != nil {
		return fail(err)
	}
	r, err := replica.New(s)
	if err != nil {
		return fail(err)
	}
	b := replica.Binding{ID: req.Copy, Name: req.Name, Root: root, Excludes: []string{}}
	if req.Create {
		registration, err = r.RegisterInitializedCandidate(b, after)
	} else {
		registration, err = r.Register(b)
	}
	if err != nil {
		return fail(err)
	}
	var registry replica.Registry
	entry := registration.After.Entries["replicas.json"]
	bytes, err := r.Control.Data(entry)
	if err != nil {
		return fail(err)
	}
	if err = json.Unmarshal(bytes, &registry); err != nil {
		return fail(err)
	}
	for _, x := range registry.Copies {
		if x.ID == b.ID {
			b = x
		}
	}
	return req, binding(registry, b), s, r, before, after, registration, nil
}
func validLabel(s string, limit int) bool {
	return strings.TrimSpace(s) != "" && strings.TrimSpace(s) == s && len(s) <= limit && !strings.ContainsFunc(s, unicode.IsControl)
}
func (m *Manager) coordinator(p Plan, blobs *workstore.Store) *workstore.Coordinator {
	return &workstore.Coordinator{Local: filepath.Join(m.Local, "project-transactions"), Identity: "project-" + p.ID, Roots: p.Batch.Roots, Blobs: blobs, Hook: m.Hook}
}
func transfer(from, to *workstore.Store, trees ...workstore.Tree) error {
	for _, t := range trees {
		for path, e := range t.Entries {
			if e.Dir {
				continue
			}
			b, err := from.Data(e)
			if err != nil {
				return err
			}
			scratch := workstore.Tree{Entries: map[string]workstore.Entry{}}
			if err = to.PutFile(&scratch, path, b, e.Mode); err != nil {
				return err
			}
		}
	}
	return nil
}
func digest(p Plan) string               { p.Digest = ""; b, _ := json.Marshal(p); return workstore.Hash(b) }
func (m *Manager) file(id string) string { return filepath.Join(m.Local, "project-plans", id+".json") }
func save(path string, v any) error {
	bytes, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(bytes) > workstore.MaxFile {
		return result.Fail("PLAN_TOO_LARGE", "项目计划过大", 4)
	}
	if err = workstore.CheckLinks(path); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".project-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(bytes)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func (m *Manager) Plan(ctx context.Context, req Request) (Plan, error) {
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	req, b, s, r, before, after, registration, err := m.prepare(req)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Version: 1, Kind: "project-registration", ID: workstore.ID(), Local: m.Local, Request: req, Binding: b}
	p.Batch.Roots = []workstore.RootBinding{{Path: "registry", Root: r.Control.Root}, {Path: "source", Root: s.Store.Root}}
	if err = transfer(r.Control, s.Store, registration.Before, registration.After); err != nil {
		return p, err
	}
	p.Batch, err = m.coordinator(p, s.Store).PreviewObserved("apply", []workstore.BatchTarget{{Path: "source", Before: before, After: after}, {Path: "registry", Before: registration.Before, After: registration.After}})
	if err != nil {
		return p, err
	}
	p.Digest = digest(p)
	return p, save(m.file(p.ID), p)
}
func (m *Manager) load(id string) (Plan, error) {
	var p Plan
	if !uuid.MatchString(id) {
		return p, result.Fail("INVALID_INPUT", "项目计划需要 UUID", 2)
	}
	b, err := workstore.ReadFile(m.file(id))
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(b, &p); err != nil {
		return p, err
	}
	root := filepath.Join(p.Request.Root, ".aidock")
	control := filepath.Join(m.Local, "replica-control", workstore.Hash([]byte(p.Binding.Project+"/"+p.Binding.Scope)))
	if p.Version != 1 || (p.Kind != "project-registration" && p.Kind != "project-removal") || p.ID != id || p.Local != m.Local || p.Digest != digest(p) || p.Request.Project != p.Binding.Project || p.Request.Copy != p.Binding.Copy || p.Request.Scope != p.Binding.Scope || p.Binding.Root != root || !uuid.MatchString(p.Binding.Project) || !uuid.MatchString(p.Binding.Copy) || p.Binding.ID != workstore.Hash([]byte(p.Binding.Project+"/"+p.Binding.Scope+"/"+p.Binding.Copy)) || len(p.Batch.Roots) != 2 || p.Batch.Roots[1] != (workstore.RootBinding{Path: "source", Root: root}) || p.Batch.Roots[0] != (workstore.RootBinding{Path: "registry", Root: control}) || len(p.Batch.Targets) != 2 || p.Batch.Targets[0].Path != "source" || p.Batch.Targets[1].Path != "registry" {
		return p, result.Fail("PROJECT_PLAN_INVALID", "项目计划身份或目录绑定损坏", 5)
	}
	canonical, err := m.coordinator(p, nil).LoadPlan(p.Batch.ID)
	if err != nil {
		return p, err
	}
	a, _ := json.Marshal(canonical)
	b, _ = json.Marshal(p.Batch)
	if string(a) != string(b) {
		return p, result.Fail("PROJECT_PLAN_INVALID", "联合计划记录不一致", 5)
	}
	return p, nil
}
func (m *Manager) Apply(ctx context.Context, id, reviewed string) (History, error) {
	p, err := m.load(id)
	if err != nil {
		return History{}, err
	}
	if reviewed == "" || reviewed != p.Digest {
		return History{}, result.Fail("PLAN_CONFIRMATION_REQUIRED", "需要项目计划完整摘要", 4)
	}
	c := m.coordinator(p, nil)
	js, err := c.History()
	if err != nil {
		return History{}, err
	}
	if len(js) > 0 && js[0].State == "committed" {
		return m.history(p)
	}
	if len(js) > 0 {
		return History{}, result.Fail("PROJECT_RECOVERY_REQUIRED", "此计划曾中断或已恢复；请检查恢复状态并重新预览", 6)
	}
	if p.Kind == "project-removal" {
		// Recheck references while holding both canonical registry and source
		// locks. A preview-time check alone could miss a subsequent snapshot.
		c.Validate = func(workstore.BatchPlan) error { return m.validateRemoval(p) }
		if _, err = c.Apply(ctx, p.Batch.ID, p.Batch.Digest); err != nil {
			return History{}, err
		}
		return m.history(p)
	}
	_, b, _, r, before, after, registration, err := m.prepare(p.Request)
	if err != nil {
		return History{}, err
	}
	if b != p.Binding || workstore.Digest(before) != workstore.Digest(p.Batch.Targets[0].Before) || workstore.Digest(after) != workstore.Digest(p.Batch.Targets[0].After) || workstore.Digest(registration.Before) != workstore.Digest(p.Batch.Targets[1].Before) || workstore.Digest(registration.After) != workstore.Digest(p.Batch.Targets[1].After) || r.Control.Root != p.Batch.Roots[0].Root {
		return History{}, result.Fail("PLAN_STALE", "项目或登记在预览后变化，请重新预览", 3)
	}
	if _, err = c.Apply(ctx, p.Batch.ID, p.Batch.Digest); err != nil {
		return History{}, err
	}
	return m.history(p)
}
func (m *Manager) history(p Plan) (History, error) {
	out := History{Plan: p, State: "PREVIEW"}
	js, err := m.coordinator(p, nil).History()
	if err != nil {
		return out, err
	}
	if len(js) > 1 {
		return out, fmt.Errorf("unexpected project transaction count")
	}
	if len(js) == 1 {
		out.State = js[0].State
		out.Pending = js[0].State == "prepared"
	}
	if !out.Pending {
		out.Pending, err = m.coordinator(p, nil).MarkersPending(p.Batch.ID)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}
func (m *Manager) History() ([]History, error) {
	out := []History{}
	dir := filepath.Join(m.Local, "project-plans")
	if err := workstore.CheckLinks(dir); err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if strings.HasPrefix(f.Name(), ".project-") {
			continue
		}
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			return nil, result.Fail("PROJECT_PLAN_INVALID", "项目记录含未知内容", 5)
		}
		p, err := m.load(strings.TrimSuffix(f.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		h, err := m.history(p)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}
func (m *Manager) Recover(id string) (History, error) {
	p, err := m.load(id)
	if err != nil {
		return History{}, err
	}
	if _, err = m.coordinator(p, nil).Recover(); err != nil {
		return History{}, err
	}
	return m.history(p)
}
