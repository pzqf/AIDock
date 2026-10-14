// Package cli declares the entire command surface and gates unimplemented actions.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/console"
	"github.com/pzqf/AIDock/internal/engine"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

type options struct {
	json                                 bool
	home, manifest, project, scope, copy string
	managedRoot                          string
	globalRoot, libraryRoot              string
	includeGenerated                     bool
	bridgeFile                           string
	nativeRoot                           string
}
type runner struct {
	out, err  io.Writer
	o         options
	emitted   bool
	operation string
}

func Run(ctx context.Context, args []string, out, errOut io.Writer) int {
	r := &runner{out: out, err: errOut}
	root := r.command()
	root.SetArgs(args)
	cmd, err := root.ExecuteContextC(ctx)
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		return 0
	}
	op := "aidock"
	if cmd != nil {
		op = strings.TrimPrefix(cmd.CommandPath(), "aidock ")
	}
	if r.operation != "" {
		op = r.operation
	}
	e := &result.Error{Code: "INVALID_INPUT", Message: err.Error(), Exit: 2}
	var classified *result.Error
	if errors.As(err, &classified) {
		e = classified
	}
	for _, arg := range args {
		if arg == "--json" || arg == "--json=true" {
			r.o.json = true
		}
	}
	if op == "mcp exec" {
		_ = json.NewEncoder(errOut).Encode(map[string]string{"code": e.Code, "message": e.Message})
		return e.Exit
	}
	if !r.emitted {
		v := result.New(op, nil)
		v.OK, v.Code, v.Summary = false, e.Code, e.Message
		v.Recovery = e.Recovery
		_ = r.emit(v)
	}
	return e.Exit
}

func (r *runner) emit(v result.Result) error {
	r.emitted = true
	if r.o.json {
		return json.NewEncoder(r.out).Encode(v)
	}
	_, err := fmt.Fprintf(r.out, "%s · %s\n%s\n", v.Operation, v.Code, v.Summary)
	if err != nil {
		return err
	}
	if v.Items != nil {
		b, e := json.MarshalIndent(v.Items, "", "  ")
		if e != nil {
			return e
		}
		_, err = fmt.Fprintln(r.out, string(b))
	}
	return err
}

func (r *runner) finish(v result.Result) error {
	if err := r.emit(v); err != nil {
		return err
	}
	if !v.OK {
		code := 3
		if v.Code == "STATIC_CHECK_FAILED" || v.Code == "NATIVE_PREFLIGHT_FAILED" {
			code = 5
		}
		return result.Fail(v.Code, v.Summary, code)
	}
	return nil
}

func (r *runner) load(requireManifest bool) (*engine.Engine, error) {
	roots, err := config.ResolveRoots(config.RootOptions{FlagHome: r.o.home, EnvHome: os.Getenv("AIDOCK_HOME")})
	if err != nil {
		return nil, result.Fail("INVALID_ROOTS", err.Error(), 2)
	}
	e := &engine.Engine{Roots: roots, BridgeFile: r.o.bridgeFile, SourceCopy: r.o.copy, NativeRoot: r.o.nativeRoot}
	if r.o.globalRoot != "" && (r.o.managedRoot != "" || r.o.libraryRoot != "" || r.o.project != "" || r.o.scope != "") {
		return nil, result.Fail("INVALID_SOURCE_BINDING", "--global-root 与项目绑定互斥", 2)
	}
	if r.o.libraryRoot != "" && r.o.managedRoot == "" {
		return nil, result.Fail("SOURCE_BINDING_REQUIRED", "--library-root 需要显式项目绑定", 4)
	}
	if r.o.managedRoot != "" || r.o.globalRoot != "" {
		if r.o.home == "" || r.o.manifest != "" {
			return nil, result.Fail("SOURCE_BINDING_REQUIRED", "开发版源管理须显式 --home，且不能同时绑定扫描 manifest", 4)
		}
		e.SourceRoot, e.SourceOwner, e.SourceScope = r.o.managedRoot, r.o.project, r.o.scope
		e.GlobalRoot, e.LibraryRoot = r.o.globalRoot, r.o.libraryRoot
		// Source transactions always stay under the explicitly selected home.
		// Personal settings must not redirect fixture backups to another location.
		e.Roots.Local = filepath.Join(roots.Home, "local")
		if _, err = e.Sources(); err != nil {
			return nil, err
		}
	}
	e.BridgeFile = r.o.bridgeFile
	if e.NativeRoot != "" {
		if _, err = e.Native(); err != nil {
			return nil, err
		}
	}
	if r.o.manifest != "" {
		e.Manifest, err = inventory.Load(r.o.manifest)
		if err != nil {
			return nil, result.Fail("INVALID_MANIFEST", err.Error(), 2)
		}
	} else if requireManifest && e.SourceRoot == "" && e.GlobalRoot == "" {
		return nil, result.Fail("BINDING_REQUIRED", "请使用 --manifest 明确只读来源清单；当前不自动扫描/登记工作区", 2)
	}
	return e, nil
}

func (r *runner) scanOptions() inventory.Options {
	return inventory.Options{Project: r.o.project, Scope: r.o.scope, Copy: r.o.copy, IncludeGenerated: r.o.includeGenerated}
}

func (r *runner) command() *cobra.Command {
	root := &cobra.Command{Use: "aidock", Short: "AIDock · 项目技能与 AI 环境管理", Long: "当前实现：1A 只读盘点、1B owned/external-source 技能源管理及 1C 原生完整包预览子集。\n源写入须显式绑定 --managed-root/--home/--project/--scope；外部采纳另需 --copy，外部目录保持只读。\n工具预览须另加 --native-root。原生写入与副本传播尚未开放。", SilenceUsage: true, SilenceErrors: true}
	root.SetOut(r.out)
	r.isolationCommands(root)
	root.SetErr(r.err)
	root.CompletionOptions.DisableDefaultCmd = true
	flags := root.PersistentFlags()
	flags.BoolVar(&r.o.json, "json", false, "输出单个结构化 JSON 结果")
	flags.StringVar(&r.o.home, "home", "", "显式数据根；绑定源模式在其 local 目录保留事务")
	flags.StringVar(&r.o.managedRoot, "managed-root", "", "显式 owned 项目 .aidock 根；启用源事务，不应用到工具")
	flags.StringVar(&r.o.globalRoot, "global-root", "", "显式全局技能 Config Root；与项目管理互斥")
	flags.StringVar(&r.o.libraryRoot, "library-root", "", "项目只读访问的全局库；版本采纳需独立预览")
	flags.StringVar(&r.o.manifest, "manifest", "", "明确来源清单；只读，不等于持久注册")
	flags.StringVar(&r.o.project, "project", "", "逻辑项目 id")
	flags.StringVar(&r.o.scope, "scope", "", "配置作用域")
	flags.StringVar(&r.o.copy, "copy", "", "限定副本 id")
	flags.BoolVar(&r.o.includeGenerated, "include-generated", false, "显式只读盘点清单中的生成镜像")
	flags.StringVar(&r.o.bridgeFile, "bridge-file", "", "显式外部规范源登记文件；源只读，项目版本采纳需额外 --copy")
	flags.StringVar(&r.o.nativeRoot, "native-root", "", "显式工具预览项目根；必须为当前 .aidock 的父目录，不写工具配置")
	root.AddCommand(&cobra.Command{Use: "version", Short: "[已实现] 版本与契约信息", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		v := result.New("version", map[string]any{"version": engine.Version, "go": runtime.Version(), "design_baseline": "v0.24", "schema_versions": map[string]int{"profile": 2, "skills": 1, "project": 1, "bridge": 1, "mcp": 1}, "write_enabled": false, "managed_source_write_supported": true, "native_write_mode": "checked-skill-files"})
		v.Summary = "默认只读；显式项目/全局绑定可使用技能源与批准缓存事务"
		return r.finish(v)
	}})
	skill := &cobra.Command{Use: "skill", Short: "技能资产管理"}
	root.AddCommand(skill)
	for _, name := range []string{"scan", "list", "show"} {
		cmd := &cobra.Command{Use: name, Short: "[已实现] 只读包盘点（候选 locator 不代表稳定资产 id）", Args: cobra.NoArgs}
		if name == "show" {
			cmd.Use = "show <candidate-id>"
			cmd.Args = cobra.ExactArgs(1)
		}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			e, err := r.load(true)
			if err != nil {
				return err
			}
			v, err := e.Inventory(cmd.Context(), r.scanOptions())
			if err != nil {
				return err
			}
			v.Operation = "skill " + cmd.Name()
			if cmd.Name() == "show" {
				report := v.Items.(inventory.Report)
				found := false
				for _, s := range report.Skills {
					if s.CandidateID == args[0] {
						v.Items = s
						v.OK = true
						v.Code = "OK"
						found = true
						break
					}
				}
				if !found {
					return result.Fail("CANDIDATE_NOT_FOUND", "当前来源清单内找不到此候选；目录变化可能使 locator 失效", 2)
				}
			}
			return r.finish(v)
		}
		skill.AddCommand(cmd)
	}
	r.sourceCommands(skill)
	skill.AddCommand(reserved("enable", "1B"), reserved("disable", "1B"))
	for _, name := range []string{"project", "plugin", "memory", "mcp"} {
		group := &cobra.Command{Use: name, Short: name + " 管理"}
		root.AddCommand(group)
		var names []string
		stage := "1"
		switch name {
		case "project":
			names = []string{"add", "list", "rm"}
		case "replica":
			names = []string{"list", "add", "rm", "plan", "apply"}
			stage = "1D"
		case "plugin":
			names = []string{"install", "enable", "disable", "update"}
		case "memory":
			names = []string{"save", "search"}
			stage = "2"
		case "mcp":
			names = []string{"start", "stop", "restart", "retire", "exec"}
			stage = "1C"
		}
		for _, n := range names {
			group.AddCommand(reserved(n, stage))
		}
	}
	r.nativeCommands(root)
	r.replicaCommands(root)
	for _, c := range []struct{ use, stage string }{{"push", "4"}, {"pull", "4"}, {"bootstrap <repo>", "4"}, {"export", "4"}, {"replay <snapshot>", "1B–1D"}, {"update", "4"}, {"audit", "1B"}} {
		root.AddCommand(reserved(c.use, c.stage))
	}
	var probe bool
	var file, kind string
	doctor := &cobra.Command{Use: "doctor", Short: "[已实现] 静态检查；运行探测未开放", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if probe {
			return result.Fail("FEATURE_NOT_IMPLEMENTED", "运行探测与 OS 隔离尚未验收；没有启动任何工具", 4)
		}
		if file != "" {
			if kind == "" {
				return result.Fail("INVALID_INPUT", "--file 需要 --kind 指定 Schema 类型", 2)
			}
			b, err := config.ReadFile(file)
			if err != nil {
				return result.Fail("FILE_UNREADABLE", err.Error(), 5)
			}
			v, err := config.Validate(kind, b)
			if err != nil {
				return result.Fail("SCHEMA_INVALID", err.Error(), 2)
			}
			items := map[string]any{"kind": kind, "structural_validation": "PASS", "read_only": true}
			if kind == "profile-v1" {
				items["migration_required"] = true
			}
			_ = v
			out := result.New("doctor", items)
			out.Summary = "结构校验通过；未解析运行依赖或批准配置应用"
			return r.finish(out)
		}
		e, err := r.load(false)
		if err != nil {
			return err
		}
		v, err := e.Doctor(cmd.Context(), r.scanOptions())
		if err != nil {
			return err
		}
		return r.finish(v)
	}}
	doctor.Flags().BoolVar(&probe, "probe", false, "请求受限运行探测（预留）")
	doctor.Flags().StringVar(&file, "file", "", "只读校验一个规范文件")
	doctor.Flags().StringVar(&kind, "kind", "", "Schema 类型，如 project/profile/skills")
	root.AddCommand(doctor)
	var noOpen bool
	ui := &cobra.Command{Use: "config", Aliases: []string{"ui"}, Short: "[已实现] 启动本地控制台；显式绑定后开放源管理", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		e, err := r.load(false)
		if err != nil {
			return err
		}
		if len(e.Manifest.Bindings) == 0 && e.SourceRoot == "" && e.GlobalRoot == "" && e.BridgeFile == "" {
			return result.Fail("BINDING_REQUIRED", "控制台需要显式 --manifest、--bridge-file 或源管理绑定", 2)
		}
		r.operation = "config"
		return console.Serve(cmd.Context(), e, func(url string) error {
			v := result.New("config", map[string]any{"url": url, "read_only": e.SourceRoot == "" && e.GlobalRoot == "", "native_write_mode": "checked-skill-files"})
			v.Summary = "控制台仅在本进程存活期间运行；关闭页面或空闲超时后退出"
			if err := r.emit(v); err != nil {
				return err
			}
			if !noOpen {
				if err := openBrowser(url); err != nil {
					fmt.Fprintln(r.err, "浏览器启动失败，请打开输出的本地 URL")
				}
			}
			return nil
		})
	}}
	ui.Flags().BoolVar(&noOpen, "no-open", false, "仅输出 URL，不调起浏览器")
	root.AddCommand(ui)
	return root
}

func reserved(use, stage string) *cobra.Command {
	cmd := &cobra.Command{Use: use, Short: "[预留 · 阶段 " + stage + "]", RunE: func(*cobra.Command, []string) error {
		return result.Fail("FEATURE_NOT_IMPLEMENTED", "此能力尚未实现（阶段 "+stage+"）；没有修改配置或启动插件", 4)
	}}
	for _, name := range []string{"plan", "from", "to", "copies", "plugin"} {
		cmd.Flags().String(name, "", "预留契约参数")
	}
	cmd.Flags().Bool("all", false, "仅当前绑定内的工具集合")
	return cmd
}

func openBrowser(url string) error {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		c = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
	case "darwin":
		c = exec.Command("open", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	if err := c.Start(); err != nil {
		return err
	}
	go func() { _ = c.Wait() }()
	return nil
}
