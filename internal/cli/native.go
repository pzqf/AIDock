package cli

import (
	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func (r *runner) nativeCommands(root *cobra.Command) {
	tools := &cobra.Command{Use: "tool", Short: "工具适配契约"}
	tools.AddCommand(&cobra.Command{Use: "list", Short: "[已实现] 适配器描述与证据边界；不执行工具", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		v := result.New("tool list", native.Adapters())
		v.Summary = "两个项目技能适配器；官方路径已核对，精确版本尚未验收，写入关闭"
		return r.finish(v)
	}})
	root.AddCommand(tools)
	var ids []string
	var all bool
	plan := &cobra.Command{Use: "plan", Short: "[已实现 1C 子集] 完整包原生应用预览；目标只读", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if all && len(ids) > 0 {
			return result.Fail("INVALID_INPUT", "--all 与 --tool 互斥", 2)
		}
		selection := ids
		if all {
			selection = []string{"claude-code", "codex-cli"}
		}
		e, err := r.load(false)
		if err != nil {
			return err
		}
		m, err := e.Native()
		if err != nil {
			return err
		}
		p, err := m.Plan(cmd.Context(), native.Request{Tools: selection})
		if err != nil {
			return err
		}
		v := result.New("plan", p)
		v.Summary = "已保存只读原生预览；没有修改源或工具目标，尚未验证工具生效"
		if p.Blocked {
			v.OK = false
			v.Code = "NATIVE_PREFLIGHT_FAILED"
			v.Summary = "原生预览含冲突或静态阻断项；目标未修改"
		}
		return r.finish(v)
	}}
	plan.Flags().StringSliceVar(&ids, "tool", nil, "claude-code/codex-cli；可重复或逗号分隔")
	plan.Flags().BoolVar(&all, "all", false, "只预览本项目绑定的两工具")
	root.AddCommand(plan)
	for _, action := range []string{"diff", "apply"} {
		var id, digest string
		cmd := &cobra.Command{Use: action, Short: "[1C 子集] diff 重新核验预览；apply 写入尚未开放", Args: cobra.NoArgs}
		cmd.Flags().StringVar(&id, "plan", "", "当前绑定的 native-config 计划 UUID")
		cmd.Flags().StringVar(&digest, "digest", "", "计划完整 digest")
		cmd.Flags().StringVar(&digest, "expected-digest", "", "确认的计划摘要（与源保存命令一致）")
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			e, err := r.load(false)
			if err != nil {
				return err
			}
			m, err := e.Native()
			if err != nil {
				return err
			}
			if cmd.Name() == "apply" {
				return m.Apply(cmd.Context(), id, digest)
			}
			p, err := m.Diff(cmd.Context(), id, digest)
			if err != nil {
				return err
			}
			v := result.New("diff", p)
			v.Summary = "预览输入仍一致；工具目标未修改，原生写入关闭"
			return r.finish(v)
		}
		root.AddCommand(cmd)
	}
}
