package cli

import (
	"encoding/json"
	"os"

	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
	"github.com/spf13/cobra"
)

func (r *runner) replicaFullPublicationOperations(group *cobra.Command) {
	for _, action := range replica.FullPublicationOperationNames() {
		var file, publication, digest string
		cmd := &cobra.Command{Use: action, Args: cobra.NoArgs, Short: "完整批次独立确认、真实状态、共同恢复或所选成员恢复"}
		cmd.Flags().StringVar(&file, "file", "", "当前操作的完整、明确 JSON 请求；不接受源正文或自定义命令")
		idOnly := action == "full-execution" || action == "full-inspect-recovery" || action == "full-recover"
		if idOnly || action == "full-apply" {
			cmd.Flags().StringVar(&publication, "publication", "", "原发起绑定的完整批次 UUID")
			if action == "full-apply" {
				cmd.Flags().StringVar(&digest, "digest", "", "原完整批次的完整确认摘要")
			}
		} else {
			_ = cmd.MarkFlagRequired("file")
		}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			var raw []byte
			var err error
			if file != "" {
				if publication != "" || digest != "" {
					return result.Fail("INVALID_INPUT", "JSON 请求与独立身份参数只能选择一种", 2)
				}
				raw, err = workstore.ReadFile(file)
			} else {
				input := map[string]string{"publication_id": publication}
				if action == "full-apply" {
					input["digest"] = digest
				}
				raw, err = json.Marshal(input)
			}
			if err != nil {
				return err
			}
			req, err := replica.ParseFullPublicationOperation(action, raw)
			if err != nil {
				return err
			}
			e, err := r.load(false)
			if err != nil {
				return err
			}
			m, err := e.Replicas()
			if err != nil {
				return err
			}
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			items, err := m.RunFullPublicationOperation(cmd.Context(), action, req, executable, e.Roots.Home)
			if err != nil {
				return err
			}
			out := result.New("replica publication "+action, items)
			out.Summary = "依本次明确操作处理完整批次；授权、检查、提案与共同确认分别进行，发布者只读，历史权限不回放"
			return r.finish(out)
		}
		group.AddCommand(cmd)
	}
}
