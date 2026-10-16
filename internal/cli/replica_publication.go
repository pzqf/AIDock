package cli

import (
	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func (r *runner) replicaPublicationCommands(group *cobra.Command) {
	publication := &cobra.Command{Use: "publication", Short: "正式 owned 技能/规则同步：预览、确认、回退、撤销与整批恢复"}
	for _, action := range []string{"plan", "apply", "show", "history", "reverse", "apply-reverse", "inspect-recovery", "recover"} {
		var file, id, digest, transaction, plan string
		cmd := &cobra.Command{Use: action, Args: cobra.NoArgs}
		cmd.Flags().StringVar(&file, "file", "", "发布请求 JSON：generation_id/digest/check_id/check_digest")
		cmd.Flags().StringVar(&id, "publication", "", "正式发布 UUID")
		cmd.Flags().StringVar(&digest, "digest", "", "正式预览或回退计划完整摘要")
		cmd.Flags().StringVar(&transaction, "transaction", "", "最新成功事务 UUID")
		cmd.Flags().StringVar(&plan, "plan", "", "回退或撤销计划 UUID")
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			e, err := r.load(false)
			if err != nil {
				return err
			}
			m, err := e.Replicas()
			if err != nil {
				return err
			}
			var items any
			switch cmd.Name() {
			case "plan":
				var req replica.PublicationRequest
				if err = replicaInput(file, &req); err == nil {
					items, err = m.PlanPublication(cmd.Context(), req)
				}
			case "apply":
				items, err = m.ApplyPublication(cmd.Context(), id, digest)
			case "show":
				items, err = m.PublicationShow(id)
			case "history":
				items, err = m.PublicationHistory()
			case "reverse":
				items, err = m.ReversePublication(id, transaction)
			case "apply-reverse":
				items, err = m.ApplyPublicationReverse(cmd.Context(), id, plan, digest)
			case "recover":
				items, err = m.RecoverPublication(id)
			case "inspect-recovery":
				items, err = m.InspectPublicationRecovery(id)
			}
			if err != nil {
				return err
			}
			v := result.New("replica publication "+cmd.Name(), items)
			v.Summary = "正式同步使用独立确认与整批恢复记录；发布者只读，不运行技能或工具"
			return r.finish(v)
		}
		publication.AddCommand(cmd)
	}
	group.AddCommand(publication)
}
