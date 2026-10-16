package cli

import (
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func (r *runner) snapshotDraftCommands(parent *cobra.Command) {
	group := &cobra.Command{Use: "draft", Short: "将快照恢复为已保存草稿；源与本机层共同提交，原生配置不应用"}
	group.AddCommand(&cobra.Command{Use: "plan <snapshot-id>", Args: cobra.ExactArgs(1), Short: "预览历史草稿的项目投影及本机覆盖恢复", RunE: func(cmd *cobra.Command, args []string) error {
		e, err := r.loadMode(false, true)
		if err != nil {
			return err
		}
		m, err := e.SnapshotDraftRecovery()
		if err != nil {
			return err
		}
		p, err := m.PrepareSnapshotDraft(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		out := result.New("snapshot draft plan", p)
		out.Summary = p.Boundary
		return r.finish(out)
	}})
	var id, digest string
	apply := &cobra.Command{Use: "apply", Args: cobra.NoArgs, Short: "确认完整摘要后共同保存两层草稿；不应用工具", RunE: func(cmd *cobra.Command, _ []string) error {
		e, err := r.loadMode(false, true)
		if err != nil {
			return err
		}
		m, err := e.SnapshotDraftRecovery()
		if err != nil {
			return err
		}
		j, err := m.ApplySnapshotDraft(cmd.Context(), id, digest)
		if err != nil {
			return err
		}
		out := result.New("snapshot draft apply", map[string]string{"id": j.Plan.ID, "state": j.State, "action": j.Plan.Action})
		out.Summary = "已保存草稿恢复结果，工具配置未应用；检查后另行授权并预览应用"
		return r.finish(out)
	}}
	apply.Flags().StringVar(&id, "plan", "", "草稿恢复/撤销计划 UUID")
	apply.Flags().StringVar(&digest, "digest", "", "完整草稿恢复计划摘要")
	group.AddCommand(apply, &cobra.Command{Use: "history", Args: cobra.NoArgs, Short: "读取本绑定的草稿恢复记录与可回退状态", RunE: func(cmd *cobra.Command, _ []string) error {
		e, err := r.loadMode(false, true)
		if err != nil {
			return err
		}
		m, err := e.SnapshotDraftRecovery()
		if err != nil {
			return err
		}
		rows, err := m.SnapshotDraftHistory()
		if err != nil {
			return err
		}
		return r.finish(result.New("snapshot draft history", rows))
	}}, &cobra.Command{Use: "reverse <transaction-id>", Args: cobra.ExactArgs(1), Short: "预览回退或撤销草稿恢复；保留不可变历史", RunE: func(cmd *cobra.Command, args []string) error {
		e, err := r.loadMode(false, true)
		if err != nil {
			return err
		}
		m, err := e.SnapshotDraftRecovery()
		if err != nil {
			return err
		}
		p, err := m.ReverseSnapshotDraft(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		out := result.New("snapshot draft reverse", p)
		out.Summary = p.Boundary
		return r.finish(out)
	}}, &cobra.Command{Use: "recover", Args: cobra.NoArgs, Short: "共同恢复中断的源和本机层；不重放任何程序", RunE: func(cmd *cobra.Command, _ []string) error {
		e, err := r.loadMode(false, true)
		if err != nil {
			return err
		}
		m, err := e.SnapshotDraftRecovery()
		if err != nil {
			return err
		}
		n, err := m.RecoverSnapshotDraft()
		if err != nil {
			return err
		}
		out := result.New("snapshot draft recover", map[string]int{"recovered": n})
		out.Summary = "草稿共同事务恢复完成；工具文件与程序未重放"
		return r.finish(out)
	}}, &cobra.Command{Use: "inspect", Args: cobra.NoArgs, Short: "只读检查草稿共同事务恢复条件", RunE: func(cmd *cobra.Command, _ []string) error {
		e, err := r.loadMode(false, true)
		if err != nil {
			return err
		}
		m, err := e.SnapshotDraftRecovery()
		if err != nil {
			return err
		}
		report, err := m.InspectSnapshotDraftRecovery()
		if err != nil {
			return err
		}
		return r.finish(result.New("snapshot draft inspect", report))
	}})
	parent.AddCommand(group)
}
