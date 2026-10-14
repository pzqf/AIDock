package cli

import (
	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func (r *runner) nativeManager() (*native.Manager, error) {
	e, err := r.load(false)
	if err != nil {
		return nil, err
	}
	return e.Native()
}
func (r *runner) nativeTransactionCommands(tools *cobra.Command) {
	for _, action := range []string{"history", "recover"} {
		cmd := &cobra.Command{Use: action, Short: "查看工具事务，或恢复未完成事务；不启动工具", Args: cobra.NoArgs}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			m, err := r.nativeManager()
			if err != nil {
				return err
			}
			var items any
			if cmd.Name() == "history" {
				items, err = m.History()
			} else {
				items, err = m.Recover()
			}
			if err != nil {
				return err
			}
			v := result.New("tool "+cmd.Name(), items)
			v.Summary = "工具事务记录/恢复；不修改技能源或启动工具"
			return r.finish(v)
		}
		tools.AddCommand(cmd)
	}
	group := &cobra.Command{Use: "rehearsal", Short: "在独立项目预演完整包应用、回退和撤销；不写入绑定工具目标"}
	for _, action := range []string{"run", "show", "plan", "apply", "recover"} {
		var id, digest, session, transaction string
		cmd := &cobra.Command{Use: action, Short: "隔离预演：" + action, Args: cobra.NoArgs}
		if action == "run" || action == "apply" {
			cmd.Flags().StringVar(&id, "plan", "", "已预览的计划 UUID")
			cmd.Flags().StringVar(&digest, "digest", "", "完整计划摘要")
		}
		if action == "plan" {
			cmd.Flags().StringVar(&transaction, "transaction", "", "最新成功的预演事务 UUID")
		}
		if action == "plan" || action == "apply" || action == "recover" {
			cmd.Flags().StringVar(&session, "session", "", "当前绑定的预演 UUID")
		}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			m, err := r.nativeManager()
			if err != nil {
				return err
			}
			var items any
			switch cmd.Name() {
			case "run":
				items, err = m.Rehearse(cmd.Context(), id, digest)
			case "show":
				items, err = m.RehearsalLatest()
			case "plan":
				items, err = m.RehearsalReverse(session, transaction)
			case "apply":
				items, err = m.RehearsalApply(cmd.Context(), session, id, digest)
			case "recover":
				items, err = m.RehearsalRecover(session)
			}
			if err != nil {
				return err
			}
			v := result.New("tool rehearsal "+cmd.Name(), items)
			v.Summary = "仅操作独立预演项目；绑定项目技能源、工具目标与草稿保留，实际会话生效未验证"
			return r.finish(v)
		}
		group.AddCommand(cmd)
	}
	tools.AddCommand(group)
}
