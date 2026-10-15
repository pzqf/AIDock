package cli

import (
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func (r *runner) mcpManifestCommands(parent *cobra.Command) {
	group := &cobra.Command{Use: "manifest", Short: "只读检查预备产物清单、权限和直接依赖，不生成已安装 SBOM"}
	for _, name := range []string{"list", "check"} {
		var selected string
		cmd := &cobra.Command{Use: name, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := r.sourceManager()
			if err != nil {
				return err
			}
			out, err := m.MCPManifest(cmd.Context(), selected)
			if err != nil {
				return err
			}
			v := result.New("mcp manifest "+cmd.Name(), out)
			v.Summary = out.Boundary
			if cmd.Name() == "check" && out.Status != "PASS" {
				v.OK, v.Code = false, "EVIDENCE_INCOMPLETE"
				if out.Status == "FAIL" {
					v.Code = "STATIC_CHECK_FAILED"
				}
			}
			return r.finish(v)
		}}
		cmd.Flags().StringVar(&selected, "name", "", "只查看指定已保存安装锁的 MCP；省略则查看全部")
		group.AddCommand(cmd)
	}
	parent.AddCommand(group)
}
