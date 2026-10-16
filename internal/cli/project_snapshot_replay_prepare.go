package cli

import (
	"os"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
	"github.com/spf13/cobra"
)

func (r *runner) snapshotReplayPrepareCommands(group *cobra.Command) {
	var file string
	prepare := &cobra.Command{Use: "prepare", Args: cobra.NoArgs, Short: "重新编译完整冻结候选；默认静态，明确 probe 才运行隔离发现；不应用", RunE: func(cmd *cobra.Command, _ []string) error {
		e, err := r.loadMode(false, true)
		if err != nil {
			return err
		}
		m, err := e.SnapshotReplayPermissions()
		if err != nil {
			return err
		}
		raw, err := workstore.ReadFile(file)
		if err != nil {
			return err
		}
		req, err := native.ParseSnapshotReplayPreparationRequest(raw)
		if err != nil {
			return err
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		v, err := m.PrepareSnapshotReplay(cmd.Context(), req, executable, e.Roots.Home)
		if err != nil {
			return err
		}
		out := result.New("snapshot replay prepare", v)
		out.Summary = v.Boundary
		return r.finish(out)
	}}
	prepare.Flags().StringVar(&file, "file", "", "snapshot_id、candidate_digest 与 preparation 的明确 JSON/YAML；probe/rule_probe 分别选择")
	read := &cobra.Command{Use: "prepared <check-id>", Args: cobra.ExactArgs(1), Short: "重读本机候选检查并复核全部输入；不重跑进程，变化明确过期", RunE: func(cmd *cobra.Command, args []string) error {
		e, err := r.loadMode(false, true)
		if err != nil {
			return err
		}
		m, err := e.SnapshotReplayPermissions()
		if err != nil {
			return err
		}
		v, err := m.ReadSnapshotReplayPreparation(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		out := result.New("snapshot replay prepared", v)
		out.Summary = v.Boundary
		return r.finish(out)
	}}
	group.AddCommand(prepare, read)
}
