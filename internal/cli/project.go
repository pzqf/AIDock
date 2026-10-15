package cli

import (
	"os"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/project"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func (r *runner) projectCommands(root *cobra.Command) {
	group := &cobra.Command{Use: "project", Short: "项目创建、打开与本机入口；复用副本登记"}
	for _, action := range []string{"list", "archived", "add", "rm", "archive", "restore", "confirm", "history", "recover"} {
		var file, id, digest, bindingID string
		cmd := &cobra.Command{Use: action, Args: cobra.NoArgs}
		if action == "add" {
			cmd.Flags().StringVar(&file, "file", "", "项目请求 JSON：root/name/create；只生成确认预览")
		}
		if action == "rm" || action == "archive" || action == "restore" {
			cmd.Flags().StringVar(&bindingID, "id", "", "项目入口 ID；只生成操作预览，使用 project confirm 确认")
		}
		if action == "confirm" || action == "recover" {
			cmd.Flags().StringVar(&id, "plan", "", "项目计划 UUID")
		}
		if action == "confirm" {
			cmd.Flags().StringVar(&digest, "digest", "", "项目计划完整摘要")
		}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			// Machine-local recovery/navigation must not depend on the old
			// --binding still being active. Match the console's project routes.
			roots, err := config.ResolveRoots(config.RootOptions{FlagHome: r.o.home, EnvHome: os.Getenv("AIDOCK_HOME")})
			if err != nil {
				return result.Fail("INVALID_ROOTS", err.Error(), 2)
			}
			if r.o.home != "" {
				roots.Local = filepath.Join(roots.Home, "local")
			}
			m, err := project.New(roots.Local)
			if err != nil {
				return err
			}
			var items any
			switch cmd.Name() {
			case "list":
				items, err = m.List()
			case "archived":
				items, err = m.Archived()
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
			case "archive", "restore":
				items, err = m.Membership(cmd.Context(), bindingID, cmd.Name() == "restore")
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
