package cli

import (
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func (r *runner) skillReadCommands(parent *cobra.Command) {
	for _, name := range []string{"scan", "list", "show"} {
		var file string
		cmd := &cobra.Command{Use: name, Short: "只读查看当前绑定的技能资产；--manifest 使用候选盘点合同", Args: cobra.NoArgs}
		if name == "scan" {
			cmd.Short = "按 --manifest 只读盘点包；候选 locator 不是稳定资产 id"
		}
		if name == "show" {
			cmd.Use = "show <id>"
			cmd.Args = cobra.ExactArgs(1)
			cmd.Long = "项目/全局绑定按稳定 UUID 查看技能身份、包文件、来源及共享/有效开关。\n--file <包内相对路径> 读取正文或附件；二进制只返回元数据。\n--manifest 模式仍按 candidate-id 查看盘点候选，不接受 --file。"
			cmd.Flags().StringVar(&file, "file", "", "读取已绑定技能的包内文件，例如 SKILL.md")
		}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			e, err := r.load(true)
			if err != nil {
				return err
			}
			managed := e.SourceRoot != "" || e.GlobalRoot != ""
			if managed && cmd.Name() != "scan" {
				if r.o.includeGenerated {
					return result.Fail("INVALID_INPUT", "--include-generated 仅用于 --manifest 盘点", 2)
				}
				m, err := e.Sources()
				if err != nil {
					return err
				}
				if cmd.Flags().Changed("file") {
					if file == "" {
						return result.Fail("INVALID_INPUT", "--file 需要包内相对路径", 2)
					}
					item, err := m.File(args[0], file)
					if err != nil {
						return err
					}
					v := result.New("skill show", map[string]any{"skill_id": args[0], "file": item})
					v.Summary = "当前绑定内的包文件；读取不会执行脚本"
					return r.finish(v)
				}
				state, err := m.State()
				if err != nil {
					return err
				}
				v := result.New("skill "+cmd.Name(), state)
				v.Project, v.Scope = m.Owner, m.Scope
				v.Summary = "当前显式绑定的稳定技能资产与有效配置；保存不代表工具已生效"
				if cmd.Name() == "show" {
					if state.Pending {
						return result.Fail("RECOVERY_REQUIRED", "源有未恢复事务；先查看 skill list 并执行 skill recover", 3)
					}
					found := false
					for _, item := range state.Effective {
						if item.Skill.ID == args[0] {
							v.Items = map[string]any{"asset": item, "description": state.Descriptions[args[0]], "shared_selection": state.Profile["skills"], "effective_selection": state.EffectiveProfile["skills"]}
							found = true
							break
						}
					}
					if !found {
						return result.Fail("SKILL_NOT_FOUND", "当前绑定内找不到此稳定技能 UUID；名称和候选 locator 不能替代资产身份", 2)
					}
				}
				return r.finish(v)
			}
			if cmd.Flags().Changed("file") {
				return result.Fail("INVALID_INPUT", "--file 只读取显式项目/全局绑定内的稳定技能资产", 2)
			}
			if managed {
				return result.Fail("MANIFEST_REQUIRED", "skill scan 需要 --manifest；查看已绑定资产请使用 skill list", 2)
			}
			v, err := e.Inventory(cmd.Context(), r.scanOptions())
			if err != nil {
				return err
			}
			v.Operation = "skill " + cmd.Name()
			if cmd.Name() == "show" {
				report := v.Items.(inventory.Report)
				found := false
				for _, s := range report.Skills {
					if s.CandidateID == args[0] {
						v.Items, v.OK, v.Code = s, true, "OK"
						found = true
						break
					}
				}
				if !found {
					return result.Fail("CANDIDATE_NOT_FOUND", "当前来源清单内找不到此候选；目录变化可能使 locator 失效", 2)
				}
			}
			return r.finish(v)
		}
		parent.AddCommand(cmd)
	}
}
