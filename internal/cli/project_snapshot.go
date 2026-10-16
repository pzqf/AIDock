package cli

import (
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func (r *runner) projectSnapshotCommands(root *cobra.Command) {
	group := &cobra.Command{Use: "snapshot", Short: "项目联合应用快照查看与正文核验；完整 replay 写入仍预留"}
	group.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, Short: "读取当前绑定的归档元数据和原事务状态", RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.nativeManager()
		if err != nil {
			return err
		}
		report, err := m.ProjectSnapshots(cmd.Context())
		if err != nil {
			return err
		}
		out := result.New("snapshot list", report)
		out.Summary = report.Boundary
		return r.finish(out)
	}})
	group.AddCommand(&cobra.Command{Use: "check <snapshot-id>", Args: cobra.ExactArgs(1), Short: "核对完整归档正文和原生备份；不应用或验证当前安全策略", RunE: func(cmd *cobra.Command, args []string) error {
		m, err := r.nativeManager()
		if err != nil {
			return err
		}
		report, err := m.CheckProjectSnapshot(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		out := result.New("snapshot check", report)
		out.Summary = "归档正文和备份字节已核对；不表示当前依赖、授权或完整项目回退可执行"
		return r.finish(out)
	}})
	root.AddCommand(group)
}
