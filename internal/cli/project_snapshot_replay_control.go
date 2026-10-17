package cli

import (
	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
	"os"
)

func (r *runner) snapshotReplayControlCommands(group *cobra.Command) {
	for _, action := range []string{"plan", "planned", "apply", "reverse", "history", "inspect", "recover"} {
		var id, digest, prepared string
		cmd := &cobra.Command{Use: action, Args: cobra.NoArgs}
		if action == "plan" {
			cmd.Flags().StringVar(&prepared, "prepared", "", "已完成全部候选检查的 UUID")
		} else if action == "planned" || action == "apply" {
			cmd.Flags().StringVar(&id, "plan", "", "共同提案 UUID")
		} else if action == "reverse" {
			cmd.Flags().StringVar(&id, "transaction", "", "最新成功共同回放或回退事务 UUID")
		}
		if action == "plan" || action == "planned" || action == "apply" {
			cmd.Flags().StringVar(&digest, "digest", "", "刚审查的完整摘要")
		}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			e, err := r.loadMode(false, true)
			if err != nil {
				return err
			}
			m, err := e.SnapshotReader()
			if err != nil {
				return err
			}
			var data any
			switch cmd.Name() {
			case "plan":
				exe, er := os.Executable()
				if er != nil {
					return er
				}
				data, err = m.PlanSnapshotReplay(cmd.Context(), prepared, digest, exe, e.Roots.Home)
			case "planned":
				data, err = m.ReadSnapshotReplayPlan(cmd.Context(), id, digest)
			case "apply":
				data, err = m.ApplySnapshotReplay(cmd.Context(), id, digest)
			case "reverse":
				data, err = m.ReverseSnapshotReplay(cmd.Context(), id)
			case "history":
				data, err = m.SnapshotReplayHistory()
			case "inspect":
				data, err = m.InspectSnapshotReplayRecovery()
			case "recover":
				var n int
				n, err = m.RecoverSnapshotReplay()
				data = map[string]int{"recovered": n}
			}
			if err != nil {
				return err
			}
			out := result.New("snapshot replay "+cmd.Name(), data)
			out.Summary = "共同回放、回退与中断恢复；执行前后重新检查，不恢复历史授权或凭据"
			if p, ok := data.(native.SnapshotReplayPreview); ok {
				out.Summary = p.Boundary
				if !p.CanApply {
					out.OK = false
					out.Code = p.Code
				}
			}
			return r.finish(out)
		}
		group.AddCommand(cmd)
	}
}
