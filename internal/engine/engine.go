// Package engine is shared by CLI and console; neither frontend scans on its own.
package engine

import (
	"context"
	"os"
	"os/exec"
	"runtime"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
)

const Version = "0.4.0-dev"

type Engine struct {
	Manifest    inventory.Manifest
	Roots       config.Roots
	SourceRoot  string
	SourceOwner string
	SourceScope string
	GlobalRoot  string
	LibraryRoot string
}

// Each operation receives its own manager; OS locks serialize actual targets.
func (e *Engine) Sources() (*source.Manager, error) {
	if e.GlobalRoot != "" {
		if e.SourceRoot != "" || e.LibraryRoot != "" {
			return nil, result.Fail("INVALID_SOURCE_BINDING", "全局管理与项目继承绑定互斥", 2)
		}
		return source.NewGlobal(e.GlobalRoot, e.Roots.Local)
	}
	if e.SourceRoot == "" {
		return nil, result.Fail("SOURCE_BINDING_REQUIRED", "源管理须显式选择 --managed-root、--project、--scope 和 --home", 4)
	}
	m, err := source.New(e.SourceRoot, e.Roots.Local, e.SourceOwner, e.SourceScope)
	if err == nil && e.LibraryRoot != "" {
		err = m.AttachLibrary(e.LibraryRoot)
	}
	return m, err
}

func (e *Engine) Inventory(ctx context.Context, o inventory.Options) (result.Result, error) {
	r, err := inventory.Scan(ctx, e.Manifest, o)
	if err != nil {
		return result.Result{}, result.Fail("SCAN_FAILED", err.Error(), 5)
	}
	v := result.New("skill scan", r)
	v.Summary = "只读技能盘点；未登记资产身份，未应用配置，工具生效未验证"
	v.Project, v.Scope = o.Project, o.Scope
	if o.Copy != "" {
		v.Copies = []string{o.Copy}
	}
	exit := 0
	for _, source := range r.Sources {
		if source.Status == "FAIL" {
			exit = 5
		}
	}
	if r.Counts["FAIL"] > 0 {
		exit = 5
	}
	if exit > 0 {
		v.OK, v.Code = false, "STATIC_CHECK_FAILED"
	}
	return v, nil
}

type Tool struct {
	ID              string `json:"id"`
	Path            string `json:"path,omitempty"`
	Installed       bool   `json:"installed"`
	VersionStatus   string `json:"version_status"`
	DiscoveryStatus string `json:"discovery_status"`
	WriteSupported  bool   `json:"write_supported"`
}

func Tools() []Tool {
	tools := []Tool{}
	for _, t := range []struct{ id, command string }{{"claude-code", "claude"}, {"codex-cli", "codex"}} {
		path, err := exec.LookPath(t.command)
		tools = append(tools, Tool{ID: t.id, Path: path, Installed: err == nil, VersionStatus: "UNKNOWN", DiscoveryStatus: "UNKNOWN", WriteSupported: false})
	}
	return tools
}

func (e *Engine) Doctor(ctx context.Context, o inventory.Options) (result.Result, error) {
	checks := []inventory.Finding{}
	for _, path := range []string{e.Roots.Config, e.Roots.Local} {
		if _, err := os.Stat(path); err != nil {
			checks = append(checks, inventory.Finding{Status: "WARN", Code: "ROOT_NOT_INITIALIZED", Detail: path + " 未初始化；只读模式不创建目录"})
		}
	}
	checks = append(checks, inventory.Finding{Status: "UNKNOWN", Code: "ISOLATION_UNAVAILABLE", Detail: "OS 隔离/broker 尚未实现，所有插件执行与写入功能保持关闭"})
	var scan any
	if len(e.Manifest.Bindings) > 0 {
		v, err := e.Inventory(ctx, o)
		if err != nil {
			return result.Result{}, err
		}
		scan = v.Items
	}
	v := result.New("doctor", map[string]any{"roots": e.Roots, "platform": runtime.GOOS, "checks": checks, "tools": Tools(), "inventory": scan, "read_only": true})
	v.OK, v.Code, v.Summary = false, "EVIDENCE_INCOMPLETE", "仅完成静态盘点；生成、工具发现、会话加载、执行隔离均未验证"
	return v, nil
}
