package cli

import (
	"github.com/pzqf/AIDock/internal/pluginsecurity"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func (r *runner) mcpSecurityCommands(parent *cobra.Command) {
	g := &cobra.Command{Use: "security", Short: "本地漏洞参考快照、覆盖和有效期，不启动扫描器或提升信任等级"}
	for _, name := range []string{"state", "check", "recover"} {
		g.AddCommand(&cobra.Command{Use: name, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := r.sourceManager()
			if err != nil {
				return err
			}
			if cmd.Name() == "recover" {
				n, err := m.RecoverMCPSecurity(cmd.Context())
				if err != nil {
					return err
				}
				return r.finish(result.New("mcp security recover", map[string]int{"recovered": n}))
			}
			out, err := m.MCPSecurity(cmd.Context())
			if err != nil {
				return err
			}
			v := result.New("mcp security "+cmd.Name(), out)
			if cmd.Name() == "check" && (len(out.Results) > 0 || len(out.Checks) > 0) {
				v.OK = false
				v.Code = "EVIDENCE_INCOMPLETE"
			}
			for _, f := range out.Checks {
				if f.Status == "FAIL" && cmd.Name() == "check" {
					v.OK = false
					v.Code = "STATIC_CHECK_FAILED"
				}
			}
			for _, c := range out.Results {
				if c.Status == "FAIL" && cmd.Name() == "check" {
					v.OK = false
					v.Code = "STATIC_CHECK_FAILED"
				}
			}
			return r.finish(v)
		}})
	}
	var file, id, digest string
	p := &cobra.Command{Use: "plan", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		var req pluginsecurity.Edit
		if err = replicaInput(file, &req); err != nil {
			return err
		}
		out, err := m.MCPSecurityPlan(cmd.Context(), req)
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp security plan", out))
	}}
	p.Flags().StringVar(&file, "file", "", "含 action、expected_sha256、快照 document 或 snapshot 的 JSON/YAML")
	a := &cobra.Command{Use: "apply", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		out, err := m.ApplyMCPSecurity(cmd.Context(), id, digest)
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp security apply", out))
	}}
	a.Flags().StringVar(&id, "plan", "", "快照计划 UUID")
	a.Flags().StringVar(&digest, "expected-digest", "", "确认预览摘要")
	g.AddCommand(p, a)
	parent.AddCommand(g)
}
