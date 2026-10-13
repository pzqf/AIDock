package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/spf13/cobra"
)

func (r *runner) sourceManager() (*source.Manager, error) {
	e, err := r.load(false)
	if err != nil {
		return nil, err
	}
	return e.Sources()
}
func (r *runner) sourceCommands(parent *cobra.Command) {
	var bridgeFile string
	bridgeCheck := func(cmd *cobra.Command, file string) error {
		if file != "" {
			r.o.bridgeFile = file
		}
		e, err := r.load(false)
		if err != nil {
			return err
		}
		v, err := e.BridgeCheck(cmd.Context())
		if err != nil {
			return err
		}
		return r.finish(v)
	}
	bridgeCheckCmd := &cobra.Command{Use: "bridge-check", Short: "[已实现 1B 子集] 只读检查 external-source 登记、候选 revision 与 drift", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return bridgeCheck(cmd, bridgeFile)
	}}
	bridgeCheckCmd.Flags().StringVar(&bridgeFile, "file", "", "外部源登记 YAML/JSON；未指定时使用 --bridge-file")
	parent.AddCommand(bridgeCheckCmd)
	bridgeGroup := &cobra.Command{Use: "bridge", Short: "外部规范源桥接"}
	checkCmd := &cobra.Command{Use: "check", Short: "[已实现 1B 子集] 只读检查登记、候选 revision 与 drift", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return bridgeCheck(cmd, bridgeFile)
	}}
	checkCmd.Flags().StringVar(&bridgeFile, "file", "", "外部源登记 YAML/JSON；未指定时使用 --bridge-file")
	bridgeGroup.AddCommand(checkCmd)
	parent.AddCommand(bridgeGroup)
	parent.AddCommand(&cobra.Command{Use: "check", Short: "[已实现 1B 子集] 只读检查依赖、完整包与兼容性声明", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := r.sourceManager()
		if err != nil {
			return err
		}
		report, err := m.Check()
		if err != nil {
			return err
		}
		v := result.New("skill check", report)
		v.Summary = "只读静态预检；尚未证明工具发现、会话加载或执行能力"
		if report.Status == "FAIL" {
			v.OK, v.Code = false, "STATIC_CHECK_FAILED"
		} else if report.Status == "UNKNOWN" {
			v.OK, v.Code = false, "EVIDENCE_INCOMPLETE"
		}
		return r.finish(v)
	}})
	parent.AddCommand(&cobra.Command{Use: "references <id>", Short: "[已实现 1B 子集] 当前绑定内的声明引用与未知范围", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		m, e := r.sourceManager()
		if e != nil {
			return e
		}
		items, e := m.References(args[0])
		if e != nil {
			return e
		}
		v := result.New("skill references", items)
		v.Summary = "当前绑定内的已知引用；其他项目/离线和动态引用仍未验证"
		return r.finish(v)
	}})
	var descriptor string
	plan := &cobra.Command{Use: "plan <action>", Short: "[已实现 1B 子集] 预览技能源保存、外部采纳和版本回退", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		m, e := r.sourceManager()
		if e != nil {
			return e
		}
		if descriptor == "" {
			return result.Fail("INVALID_INPUT", "源计划需要 --file 结构化描述", 2)
		}
		b, e := config.ReadFile(descriptor)
		if e != nil {
			return e
		}
		v, e := config.Parse(b)
		if e != nil {
			return e
		}
		raw, _ := json.Marshal(v)
		var req source.Request
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if e = d.Decode(&req); e != nil {
			return result.Fail("INVALID_INPUT", e.Error(), 2)
		}
		if req.Action != "" && req.Action != args[0] {
			return result.Fail("PLAN_KIND_MISMATCH", "描述动作不匹配", 2)
		}
		req.Action = args[0]
		if req.Action == "import" && req.PackagePath != "" && !filepath.IsAbs(req.PackagePath) {
			req.PackagePath = filepath.Join(filepath.Dir(descriptor), req.PackagePath)
		}
		out, e := m.Plan(cmd.Context(), req)
		if e != nil {
			return e
		}
		res := result.New("skill plan", out)
		res.Summary = "已保存源计划；尚未修改技能源或工具配置"
		return r.finish(res)
	}}
	plan.Flags().StringVar(&descriptor, "file", "", "动作描述 YAML/JSON（包路径相对描述文件）")
	parent.AddCommand(plan)
	var id, digest string
	apply := &cobra.Command{Use: "apply", Short: "[已实现 1B 子集] 保存已预览的技能源或外部版本选择计划", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		m, e := r.sourceManager()
		if e != nil {
			return e
		}
		if id == "" {
			return result.Fail("INVALID_INPUT", "需要 --plan <id>", 2)
		}
		j, e := m.Apply(cmd.Context(), id, digest)
		if e != nil {
			return e
		}
		v := result.New("skill apply", map[string]any{"transaction": j.Plan.ID, "state": j.State, "pending_native_apply": true, "pending_replica_publish": true})
		v.Summary = "技能源已保存，完整原字节可恢复；未应用到工具或副本"
		return r.finish(v)
	}}
	apply.Flags().StringVar(&id, "plan", "", "源计划 id")
	apply.Flags().StringVar(&digest, "expected-digest", "", "确认的计划摘要")
	parent.AddCommand(apply)
	for _, name := range []string{"history", "recover", "managed-list"} {
		cmd := &cobra.Command{Use: name, Args: cobra.NoArgs, Short: "[已实现 1B 子集] owned 源记录与中断恢复"}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			m, e := r.sourceManager()
			if e != nil {
				return e
			}
			var items any
			if cmd.Name() == "recover" {
				n, err := m.Recover()
				e = err
				items = map[string]any{"recovered": n}
			} else {
				state, err := m.State()
				e = err
				items = state
			}
			if e != nil {
				return e
			}
			v := result.New("skill "+cmd.Name(), items)
			v.Summary = "只针对当前显式绑定的技能源与批准缓存"
			return r.finish(v)
		}
		parent.AddCommand(cmd)
	}
}
