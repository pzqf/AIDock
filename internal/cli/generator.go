package cli

import (
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func (r *runner) generatorCommands(root *cobra.Command) {
	group := &cobra.Command{Use: "generator", Short: "外部生成器离线交换：锁定、暂存、返回校验与清理"}
	for _, action := range []string{"inspect", "stage", "show", "history", "validate", "clean"} {
		action := action
		var adapter, id, digest string
		cmd := &cobra.Command{Use: action, Short: "[已实现] " + action + "；不运行程序或发布", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			if r.o.home == "" {
				return result.Fail("SOURCE_BINDING_REQUIRED", "生成器交换需明确 --home 和 owned 源绑定", 4)
			}
			e, err := r.load(false)
			if err != nil {
				return err
			}
			m, err := e.Generators()
			if err != nil {
				return err
			}
			var items any
			switch action {
			case "inspect":
				items, err = m.Inspect(cmd.Context(), adapter)
			case "stage":
				items, err = m.Stage(cmd.Context(), adapter, digest)
			case "show":
				items, err = m.Show(cmd.Context(), id)
			case "history":
				items, err = m.History(cmd.Context())
			case "validate":
				v, validationErr := m.Validate(cmd.Context(), id, digest)
				if validationErr != nil {
					return validationErr
				}
				out := result.New("generator validate", v)
				out.Summary = "离线返回包校验；不证明程序执行或工具生效"
				if v.Status != "PASS" {
					out.OK = false
					out.Code = "STATIC_CHECK_FAILED"
				}
				return r.finish(out)
			case "clean":
				items, err = m.Cleanup(cmd.Context(), id, digest)
			}
			if err != nil {
				return err
			}
			out := result.New("generator "+action, items)
			out.Summary = "生成器离线交换；外部执行与正式发布仍关闭"
			return r.finish(out)
		}}
		if action == "inspect" || action == "stage" {
			cmd.Flags().StringVar(&adapter, "adapter", "", "适配合同 JSON 的规范绝对路径")
			_ = cmd.MarkFlagRequired("adapter")
		}
		if action == "show" || action == "validate" || action == "clean" {
			cmd.Flags().StringVar(&id, "stage", "", "暂存 id")
			_ = cmd.MarkFlagRequired("stage")
		}
		if action == "stage" || action == "validate" || action == "clean" {
			cmd.Flags().StringVar(&digest, "digest", "", "stage 使用预览摘要；validate 使用暂存摘要；clean 使用当前 observation_digest")
			_ = cmd.MarkFlagRequired("digest")
		}
		group.AddCommand(cmd)
	}
	group.AddCommand(&cobra.Command{Use: "run", Short: "[未开放] 外部程序执行", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		return result.Fail("GENERATOR_EXECUTION_UNAVAILABLE", "当前隔离证据与外部运行适配未完成；未启动任何生成器", 4)
	}})
	root.AddCommand(group)
}
