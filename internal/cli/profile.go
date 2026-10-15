package cli

import (
	"github.com/pzqf/AIDock/internal/profile"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/spf13/cobra"
)

func (r *runner) profileCommands(root *cobra.Command) {
	group := &cobra.Command{Use: "profile", Short: "Profile 分层来源、共享规则与本机覆盖管理"}
	var input string
	resolve := &cobra.Command{Use: "resolve", Args: cobra.NoArgs, Short: "只读合并显式层描述，不加载或写入项目", RunE: func(cmd *cobra.Command, _ []string) error {
		var req profile.Input
		if err := replicaInput(input, &req); err != nil {
			return err
		}
		out, err := profile.Resolve(req)
		if err != nil {
			return err
		}
		v := result.New("profile resolve", out)
		v.Summary = "配置层链已展开；未写入源或工具，不代表运行授权"
		return r.finish(v)
	}}
	resolve.Flags().StringVar(&input, "file", "", "显式层及已登记身份/可取得修订清单的 YAML/JSON")
	group.AddCommand(resolve)
	show := &cobra.Command{Use: "show", Args: cobra.NoArgs, Short: "查看当前已保存 Profile 和来源报告", RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		out, err := m.Profile()
		if err != nil {
			return err
		}
		v := result.New("profile show", out)
		v.Summary = "当前共享 Profile；保存状态与工具生效分别检查"
		return r.finish(v)
	}}
	var descriptor string
	plan := &cobra.Command{Use: "plan", Args: cobra.NoArgs, Short: "预览共享 Profile、规则文件和批量技能开关", RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		var req source.ProfileEdit
		if err = replicaInput(descriptor, &req); err != nil {
			return err
		}
		out, err := m.ProfilePlan(cmd.Context(), req)
		if err != nil {
			return err
		}
		v := result.New("profile plan", out)
		v.Summary = "仅生成配置计划；使用 profile apply --target shared|local 确认，原生应用单独预览"
		return r.finish(v)
	}}
	plan.Flags().StringVar(&descriptor, "file", "", "配置编辑 YAML/JSON；target 可选 shared 或 local，本机支持 clear、snapshot、library_root")
	var target, id, digest string
	apply := &cobra.Command{Use: "apply", Args: cobra.NoArgs, Short: "确认指定层的配置计划", RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		if id == "" || digest == "" {
			return result.Fail("INVALID_INPUT", "需要 --plan 和 --expected-digest", 2)
		}
		out, err := m.ApplyProfile(cmd.Context(), target, id, digest)
		if err != nil {
			return err
		}
		return r.finish(result.New("profile apply", out))
	}}
	apply.Flags().StringVar(&target, "target", "shared", "shared 或 local，必须与预览目标一致")
	apply.Flags().StringVar(&id, "plan", "", "计划 UUID")
	apply.Flags().StringVar(&digest, "expected-digest", "", "确认预览摘要")
	recover := &cobra.Command{Use: "recover-local", Args: cobra.NoArgs, Short: "恢复当前副本未完成的本机配置事务", RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		out, err := m.RecoverLocalProfile()
		if err != nil {
			return err
		}
		return r.finish(result.New("profile recover-local", out))
	}}
	group.AddCommand(show, plan, apply, recover)
	root.AddCommand(group)
}
