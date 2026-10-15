package cli

import (
	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func (r *runner) nativePrepareCommand() *cobra.Command {
	var ids []string
	var all, probe, ruleProbe bool
	var rules string
	cmd := &cobra.Command{Use: "prepare", Short: "检查并准备工具应用；--probe 显式检查隔离发现，安装仍须单独确认", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if all && len(ids) > 0 {
			return result.Fail("INVALID_INPUT", "--all 与 --tool 互斥", 2)
		}
		selection := ids
		if all {
			selection = []string{"claude-code", "codex-cli"}
		}
		var decisions []native.RuleResolution
		if rules != "" {
			if err := replicaInput(rules, &decisions); err != nil {
				return err
			}
		}
		e, err := r.load(false)
		if err != nil {
			return err
		}
		m, err := e.Native()
		if err != nil {
			return err
		}
		report, err := m.Prepare(cmd.Context(), native.CheckRequest{Tools: selection, Probe: probe, RuleProbe: ruleProbe, RuleResolutions: decisions})
		if err != nil {
			return err
		}
		v := result.New("tool prepare", report)
		v.Summary = "工具应用准备完成；查看预览后可单独确认文件安装，实际会话生效仍待验证"
		if report.Status != "READY" {
			v.OK, v.Code, v.Summary = false, "EVIDENCE_INCOMPLETE", "已保存检查与文件预览；请先处理阻断或未验证条件，尚不可安装"
			if report.Status == "BLOCKED" {
				v.Code = "NATIVE_PREFLIGHT_FAILED"
			}
		}
		return r.finish(v)
	}}
	cmd.Flags().StringSliceVar(&ids, "tool", nil, "claude-code/codex-cli")
	cmd.Flags().BoolVar(&all, "all", false, "准备绑定项目的两款工具")
	cmd.Flags().BoolVar(&probe, "probe", false, "显式启动隔离项目的工具发现协议；不发送模型提示")
	cmd.Flags().BoolVar(&ruleProbe, "rule-probe", false, "配合 --probe 核对独立候选规则的临时会话加载来源")
	cmd.Flags().StringVar(&rules, "rules-file", "", "逐工具规则覆盖决策与完整文件 observed_digest JSON/YAML 列表")
	return cmd
}
