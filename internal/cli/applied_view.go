package cli

import (
	"github.com/pzqf/AIDock/internal/result"
	"github.com/spf13/cobra"
)

func (r *runner) appliedViewCommand(parent *cobra.Command) {
	parent.AddCommand(&cobra.Command{Use: "applied", Args: cobra.NoArgs, Short: "区分已应用版本、已保存草稿与当前启动门槛，不运行工具", RunE: func(cmd *cobra.Command, _ []string) error {
		e, err := r.loadMode(false, true)
		if err != nil {
			return err
		}
		m, err := e.SnapshotReader()
		if err != nil {
			return err
		}
		v, err := m.AppliedViews(cmd.Context())
		if err != nil {
			return err
		}
		out := result.New("tool applied", v)
		out.Summary = v.Boundary
		return r.finish(out)
	}})
}
