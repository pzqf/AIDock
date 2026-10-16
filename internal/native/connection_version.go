package native

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type MCPConnectionVersions struct {
	Version  int                 `json:"schema_version"`
	ID       string              `json:"id"`
	Root     string              `json:"root"`
	Owner    string              `json:"owner"`
	Scope    string              `json:"scope_id"`
	Copy     string              `json:"copy"`
	Created  string              `json:"created"`
	Expires  string              `json:"expires"`
	Tools    []QualifiedTool     `json:"tools"`
	Checks   []inventory.Finding `json:"checks"`
	Digest   string              `json:"digest"`
	Boundary string              `json:"boundary"`
}

func connectionVersionDigest(r MCPConnectionVersions) string {
	r.Digest = ""
	return workstore.Hash(jsonData(r))
}
func (m *Manager) connectionVersionsDir() string {
	return filepath.Join(m.Source.Store.Local, "native-mcp-versions", filepath.Base(m.connectionArea()))
}

func (m *Manager) LatestMCPConnectionVersions() (*MCPConnectionVersions, error) {
	entries, err := os.ReadDir(m.connectionVersionsDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	items := []MCPConnectionVersions{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := workstore.ReadFile(filepath.Join(m.connectionVersionsDir(), entry.Name()))
		if err != nil {
			return nil, err
		}
		var r MCPConnectionVersions
		if strict(data, &r) != nil || r.Version != 1 || !validID.MatchString(r.ID) || r.ID+".json" != entry.Name() || r.Root != m.Root || r.Owner != m.Source.Owner || r.Scope != m.Source.Scope || r.Copy != m.Source.Copy || r.Digest != connectionVersionDigest(r) {
			return nil, result.Fail("CHECK_CORRUPT", "MCP 客户端版本记录身份或摘要不一致", 5)
		}
		items = append(items, r)
	}
	if len(items) == 0 {
		return nil, nil
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Created > items[j].Created })
	return &items[0], nil
}

// CheckMCPConnectionVersions runs only --version, in an empty temporary project
// with dedicated homes and the fixed minimal environment. It never loads the
// bound project's configuration, starts an MCP or sends a model request.
func (m *Manager) CheckMCPConnectionVersions(ctx context.Context, tools []string) (MCPConnectionVersions, error) {
	var r MCPConnectionVersions
	as, err := selectAdapters(tools)
	if err != nil {
		return r, err
	}
	if err = workstore.CheckLinks(m.Source.Store.Local); err != nil {
		return r, err
	}
	parent := filepath.Join(m.Source.Store.Local, "native-probes")
	if err = os.MkdirAll(parent, 0700); err != nil {
		return r, err
	}
	run, err := os.MkdirTemp(parent, "mcp-version-")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(run)
	now := time.Now().UTC()
	r = MCPConnectionVersions{Version: 1, ID: workstore.ID(), Root: m.Root, Owner: m.Source.Owner, Scope: m.Source.Scope, Copy: m.Source.Copy, Created: now.Format(time.RFC3339Nano), Expires: now.Add(24 * time.Hour).Format(time.RFC3339Nano), Tools: []QualifiedTool{}, Checks: []inventory.Finding{}, Boundary: "只在空配置临时项目中执行所选工具的 --version，记录精确版本与程序摘要；不加载当前项目配置、不启动 MCP、不发送模型请求。此记录只用于原生配置格式门槛，不提升发现、会话或能力 L2–L4。"}
	for _, a := range as {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		project, home := filepath.Join(run, a.ID, "project"), filepath.Join(run, a.ID, "home")
		for _, dir := range []string{project, home} {
			if err = os.MkdirAll(dir, 0700); err != nil {
				return r, err
			}
		}
		binary, e := m.probes().resolve(a.ID)
		if e != nil {
			r.Checks = append(r.Checks, inventory.Finding{Status: "UNKNOWN", Code: "TOOL_NOT_AVAILABLE", Detail: a.ID + "：没有可直接运行的工具程序"})
			continue
		}
		version, e := m.probes().version(ctx, binary, a.ID, project, home)
		if e != nil {
			r.Checks = append(r.Checks, inventory.Finding{Status: "UNKNOWN", Code: "TOOL_VERSION_FAILED", Detail: a.ID + "：版本检查失败或超时"})
			continue
		}
		if !hasVersion(a.Verified, version) {
			r.Checks = append(r.Checks, inventory.Finding{Status: "UNKNOWN", Code: "TOOL_VERSION_UNSUPPORTED", Detail: a.ID + "：此精确版本尚未验证项目配置合同"})
			continue
		}
		r.Tools = append(r.Tools, QualifiedTool{Tool: a.ID, Version: version, Executable: binary.Path, BinaryDigest: binary.Digest})
		r.Checks = append(r.Checks, inventory.Finding{Status: "PASS", Code: "TOOL_VERSION_OBSERVED", Detail: a.ID + "：已核对精确版本 " + version + "；项目加载另行验证"})
	}
	r.Digest = connectionVersionDigest(r)
	err = m.Source.Store.Locked(func() error { return save(filepath.Join(m.connectionVersionsDir(), r.ID+".json"), jsonData(r)) })
	return r, err
}
