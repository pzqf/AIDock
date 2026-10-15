package cli

import (
	"github.com/pzqf/AIDock/internal/pluginorigin"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func (r *runner) mcpOriginCommands(parent *cobra.Command) {
	group := &cobra.Command{Use: "origin", Short: "当前项目本机公钥与离线来源声明，可预览、回退及恢复"}
	for _, name := range []string{"state", "recover"} {
		group.AddCommand(&cobra.Command{Use: name, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := r.sourceManager()
			if err != nil {
				return err
			}
			var out any
			if cmd.Name() == "state" {
				out, err = m.MCPOrigin(cmd.Context())
			} else {
				var n int
				n, err = m.RecoverMCPOrigin(cmd.Context())
				out = map[string]int{"recovered": n}
			}
			if err != nil {
				return err
			}
			return r.finish(result.New("mcp origin "+cmd.Name(), out))
		}})
	}
	var file, id, digest, subject string
	plan := &cobra.Command{Use: "plan", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		var req pluginorigin.Edit
		if err = replicaInput(file, &req); err != nil {
			return err
		}
		out, err := m.MCPOriginPlan(cmd.Context(), req)
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp origin plan", out))
	}}
	plan.Flags().StringVar(&file, "file", "", "包含 action、expected_sha256 及公钥/声明/恢复点的 JSON/YAML")
	apply := &cobra.Command{Use: "apply", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		out, err := m.ApplyMCPOrigin(cmd.Context(), id, digest)
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp origin apply", out))
	}}
	apply.Flags().StringVar(&id, "plan", "", "来源证据计划 UUID")
	apply.Flags().StringVar(&digest, "expected-digest", "", "确认预览摘要")
	show := &cobra.Command{Use: "show", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		out, err := m.ShowMCPOrigin(cmd.Context(), subject)
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp origin show", map[string]string{"document": out}))
	}}
	show.Flags().StringVar(&subject, "subject", "", "产物 subject SHA-256")
	group.AddCommand(plan, apply, show)
	parent.AddCommand(group)
}
