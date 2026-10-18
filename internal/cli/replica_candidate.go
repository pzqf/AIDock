package cli

import (
	"os"

	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
	"github.com/spf13/cobra"
)

func (r *runner) replicaCandidateCommands(group *cobra.Command) {
	candidate := &cobra.Command{Use: "candidate", Short: "按已保存候选独立准备目标 MCP；不修改规范源或工具"}
	for _, action := range []string{"show", "artifact-plan", "artifact-confirm", "install-plan", "install-confirm", "permission-plan", "permission-confirm", "runtime-check", "prepare", "prepared"} {
		var file string
		cmd := &cobra.Command{Use: action, Args: cobra.NoArgs}
		cmd.Flags().StringVar(&file, "file", "", "JSON：plan_id/digest/copy_id；操作另含 edit、check 或 operation_plan_id/operation_digest/name")
		_ = cmd.MarkFlagRequired("file")
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
			case "prepare":
				var raw []byte
				raw, err = workstore.ReadFile(file)
				if err == nil {
					var req replica.CandidatePreparationRequest
					req, err = replica.ParseCandidatePreparationRequest(raw)
					if err == nil {
						var executable string
						executable, err = os.Executable()
						if err == nil {
							items, err = m.PrepareCandidate(cmd.Context(), req, executable, e.Roots.Home)
						}
					}
				}
			case "prepared":
				var req replica.CandidatePreparedRequest
				if err = replicaCandidateInput(file, &req); err == nil {
					items, err = m.ReadCandidatePreparation(cmd.Context(), req)
				}
			case "show":
				var req replica.CandidateRequest
				if err = replicaCandidateInput(file, &req); err == nil {
					items, err = m.Candidate(cmd.Context(), req)
				}
			case "artifact-plan":
				var req replica.CandidateArtifactRequest
				if err = replicaCandidateInput(file, &req); err == nil {
					items, err = m.CandidateArtifactPlan(cmd.Context(), req.CandidateRequest, req.Edit)
				}
			case "install-plan":
				var req replica.CandidateInstallRequest
				if err = replicaCandidateInput(file, &req); err == nil {
					items, err = m.CandidateInstallPlan(cmd.Context(), req.CandidateRequest, req.Edit)
				}
			case "permission-plan":
				var req replica.CandidatePermissionRequest
				if err = replicaCandidateInput(file, &req); err == nil {
					items, err = m.CandidatePermissionPlan(cmd.Context(), req.CandidateRequest, req.Edit)
				}
			case "runtime-check":
				var req replica.CandidateRuntimeRequest
				if err = replicaCandidateInput(file, &req); err == nil {
					items, err = m.CandidateRuntimeCheck(cmd.Context(), req.CandidateRequest, req.Check)
				}
			default:
				var req replica.CandidateConfirmation
				if err = replicaCandidateInput(file, &req); err == nil {
					switch cmd.Name() {
					case "artifact-confirm":
						items, err = m.CandidateArtifactApply(cmd.Context(), req.CandidateRequest, req.Name, req.OperationPlan, req.OperationDigest)
					case "install-confirm":
						items, err = m.CandidateInstallApply(cmd.Context(), req.CandidateRequest, req.Name, req.OperationPlan, req.OperationDigest)
					case "permission-confirm":
						items, err = m.CandidatePermissionApply(cmd.Context(), req.CandidateRequest, req.Name, req.OperationPlan, req.OperationDigest)
					}
				}
			}
			if err != nil {
				return err
			}
			v := result.New("replica candidate "+cmd.Name(), items)
			v.Summary = "只针对明确目标和未来配置；独立准备不等于正式同步，规范源和工具文件保留"
			return r.finish(v)
		}
		candidate.AddCommand(cmd)
	}
	group.AddCommand(candidate)
}

func replicaCandidateInput(file string, v any) error {
	raw, err := workstore.ReadFile(file)
	if err != nil {
		return err
	}
	return replica.ParseCandidateInput(raw, v)
}
