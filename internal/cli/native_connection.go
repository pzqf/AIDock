package cli

import (
	"os"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
	"github.com/spf13/cobra"
)

func (r *runner) nativeConnectionCommands(parent *cobra.Command) {
	group := &cobra.Command{Use: "mcp", Short: "项目级受管 MCP 连接安装、移除与独立回退；不启动插件"}
	var versionTools []string
	var versionProbe, versionAll bool
	versions := &cobra.Command{Use: "versions", Args: cobra.NoArgs, Short: "读取版本记录；--probe 只在空配置临时目录执行所选工具 --version", RunE: func(cmd *cobra.Command, _ []string) error {
		if versionAll && len(versionTools) > 0 || !versionProbe && (versionAll || len(versionTools) > 0) {
			return result.Fail("INVALID_INPUT", "--all/--tool 互斥；工具选择需要 --probe", 2)
		}
		m, err := r.nativeManager()
		if err != nil {
			return err
		}
		var out any
		if versionProbe {
			selected := versionTools
			if versionAll {
				selected = []string{"claude-code", "codex-cli"}
			}
			out, err = m.CheckMCPConnectionVersions(cmd.Context(), selected)
		} else {
			out, err = m.LatestMCPConnectionVersions()
		}
		if err != nil {
			return err
		}
		v := result.New("tool mcp versions", out)
		v.Summary = "只核对工具程序精确版本；未加载项目配置或启动 MCP"
		return r.finish(v)
	}}
	versions.Flags().StringSliceVar(&versionTools, "tool", nil, "claude-code/codex-cli")
	versions.Flags().BoolVar(&versionAll, "all", false, "选择两款工具")
	versions.Flags().BoolVar(&versionProbe, "probe", false, "明确执行所选工具 --version；空项目和独立 home")
	group.AddCommand(versions)
	for _, action := range []string{"plan", "apply", "history", "recover", "rollback-plan", "rollback-apply"} {
		var file, id, digest, transaction string
		cmd := &cobra.Command{Use: action, Args: cobra.NoArgs}
		if action == "plan" {
			cmd.Flags().StringVar(&file, "file", "", "明确 action、tools、check_id 和 session 绑定的 JSON 文件")
		}
		if action == "apply" || action == "rollback-apply" {
			cmd.Flags().StringVar(&id, "plan", "", "已审查预览 UUID")
			cmd.Flags().StringVar(&digest, "digest", "", "完整预览摘要")
		}
		if action == "rollback-plan" {
			cmd.Flags().StringVar(&transaction, "transaction", "", "最新成功连接事务 UUID；回退后的最新事务可撤销回退")
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
			var out any
			switch cmd.Name() {
			case "plan":
				var raw []byte
				raw, err = workstore.ReadFile(file)
				if err == nil {
					var req native.MCPConnectionRequest
					req, err = native.ParseMCPConnectionRequest(raw)
					if err == nil {
						var executable string
						executable, err = os.Executable()
						if err == nil {
							out, err = m.MCPConnectionPlan(cmd.Context(), req, executable, e.Roots.Home)
						}
					}
				}
			case "apply":
				out, err = m.ApplyMCPConnection(cmd.Context(), id, digest)
			case "history":
				out, err = m.MCPConnectionHistory()
			case "recover":
				out, err = m.RecoverMCPConnections()
			case "rollback-plan":
				out, err = m.ReverseMCPConnection(cmd.Context(), transaction)
			case "rollback-apply":
				out, err = m.ApplyMCPConnectionReverse(cmd.Context(), id, digest)
			}
			if err != nil {
				return err
			}
			v := result.New("tool mcp "+cmd.Name(), out)
			v.Summary = "项目级受管 MCP 字段与归属共同处理；不启动插件，不提升实际客户端 L3/L4，原始字节备份仅保存在受限本机目录"
			return r.finish(v)
		}
		group.AddCommand(cmd)
	}
	parent.AddCommand(group)
}
