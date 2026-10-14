// Package replica manages explicit local bindings and protected common
// baselines. This milestone observes managed sources; it never publishes or
// regenerates a project, starts tools, or adopts directories by resemblance.
package replica

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Binding struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Root     string   `json:"root"`
	Branch   string   `json:"branch"`
	Excludes []string `json:"local_excludes"`
}
type Registry struct {
	Version int       `json:"schema_version"`
	Owner   string    `json:"owner"`
	Scope   string    `json:"scope_id"`
	Copies  []Binding `json:"copies"`
}
type Baseline struct {
	Version int                           `json:"schema_version"`
	Owner   string                        `json:"owner"`
	Scope   string                        `json:"scope_id"`
	Copies  []string                      `json:"copies"`
	Entries map[string]source.SharedEntry `json:"entries"`
}
type Manager struct {
	Source  *source.Manager
	Control *workstore.Store
}

func New(m *source.Manager) (*Manager, error) {
	if m.Owner == "global" {
		return nil, result.Fail("REPLICA_PROJECT_REQUIRED", "副本管理需要项目绑定", 4)
	}
	root := filepath.Join(m.Store.Local, "replica-control", workstore.Hash([]byte(m.Owner+"/"+m.Scope)))
	if err := workstore.CheckLinks(filepath.Dir(root)); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(root), 0700); err != nil {
		return nil, err
	}
	s := &workstore.Store{Root: root, Local: filepath.Join(m.Store.Local, "replica-journals"), PlanKind: "replica-control"}
	s.Validate = func(p workstore.Plan) error {
		if p.Action != "replica-register" && p.Action != "replica-baseline" {
			return result.Fail("PLAN_KIND_MISMATCH", "只接受副本登记或共同基线计划", 2)
		}
		return nil
	}
	return &Manager{Source: m, Control: s}, nil
}
func data(v any) []byte { b, _ := json.MarshalIndent(v, "", "  "); return append(b, '\n') }
func decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var tail any
	if err := d.Decode(&tail); err != io.EOF {
		return fmt.Errorf("需要单个 JSON 对象")
	}
	return nil
}
func (m *Manager) registry(t workstore.Tree) (Registry, error) {
	r := Registry{Version: 1, Owner: m.Source.Owner, Scope: m.Source.Scope, Copies: []Binding{}}
	if e, ok := t.Entries["replicas.json"]; ok {
		b, err := m.Control.Data(e)
		if err != nil {
			return r, err
		}
		if err = decode(b, &r); err != nil {
			return r, result.Fail("REPLICA_REGISTRY_INVALID", err.Error(), 5)
		}
	}
	if r.Version != 1 || r.Owner != m.Source.Owner || r.Scope != m.Source.Scope || len(r.Copies) > 32 {
		return r, result.Fail("REPLICA_REGISTRY_INVALID", "副本登记身份或数量无效", 5)
	}
	for i, b := range r.Copies {
		if err := m.validateBinding(b); err != nil {
			return r, err
		}
		for j, c := range r.Copies {
			if i != j && (b.ID == c.ID || strings.EqualFold(b.Name, c.Name) || overlap(b.Root, c.Root)) {
				return r, result.Fail("REPLICA_DUPLICATE", "副本身份、名称或目录重复/重叠", 2)
			}
		}
	}
	return r, nil
}
func overlap(a, b string) bool { return workstore.Within(a, b) || workstore.Within(b, a) }
func (m *Manager) validateBinding(b Binding) error {
	if !uuid.MatchString(b.ID) || strings.TrimSpace(b.Name) == "" || len(b.Name) > 120 || !filepath.IsAbs(b.Root) || filepath.Clean(b.Root) != b.Root || filepath.Base(b.Root) != ".aidock" {
		return result.Fail("INVALID_INPUT", "副本需要 UUID、名称和绝对 .aidock 源目录", 2)
	}
	if overlap(b.Root, m.Source.Store.Local) {
		return result.Fail("INVALID_ROOTS", "副本源不能与本机事务根重叠", 2)
	}
	for _, p := range b.Excludes {
		if err := workstore.SafeRelative(p); err != nil {
			return err
		}
	}
	return nil
}
func branch(root string) string {
	// Read only the repository's local HEAD. No git/svn executable, config,
	// credential, hook, update or network access is used by registration.
	for p := filepath.Dir(root); ; p = filepath.Dir(p) {
		dir := filepath.Join(p, ".git")
		if st, err := os.Lstat(dir); err == nil {
			if !st.IsDir() {
				return "UNKNOWN"
			} // linked worktree metadata is not followed
			if err = workstore.CheckLinks(dir); err != nil {
				return "UNKNOWN"
			}
			b, err := workstore.ReadFile(filepath.Join(dir, "HEAD"))
			if err != nil {
				return "UNKNOWN"
			}
			head := strings.TrimSpace(string(b))
			if strings.HasPrefix(head, "ref: refs/heads/") {
				return strings.TrimPrefix(head, "ref: refs/heads/")
			}
			return "DETACHED"
		}
		if filepath.Dir(p) == p {
			return "UNKNOWN"
		}
	}
}
func (m *Manager) observe(b Binding) (source.SharedCatalog, error) {
	if err := m.validateBinding(b); err != nil {
		return source.SharedCatalog{}, err
	}
	s, err := source.New(b.Root, m.Source.Store.Local, m.Source.Owner, m.Source.Scope)
	if err != nil {
		return source.SharedCatalog{}, err
	}
	return s.Shared()
}
func (m *Manager) List() (Registry, error) {
	var r Registry
	err := m.Control.Locked(func() error {
		if err := m.Control.Pending(); err != nil {
			return err
		}
		t, err := m.Control.Capture(m.Control.Root)
		if err != nil {
			return err
		}
		r, err = m.registry(t)
		return err
	})
	return r, err
}
func (m *Manager) Register(b Binding) (workstore.Plan, error) {
	if b.ID == "" {
		b.ID = workstore.ID()
	}
	b.Root = filepath.Clean(b.Root)
	if b.Excludes == nil {
		b.Excludes = []string{}
	}
	if err := m.validateBinding(b); err != nil {
		return workstore.Plan{}, err
	}
	if _, err := m.observe(b); err != nil {
		return workstore.Plan{}, err
	}
	b.Branch = branch(b.Root)
	var p workstore.Plan
	err := m.Control.Locked(func() error {
		if err := m.Control.Pending(); err != nil {
			return err
		}
		t, err := m.Control.Capture(m.Control.Root)
		if err != nil {
			return err
		}
		r, err := m.registry(t)
		if err != nil {
			return err
		}
		for _, old := range r.Copies {
			if old.ID == b.ID || strings.EqualFold(old.Name, b.Name) || overlap(old.Root, b.Root) {
				return result.Fail("REPLICA_DUPLICATE", "已登记此身份、名称或目录；不自动重新绑定", 2)
			}
		}
		r.Copies = append(r.Copies, b)
		sort.Slice(r.Copies, func(i, j int) bool { return r.Copies[i].ID < r.Copies[j].ID })
		if len(r.Copies) > 32 {
			return result.Fail("INVALID_INPUT", "首期最多登记 32 个副本", 2)
		}
		after := workstore.Clone(t)
		after.Exists = true
		if err = m.Control.PutFile(&after, "replicas.json", data(r), 0600); err != nil {
			return err
		}
		p, err = m.Control.PreviewBound("replica-register", t, after, map[string]string{"binding_id": b.ID, "binding_root": b.Root, "binding_branch": b.Branch})
		return err
	})
	return p, err
}
func (m *Manager) Apply(ctx context.Context, id, digest string) (workstore.Journal, error) {
	p, err := m.Control.LoadPlan(id)
	if err != nil {
		return workstore.Journal{}, err
	}
	if digest == "" || p.Digest != digest {
		return workstore.Journal{}, result.Fail("PLAN_CONFIRMATION_REQUIRED", "需要副本登记/基线预览的完整摘要", 4)
	}
	m.Control.Validate = func(p workstore.Plan) error {
		if p.Action == "replica-register" {
			r, err := m.registry(p.After)
			if err != nil {
				return err
			}
			for _, b := range r.Copies {
				if b.ID == p.Inputs["binding_id"] {
					if branch(b.Root) != p.Inputs["binding_branch"] {
						return result.Fail("PLAN_STALE", "登记后分支观察变化", 3)
					}
					_, err = m.observe(b)
					return err
				}
			}
			return result.Fail("PLAN_KIND_MISMATCH", "缺少登记身份", 2)
		}
		if p.Action == "replica-baseline" {
			return m.validateBaselinePlan(p)
		}
		return result.Fail("PLAN_KIND_MISMATCH", "只确认副本登记或共同基线", 2)
	}
	return m.Control.Apply(ctx, id)
}
func (m *Manager) Recover() (int, error) { return m.Control.Recover() }
