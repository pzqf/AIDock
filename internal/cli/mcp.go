package cli

import (
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/spf13/cobra"
)

func (r *runner) mcpCommands(root *cobra.Command) {
	group := &cobra.Command{Use: "mcp", Short: "MCP 定义管理与静态检查；受管启动继续验收"}
	for _, name := range []string{"list", "check"} {
		group.AddCommand(&cobra.Command{Use: name, Args: cobra.NoArgs, Short: "读取已保存定义及安装/执行门槛，不启动程序", RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := r.sourceManager()
			if err != nil {
				return err
			}
			out, err := m.MCP()
			if err != nil {
				return err
			}
			v := result.New("mcp "+cmd.Name(), out)
			v.Summary = "MCP 定义已读取；逐项状态见 servers，保存与运行分别检查"
			if cmd.Name() == "check" && out.Status != "PASS" {
				v.OK, v.Code = false, "EVIDENCE_INCOMPLETE"
				if out.Status == "FAIL" {
					v.Code = "STATIC_CHECK_FAILED"
				}
			}
			return r.finish(v)
		}})
	}
	var file, id, digest string
	plan := &cobra.Command{Use: "plan", Args: cobra.NoArgs, Short: "预览 MCP 定义增删与启停声明", RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		var req source.MCPEdit
		if err = replicaInput(file, &req); err != nil {
			return err
		}
		out, err := m.MCPPlan(cmd.Context(), req)
		if err != nil {
			return err
		}
		v := result.New("mcp plan", out)
		v.Summary = "仅预览定义变化；确认后保留源回退点，不安装或启动 MCP"
		return r.finish(v)
	}}
	plan.Flags().StringVar(&file, "file", "", "包含 expected_sha256 与完整 config 的 YAML/JSON")
	apply := &cobra.Command{Use: "apply", Args: cobra.NoArgs, Short: "确认 MCP 定义计划，保留完整源回退点", RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		out, err := m.ApplyMCP(cmd.Context(), id, digest)
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp apply", out))
	}}
	apply.Flags().StringVar(&id, "plan", "", "预览计划 UUID")
	apply.Flags().StringVar(&digest, "expected-digest", "", "确认预览摘要")
	group.AddCommand(plan, apply)
	r.mcpLockCommands(group)
	r.mcpOriginCommands(group)
	r.mcpManifestCommands(group)
	for _, name := range []string{"start", "stop", "restart", "retire", "exec"} {
		group.AddCommand(reserved(name, "1C"))
	}
	root.AddCommand(group)
}
