package cli

import (
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/spf13/cobra"
)

func (r *runner) mcpLockCommands(parent *cobra.Command) {
	group := &cobra.Command{Use: "lock", Short: "本地产物清单、安装锁保存和只读完整性核验"}
	for _, name := range []string{"list", "check"} {
		group.AddCommand(&cobra.Command{Use: name, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := r.sourceManager()
			if err != nil {
				return err
			}
			out, err := m.MCPLock(cmd.Context())
			if err != nil {
				return err
			}
			v := result.New("mcp lock "+cmd.Name(), out)
			v.Summary = "安装锁与本地产物已核验；字节匹配、依赖解析、来源信任分别报告"
			if cmd.Name() == "check" && out.Status != "PASS" {
				v.OK = false
				v.Code = "EVIDENCE_INCOMPLETE"
				if out.Status == "FAIL" {
					v.Code = "STATIC_CHECK_FAILED"
				}
			}
			return r.finish(v)
		}})
	}
	var inspectFile, editFile, id, digest string
	inspect := &cobra.Command{Use: "inspect", Args: cobra.NoArgs, Short: "读取已备好的本地产物并生成 L0 候选清单，不保存源", RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		var req pluginlock.Preparation
		if err = replicaInput(inspectFile, &req); err != nil {
			return err
		}
		out, err := m.InspectMCPLock(cmd.Context(), req)
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp lock inspect", out))
	}}
	inspect.Flags().StringVar(&inspectFile, "file", "", "包含 name、artifact_path、version 和 runtime 的 JSON/YAML")
	plan := &cobra.Command{Use: "plan", Args: cobra.NoArgs, Short: "预览锁文件保存及本地产物检查结果", RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		var req source.MCPLockEdit
		if err = replicaInput(editFile, &req); err != nil {
			return err
		}
		out, err := m.MCPLockPlan(cmd.Context(), req)
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp lock plan", out))
	}}
	plan.Flags().StringVar(&editFile, "file", "", "包含 expected_sha256 和 document 的 JSON/YAML")
	apply := &cobra.Command{Use: "apply", Args: cobra.NoArgs, Short: "确认保存安装锁并建立源回退点，不安装或运行", RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		out, err := m.ApplyMCPLock(cmd.Context(), id, digest)
		if err != nil {
			return err
		}
		return r.finish(result.New("mcp lock apply", out))
	}}
	apply.Flags().StringVar(&id, "plan", "", "预览计划 UUID")
	apply.Flags().StringVar(&digest, "expected-digest", "", "确认预览摘要")
	group.AddCommand(inspect, plan, apply)
	parent.AddCommand(group)
}
