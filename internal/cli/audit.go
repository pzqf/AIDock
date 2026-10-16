package cli

import (
	"github.com/pzqf/AIDock/internal/auditlog"
	"github.com/spf13/cobra"
)

func (r *runner) auditCommand(parent *cobra.Command) {
	var q auditlog.Query
	cmd := &cobra.Command{Use: "audit", Short: "[已实现] 只读查询当前绑定的五类安全审计与本机完整性状态", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		e, err := r.load(false)
		if err != nil {
			return err
		}
		v, err := e.Audit(cmd.Context(), q)
		if err != nil {
			return err
		}
		return r.finish(v)
	}}
	cmd.Flags().StringVar(&q.Category, "category", "", "origin/install/permission/runtime/session；默认全部")
	cmd.Flags().StringVar(&q.Search, "search", "", "搜索事件ID、动作、主体、账户或关联尝试；不搜索正文")
	cmd.Flags().IntVar(&q.Page, "page", 1, "每页50条，从1开始；新事件可能改变页边界")
	parent.AddCommand(cmd)
}
