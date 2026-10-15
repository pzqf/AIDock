package cli

import (
	"github.com/pzqf/AIDock/internal/plugininstall"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func (r *runner) mcpInstallCommands(parent *cobra.Command) {
	group := &cobra.Command{Use: "install", Short: "登记已核验本地产物、轻量 SBOM 与可恢复本机历史，不运行程序"}
	for _, name := range []string{"state", "recover"} {
		group.AddCommand(&cobra.Command{Use: name, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := r.sourceManager()
			if err != nil {
				return err
			}
			var out any
			if cmd.Name() == "state" {
				out, err = m.MCPInstall(cmd.Context())
			} else {
				var n int
				n, err = m.RecoverMCPInstall(cmd.Context())
				out = map[string]int{"recovered": n}
			}
			if err != nil {
				return err
			}
			return r.finish(result.New("mcp install "+cmd.Name(), out))
		}})
	}
	var file, id, digest, name string
	plan := &cobra.Command{Use: "plan", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		var req plugininstall.Edit
		if err = replicaInput(file, &req); err != nil {
			return err
		}
		out, err := m.MCPInstallPlan(cmd.Context(), req)
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp install plan", out))
	}}
	plan.Flags().StringVar(&file, "file", "", "含 action、expected_sha256、name 或 snapshot，以及明确低信任放行与理由的 JSON/YAML")
	apply := &cobra.Command{Use: "apply", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		out, err := m.ApplyMCPInstall(cmd.Context(), id, digest)
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp install apply", out))
	}}
	apply.Flags().StringVar(&id, "plan", "", "本机安装登记计划 UUID")
	apply.Flags().StringVar(&digest, "expected-digest", "", "确认预览摘要")
	sbom := &cobra.Command{Use: "sbom", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		out, err := m.MCPInstalledSBOM(cmd.Context(), name)
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp install sbom", out))
	}}
	sbom.Flags().StringVar(&name, "name", "", "已登记 MCP 名称")
	group.AddCommand(plan, apply, sbom)
	parent.AddCommand(group)
}
