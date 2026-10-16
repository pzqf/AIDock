package cli

import (
	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func (r *runner) snapshotReader() (*native.Manager, error) {
	e, err := r.loadMode(false, true)
	if err != nil {
		return nil, err
	}
	return e.SnapshotReader()
}

func (r *runner) projectSnapshotCommands(root *cobra.Command) {
	group := &cobra.Command{Use: "snapshot", Short: "联合应用快照核验、历史重建与草稿恢复；完整应用 replay 仍预留"}
	group.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, Short: "读取当前绑定的归档元数据和原事务状态", RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.snapshotReader()
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
		m, err := r.snapshotReader()
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
	group.AddCommand(&cobra.Command{Use: "assess <snapshot-id>", Args: cobra.ExactArgs(1), Short: "重建冻结配置并检查当前依赖；只补齐本机不可变缓存，不应用", RunE: func(cmd *cobra.Command, args []string) error {
		m, err := r.snapshotReader()
		if err != nil {
			return err
		}
		report, err := m.AssessProjectSnapshot(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		out := result.New("snapshot assess", report)
		out.Summary = report.Boundary
		return r.finish(out)
	}})
	r.snapshotDraftCommands(group)
	root.AddCommand(group)
}
