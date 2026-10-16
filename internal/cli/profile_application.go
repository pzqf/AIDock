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
