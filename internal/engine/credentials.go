package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/credentials"
	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type CredentialReference struct {
	Name        string `json:"name"`
	Plugin      string `json:"plugin"`
	Project     string `json:"project"`
	Scope       string `json:"scope"`
	Copy        string `json:"copy"`
	Status      string `json:"status"`
	Present     bool   `json:"present"`
	ProjectName string `json:"project_name"`
	CopyName    string `json:"copy_name"`
}
type CredentialReport struct {
	Vault      credentials.State     `json:"vault"`
	References []CredentialReference `json:"references"`
	Unknown    []string              `json:"unknown"`
	Review     string                `json:"review"`
	Boundary   string                `json:"boundary"`
}

func (e *Engine) Credentials() (*credentials.Manager, error) { return credentials.New(e.Roots.Local) }

// Only registered/current AIDock definitions are read. This is an impact review,
// not a discovery of arbitrary external clients or a grant to launch a plugin.
func (e *Engine) CredentialState(ctx context.Context) (CredentialReport, error) {
	out := CredentialReport{References: []CredentialReference{}, Unknown: []string{}, Boundary: "检查当前绑定及本数据根登记的项目、副本、归档和已解除登记目录的 MCP 定义中 secrets 名称声明。未登记项目、外部客户端和其它数据根不可知；缺失值会阻断后续受管启动。"}
	m, err := e.Credentials()
	if err != nil {
		return out, err
	}
	out.Vault, err = m.State(ctx)
	if err != nil {
		return out, err
	}
	known := map[string]bool{}
	for _, v := range out.Vault.Entries {
		known[v.Name] = true
	}
	type target struct{ Root, Project, Scope, Copy, Status, Name string }
	targets := []target{}
	seen := map[string]bool{}
	add := func(t target) {
		if !seen[t.Root] {
			targets = append(targets, t)
			seen[t.Root] = true
		}
	}
	if e.SourceRoot != "" {
		add(target{e.SourceRoot, e.SourceOwner, e.SourceScope, e.SourceCopy, "current", e.ProjectName})
	}
	registries, err := replica.Catalog(e.Roots.Local)
	if err != nil {
		out.Unknown = append(out.Unknown, "项目登记表不可读，引用范围不完整")
	} else {
		for _, r := range registries {
			for _, b := range r.Copies {
				add(target{b.Root, r.Owner, r.Scope, b.ID, "registered", b.Name})
			}
			for _, a := range r.Archived {
				add(target{a.Binding.Root, r.Owner, r.Scope, a.Binding.ID, "archived", a.Binding.Name})
			}
			for _, a := range r.Removed {
				add(target{a.Binding.Root, r.Owner, r.Scope, a.Binding.ID, "removed", a.Binding.Name})
			}
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Root < targets[j].Root })
	inputs := []string{}
	for _, t := range targets {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		path := filepath.Join(t.Root, "mcp.yaml")
		b, readErr := workstore.ReadFile(path)
		if os.IsNotExist(readErr) {
			if st, e := os.Stat(t.Root); e == nil && st.IsDir() {
				inputs = append(inputs, t.Root+":absent")
				continue
			}
		}
		if readErr != nil {
			out.Unknown = append(out.Unknown, t.Project+" / "+t.Copy+"：MCP 声明不可读")
			continue
		}
		v, parseErr := config.Validate("mcp", b)
		if parseErr != nil {
			out.Unknown = append(out.Unknown, t.Project+" / "+t.Copy+"：MCP 声明无效")
			continue
		}
		inputs = append(inputs, t.Root+":"+workstore.Hash(b))
		for _, raw := range v.(map[string]any)["servers"].([]any) {
			server := raw.(map[string]any)
			names, _ := server["secrets"].([]any)
			for _, name := range names {
				n := name.(string)
				out.References = append(out.References, CredentialReference{Name: n, Plugin: server["name"].(string), Project: t.Project, Scope: t.Scope, Copy: t.Copy, Status: t.Status, Present: known[n], ProjectName: t.Name, CopyName: t.Name})
			}
		}
	}
	b, _ := json.Marshal(struct {
		Inputs     []string
		References []CredentialReference
		Unknown    []string
	}{inputs, out.References, out.Unknown})
	out.Review = workstore.Hash(b)
	return out, nil
}
func (e *Engine) ChangeCredential(ctx context.Context, req credentials.Edit) (credentials.Entry, error) {
	if req.Action == "delete" {
		report, err := e.CredentialState(ctx)
		if err != nil {
			return credentials.Entry{}, err
		}
		if !req.ReferencesReviewed || req.ExpectedReferences == "" || req.ExpectedReferences != report.Review {
			return credentials.Entry{}, result.Fail("CREDENTIAL_REFERENCES_STALE", "请重新读取密钥引用范围并确认删除影响", 3)
		}
	}
	m, err := e.Credentials()
	if err != nil {
		return credentials.Entry{}, err
	}
	return m.Change(ctx, req)
}
