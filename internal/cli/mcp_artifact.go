package cli

import (
	"github.com/pzqf/AIDock/internal/pluginartifact"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func (r *runner) mcpArtifactCommands(parent *cobra.Command) {
	group := &cobra.Command{Use: "artifact", Short: "按已保存锁导入完整本机 ZIP，支持回退、撤销与中断恢复"}
	state := &cobra.Command{Use: "state", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		out, err := m.MCPArtifacts(cmd.Context())
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp artifact state", out))
	}}
	var file, id, digest, name, path string
	plan := &cobra.Command{Use: "plan", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		var req pluginartifact.Edit
		if err = replicaInput(file, &req); err != nil {
			return err
		}
		out, err := m.MCPArtifactPlan(cmd.Context(), req)
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp artifact plan", out))
	}}
	plan.Flags().StringVar(&file, "file", "", "含 action、name、expected_sha256、expected_lock_sha256，以及 archive_path 或 snapshot 的 JSON/YAML")
	apply := &cobra.Command{Use: "apply", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		out, err := m.ApplyMCPArtifact(cmd.Context(), name, id, digest)
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp artifact apply", out))
	}}
	apply.Flags().StringVar(&name, "name", "", "已锁定 MCP 名称")
	apply.Flags().StringVar(&id, "plan", "", "完整包计划 UUID")
	apply.Flags().StringVar(&digest, "expected-digest", "", "确认预览摘要")
	recover := &cobra.Command{Use: "recover", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		n, err := m.RecoverMCPArtifact(cmd.Context(), path)
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp artifact recover", map[string]int{"recovered": n}))
	}}
	recover.Flags().StringVar(&path, "path", "", "Local Root 中的 mcp-artifacts/... 相对目录；不依赖当前锁内容")
	group.AddCommand(state, plan, apply, recover)
	parent.AddCommand(group)
}
