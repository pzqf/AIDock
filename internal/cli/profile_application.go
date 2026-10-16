package cli

import (
	"os"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
	"github.com/spf13/cobra"
)

func (r *runner) profileApplicationCommands(parent *cobra.Command) {
	group := &cobra.Command{Use: "profile", Short: "技能、规则与全部有效 MCP 连接的联合预览、应用和恢复"}
	group.AddCommand(r.profilePrepareCommand())
	for _, action := range []string{"review", "plan", "apply", "history", "recover", "rollback-plan", "rollback-apply"} {
		var file, id, digest, transaction string
		cmd := &cobra.Command{Use: action, Args: cobra.NoArgs}
		if action == "plan" {
			cmd.Flags().StringVar(&file, "file", "", "已通过检查的 native_plan/native_digest 与全部 sessions 审查绑定")
		}
		if action == "apply" || action == "rollback-apply" {
			cmd.Flags().StringVar(&id, "plan", "", "联合预览 UUID")
			cmd.Flags().StringVar(&digest, "digest", "", "联合预览完整摘要")
		}
		if action == "rollback-plan" {
			cmd.Flags().StringVar(&transaction, "transaction", "", "最新成功联合事务 UUID")
		}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			e, err := r.load(false)
			if err != nil {
				return err
			}
			m, err := e.Native()
			if err != nil {
				return err
			}
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			var out any
			switch cmd.Name() {
			case "review":
				out, err = m.ReviewProfileApplication(cmd.Context(), executable, e.Roots.Home)
			case "plan":
				var raw []byte
				raw, err = workstore.ReadFile(file)
				if err == nil {
					var req native.ProfileApplicationRequest
					req, err = native.ParseProfileApplicationRequest(raw)
					if err == nil {
						out, err = m.ProfileApplicationPlan(cmd.Context(), req, executable, e.Roots.Home)
					}
				}
			case "apply":
				out, err = m.ApplyProfileApplication(cmd.Context(), id, digest)
			case "history":
				out, err = m.ProfileApplicationHistory()
			case "recover":
				out, err = m.RecoverProfileApplication()
			case "rollback-plan":
				out, err = m.ReverseProfileApplication(cmd.Context(), transaction)
			case "rollback-apply":
				out, err = m.ApplyProfileApplicationReverse(cmd.Context(), id, digest)
			}
			if err != nil {
				return err
			}
			v := result.New("tool profile "+cmd.Name(), out)
			v.Summary = "技能、规则与有效 MCP 连接共用一次文件事务；安装不启动插件或授予权限，L3/L4 另行验证"
			return r.finish(v)
		}
		group.AddCommand(cmd)
	}
	parent.AddCommand(group)
}

func (r *runner) profilePrepareCommand() *cobra.Command {
	var tools []string
	var all, probe, ruleProbe bool
	var rules string
	cmd := &cobra.Command{Use: "prepare", Short: "检查技能规则与已有MCP授权，准备一次联合应用；安装仍须确认", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if all && len(tools) > 0 {
			return result.Fail("INVALID_INPUT", "--all 与 --tool 互斥", 2)
		}
		selected := tools
		if all {
			selected = []string{"claude-code", "codex-cli"}
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
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		out, err := m.PrepareProfile(cmd.Context(), native.CheckRequest{Tools: selected, Probe: probe, RuleProbe: ruleProbe, RuleResolutions: decisions}, executable, e.Roots.Home)
		if err != nil {
			return err
		}
		v := result.New("tool profile prepare", out)
		v.Summary = out.Boundary
		if out.Status != "READY" {
			v.OK = false
			v.Code = "EVIDENCE_INCOMPLETE"
			if out.Status == "BLOCKED" {
				v.Code = "NATIVE_PREFLIGHT_FAILED"
			}
		}
		return r.finish(v)
	}}
	cmd.Flags().StringSliceVar(&tools, "tool", nil, "claude-code/codex-cli")
	cmd.Flags().BoolVar(&all, "all", false, "选择当前绑定的两个工具")
	cmd.Flags().BoolVar(&probe, "probe", false, "显式启动空配置项目中的工具发现；不启动MCP")
	cmd.Flags().BoolVar(&ruleProbe, "rule-probe", false, "配合 --probe 核对独立候选规则来源")
	cmd.Flags().StringVar(&rules, "rules-file", "", "逐工具规则覆盖决策")
	return cmd
}
