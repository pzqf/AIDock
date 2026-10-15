package cli

import (
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
	"github.com/spf13/cobra"
)

func (r *runner) mcpPermissionCommands(parent *cobra.Command) {
	group := &cobra.Command{Use: "permission", Short: "管理当前项目副本的权限授权、吊销和独立恢复，不运行程序"}
	for _, name := range []string{"state", "recover"} {
		group.AddCommand(&cobra.Command{Use: name, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := r.sourceManager()
			if err != nil {
				return err
			}
			var out any
			if cmd.Name() == "state" {
				out, err = m.MCPPermission(cmd.Context())
			} else {
				var n int
				n, err = m.RecoverMCPPermission(cmd.Context())
				out = map[string]int{"recovered": n}
			}
			if err != nil {
				return err
			}
			return r.finish(result.New("mcp permission "+cmd.Name(), out))
		}})
	}
	var file, id, digest string
	plan := &cobra.Command{Use: "plan", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		raw, err := workstore.ReadFile(file)
		if err != nil {
			return err
		}
		req, err := pluginpermission.ParseEdit(raw)
		if err != nil {
			return err
		}
		out, err := m.MCPPermissionPlan(cmd.Context(), req)
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp permission plan", out))
	}}
	plan.Flags().StringVar(&file, "file", "", "含 action、已读/审查摘要、name 或 snapshot；权限确认、confirm_secrets 密钥确认、confirm_unisolated 自有可信非隔离确认、用途和 Public 业务数据边界的 JSON/YAML")
	apply := &cobra.Command{Use: "apply", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		out, err := m.ApplyMCPPermission(cmd.Context(), id, digest)
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp permission apply", out))
	}}
	apply.Flags().StringVar(&id, "plan", "", "项目授权计划 UUID")
	apply.Flags().StringVar(&digest, "expected-digest", "", "确认预览摘要")
	group.AddCommand(plan, apply)
	parent.AddCommand(group)
}
