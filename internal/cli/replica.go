package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
	"github.com/spf13/cobra"
)

func (r *runner) replicaCommands(root *cobra.Command) {
	group := &cobra.Command{Use: "replica", Short: "本机副本登记、共同基线和三方比较；项目发布仍未开放"}
	for _, action := range []string{"list", "register", "rm", "compare", "baseline-plan", "confirm", "recover", "resolve"} {
		var file, from, id, digest, copyID string
		var targets []string
		cmd := &cobra.Command{Use: action, Args: cobra.NoArgs}
		if action == "rm" {
			cmd.Flags().StringVar(&copyID, "copy", "", "副本 UUID；只预览解除登记，使用 project confirm/history/recover")
		}
		if action == "register" || action == "resolve" {
			cmd.Flags().StringVar(&file, "file", "", "显式副本登记 JSON：id/name/root/local_excludes；root 为绝对 .aidock 源目录")
		}
		if action == "compare" || action == "baseline-plan" {
			cmd.Flags().StringVar(&from, "from", "", "已登记的源副本 UUID")
			cmd.Flags().StringSliceVar(&targets, "to", nil, "明确选择的目标副本 UUID")
		}
		if action == "confirm" {
			cmd.Flags().StringVar(&id, "plan", "", "登记/基线计划 UUID")
			cmd.Flags().StringVar(&digest, "digest", "", "完整计划摘要")
		}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			e, err := r.load(false)
			if err != nil {
				return err
			}
			m, err := e.Replicas()
			if err != nil {
				return err
			}
			var items any
			switch cmd.Name() {
			case "list":
				items, err = m.List()
			case "rm":
				projects, e2 := e.Projects()
				if e2 != nil {
					return e2
				}
				items, err = projects.RemoveCopy(cmd.Context(), m.Source.Owner, m.Source.Scope, copyID)
			case "register":
				var b replica.Binding
				err = replicaInput(file, &b)
				if err == nil {
					items, err = m.Register(b)
				}
			case "compare":
				items, err = m.Compare(cmd.Context(), replica.Request{From: from, Targets: targets})
			case "baseline-plan":
				items, err = m.BaselinePlan(cmd.Context(), replica.Request{From: from, Targets: targets})
			case "confirm":
				items, err = m.Apply(cmd.Context(), id, digest)
			case "recover":
				items, err = m.Recover()
			case "resolve":
				var req replica.ResolveRequest
				err = replicaInput(file, &req)
				if err == nil {
					items, err = m.Resolve(cmd.Context(), req)
				}
			}
			if err != nil {
				return err
			}
			v := result.New("replica "+cmd.Name(), items)
			v.Summary = "副本项目只读；确认只写本机登记/共同基线，未发布技能源或生成工具文件"
			return r.finish(v)
		}
		group.AddCommand(cmd)
	}
	group.AddCommand(reserved("plan", "1D"), reserved("apply", "1D"))
	generation := &cobra.Command{Use: "generation", Short: "内置完整包编译器暂存与恢复清单；不运行外部脚本"}
	for _, action := range []string{"stage", "external-stage", "show"} {
		var id, digest, stageID, stageDigest string
		var tools []string
		cmd := &cobra.Command{Use: action, Args: cobra.NoArgs}
		cmd.Flags().StringVar(&id, "plan", "", "副本候选（stage/external-stage）或生成清单（show）UUID")
		cmd.Flags().StringVar(&digest, "digest", "", "副本候选完整摘要（external-stage 为候选摘要）")
		if action == "stage" {
			cmd.Flags().StringSliceVar(&tools, "tools", nil, "明确选择 claude-code,codex-cli")
		}
		if action == "external-stage" {
			cmd.Flags().StringVar(&stageID, "stage", "", "已通过离线返回包校验的生成器暂存 UUID")
			cmd.Flags().StringVar(&stageDigest, "stage-digest", "", "生成器暂存完整摘要")
			_ = cmd.MarkFlagRequired("stage")
			_ = cmd.MarkFlagRequired("stage-digest")
		}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			e, err := r.load(false)
			if err != nil {
				return err
			}
			m, err := e.Replicas()
			if err != nil {
				return err
			}
			var items any
			if cmd.Name() == "stage" {
				items, err = m.StageGeneration(cmd.Context(), replica.GenerationRequest{Plan: id, Digest: digest, Tools: tools})
			} else if cmd.Name() == "external-stage" {
				items, err = m.StageExternalGeneration(cmd.Context(), replica.ExternalGenerationRequest{Plan: id, Digest: digest, Stage: stageID, StageDigest: stageDigest})
			} else {
				items, err = m.GenerationShow(id, digest)
			}
			if err != nil {
				return err
			}
			v := result.New("replica generation "+cmd.Name(), items)
			v.Summary = "完整输入/输出与恢复清单只存 Local；已登记项目未修改"
			return r.finish(v)
		}
		generation.AddCommand(cmd)
	}
	group.AddCommand(generation)
	var repairFile string
	repair := &cobra.Command{Use: "repair", Short: "按各目标自己的源预览纯生成物修复；不采用源副本或推进基线", Args: cobra.NoArgs}
	repair.Flags().StringVar(&repairFile, "file", "", "修复 JSON：from/targets/comparison_digest/tools")
	repair.RunE = func(cmd *cobra.Command, _ []string) error {
		e, err := r.load(false)
		if err != nil {
			return err
		}
		m, err := e.Replicas()
		if err != nil {
			return err
		}
		var req replica.GenerationRepairRequest
		if err = replicaInput(repairFile, &req); err != nil {
			return err
		}
		items, err := m.StageRepair(cmd.Context(), req)
		if err != nil {
			return err
		}
		v := result.New("replica generation repair", items)
		v.Summary = "纯生成物修复预览只存 Local；源、共同基线和已登记工具目录未修改"
		return r.finish(v)
	}
	generation.AddCommand(repair)
	checks := &cobra.Command{Use: "check", Short: "逐副本候选发现与发布门槛；默认不启动进程"}
	for _, action := range []string{"run", "show", "history"} {
		var id, digest, checkID string
		var probe bool
		cmd := &cobra.Command{Use: action, Args: cobra.NoArgs}
		if action == "run" {
			cmd.Flags().StringVar(&id, "plan", "", "暂存生成清单 UUID")
			cmd.Flags().StringVar(&digest, "digest", "", "生成清单完整摘要")
			cmd.Flags().BoolVar(&probe, "probe", false, "显式在独立临时项目逐目标启动工具，验证候选技能发现；不执行技能")
		}
		if action == "show" {
			cmd.Flags().StringVar(&checkID, "check", "", "检查 UUID；只读重新核对有效性")
		}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			e, err := r.load(false)
			if err != nil {
				return err
			}
			m, err := e.Replicas()
			if err != nil {
				return err
			}
			var items any
			switch cmd.Name() {
			case "run":
				items, err = m.CheckGeneration(cmd.Context(), replica.GenerationCheckRequest{Plan: id, Digest: digest, Probe: probe})
			case "show":
				items, err = m.GenerationCheckShow(cmd.Context(), checkID)
			case "history":
				items, err = m.GenerationCheckHistory()
			}
			if err != nil {
				return err
			}
			v := result.New("replica generation check "+cmd.Name(), items)
			v.Summary = "逐副本候选发现与发布预检；不修改登记项目，检查不授权正式发布或安装"
			if report, ok := items.(replica.GenerationCheck); ok {
				v.OK, v.Code = false, "EVIDENCE_INCOMPLETE"
				if report.CandidateStatus == "FAIL" {
					v.Code = "STATIC_CHECK_FAILED"
				}
				if report.Stale {
					v.Code, v.Summary = "CHECK_STALE", report.StaleReason
				}
			}
			return r.finish(v)
		}
		checks.AddCommand(cmd)
	}
	generation.AddCommand(checks)
	rehearsal := &cobra.Command{Use: "rehearsal", Short: "独立副本中的应用、基线推进、回退和撤销；真实项目保留"}
	for _, action := range []string{"run", "run-generated", "latest", "history", "show", "plan", "apply", "recover"} {
		var session, id, digest, transaction string
		cmd := &cobra.Command{Use: action, Args: cobra.NoArgs}
		cmd.Flags().StringVar(&session, "session", "", "副本预演 UUID")
		cmd.Flags().StringVar(&id, "plan", "", "副本候选或回退计划 UUID")
		cmd.Flags().StringVar(&digest, "digest", "", "完整计划摘要")
		cmd.Flags().StringVar(&transaction, "transaction", "", "最新成功预演事务 UUID")
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			e, err := r.load(false)
			if err != nil {
				return err
			}
			m, err := e.Replicas()
			if err != nil {
				return err
			}
			var items any
			switch cmd.Name() {
			case "run":
				items, err = m.Rehearse(cmd.Context(), id, digest)
			case "run-generated":
				items, err = m.RehearseGenerated(cmd.Context(), id, digest)
			case "latest":
				items, err = m.RehearsalLatest()
			case "history":
				items, err = m.RehearsalHistory()
			case "show":
				items, err = m.RehearsalShow(session)
			case "plan":
				items, err = m.RehearsalReverse(session, transaction)
			case "apply":
				items, err = m.RehearsalApply(cmd.Context(), session, id, digest)
			case "recover":
				items, err = m.RehearsalRecover(session)
			}
			if err != nil {
				return err
			}
			v := result.New("replica rehearsal "+cmd.Name(), items)
			v.Summary = "只写独立预演副本；已登记项目与真实共同基线未修改"
			return r.finish(v)
		}
		rehearsal.AddCommand(cmd)
	}
	group.AddCommand(rehearsal)
	root.AddCommand(group)
}

func replicaInput(file string, v any) error {
	raw, err := workstore.ReadFile(file)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(v); err != nil {
		return err
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return fmt.Errorf("需要单个 JSON 对象")
	}
	return nil
}
