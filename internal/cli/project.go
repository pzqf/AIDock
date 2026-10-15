package cli

import (
	"github.com/pzqf/AIDock/internal/project"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
	"path/filepath"
)

func (r *runner) projectCommands(root *cobra.Command) {
	group := &cobra.Command{Use: "project", Short: "项目创建、打开与本机入口；复用副本登记"}
	for _, action := range []string{"list", "add", "rm", "confirm", "history", "recover"} {
		var file, id, digest, bindingID string
		cmd := &cobra.Command{Use: action, Args: cobra.NoArgs}
		if action == "add" {
			cmd.Flags().StringVar(&file, "file", "", "项目请求 JSON：root/name/create；只生成确认预览")
		}
		if action == "rm" {
			cmd.Flags().StringVar(&bindingID, "id", "", "项目入口 ID；只预览解除登记，使用 project confirm 确认")
		}
		if action == "confirm" || action == "recover" {
			cmd.Flags().StringVar(&id, "plan", "", "项目计划 UUID")
		}
		if action == "confirm" {
			cmd.Flags().StringVar(&digest, "digest", "", "项目计划完整摘要")
		}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			e, err := r.load(false)
			if err != nil {
				return err
			}
			if r.o.home != "" {
				e.Roots.Local = filepath.Join(e.Roots.Home, "local")
			}
			m, err := e.Projects()
			if err != nil {
				return err
			}
			var items any
			switch cmd.Name() {
			case "list":
				items, err = m.List()
			case "add":
				var req project.Request
				err = replicaInput(file, &req)
				if err == nil {
					items, err = m.Plan(cmd.Context(), req)
				}
			case "confirm":
				items, err = m.Apply(cmd.Context(), id, digest)
			case "rm":
				items, err = m.Remove(cmd.Context(), bindingID)
			case "history":
				items, err = m.History()
			case "recover":
				items, err = m.Recover(id)
			}
			if err != nil {
				return err
			}
			v := result.New("project "+cmd.Name(), items)
			v.Summary = "项目入口与技能源使用可恢复事务；不安装工具文件、不运行程序"
			return r.finish(v)
		}
		group.AddCommand(cmd)
	}
	root.AddCommand(group)
}
