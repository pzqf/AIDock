package cli

import (
	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
	"github.com/spf13/cobra"
)

func (r *runner) snapshotReplayCommands(parent *cobra.Command) {
	manager := func() (*native.Manager, error) {
		e, err := r.loadMode(false, true)
		if err != nil {
			return nil, err
		}
		return e.SnapshotReplayPermissions()
	}
	group := &cobra.Command{Use: "replay", Short: "审查冻结回放候选并明确授权；完整应用回放尚未开放"}
	group.AddCommand(&cobra.Command{Use: "review <snapshot-id>", Args: cobra.ExactArgs(1), Short: "读取冻结候选的当前安装和授权输入，不应用或启动", RunE: func(cmd *cobra.Command, args []string) error {
		m, err := manager()
		if err != nil {
			return err
		}
		v, err := m.ReviewSnapshotReplayPermissions(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		out := result.New("snapshot replay review", v)
		out.Summary = v.Boundary
		return r.finish(out)
	}})
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
		out.Summary = "候选项目授权及审计已更新；同名旧授权失效，源和工具文件未应用，完整项目回放尚未开放"
		return r.finish(out)
	}}
	apply.Flags().StringVar(&id, "plan", "", "候选授权计划 UUID")
	apply.Flags().StringVar(&digest, "digest", "", "完整确认摘要")
	permission.AddCommand(plan, apply)
	group.AddCommand(permission)
	parent.AddCommand(group)
}
