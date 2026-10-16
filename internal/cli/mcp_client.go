package cli

import (
	"io"
	"os"

	"github.com/pzqf/AIDock/internal/mcprelay"
	"github.com/pzqf/AIDock/internal/mcpsession"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/spf13/cobra"
)

type protocolWriter struct{ io.Writer }

func (protocolWriter) Close() error { return nil }

func (r *runner) mcpClientCommands(parent *cobra.Command) {
	var req source.MCPSessionRequest
	exec := &cobra.Command{Use: "exec", Args: cobra.NoArgs, Short: "连接已授权自有本地 Node MCP；stdout 只输出 stdio 协议，Tier 0 非隔离", RunE: func(cmd *cobra.Command, _ []string) error {
		r.operation = "mcp exec"
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		var writer io.WriteCloser = protocolWriter{r.out}
		if closer, ok := r.out.(io.WriteCloser); ok {
			writer = closer
		}
		e, err := m.ServeMCPClient(cmd.Context(), req, mcprelay.Endpoint{Reader: os.Stdin, Writer: writer})
		if err != nil {
			return err
		}
		if e.Code != "MCP_CLIENT_CLOSED" {
			return result.Fail(e.Code, mcpsession.Outcome(e.Code), 4)
		}
		return nil
	}}
	exec.Flags().BoolVar(&req.Probe, "allow-unisolated", false, "明确以已保存自有可信授权启动非隔离 stdio 会话；不授予新权限")
	exec.Flags().StringVar(&req.Name, "plugin", "", "当前已安装、启用且独立授权的自有可信本地 MCP")
	exec.Flags().StringVar(&req.ExpectedSHA256, "expected-permission", "", "已审查的授权 SHA")
	exec.Flags().StringVar(&req.ExpectedReviewSHA256, "expected-review", "", "已审查的输入 SHA")
	exec.Flags().StringVar(&req.ExpectedRuntimeAttempt, "expected-runtime-attempt", "", "当前有效的 Node 版本核验 attempt")
	var configReq source.MCPSessionRequest
	config := &cobra.Command{Use: "client-config", Args: cobra.NoArgs, Short: "只读预览独立 command/args；不写原生文件、不启动插件", RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		e, err := r.load(false)
		if err != nil {
			return err
		}
		out, err := m.MCPClientConfig(cmd.Context(), configReq, executable, e.Roots.Home)
		if err != nil {
			return err
		}
		v := result.New("mcp client-config", out)
		v.Summary = out.Boundary
		return r.finish(v)
	}}
	config.Flags().StringVar(&configReq.Name, "plugin", "", "已授权插件名称")
	config.Flags().StringVar(&configReq.ExpectedSHA256, "expected-permission", "", "已审查的授权 SHA")
	config.Flags().StringVar(&configReq.ExpectedReviewSHA256, "expected-review", "", "已审查的输入 SHA")
	config.Flags().StringVar(&configReq.ExpectedRuntimeAttempt, "expected-runtime-attempt", "", "当前版本核验 attempt")
	parent.AddCommand(exec, config)
}
