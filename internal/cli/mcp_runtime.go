package cli

import (
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/spf13/cobra"
)

func (r *runner) mcpRuntimeCommands(parent *cobra.Command) {
	var req source.MCPRuntimeRequest
	check := &cobra.Command{Use: "runtime-check", Args: cobra.NoArgs, Short: "读取运行时核验记录；--probe 显式执行自有可信本地 Node 的固定版本命令", RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		var out any
		if req.Probe {
			e, checkErr := m.CheckMCPRuntime(cmd.Context(), req)
			if checkErr != nil {
				return checkErr
			}
			v := result.New("mcp runtime-check", e)
			if e.Code != "RUNTIME_VERSION_MATCH" {
				v.OK = false
				v.Code = e.Code
			}
			return r.finish(v)
		}
		out, err = m.MCPRuntime(cmd.Context())
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp runtime-check", out))
	}}
	check.Flags().BoolVar(&req.Probe, "probe", false, "显式运行锁定 Node 的 --version；不启动插件或读取密钥，非隔离")
	check.Flags().StringVar(&req.Name, "plugin", "", "已安装且独立确认自有可信、Tier 0 的本地 MCP 名称")
	check.Flags().StringVar(&req.ExpectedSHA256, "expected-permission", "", "刚读取的 permission state sha256")
	check.Flags().StringVar(&req.ExpectedReviewSHA256, "expected-review", "", "刚读取的 permission state review_sha256")
	recover := &cobra.Command{Use: "runtime-recover", Args: cobra.NoArgs, Short: "恢复运行时核验的审计事务；不重放进程或意图", RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		n, err := m.RecoverMCPRuntime()
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp runtime-recover", map[string]int{"recovered": n}))
	}}
	parent.AddCommand(check, recover)
}
