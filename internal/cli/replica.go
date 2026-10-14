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
	for _, action := range []string{"list", "register", "compare", "baseline-plan", "confirm", "recover"} {
		var file, from, id, digest string
		var targets []string
		cmd := &cobra.Command{Use: action, Args: cobra.NoArgs}
		if action == "register" {
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
			case "register":
				var b replica.Binding
				var raw []byte
				raw, err = workstore.ReadFile(file)
				if err == nil {
					decoder := json.NewDecoder(bytes.NewReader(raw))
					decoder.DisallowUnknownFields()
					err = decoder.Decode(&b)
					if err == nil {
						var trailing any
						if decoder.Decode(&trailing) != io.EOF {
							err = fmt.Errorf("副本登记需要单个 JSON 对象")
						}
					}
				}
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
	group.AddCommand(reserved("plan", "1D"), reserved("apply", "1D"), reserved("rm", "1D"))
	root.AddCommand(group)
}
