package cli

import (
	"github.com/pzqf/AIDock/internal/isolation"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func isolationResult(operation string, report *isolation.Report) result.Result {
	v := result.New(operation, report)
	v.OK, v.Code, v.Summary = false, "EVIDENCE_INCOMPLETE", "隔离原型证据不足；外部生成器执行仍未开放"
	if report == nil {
		v.Summary = "尚无隔离检查记录；读取历史不会启动进程"
		return v
	}
	if report.Stale {
		v.Code, v.Summary = "CHECK_STALE", report.StaleReason
	} else if report.Status == "FAIL" {
		v.Code, v.Summary = "STATIC_CHECK_FAILED", "隔离原型检查存在阻断；外部执行未开放"
	} else if report.Status == "PASS" {
		v.OK, v.Code, v.Summary = true, "OK", "固定测试程序隔离原型通过；不授权第三方执行或正式发布"
	}
	return v
}
func (r *runner) isolationCommands(root *cobra.Command) {
	platform := &cobra.Command{Use: "platform", Short: "OS 执行隔离原型与本机证据"}
	var probe, last bool
	check := &cobra.Command{Use: "check", Short: "[已实现] 静态能力；--probe 只运行内置固定测试程序，--last 仅读历史", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if probe && last {
			return result.Fail("INVALID_INPUT", "--probe 与 --last 互斥", 2)
		}
		if (probe || last) && r.o.home == "" {
			return result.Fail("SOURCE_BINDING_REQUIRED", "运行或读取隔离检查需明确 --home；只使用其 Local 测试夹具", 4)
		}
		e, err := r.load(false)
		if err != nil {
			return err
		}
		m, err := e.Isolation()
		if err != nil {
			return err
		}
		if last {
			report, err := m.Latest()
			if err != nil {
				return err
			}
			return r.finish(isolationResult("platform check", report))
		}
		report, err := m.Check(cmd.Context(), probe)
		if err != nil {
			return err
		}
		return r.finish(isolationResult("platform check", &report))
	}}
	check.Flags().BoolVar(&probe, "probe", false, "显式运行 OS 隔离固定夹具，不执行外部命令")
	check.Flags().BoolVar(&last, "last", false, "读取最近记录，核对平台/程序/策略/有效期；不启动进程")
	platform.AddCommand(check)
	var id string
	show := &cobra.Command{Use: "show", Short: "[已实现] 读取指定隔离检查并核对有效性", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if r.o.home == "" {
			return result.Fail("SOURCE_BINDING_REQUIRED", "读取检查需明确 --home", 4)
		}
		e, err := r.load(false)
		if err != nil {
			return err
		}
		m, err := e.Isolation()
		if err != nil {
			return err
		}
		report, err := m.Show(id)
		if err != nil {
			return err
		}
		return r.finish(isolationResult("platform show", &report))
	}}
	show.Flags().StringVar(&id, "check", "", "检查 id")
	_ = show.MarkFlagRequired("check")
	platform.AddCommand(show)
	platform.AddCommand(&cobra.Command{Use: "pending", Short: "[已实现] 只读列出未清理的隔离测试", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if r.o.home == "" {
			return result.Fail("SOURCE_BINDING_REQUIRED", "检查恢复需明确 --home", 4)
		}
		e, err := r.load(false)
		if err != nil {
			return err
		}
		m, err := e.Isolation()
		if err != nil {
			return err
		}
		pending, err := m.Pending()
		if err != nil {
			return err
		}
		v := result.New("platform pending", pending)
		v.Summary = "未清理隔离测试；不启动进程，不删除资源"
		return r.finish(v)
	}})
	platform.AddCommand(&cobra.Command{Use: "recover", Short: "[已实现] 清理协议标记的隔离测试残留；不改变任何项目", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if r.o.home == "" {
			return result.Fail("SOURCE_BINDING_REQUIRED", "恢复隔离测试需明确 --home", 4)
		}
		e, err := r.load(false)
		if err != nil {
			return err
		}
		m, err := e.Isolation()
		if err != nil {
			return err
		}
		count, err := m.Recover()
		if err != nil {
			return err
		}
		v := result.New("platform recover", map[string]int{"cleaned": count})
		v.Summary = "专用隔离测试残留已清理；没有更改项目或扩大执行权限"
		return r.finish(v)
	}})
	root.AddCommand(platform)
}
