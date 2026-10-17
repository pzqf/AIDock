package cli

import (
	"os"

	"github.com/pzqf/AIDock/internal/engine"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func (r *runner) replayCommand(root *cobra.Command) {
	var req engine.ReplayRequest
	var copies []string
	cmd := &cobra.Command{Use: "replay <snapshot>", Args: cobra.ExactArgs(1), Short: "项目快照或副本恢复；先审查/预览，完整摘要与 --confirm 才写入", Long: `恢复项目快照或副本批次；不自动授予权限、运行检查或安装依赖。

项目：replay <快照> 默认只读审查；完成完整候选检查后，使用 --prepared 和检查摘要预览。
副本：replay <批次或事务> 默认预览整批；--copies 仅选择原批次目标及对应基线。
重读：--plan 与 --digest；确认：再加 --confirm。撤销副本回退时选择该回退事务的节点。
仅最新成功回退/撤销事务可撤销；原始发布节点仍可再次恢复，旧回退节点不代表完整历史时间点。
同一 UUID 匹配不同类型时须明确 --kind；项目回放还需要明确的 --native-root。`, RunE: func(cmd *cobra.Command, args []string) error {
		req.Snapshot = args[0]
		if cmd.Flags().Changed("copies") {
			req.Copies = copies
			if len(copies) == 0 {
				req.Copies = []string{}
			}
		}
		if req.Prepared != "" {
			req.Action = "plan"
		} else if req.Plan != "" {
			req.Action = "planned"
			if req.Confirm {
				req.Action = "apply"
			}
		}
		// Validate phase/identity before even resolving a data home or source.
		if err := engine.ValidateReplayRequest(req); err != nil {
			return err
		}
		e, err := r.loadMode(false, true)
		if err != nil {
			return err
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		v, err := e.Replay(cmd.Context(), req, executable)
		if err != nil {
			return err
		}
		out := result.New("replay", v)
		out.Summary = v.Boundary
		if v.Code != "" {
			out.OK, out.Code = false, v.Code
		}
		return r.finish(out)
	}}
	cmd.Flags().StringVar(&req.Kind, "kind", "", "明确节点类型 project 或 replica；默认在当前绑定中匹配")
	cmd.Flags().StringSliceVar(&copies, "copies", nil, "原副本批次中要恢复的成员 UUID，逗号分隔；省略恢复整批")
	cmd.Flags().StringVar(&req.Prepared, "prepared", "", "项目完整候选检查 UUID；与检查摘要共同生成预览")
	cmd.Flags().StringVar(&req.Plan, "plan", "", "刚预览的恢复提案 UUID")
	cmd.Flags().StringVar(&req.Digest, "digest", "", "检查或提案的完整摘要")
	cmd.Flags().BoolVar(&req.Confirm, "confirm", false, "确认本恢复节点与完整提案摘要；执行仍再次核验")
	root.AddCommand(cmd)
}
