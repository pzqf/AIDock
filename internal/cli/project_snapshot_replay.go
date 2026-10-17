package cli

import (
	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
	"github.com/spf13/cobra"
	"os"
)

func (r *runner) snapshotReplayCommands(parent *cobra.Command) {
	manager := func() (*native.Manager, error) {
		e, err := r.loadMode(false, true)
		if err != nil {
			return nil, err
		}
		return e.SnapshotReplayPermissions()
	}
	group := &cobra.Command{Use: "replay", Short: "审查冻结回放候选并明确授权；共同回放须另行检查完整候选并预览确认"}
	var readReviewRestore func() (*native.SnapshotReplayRestoreTarget, error)
	reviewCmd := &cobra.Command{Use: "review <snapshot-id>", Args: cobra.ExactArgs(1), Short: "读取冻结候选的当前安装和授权输入，不应用或启动", RunE: func(cmd *cobra.Command, args []string) error {
		m, err := manager()
		if err != nil {
			return err
		}
		restore, err := readReviewRestore()
		if err != nil {
			return err
		}
		v, err := m.ReviewSnapshotReplayTarget(cmd.Context(), args[0], restore)
		if err != nil {
			return err
		}
		out := result.New("snapshot replay review", v)
		out.Summary = v.Boundary
		return r.finish(out)
	}}
	readReviewRestore = replayRestoreFlags(reviewCmd)
	group.AddCommand(reviewCmd)
	permission := &cobra.Command{Use: "permission", Short: "为精确候选重新授权，替换同名当前授权；不回放旧权限"}
	var file, id, digest string
	plan := &cobra.Command{Use: "plan", Args: cobra.NoArgs, Short: "预览本次候选授权及替换影响", RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := manager()
		if err != nil {
			return err
		}
		raw, err := workstore.ReadFile(file)
		if err != nil {
			return err
		}
		req, err := native.ParseSnapshotReplayPermissionRequest(raw)
		if err != nil {
			return err
		}
		v, err := m.PlanSnapshotReplayPermission(cmd.Context(), req)
		if err != nil {
			return err
		}
		out := result.New("snapshot replay permission plan", v)
		out.Summary = v.Boundary
		return r.finish(out)
	}}
	plan.Flags().StringVar(&file, "file", "", "含 snapshot_id、candidate_digest 和 authorization 本次明确确认的 JSON/YAML")
	apply := &cobra.Command{Use: "apply", Args: cobra.NoArgs, Short: "确认候选授权计划，源/本机/工具文件保持不变", RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := manager()
		if err != nil {
			return err
		}
		j, err := m.ApplySnapshotReplayPermission(cmd.Context(), id, digest)
		if err != nil {
			return err
		}
		out := result.New("snapshot replay permission apply", map[string]string{"id": j.Plan.ID, "state": j.State})
		out.Summary = "候选项目授权及审计已更新；同名旧授权失效，源和工具文件未应用，共同回放需另行检查并确认提案"
		return r.finish(out)
	}}
	apply.Flags().StringVar(&id, "plan", "", "候选授权计划 UUID")
	apply.Flags().StringVar(&digest, "digest", "", "完整确认摘要")
	permission.AddCommand(plan, apply)
	group.AddCommand(permission)
	runtime := &cobra.Command{Use: "runtime", Short: "读取候选版本记录；明确核验只执行锁定 Node --version"}
	var candidate, runtimeFile string
	var readStateRestore func() (*native.SnapshotReplayRestoreTarget, error)
	state := &cobra.Command{Use: "state <snapshot-id>", Args: cobra.ExactArgs(1), Short: "读取此冻结候选的本机版本记录，不启动程序", RunE: func(cmd *cobra.Command, args []string) error {
		m, err := manager()
		if err != nil {
			return err
		}
		restore, err := readStateRestore()
		if err != nil {
			return err
		}
		v, err := m.ReviewSnapshotReplayRuntime(cmd.Context(), native.SnapshotReplayCandidateRequest{Snapshot: args[0], Candidate: candidate, Restore: restore})
		if err != nil {
			return err
		}
		out := result.New("snapshot replay runtime state", v)
		out.Summary = v.Boundary
		return r.finish(out)
	}}
	readStateRestore = replayRestoreFlags(state)
	state.Flags().StringVar(&candidate, "candidate", "", "刚审查的完整候选摘要")
	check := &cobra.Command{Use: "check", Args: cobra.NoArgs, Short: "明确执行候选的固定版本命令并记录审计，不启动插件", RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := manager()
		if err != nil {
			return err
		}
		raw, err := workstore.ReadFile(runtimeFile)
		if err != nil {
			return err
		}
		req, err := native.ParseSnapshotReplayRuntimeRequest(raw)
		if err != nil {
			return err
		}
		v, err := m.CheckSnapshotReplayRuntime(cmd.Context(), req)
		if err != nil {
			return err
		}
		out := result.New("snapshot replay runtime check", v)
		if v.Code != "RUNTIME_VERSION_MATCH" {
			out.OK = false
			out.Code = v.Code
		}
		return r.finish(out)
	}}
	check.Flags().StringVar(&runtimeFile, "file", "", "snapshot_id、candidate_digest、runtime 明确 probe 的 JSON/YAML")
	runtime.AddCommand(state, check)
	var clientCandidate string
	var readClientRestore func() (*native.SnapshotReplayRestoreTarget, error)
	clients := &cobra.Command{Use: "clients <snapshot-id>", Args: cobra.ExactArgs(1), Short: "只读生成候选全部启用 MCP 的连接参数；须完成本次授权和版本核验", RunE: func(cmd *cobra.Command, args []string) error {
		m, err := manager()
		if err != nil {
			return err
		}
		e, err := r.loadMode(false, true)
		if err != nil {
			return err
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		restore, err := readClientRestore()
		if err != nil {
			return err
		}
		v, err := m.SnapshotReplayClientConfigs(cmd.Context(), native.SnapshotReplayCandidateRequest{Snapshot: args[0], Candidate: clientCandidate, Restore: restore}, executable, e.Roots.Home)
		if err != nil {
			return err
		}
		out := result.New("snapshot replay clients", v)
		out.Summary = v.Boundary
		return r.finish(out)
	}}
	readClientRestore = replayRestoreFlags(clients)
	clients.Flags().StringVar(&clientCandidate, "candidate", "", "刚审查的完整候选摘要")
	group.AddCommand(runtime, clients)
	r.snapshotReplayPrepareCommands(group)
	r.snapshotReplayControlCommands(group)
	parent.AddCommand(group)
}
