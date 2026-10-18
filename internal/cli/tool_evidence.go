package cli

import (
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func (r *runner) toolEvidenceCommand(group *cobra.Command) {
	group.AddCommand(&cobra.Command{Use: "evidence", Args: cobra.NoArgs, Short: "只读核对当前技能文件，关联独立检查与完整批次历史；不运行工具", RunE: func(cmd *cobra.Command, _ []string) error {
		e, err := r.load(false)
		if err != nil {
			return err
		}
		report, err := e.NativeEvidence(cmd.Context())
		if err != nil {
			return err
		}
		v := result.New("tool evidence", report)
		v.Summary = report.Boundary
		return r.finish(v)
	}})
}
