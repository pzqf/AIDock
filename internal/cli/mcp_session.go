package cli

import (
	"github.com/pzqf/AIDock/internal/mcpsession"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/spf13/cobra"
)

func (r *runner) mcpSessionCommands(parent *cobra.Command) {
	var req source.MCPSessionRequest
	check := &cobra.Command{Use: "session-check", Args: cobra.NoArgs, Short: "读取项目会话记录；--probe 显式启动已信任的本地 Node 插件完成限时握手", RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		var out any
		if req.Probe {
			e, checkErr := m.CheckMCPSession(cmd.Context(), req)
			if checkErr != nil {
				return checkErr
			}
			v := result.New("mcp session-check", e)
			v.Summary = mcpsession.Outcome(e.Code)
			if e.Code != "MCP_SESSION_MATCH" {
				v.OK = false
				v.Code = e.Code
			}
			return r.finish(v)
		}
		out, err = m.MCPSessions(cmd.Context())
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp session-check", out))
	}}
	check.Flags().BoolVar(&req.Probe, "probe", false, "显式启动锁定插件入口完成初始化和工具清单核对；Tier 0 非隔离，不调用业务工具")
	check.Flags().StringVar(&req.Name, "plugin", "", "已安装且独立确认自有可信、Tier 0 的本地 MCP 名称")
	check.Flags().StringVar(&req.ExpectedSHA256, "expected-permission", "", "刚读取的 permission state sha256")
	check.Flags().StringVar(&req.ExpectedReviewSHA256, "expected-review", "", "刚读取的 permission state review_sha256")
	check.Flags().StringVar(&req.ExpectedRuntimeAttempt, "expected-runtime-attempt", "", "刚读取且当前有效的运行时版本核验 attempt")
	recover := &cobra.Command{Use: "session-recover", Args: cobra.NoArgs, Short: "恢复会话审计事务；不重放插件或推断握手成功", RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		n, err := m.RecoverMCPSessions()
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp session-recover", map[string]int{"recovered": n}))
	}}
	parent.AddCommand(check, recover)
}
