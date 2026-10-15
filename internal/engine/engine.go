// Package engine is shared by CLI and console; neither frontend scans on its own.
package engine

import (
	"context"
	"os"
	"os/exec"
	"runtime"

	"github.com/pzqf/AIDock/internal/bridge"
	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
)

const Version = "0.25.0-dev"

type Engine struct {
	Manifest    inventory.Manifest
	Roots       config.Roots
	SourceRoot  string
	SourceOwner string
	SourceScope string
	GlobalRoot  string
	LibraryRoot string
	BridgeFile  string
	SourceCopy  string
	NativeRoot  string
	ProjectName string
}

func (e *Engine) Native() (*native.Manager, error) {
	m, err := e.Sources()
	if err != nil {
		return nil, err
	}
	return native.New(m, e.NativeRoot)
}

func (e *Engine) Replicas() (*replica.Manager, error) {
	m, err := e.Sources()
	if err != nil {
		return nil, err
	}
	return replica.New(m)
}

// Each operation receives its own manager; OS locks serialize actual targets.
func (e *Engine) Sources() (*source.Manager, error) {
	if e.GlobalRoot != "" {
		if e.SourceRoot != "" || e.LibraryRoot != "" {
			return nil, result.Fail("INVALID_SOURCE_BINDING", "全局管理与项目继承绑定互斥", 2)
		}
		m, err := source.NewGlobal(e.GlobalRoot, e.Roots.Local)
		if err == nil {
			err = m.BindBridge(e.BridgeFile, "")
		}
		return m, err
	}
	if e.SourceRoot == "" {
		return nil, result.Fail("SOURCE_BINDING_REQUIRED", "源管理须显式选择 --managed-root、--project、--scope 和 --home", 4)
	}
	m, err := source.New(e.SourceRoot, e.Roots.Local, e.SourceOwner, e.SourceScope)
	if err == nil {
		err = m.BindBridge(e.BridgeFile, e.SourceCopy)
	}
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

// BridgeCheck loads an explicitly selected bridge index and performs a
// read-only external-source scan. It never creates an index or writes a source.
func (e *Engine) BridgeCheck(ctx context.Context) (result.Result, error) {
	if e.BridgeFile == "" {
		return result.Result{}, result.Fail("BRIDGE_BINDING_REQUIRED", "外部源检查需要明确 --bridge-file", 4)
	}
	idx, err := bridge.Load(e.BridgeFile)
	if err != nil {
		return result.Result{}, result.Fail("INVALID_BRIDGE", err.Error(), 2)
	}
	report, err := bridge.Check(ctx, idx)
	if err != nil {
		return result.Result{}, result.Fail("BRIDGE_CHECK_FAILED", err.Error(), 5)
	}
	if err = e.annotateExternalPins(&report); err != nil {
		return result.Result{}, err
	}
	v := result.New("skill bridge check", report)
	v.Summary = "只读外部规范源检查；外部目录仍是权威源，候选不会自动采纳或写入项目"
	if report.Status == "FAIL" {
		v.OK, v.Code = false, "STATIC_CHECK_FAILED"
	} else if report.Status == "STALE" {
		v.OK, v.Code = false, "EXTERNAL_SOURCE_DRIFT"
	} else if report.Status == "WARN" || report.Status == "UNKNOWN" {
		v.OK, v.Code = false, "EVIDENCE_INCOMPLETE"
	}
	return v, nil
}

type Tool struct {
	ID              string          `json:"id"`
	Path            string          `json:"path,omitempty"`
	Installed       bool            `json:"installed"`
	VersionStatus   string          `json:"version_status"`
	DiscoveryStatus string          `json:"discovery_status"`
	WriteSupported  bool            `json:"write_supported"`
	Adapter         *native.Adapter `json:"adapter,omitempty"`
}

func Tools() []Tool {
	tools := []Tool{}
	for _, t := range []struct{ id, command string }{{"claude-code", "claude"}, {"codex-cli", "codex"}} {
		path, err := exec.LookPath(t.command)
		tools = append(tools, Tool{ID: t.id, Path: path, Installed: err == nil, VersionStatus: "UNKNOWN", DiscoveryStatus: "UNKNOWN", WriteSupported: false})
	}
	for i := range tools {
		for _, a := range native.Adapters() {
			if a.ID == tools[i].ID {
				copy := a
				tools[i].Adapter = &copy
			}
		}
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
	checks = append(checks, inventory.Finding{Status: "UNKNOWN", Code: "ISOLATION_UNAVAILABLE", Detail: "OS 隔离/broker 尚未实现，受管插件执行保持关闭；技能文件安装不执行代码，原生客户端后续执行属于 external"})
	var scan any
	var sourceReport any
	var bridgeReport any
	var nativeCheck any
	sourceFailed := false
	externalDrift := false
	if e.SourceRoot != "" || e.GlobalRoot != "" {
		m, err := e.Sources()
		if err != nil {
			return result.Result{}, err
		}
		report, err := m.Check()
		if err != nil {
			return result.Result{}, err
		}
		sourceReport = report
		sourceFailed = report.Status == "FAIL"
	}
	if len(e.Manifest.Bindings) > 0 {
		v, err := e.Inventory(ctx, o)
		if err != nil {
			return result.Result{}, err
		}
		scan = v.Items
	}
	if e.BridgeFile != "" {
		bv, err := e.BridgeCheck(ctx)
		if err != nil {
			return result.Result{}, err
		}
		bridgeReport = bv.Items
		if report, ok := bv.Items.(bridge.Report); ok && report.Status == "FAIL" {
			sourceFailed = true
		}
		externalDrift = bv.Code == "EXTERNAL_SOURCE_DRIFT"
	}
	if e.NativeRoot != "" {
		m, err := e.Native()
		if err != nil {
			return result.Result{}, err
		}
		nativeCheck, err = m.Latest(ctx)
		if err != nil {
			return result.Result{}, err
		}
	}
	v := result.New("doctor", map[string]any{"roots": e.Roots, "platform": runtime.GOOS, "checks": checks, "tools": Tools(), "inventory": scan, "source_preflight": sourceReport, "external_bridges": bridgeReport, "native_check": nativeCheck, "read_only": true})
	v.OK, v.Code, v.Summary = false, "EVIDENCE_INCOMPLETE", "仅完成静态盘点；生成、工具发现、会话加载、执行隔离均未验证"
	if sourceFailed {
		v.Code, v.Summary = "STATIC_CHECK_FAILED", "技能源存在静态阻断项；工具实际生效尚未验证"
	} else if externalDrift {
		v.Code, v.Summary = "EXTERNAL_SOURCE_DRIFT", "外部规范源与批准基线不一致；需核对候选，工具实际生效尚未验证"
	}
	return v, nil
}
